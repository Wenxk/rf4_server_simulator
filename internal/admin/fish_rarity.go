package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"rf4server/internal/store"
)

// 稀有度分档口径的运行时调整。
//
// 背景：鱼种稀有度原本是生成 fish_ecology_defs.json 时按 species_grade.catches
// 降序四分位写死的，每档强制 25%。结果一张 26 种鱼的图上必有 6~7 种被打成「极罕见」，
// 与实际好不好钓无关——琥珀湖鳞鲤 15 条、占该图 11%、排第 1，却是罕见（权重 15）；
// 普通鲤鱼全服 58 条落在常规（权重 100），同一张图里权重方向是反的。
//
// 现在改成按全服渔获总数的绝对阈值分档，长尾分布（默认 44/35/16/9%）。依据的
// species_grade 属于 StaticTables，只由 seed 从攻略站覆盖，**与本地游戏进度无关**：
// 玩家测试期怎么调权重、钓上多少条，都不会污染这个分档依据。
//
// 保存时可选一并重设基础权重（reset_weights），因为改完档位如果不跟着重设，
// 库里的基础权重还是按旧档位写的，看起来像「改了没生效」。

// handleFishRarityGet GET：当前阈值 + 各档分布。带 cut0/cut1/cut2 时按给定阈值预览。
func (s *Server) handleFishRarityGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cuts, active, err := s.db.FishRarityThresholds(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	preview := cuts
	if v, ok := queryCuts(r); ok {
		preview = v
	}
	stats, err := s.db.FishRarityPreview(ctx, preview)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	weights, weightsActive := s.currentFishWeightTiers(ctx)
	writeJSON(w, map[string]any{
		"cuts":           cuts[:],
		"preview_cuts":   preview[:],
		"default_cuts":   store.DefaultFishRarityCuts[:],
		"active":         active, // false = 阈值表为空，走的是默认值
		"tiers":          stats,
		"weights":        weights[:],
		"weights_active": weightsActive,
	})
}

// handleFishRaritySave POST：保存阈值 → 重算档位 →（可选）按档位重设基础权重。
func (s *Server) handleFishRaritySave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cut0 *int64 `json:"cut0"`
		Cut1 *int64 `json:"cut1"`
		Cut2 *int64 `json:"cut2"`
		// ResetWeights 为 true 时，重算档位后按当前档位权重重新写一遍基础权重。
		ResetWeights bool `json:"reset_weights"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cuts, _, err := s.db.FishRarityThresholds(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	for i, v := range []*int64{req.Cut0, req.Cut1, req.Cut2} {
		if v != nil {
			cuts[i] = *v
		}
	}
	norm, ok := store.NormalizeFishRarityCuts(cuts)
	if !ok {
		httpError(w, 400, "阈值必须严格递减（常规 > 少见 > 罕见）且都不小于 1")
		return
	}

	res, err := s.db.RecalcFishRarity(ctx, norm)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"ok": true, "cuts": norm[:], "changed": res.Changed,
		"total": res.Total, "skipped": res.Skipped, "tiers": res.Tiers,
	}
	audit := fmt.Sprintf("cuts=%v changed=%d", norm, res.Changed)
	if req.ResetWeights {
		weights, _ := s.currentFishWeightTiers(ctx)
		wres, werr := s.db.PresetFishBaseWeights(ctx, weights)
		if werr != nil {
			httpError(w, 500, werr.Error())
			return
		}
		out["weights"] = weights[:]
		out["updated"] = wres.Updated
		audit += fmt.Sprintf(" weights=%v rows=%d", weights, wres.Updated)
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_rarity", "all", audit)
	writeJSON(w, out)
}

// handleFishRarityRestore POST：阈值回到默认 30/14/7 并重算，
// 附加 reset_weights=true 时同时把基础权重按档位重设一遍。
func (s *Server) handleFishRarityRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResetWeights bool `json:"reset_weights"`
	}
	_ = readJSON(r, &req)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cuts := store.DefaultFishRarityCuts
	res, err := s.db.RecalcFishRarity(ctx, cuts)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"ok": true, "cuts": cuts[:], "changed": res.Changed,
		"total": res.Total, "skipped": res.Skipped, "tiers": res.Tiers,
	}
	if req.ResetWeights {
		weights, _ := s.currentFishWeightTiers(ctx)
		wres, werr := s.db.PresetFishBaseWeights(ctx, weights)
		if werr != nil {
			httpError(w, 500, werr.Error())
			return
		}
		out["weights"] = weights[:]
		out["updated"] = wres.Updated
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_rarity_restore", "all",
		fmt.Sprintf("cuts=%v changed=%d", cuts, res.Changed))
	writeJSON(w, out)
}

// queryCuts 解析 ?cut0=&cut1=&cut2=，三个都齐且合法才采用，否则返回 ok=false。
func queryCuts(r *http.Request) ([3]int64, bool) {
	q := r.URL.Query()
	var out [3]int64
	for i, key := range []string{"cut0", "cut1", "cut2"} {
		raw := q.Get(key)
		if raw == "" {
			return out, false
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return out, false
		}
		out[i] = v
	}
	return store.NormalizeFishRarityCuts(out)
}
