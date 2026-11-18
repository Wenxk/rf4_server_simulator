package admin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"rf4server/internal/store"
)

// 基础权重的稀有度档位预设。
//
// 鱼口页的「基础权重」原来来自 loc_fish.catches（攻略站渔获数），各图尺度差十倍以上：
// 琥珀湖 1~15、铜湖 1~199。档位化之后跨图同档同权重，水域差异交给名册表达。
//
// 存储是独立表 fish_weight_preset（loc_fish 是只读静态视图，改不了），由 LocRoster
// 在读取时优先取用，所以判鱼链路一行都不用改。清空表即回滚到攻略原值。

// defaultFishWeightTiers 预设表被清空时给后台展示的默认值，
// 与仓库里常见配法一致（20:8:3:1）。
var defaultFishWeightTiers = [4]float64{100, 40, 15, 5}

// currentFishWeightTiers 读当前生效的档位：以预设表为准，表空时退回默认。
func (s *Server) currentFishWeightTiers(ctx context.Context) ([4]float64, bool) {
	w, ok, err := s.db.FishWeightPresetWeights(ctx)
	if err != nil || !ok {
		return defaultFishWeightTiers, false
	}
	out := defaultFishWeightTiers
	for i := range out {
		if w[i] > 0 {
			out[i] = w[i]
		}
	}
	return out, true
}

// handleFishWeightPresetGet GET：预览当前档位与影响面，不写库。
// 前端在确认弹窗里展示「这档有多少鱼 / 多少行 / 攻略原值平均多少」，
// 避免点下去才发现某档鱼全被压成一条线。
func (s *Server) handleFishWeightPresetGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	weights, active := s.currentFishWeightTiers(ctx)
	stats, err := s.db.FishWeightPresetStats(ctx, weights)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	var rows int64
	for _, st := range stats {
		rows += st.Rows
	}
	writeJSON(w, map[string]any{
		"weights": weights[:],
		"tiers":   stats,
		"rows":    rows,
		"active":  active, // false = 预设表为空，当前走的是攻略站原值
	})
}

// handleFishWeightPresetSave POST：按档位重设全部水域基础权重。
func (s *Server) handleFishWeightPresetSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tier0 *float64 `json:"tier0"`
		Tier1 *float64 `json:"tier1"`
		Tier2 *float64 `json:"tier2"`
		Tier3 *float64 `json:"tier3"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	weights, _ := s.currentFishWeightTiers(ctx)
	for i, v := range []*float64{req.Tier0, req.Tier1, req.Tier2, req.Tier3} {
		if v == nil {
			continue
		}
		if *v < 0.1 || *v > 100000 {
			httpError(w, 400, fmt.Sprintf("%s 档权重超出范围（0.1~100000）", store.FishWeightTierLabel(i)))
			return
		}
		weights[i] = *v
	}
	// 四档全相同等于取消稀有度分层，多半是手误：提前拦一下。
	same := true
	for i := 1; i < len(weights); i++ {
		if weights[i] != weights[0] {
			same = false
			break
		}
	}
	if same {
		httpError(w, 400, "四档权重不能完全相同：那样稀有度就没有区别了")
		return
	}

	res, err := s.db.PresetFishBaseWeights(ctx, weights)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_weight_preset", "all",
		fmt.Sprintf("tiers=%v", weights))
	writeJSON(w, map[string]any{
		"ok": true, "weights": weights[:], "updated": res.Updated, "tiers": res.Tiers,
	})
}

// handleFishWeightRestore POST：清空预设，基础权重回到攻略站原值。
func (s *Server) handleFishWeightRestore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	n, err := s.db.ClearFishWeightPreset(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "fish_weight_restore", "all", fmt.Sprintf("rows=%d", n))
	writeJSON(w, map[string]any{"ok": true, "restored": n})
}
