package gameserver

import (
	"fmt"
	"math"
	"time"
)

// 区域以玩家世界位置划分；缺位置时使用客户端场景与钓点编号。
func fishArea(loc string, c fishRollContext) string {
	if c.hasPos && !math.IsNaN(c.x) && !math.IsNaN(c.z) && !math.IsInf(c.x, 0) && !math.IsInf(c.z, 0) {
		return fmt.Sprintf("%s/%.0f/%.0f", loc, math.Floor(c.x/40), math.Floor(c.z/40))
	}
	return fmt.Sprintf("%s/%d", loc, c.spot)
}

// distributionMultiplier 只回答一件事：这种鱼这一刻在不在这块水域。
// 空间（40 世界单位一格）× 时间（每四个游戏小时重抽）的纯散布乘子，
// 返回 0 表示这一格这段时辰根本没有这种鱼；命中时按 0.15~4.0 给权重。
//
// 昼夜习性不在这里。原先此处另挂了一张写死 9 种鱼的活性表（夜行 ×1.8、
// 白天鱼 ×1.4），与生态档案的 diel_mask 是两套参数：既会互相抵消（分布的
// 0~4 倍把昼夜那 1.67 倍淹掉），口径还互相打架——董氏须鳅（barb_toni）是
// 底栖夜行鱼，掩码为纯夜，那张表却把它当白天鱼。2026-11-18 起删除该表，
// 昼夜只由 fishDielWeight（生态档案 diel_mask × 活跃加成）一处决定。
func distributionMultiplier(area, species string, now time.Time) float64 {
	// 每四个游戏小时重新分布；同区域同鱼种共享结果，不因玩家或换饵重抽。
	period := (now.Unix() - worldDayEpochUnix) / (4 * realSecPerGameHour)
	v := fishVariation(fmt.Sprintf("%s/%s/%d", area, species, period))
	if v < 0.3 {
		return 0
	}
	return 0.15 + 3.85*v*v
}

func applyFishDistribution(loc string, keys []string, weights []float64, c fishRollContext) {
	area := fishArea(loc, c)
	for i, key := range keys {
		m := distributionMultiplier(area, key, c.now)
		// 保底杂口：本时段本格散布为 0 的鱼保底 0.15，避免连续空杆死寂；
		// 仅 pityEnv 重掷路径生效，正常判鱼不动。
		if m == 0 && c.pityEnv {
			m = 0.15
		}
		weights[i] *= m
	}
}
