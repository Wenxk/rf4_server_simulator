package admin

import (
	"context"
	"strconv"
	"time"

	"rf4server/internal/store"
	"rf4server/internal/worldclock"
)

// 鱼口页的昼夜展示。
//
// 「基础权重」是全天不变的静态值，玩家在后台看到它，自然以为这条鱼一直这么常见；
// 实际判鱼时权重还要乘一个昼夜系数（夜行鱼夜里 ×2、白天 ×0.3）。这里把那个系数
// 按**当前游戏时刻**算出来一起下发，页面就能写「10 → 夜 20」而不是只写「10」。
//
// 系数口径必须和判鱼侧完全一致：段划分、专一度折算都走 internal/worldclock，
// 否则后台写着 ×2、判鱼实际乘 1.3，调参就变成盲调。

// dielTunables 是昼夜层的三个运行参数。
type dielTunables struct {
	Enabled bool
	Peak    float64 // 专性最强（1 段）鱼的活跃时段加成
	Penalty float64 // 非活跃时段压制
}

// 与 gameserver 的 tunableF 默认值保持一致：改默认值要两边一起改。
var dielTunableDefaults = dielTunables{Enabled: true, Peak: 2.0, Penalty: 0.3}

func (s *Server) dielWeightTunables(ctx context.Context) dielTunables {
	out := dielTunableDefaults
	kv, err := s.db.TunableList(ctx)
	if err != nil {
		return out
	}
	if v, ok := tunableNum(kv, "fish_diel_weight_enabled"); ok {
		out.Enabled = v == 1
	}
	if v, ok := tunableNum(kv, "fish_diel_active_boost"); ok && v > 0 {
		out.Peak = v
	}
	if v, ok := tunableNum(kv, "fish_diel_inactive_penalty"); ok && v > 0 {
		out.Penalty = v
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
	cfg.DielPeakBoost = t.Peak
	cfg.DielPenalty = t.Penalty

	for i := range cfg.Fish {
		f := &cfg.Fish[i]
		active := f.DielMask&(1<<uint(seg)) != 0
		f.DielActive = active
		factor := 1.0
		if t.Enabled {
			if active {
				factor = worldclock.ActiveBoost(f.DielMask, t.Peak)
			} else {
				factor = t.Penalty
			}
		}
		f.DielFactor = factor
		f.EffectiveWeight = f.Weight * factor
	}
}
