package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"rf4server/internal/store"
)

// 生态档案「水域 × 鱼种」覆盖的后台读写。判鱼侧由 gameserver 的 fish_ecology.go 消费，
// 保存/删除后经管理队列推 reload_fish_ecology 主动失效缓存（reload_tunables 也会重载）。
//
// 语义：未填的字段继承全局 fish_ecology，所以只改水温窗口也能生效；
// 保存整行 = 该行显式给出的字段全部生效，缺失字段回到继承。

// handleFishEcologyLoc GET：?loc= 返回该水域全部覆盖行。
// 额外带 &fish_key= 时一并返回该鱼种的全局档案，供弹窗把「继承」显示成
// 实际生效值——否则界面只有「继承」两个字，看不出这张图当前用的是哪组参数。
func (s *Server) handleFishEcologyLoc(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	loc := strings.TrimSpace(q.Get("loc"))
	fishKey := strings.TrimSpace(q.Get("fish_key"))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := s.db.ListFishEcologyLoc(ctx, loc)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	resp := map[string]any{"overrides": rows}
	if fishKey != "" {
		global, err := s.globalFishEcology(ctx, fishKey)
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		resp["global"] = global
	}
	writeJSON(w, resp)
}

// ecoGlobalView 是全局档案里与会话覆盖同名的字段。diet/weather 等 JSON 列不传：
// 弹窗不编辑它们，多传只会让响应变大。
type ecoGlobalView struct {
	LayerMin   *float64 `json:"layer_min"`
	LayerPref  *float64 `json:"layer_pref"`
	LayerMax   *float64 `json:"layer_max"`
	TempMinC   *float64 `json:"temp_min_c"`
	TempPrefC  *float64 `json:"temp_pref_c"`
	TempMaxC   *float64 `json:"temp_max_c"`
	DielMask   *int64   `json:"diel_mask"`
	DielCurve  *string  `json:"diel_curve"`
	SeasonMask *int64   `json:"season_mask"`
	Activity   *float64 `json:"activity"`
	Enabled    *int64   `json:"enabled"`
}

// globalFishEcology 读该鱼种的全局档案；没有该行时返回 (nil, nil)，
// 前端据此退回「继承」字样（与判鱼侧 defaultFishEcology 兜底同义）。
func (s *Server) globalFishEcology(ctx context.Context, fishKey string) (*ecoGlobalView, error) {
	var (
		lm, lp, lmax, tmin, tpref, tmax, act sql.NullFloat64
		diel, season, enabled                sql.NullInt64
		dielCurve                            sql.NullString
	)
	err := s.db.SQL.QueryRowContext(ctx, `
		SELECT layer_min, layer_pref, layer_max, temp_min_c, temp_pref_c, temp_max_c,
		       diel_mask, diel_curve, season_mask, activity, enabled
		FROM fish_ecology WHERE fish_key=?`, fishKey).Scan(
		&lm, &lp, &lmax, &tmin, &tpref, &tmax, &diel, &dielCurve, &season, &act, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fp := func(n sql.NullFloat64) *float64 {
		if !n.Valid {
			return nil
		}
		v := n.Float64
		return &v
	}
	ip := func(n sql.NullInt64) *int64 {
		if !n.Valid {
			return nil
		}
		v := n.Int64
		return &v
	}
	sp := func(n sql.NullString) *string {
		if !n.Valid {
			return nil
		}
		v := n.String
		return &v
	}
	return &ecoGlobalView{
		LayerMin: fp(lm), LayerPref: fp(lp), LayerMax: fp(lmax),
		TempMinC: fp(tmin), TempPrefC: fp(tpref), TempMaxC: fp(tmax),
		DielMask: ip(diel), DielCurve: sp(dielCurve), SeasonMask: ip(season),
		Activity: fp(act), Enabled: ip(enabled),
	}, nil
}

// handleFishEcologyLocSave POST：body 即 store.FishEcologyLoc（同键覆盖）。
func (s *Server) handleFishEcologyLocSave(w http.ResponseWriter, r *http.Request) {
	var o store.FishEcologyLoc
	if err := readJSON(r, &o); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	o.StatLoc = strings.TrimSpace(o.StatLoc)
	o.FishKey = strings.TrimSpace(o.FishKey)
	if o.StatLoc == "" || o.FishKey == "" {
		httpError(w, 400, "水域与鱼种都必须指定")
		return
	}
	if msg := validateFishEcologyLoc(o); msg != "" {
		httpError(w, 400, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// 拼错鱼种会得到一条永不命中的覆盖行，且后台看不出异常——逐个核对鱼库。
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
	if err := s.db.UpsertFishEcologyLoc(ctx, o, adminOf(r)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_ecology_loc_save", o.StatLoc,
		"fish="+o.FishKey+" fields="+strconvFields(o))
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_fish_ecology"}))
	writeJSON(w, map[string]any{"ok": true})
}

// handleFishEcologyLocDelete POST：body {"stat_loc":"..","fish_key":".."}。
func (s *Server) handleFishEcologyLocDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StatLoc string `json:"stat_loc"`
		FishKey string `json:"fish_key"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	req.StatLoc = strings.TrimSpace(req.StatLoc)
	req.FishKey = strings.TrimSpace(req.FishKey)
	if req.StatLoc == "" || req.FishKey == "" {
		httpError(w, 400, "水域与鱼种都必须指定")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.DeleteFishEcologyLoc(ctx, req.StatLoc, req.FishKey); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_ecology_loc_delete", req.StatLoc, "fish="+req.FishKey)
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_fish_ecology"}))
	writeJSON(w, map[string]any{"ok": true})
}

// validateFishEcologyLoc 只校验显式给出的字段（nil = 继承，不校验）。
// 值与 store/fish_ecology 的列口径一致：水层 0~1、温度 -5~45℃、掩码 0~15。
func validateFishEcologyLoc(o store.FishEcologyLoc) string {
	for _, f := range []struct {
		name string
		v    *float64
		min  float64
		max  float64
	}{
		{"栖息水层下限", o.LayerMin, 0, 1},
		{"栖息水层偏好", o.LayerPref, 0, 1},
		{"栖息水层上限", o.LayerMax, 0, 1},
		{"温度下限", o.TempMinC, -5, 45},
		{"温度偏好", o.TempPrefC, -5, 45},
		{"温度上限", o.TempMaxC, -5, 45},
		{"活跃度", o.Activity, 0, 5},
	} {
		if f.v != nil && (*f.v < f.min || *f.v > f.max) {
			return fmt.Sprintf("%s 超出范围（%.0f~%.0f）", f.name, f.min, f.max)
		}
	}
	if o.LayerMin != nil && o.LayerMax != nil && *o.LayerMin > *o.LayerMax {
		return "栖息水层下限不能大于上限"
	}
	if o.TempMinC != nil && o.TempMaxC != nil && *o.TempMinC > *o.TempMaxC {
		return "温度下限不能大于上限"
	}
	if o.TempMinC != nil && o.TempPrefC != nil && *o.TempMinC > *o.TempPrefC {
		return "温度下限不能大于偏好温度"
	}
	if o.TempMaxC != nil && o.TempPrefC != nil && *o.TempPrefC > *o.TempMaxC {
		return "偏好温度不能大于温度上限"
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
	} {
		if f.v == nil {
			continue
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(*f.v), &probe); err != nil {
			return f.name + " 必须是 JSON 对象"
		}
	}
	// 昼夜曲线：空串 = 该水域该鱼种回退到位图判断；否则必须是四段 0~1 的数组，
	// 顺序固定 [夜,晨,昼,昏]，与判鱼侧 dielCurveWeights 同口径。
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

// strconvFields 供审计日志记录本次覆盖了哪些字段（不记具体值，避免日志膨胀）。
func strconvFields(o store.FishEcologyLoc) string {
	names := make([]string, 0, 12)
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
	add("activity", o.Activity != nil)
	add("enabled", o.Enabled != nil)
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}
