package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"rf4server/internal/store"
)

// 「鱼种档案」后台：按鱼种维护全套生态基线。
//
// 与「逐鱼种生态」弹窗（fish_ecology_loc）的区别：
//   - 这里改的是**这个物种本身**的习性，对全服所有水域生效；
//   - 那边改的是「某张图上的这种鱼」，只影响一张图。
//
// 写入落在 fish_ecology_species（00082），不写 fish_ecology——后者由资产
// 按 profile_version 重灌，手工值会被覆盖。合并顺序见 store 层注释。

// ecologySpeciesRow 是列表里的一行：资产基线 + 覆盖原文。
//
// 生效值（基线叠覆盖）**不在后端算**，交给前端一次 spread 完成：
// 真正决定判鱼的是 gameserver.fishEcologyFor 里的三层合并，后端再抄一份
// 只用于展示的合并逻辑迟早和它走偏，不如让前端用同一份 base+override 现算。
type ecologySpeciesRow struct {
	FishKey  string `json:"fish_key"`
	NameZh   string `json:"name_zh"`
	Rarity   int    `json:"rarity_tier"`
	Origin   string `json:"origin"`
	Overlaid bool   `json:"overlaid"`

	// Locs 是该鱼出现过的水域（loc_wiki_fish.loc，截断 6 个）。
	// 2026-11-25 从「饵料规则」表搬来：水域归属是鱼种属性，不是饵料属性，
	// 放在这里才能和稀有度、水温、水层一起看。
	Locs []string `json:"locs"`

	// Base 是资产基线，全部字段都有值（fish_ecology 的列都是 NOT NULL）。
	Base store.FishEcologySpecies `json:"base"`
	// Override 是覆盖行原文，nil 字段表示继承基线；整条 nil = 从未覆盖过。
	Override *store.FishEcologySpecies `json:"override"`
}

// handleFishEcologySpecies GET：?q= 模糊过滤（鱼种键或中文名）、?only=overlaid 只看已覆盖。
func (s *Server) handleFishEcologySpecies(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	onlyOverlaid := r.URL.Query().Get("only") == "overlaid"
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// 先读完基线并关游标：SQLite 单写连接下，游标不释放会让下一条查询卡住。
	like := "%" + q + "%"
	rows, err := s.db.SQL.QueryContext(ctx, `
		SELECT e.fish_key, COALESCE(MAX(NULLIF(l.name_zh,'')), e.fish_key), e.rarity_tier,
		       e.layer_min, e.layer_pref, e.layer_max,
		       e.temp_min_c, e.temp_pref_c, e.temp_max_c,
		       e.diel_mask, e.diel_curve, e.season_mask,
		       e.diet_json, e.weather_json, e.action_json, e.substrate_json, e.color_json,
		       e.current_pref, e.activity, e.light_pref, e.clarity_pref, e.aggression, e.enabled
		FROM fish_ecology e LEFT JOIN loc_fish l ON l.fish_key = e.fish_key
		WHERE e.fish_key LIKE ? OR l.name_zh LIKE ?
		GROUP BY e.fish_key
		ORDER BY e.fish_key`, like, like)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	type baseRow struct {
		row  ecologySpeciesRow
		base store.FishEcologySpecies
	}
	var basics []baseRow
	for rows.Next() {
		var (
			key, name                                 string
			rarity                                    int
			lmin, lpref, lmax                         float64
			tmin, tpref, tmax                         float64
			dielMask, seasonMask                      int64
			dielCurve                                 string
			diet, weather, action, substrate, color   string
			currentPref, activity, lightPref, clarity float64
			aggression                                float64
			enabled                                   int64
		)
		if err := rows.Scan(&key, &name, &rarity,
			&lmin, &lpref, &lmax, &tmin, &tpref, &tmax,
			&dielMask, &dielCurve, &seasonMask,
			&diet, &weather, &action, &substrate, &color,
			&currentPref, &activity, &lightPref, &clarity, &aggression, &enabled); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		// 基线转成全字段指针形态，前端可以像读覆盖行一样读它（同一套渲染代码）。
		base := store.FishEcologySpecies{
			FishKey:       key,
			LayerMin:      ptrF(lmin),
			LayerPref:     ptrF(lpref),
			LayerMax:      ptrF(lmax),
			TempMinC:      ptrF(tmin),
			TempPrefC:     ptrF(tpref),
			TempMaxC:      ptrF(tmax),
			DielMask:      ptrI(dielMask),
			DielCurve:     ptrS(dielCurve),
			SeasonMask:    ptrI(seasonMask),
			DietJSON:      ptrS(diet),
			WeatherJSON:   ptrS(weather),
			ActionJSON:    ptrS(action),
			SubstrateJSON: ptrS(substrate),
			ColorJSON:     ptrS(color),
			CurrentPref:   ptrF(currentPref),
			Activity:      ptrF(activity),
			LightPref:     ptrF(lightPref),
			ClarityPref:   ptrF(clarity),
			Aggression:    ptrF(aggression),
			Enabled:       ptrI(enabled),
			Origin:        "baseline",
		}
		basics = append(basics, baseRow{
			row: ecologySpeciesRow{
				FishKey: key, NameZh: name, Rarity: rarity,
				Base: base,
			},
			base: base,
		})
	}
	if err := rows.Err(); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	rows.Close()

	overrides, err := s.db.AllFishEcologySpecies(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	ovMap := make(map[string]*store.FishEcologySpecies, len(overrides))
	for i := range overrides {
		ovMap[overrides[i].FishKey] = &overrides[i]
	}

	// 水域归属：loc_wiki_fish 的 loc 列。水层在 base 里已经有了，
	// 这里只补「这张图上有哪些鱼」这一维——两列原本都在「饵料规则」页，
	// 但都不是饵料属性，搬回鱼种档案才归位。
	// 游标必须读完就关：SQLite 单写连接下留着游标会把后续查询卡死。
	locsByFish := map[string][]string{}
	if locRows, lerr := s.db.SQL.QueryContext(ctx,
		`SELECT fish_key, loc FROM loc_wiki_fish WHERE loc <> '' ORDER BY loc`); lerr == nil {
		for locRows.Next() {
			var fk, l string
			if locRows.Scan(&fk, &l) == nil {
				if len(locsByFish[fk]) < 6 {
					locsByFish[fk] = append(locsByFish[fk], l)
				}
			}
		}
		locRows.Close()
	}

	out := make([]ecologySpeciesRow, 0, len(basics))
	for _, b := range basics {
		item := b.row
		item.Locs = locsByFish[item.FishKey]
		if ov := ovMap[b.row.FishKey]; ov != nil {
			item.Overlaid = true
			item.Origin = ov.Origin
			item.Override = ov
		}
		if onlyOverlaid && !item.Overlaid {
			continue
		}
		out = append(out, item)
	}
	writeJSON(w, map[string]any{
		"items": out,
		"count": len(out),
		"total": len(basics),
		"remark": "温度单位 ℃；季节掩码 1 春 2 夏 4 秋 8 冬；昼夜掩码 1 夜 2 晨 4 昼 8 昏；" +
			"水层 0=水面 1=水底。override 里为 null 的字段表示继承 base；" +
			"水域级覆盖（逐鱼种生态弹窗）优先级高于这一层。",
	})
}

func ptrF(v float64) *float64 { return &v }
func ptrI(v int64) *int64     { return &v }
func ptrS(v string) *string   { return &v }

// handleFishEcologySpeciesSave POST：body 即 store.FishEcologySpecies。
// 全字段为 nil 时等价于删除（回到资产基线），避免留一条语义为「无覆盖」的脏行。
func (s *Server) handleFishEcologySpeciesSave(w http.ResponseWriter, r *http.Request) {
	var o store.FishEcologySpecies
	if err := readJSON(r, &o); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	o.FishKey = strings.TrimSpace(o.FishKey)
	if o.FishKey == "" {
		httpError(w, 400, "必须指定鱼种")
		return
	}
	if msg := validateFishEcologySpecies(o); msg != "" {
		httpError(w, 400, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var n int
	if err := s.db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM species_weight WHERE fish_key=?`, o.FishKey).Scan(&n); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	if n == 0 {
		httpError(w, 400, "未知鱼种: "+o.FishKey)
		return
	}
	if o.Origin == "" {
		o.Origin = "manual"
	}
	if isEmptyFishEcologySpecies(o) {
		if err := s.db.DeleteFishEcologySpecies(ctx, o.FishKey); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_ecology_species_clear", o.FishKey, "all-fields-empty")
	} else {
		if err := s.db.UpsertFishEcologySpecies(ctx, o, adminOf(r)); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_ecology_species_save", o.FishKey,
			"fields="+strconvSpeciesFields(o))
	}
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_fish_ecology"}))
	writeJSON(w, map[string]any{"ok": true})
}

// handleFishEcologySpeciesReset POST：body {"fish_key":".."}，删除该鱼种的覆盖。
func (s *Server) handleFishEcologySpeciesReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FishKey string `json:"fish_key"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	req.FishKey = strings.TrimSpace(req.FishKey)
	if req.FishKey == "" {
		httpError(w, 400, "必须指定鱼种")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.DeleteFishEcologySpecies(ctx, req.FishKey); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_ecology_species_reset", req.FishKey, "reset-to-baseline")
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_fish_ecology"}))
	writeJSON(w, map[string]any{"ok": true})
}

// isEmptyFishEcologySpecies 判断是否所有可写字段都为空（= 无覆盖）。
// Origin/UpdatedBy/UpdatedTS 是元数据，不参与判断。
func isEmptyFishEcologySpecies(o store.FishEcologySpecies) bool {
	return o.LayerMin == nil && o.LayerPref == nil && o.LayerMax == nil &&
		o.TempMinC == nil && o.TempPrefC == nil && o.TempMaxC == nil &&
		o.DielMask == nil && o.DielCurve == nil && o.SeasonMask == nil &&
		o.DietJSON == nil && o.WeatherJSON == nil && o.ActionJSON == nil &&
		o.SubstrateJSON == nil && o.ColorJSON == nil &&
		o.CurrentPref == nil && o.Activity == nil && o.LightPref == nil &&
		o.ClarityPref == nil && o.Aggression == nil && o.Enabled == nil
}

// validateFishEcologySpecies 校验显式给出的字段（nil = 继承基线，不校验）。
// 口径与 fish_ecology_loc 一致，另含全部生态字段。
func validateFishEcologySpecies(o store.FishEcologySpecies) string {
	for _, f := range []struct {
		name string
		v    *float64
		min  float64
		max  float64
	}{
		{"栖息水层下限", o.LayerMin, 0, 1},
		{"栖息水层偏好", o.LayerPref, 0, 1},
		{"栖息水层上限", o.LayerMax, 0, 1},
		{"水温下限", o.TempMinC, -5, 45},
		{"水温偏好", o.TempPrefC, -5, 45},
		{"水温上限", o.TempMaxC, -5, 45},
		{"水流偏好", o.CurrentPref, 0, 1},
		{"活跃度", o.Activity, 0, 5},
		{"光照偏好", o.LightPref, 0, 1},
		{"清澈度偏好", o.ClarityPref, 0, 1},
		{"攻击性", o.Aggression, 0, 5},
	} {
		if f.v != nil && (*f.v < f.min || *f.v > f.max) {
			return fmt.Sprintf("%s 超出范围（%.0f~%.0f）", f.name, f.min, f.max)
		}
	}
	if o.LayerMin != nil && o.LayerMax != nil && *o.LayerMin > *o.LayerMax {
		return "栖息水层下限不能大于上限"
	}
	if o.LayerMin != nil && o.LayerPref != nil && *o.LayerMin > *o.LayerPref {
		return "栖息水层下限不能大于偏好"
	}
	if o.LayerMax != nil && o.LayerPref != nil && *o.LayerPref > *o.LayerMax {
		return "栖息水层偏好不能大于上限"
	}
	if o.TempMinC != nil && o.TempMaxC != nil && *o.TempMinC > *o.TempMaxC {
		return "水温下限不能大于上限"
	}
	if o.TempMinC != nil && o.TempPrefC != nil && *o.TempMinC > *o.TempPrefC {
		return "水温下限不能大于偏好温度"
	}
	if o.TempMaxC != nil && o.TempPrefC != nil && *o.TempPrefC > *o.TempMaxC {
		return "水温偏好不能大于上限"
	}
	for _, f := range []struct {
		name string
		v    *int64
	}{
		{"昼夜掩码", o.DielMask},
		{"季节掩码", o.SeasonMask},
	} {
		if f.v != nil && (*f.v < 0 || *f.v > 15) {
			return f.name + " 必须是 0~15 的位掩码"
		}
	}
	if o.Enabled != nil && *o.Enabled != 0 && *o.Enabled != 1 {
		return "启用标记只能是 0 或 1"
	}
	for _, f := range []struct {
		name string
		v    *string
	}{
		{"食性", o.DietJSON},
		{"天气", o.WeatherJSON},
		{"拟饵动作", o.ActionJSON},
		{"底质", o.SubstrateJSON},
		{"水色", o.ColorJSON},
	} {
		if f.v == nil {
			continue
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(*f.v), &probe); err != nil {
			return f.name + " 必须是 JSON 对象"
		}
	}
	if o.DielCurve != nil {
		raw := strings.TrimSpace(*o.DielCurve)
		if raw != "" {
			var w []float64
			if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w) != 4 {
				return `昼夜曲线必须是四个数字的 JSON 数组，如 [1,0.3,0.2,0.5]（顺序 夜,晨,昼,昏）`
			}
			for _, v := range w {
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
					return "昼夜曲线的每个值都必须在 0~1 之间"
				}
			}
		}
	}
	return ""
}

// strconvSpeciesFields 供审计日志记录本次覆盖了哪些字段（不记具体值）。
func strconvSpeciesFields(o store.FishEcologySpecies) string {
	names := make([]string, 0, 20)
	add := func(name string, set bool) {
		if set {
			names = append(names, name)
		}
	}
	add("layer", o.LayerMin != nil || o.LayerPref != nil || o.LayerMax != nil)
	add("temp", o.TempMinC != nil || o.TempPrefC != nil || o.TempMaxC != nil)
	add("diel", o.DielMask != nil)
	add("curve", o.DielCurve != nil)
	add("season", o.SeasonMask != nil)
	add("diet", o.DietJSON != nil)
	add("weather", o.WeatherJSON != nil)
	add("action", o.ActionJSON != nil)
	add("substrate", o.SubstrateJSON != nil)
	add("color", o.ColorJSON != nil)
	add("current", o.CurrentPref != nil)
	add("activity", o.Activity != nil)
	add("light", o.LightPref != nil)
	add("clarity", o.ClarityPref != nil)
	add("aggression", o.Aggression != nil)
	add("enabled", o.Enabled != nil)
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}
