package store

import "context"

// 鱼种稀有度是**鱼种固有属性**，不是钓获量的函数。
//
// 权威来源是静态库 server_data/static.sqlite3 的 species_rarity 表（rf4db 抓取，
// 253 条）：0 普通 / 1 稀有 / 2 超稀有。它回答的是「这条鱼本身有多难遇到」，
// 与攻略站登记的全服渔获数、本服测试期钓了多少条都无关——那些量反映的是玩家
// 登记意愿和地图热门程度。反例：剑鱼在挪威海占 1.2% 的渔获却被算成「常规」，
// 锦鲤变种没人登记被算成「极罕见」。
//
// 曾按 species_grade.catches 的绝对阈值分四档（常规/少见/罕见/极罕见）重算过一版，
// 已整体移除：用渔获量反推稀缺度这条思路本身就是错的，档位阈值表、重算接口、
// 后台弹窗都跟着一起删掉了。
//
// fish_ecology.rarity_tier 是这张属性表在本地的投影（判鱼权重、档位预设、后台
// 展示都读它），启动时用 SyncFishRarityFromStatic 对齐一次，幂等。

// 稀有度档位（数值与 species_rarity.rarity 一致）。
const (
	FishRarityCommon    = 0
	FishRarityRare      = 1
	FishRaritySuperRare = 2
)

// FishRarityTiers 稀有度档位数（普通/稀有/超稀有）。
const FishRarityTiers = 3

// FishRarityLabel 稀有度中文名。
func FishRarityLabel(r int) string {
	switch r {
	case FishRarityRare:
		return "稀有"
	case FishRaritySuperRare:
		return "超稀有"
	}
	return "普通"
}

// SyncFishRarityFromStatic 把静态库 species_rarity 的固有稀有度同步进
// fish_ecology.rarity_tier，返回值是实际改动的行数（幂等，重复调用为 0）。
//
// 静态表里没有条目的鱼保持原值（缺省 0=普通）：没有依据就不改，也不能按
// 「0 条渔获」推断档位。只同步有变化的行，避免每次启动都写 253 行。
func (db *DB) SyncFishRarityFromStatic(ctx context.Context) (int64, error) {
	if db == nil || db.SQL == nil {
		return 0, nil
	}
	res, err := db.SQL.ExecContext(ctx, `
		UPDATE fish_ecology
		SET rarity_tier = (
			SELECT s.rarity FROM species_rarity s WHERE s.fish_key = fish_ecology.fish_key
		)
		WHERE EXISTS (SELECT 1 FROM species_rarity s WHERE s.fish_key = fish_ecology.fish_key)
		  AND rarity_tier IS NOT (
			SELECT s.rarity FROM species_rarity s WHERE s.fish_key = fish_ecology.fish_key
		  )`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
