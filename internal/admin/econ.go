package admin

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rf4server/internal/gameserver"
	"rf4server/internal/store"
)

// 「鱼种经济」后台：按鱼种逐档覆盖 XP/克 与基准水域银/公斤。
//
// 列表同时给出内置默认值与覆盖值，前端只需渲染；
// 试算复用 gameserver 的导出函数，保证后台看到的数字与主服实际结算一致。

type econGrade struct {
	Grade     int64    `json:"grade"`
	BaseXP    float64  `json:"base_xp"`
	BasePrice float64  `json:"base_price"`
	XPC       *float64 `json:"xp_c"`
	Price     *float64 `json:"price_per_kg"`
}

type econSpecies struct {
	FishKey      string      `json:"fish_key"`
	NameZh       string      `json:"name_zh"`
	MinG         int64       `json:"min_g"`
	TrophyG      int64       `json:"trophy_g"`
	TrophyLineG  int64       `json:"trophy_line_g"`
	SuperLineG   int64       `json:"super_trophy_line_g"`
	Grades       []econGrade `json:"grades"`
	Overridden   bool        `json:"overridden"`
	PriceMissing bool        `json:"price_missing"`
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// handleEconSpecies 返回鱼种名册 + 四档内置值 + 已保存的覆盖值。
func (s *Server) handleEconSpecies(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	onlyOverridden := r.URL.Query().Get("only") == "overridden"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	like := "%" + q + "%"
	rows, err := s.db.SQL.QueryContext(r.Context(), `
		SELECT w.fish_key, COALESCE(MAX(NULLIF(l.name_zh,'')),w.fish_key),
		       COALESCE(MAX(COALESCE(w.min_g,0)),0), COALESCE(MAX(COALESCE(w.trophy_g,0)),0),
		       COALESCE(MAX(COALESCE(w.trophy_line_g,0)),0), COALESCE(MAX(COALESCE(w.super_trophy_line_g,0)),0)
		FROM species_weight w LEFT JOIN loc_fish l ON l.fish_key=w.fish_key
		WHERE w.fish_key LIKE ? OR l.name_zh LIKE ?
		GROUP BY w.fish_key ORDER BY w.fish_key LIMIT ?`, like, like, limit)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	// SQLite 连接池只有一个写连接：必须先把名册读完并关掉游标，
	// 再去读覆盖表，否则第二条查询会等第一条的游标释放（实测会卡住整个请求）。
	type baseRow struct {
		key, name                                    string
		minG, trophyG, trophyLineG, superTrophyLineG int64
	}
	basics := make([]baseRow, 0, 256)
	for rows.Next() {
		var b baseRow
		if err := rows.Scan(&b.key, &b.name, &b.minG, &b.trophyG, &b.trophyLineG, &b.superTrophyLineG); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		basics = append(basics, b)
	}
	if err := rows.Err(); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	rows.Close()

	overrides, err := s.db.SpeciesEconList(r.Context())
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	ov := map[string]map[int64]store.SpeciesEcon{}
	for _, rec := range overrides {
		if ov[rec.FishKey] == nil {
			ov[rec.FishKey] = map[int64]store.SpeciesEcon{}
		}
		ov[rec.FishKey][rec.Grade] = rec
	}

	out := make([]econSpecies, 0, len(basics))
	for _, b := range basics {
		item := econSpecies{
			FishKey: b.key, NameZh: b.name, MinG: b.minG,
			TrophyG: b.trophyG, TrophyLineG: b.trophyLineG, SuperLineG: b.superTrophyLineG,
		}
		item.Grades = make([]econGrade, 0, 4)
		for grade := int64(1); grade <= 4; grade++ {
			g := econGrade{
				Grade:     grade,
				BaseXP:    round6(gameserver.SpeciesBaseXPRate(item.FishKey, grade)),
				BasePrice: round6(gameserver.SpeciesBasePricePerKg(item.FishKey, grade)),
			}
			if rec, ok := ov[item.FishKey][grade]; ok {
				g.XPC = rec.XPC
				g.Price = rec.PricePerKg
				item.Overridden = true
			}
			if g.BasePrice <= 0 {
				item.PriceMissing = true
			}
			item.Grades = append(item.Grades, g)
		}
		if onlyOverridden && !item.Overridden {
			continue
		}
		out = append(out, item)
	}
	writeJSON(w, map[string]any{
		"items":  out,
		"count":  len(out),
		"base":   gameserver.MarketBaseMapKey(),
		"maps":   gameserver.MarketMapList(),
		"now":    time.Now().Unix(),
		"remark": "经验单位 XP/克；鱼价单位 银/公斤（基准水域），其他水域按倍率折算",
	})
}

// handleEconSpeciesSave 按鱼种整体提交四档覆盖；某档两个值都为空即删除该档覆盖。
func (s *Server) handleEconSpeciesSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FishKey string `json:"fish_key"`
		Grades  []struct {
			Grade      int64    `json:"grade"`
			XPC        *float64 `json:"xp_c"`
			PricePerKg *float64 `json:"price_per_kg"`
		} `json:"grades"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.FishKey) == "" || len(req.Grades) == 0 {
		httpError(w, 400, "参数错误")
		return
	}
	key := strings.TrimSpace(req.FishKey)
	// 名册校验只做软校验：主库没挂静态库（独立调试/测试）时跳过，不阻塞保存。
	var exists int
	if err := s.db.SQL.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM species_weight WHERE fish_key=?`, key).Scan(&exists); err == nil && exists == 0 {
		httpError(w, 400, "鱼种不存在："+key)
		return
	}
	recs := make([]store.SpeciesEcon, 0, len(req.Grades))
	for _, g := range req.Grades {
		recs = append(recs, store.SpeciesEcon{Grade: g.Grade, XPC: g.XPC, PricePerKg: g.PricePerKg})
	}
	if err := s.db.SpeciesEconSave(r.Context(), key, recs, adminOf(r)); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	detail := econSaveDetail(recs)
	_ = s.db.AuditLogWrite(r.Context(), adminOf(r), "species_econ", key, detail)
	_ = s.db.QueuePush(r.Context(), mustJSON(map[string]any{"op": "reload_tunables"}))
	writeJSON(w, map[string]any{"ok": true})
}

func econSaveDetail(recs []store.SpeciesEcon) string {
	parts := make([]string, 0, len(recs))
	for _, rec := range recs {
		switch {
		case rec.XPC == nil && rec.PricePerKg == nil:
			parts = append(parts, fmt.Sprintf("g%d=默认", rec.Grade))
		case rec.XPC != nil && rec.PricePerKg != nil:
			parts = append(parts, fmt.Sprintf("g%d xp=%g price=%g", rec.Grade, *rec.XPC, *rec.PricePerKg))
		case rec.XPC != nil:
			parts = append(parts, fmt.Sprintf("g%d xp=%g", rec.Grade, *rec.XPC))
		default:
			parts = append(parts, fmt.Sprintf("g%d price=%g", rec.Grade, *rec.PricePerKg))
		}
	}
	return strings.Join(parts, "; ")
}

// handleEconSpeciesReset 清空某鱼种全部覆盖，回到内置表。
func (s *Server) handleEconSpeciesReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FishKey string `json:"fish_key"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.FishKey) == "" {
		httpError(w, 400, "参数错误")
		return
	}
	key := strings.TrimSpace(req.FishKey)
	n, err := s.db.SpeciesEconReset(r.Context(), key)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(r.Context(), adminOf(r), "species_econ_reset", key,
		fmt.Sprintf("删除 %d 行覆盖", n))
	_ = s.db.QueuePush(r.Context(), mustJSON(map[string]any{"op": "reload_tunables"}))
	writeJSON(w, map[string]any{"ok": true, "deleted": n})
}

// handleEconPreview 试算一尾鱼的经验与卖价；未传覆盖值时用库里已保存的覆盖，再退回内置表。
func (s *Server) handleEconPreview(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	species := strings.TrimSpace(query.Get("species"))
	if species == "" {
		httpError(w, 400, "缺少鱼种")
		return
	}
	grade, _ := strconv.ParseInt(query.Get("grade"), 10, 64)
	if grade < 1 || grade > 4 {
		grade = 3
	}
	weight, _ := strconv.ParseInt(query.Get("weight_g"), 10, 64)
	if weight <= 0 {
		weight = 1000
	}
	mapKey := strings.TrimSpace(query.Get("map"))
	if mapKey == "" {
		mapKey = gameserver.MarketBaseMapKey()
	}
	parseOpt := func(name string) float64 {
		raw := strings.TrimSpace(query.Get(name))
		if raw == "" {
			return 0
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 {
			return 0
		}
		return v
	}
	xpOv, priceOv := parseOpt("xp_c"), parseOpt("price_per_kg")
	var saved *store.SpeciesEcon
	if xpOv <= 0 || priceOv <= 0 {
		if rec, err := s.db.SpeciesEconGet(r.Context(), species, grade); err == nil && rec != nil {
			saved = rec
			if xpOv <= 0 && rec.XPC != nil {
				xpOv = *rec.XPC
			}
			if priceOv <= 0 && rec.PricePerKg != nil {
				priceOv = *rec.PricePerKg
			}
		}
	}
	now := float64(time.Now().Unix())
	xp, cents := gameserver.PreviewSpeciesEcon(species, grade, weight, xpOv, priceOv, mapKey, now, now)
	writeJSON(w, map[string]any{
		"xp":           xp,
		"price_cents":  cents,
		"price_silver": float64(cents) / 100,
		"base_xp_rate": gameserver.SpeciesBaseXPRate(species, grade),
		"base_price":   gameserver.SpeciesBasePricePerKg(species, grade),
		"xp_c":         xpOv,
		"price_per_kg": priceOv,
		"map":          mapKey,
		"map_pct":      gameserver.MarketMapPct(mapKey),
		"weight_g":     weight,
		"grade":        grade,
		"saved":        saved != nil,
		// 当日行情系数：卖价 = 基准价 × 本系数 × 克重 × 新鲜度。
		// 不返回它，试算页就只能显示「1kg 却比每公斤价高」，看起来像算错。
		"market_factor": gameserver.SpeciesDailyMarketFactor(species, now),
	})
}
