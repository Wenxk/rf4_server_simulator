package admin

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"rf4server/internal/store"
	"rf4server/internal/worldclock"
)

// 鱼口页的昼夜展示。
//
// 昼夜系数是**乘性权重**：四段曲线 `[夜,晨,昼,昏]` 的峰值恒为 1.0、最低钳到 0.08，
// 只压低不抬高。玩家在后台看到「基础权重 10」，实际这一杆的权重是
// 10 × 当前时段系数；这里把系数按**当前游戏时刻**算出来一起下发，
// 页面就能写「10 → 6.7（昼 ×0.67）」而不是只写「10」。
//
// 系数口径必须和判鱼侧完全一致：段划分走 worldclock.SegmentOf，曲线取段走
// 同一组 `[夜,晨,昼,昏]` 下标，两边各写一套迟早会漂（曾经就漂过一次位序）。

// dielCurveMin 是曲线的最低系数，与 gameserver.fishDielCurveMin 同值。
// 后台只用于展示「最低会压到多少」，不参与判定。
const dielCurveMin = 0.08

// dielMaskFallbackMiss 是「没有曲线」时位图未命中段的压制值，与判鱼侧同口径。
const dielMaskFallbackMiss = 0.35

// dielCurveWeights 解析四段曲线；空串/非四段/非法 JSON 返回 nil（回退位图）。
func dielCurveWeights(raw string) []float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var w []float64
	if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w) != 4 {
		return nil
	}
	for i := range w {
		w[i] = math.Max(0, math.Min(1, w[i]))
	}
	return w
}

// dielTunables 是昼夜层的运行参数。曲线模型下只剩一个开关：
// 峰值与最低值由数据本身决定（1.0 / 0.08），不再对外暴露可调倍率。
type dielTunables struct {
	Enabled bool
}

var dielTunableDefaults = dielTunables{Enabled: true}

func (s *Server) dielWeightTunables(ctx context.Context) dielTunables {
	out := dielTunableDefaults
	kv, err := s.db.TunableList(ctx)
	if err != nil {
		return out
	}
	if v, ok := tunableNum(kv, "fish_diel_weight_enabled"); ok {
		out.Enabled = v == 1
	}
	return out
}

func tunableNum(kv map[string]string, key string) (float64, bool) {
	raw, ok := kv[key]
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// applyDielWeightView 按当前游戏时刻，给每条鱼补上「是否活跃 / 昼夜系数 / 生效权重」。
func applyDielWeightView(cfg *store.AdminFishConfig, t dielTunables) {
	if cfg == nil {
		return
	}
	day, hour := worldclock.DayAndHour(time.Now().Unix())
	seg := worldclock.SegmentOf(hour)
	cfg.GameDay, cfg.GameHour = day, hour
	cfg.DielNowSegment = seg
	cfg.DielNowName = worldclock.SegmentName(seg)
	cfg.DielWeightEnabled = t.Enabled
	cfg.DielCurveMin = dielCurveMin

	for i := range cfg.Fish {
		f := &cfg.Fish[i]
		curve := dielCurveWeights(f.DielCurve)
		active := f.DielMask&(1<<uint(seg)) != 0
		f.DielActive = active
		factor := 1.0
		if t.Enabled {
			if curve != nil {
				// 曲线口径：按当前段取权重，下限 0.08、上限 1。
				factor = math.Max(dielCurveMin, math.Min(1, curve[seg]))
				// 「活跃」在曲线下没有二元语义，用 0.5 分界给页面一个可读的标记。
				f.DielActive = factor >= 0.5
			} else if active {
				factor = 1
			} else {
				factor = dielMaskFallbackMiss
			}
		}
		f.DielFactor = factor
		f.EffectiveWeight = f.Weight * factor
	}
}
