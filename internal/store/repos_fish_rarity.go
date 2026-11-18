package store

import (
	"context"
	"fmt"
	"time"
)

// 稀有度分档重算：按 species_grade.catches 的绝对阈值重写 fish_ecology.rarity_tier。
//
// 为什么不再用「四分位」：生成生态档案时用的是 species_grade.catches 降序四分位，
// 每档强制 25%。一张只有 26 种鱼的图也必有 6~7 种被打成「极罕见」，这纯属配额，
// 与那条鱼好不好钓无关。实测反例：琥珀湖鳞鲤 15 条、占该图 11%、排第 1，却是罕见；
// 而普通鲤鱼全服 58 条落在常规（权重 100）——同一张图里权重方向反了。
//
// 为什么依据是 species_grade 而不是别的：
//   - loc_fish 是按图分摊的攻略渔获，各图总量差十几倍（琥珀湖 136 / 挪威海 1503），
//     同一鱼种在不同图的名次还互相矛盾（欧鳊在小河里排第 1，全服榜上却极罕见）。
//     鱼种级稀有度只能用跨图可比的量，所以用全服渔获总数。
//   - catch_log / catch_seen 是玩家自己的渔获记录。测试期为了刷稀有鱼人为调高过
//     权重，这份数据已经被污染，不能用来定档。
//   - species_grade 属于 StaticTables，只由 seed 从攻略站覆盖，与本地游戏进度无关，
//     也不受任何后台操作影响——这是唯一「鱼种本身」的、干净的稀有度依据。
//
// 分布形态：默认阈值 30/14/7 下是 44% / 35% / 16% / 9%，典型长尾。稀有档只留给
// 真正冷门的鱼，不再有「每档必须占四分之一」的配额。

// DefaultFishRarityCuts 默认三档下限（常规 / 少见 / 罕见），与 00073 迁移保持一致。
// 极罕见没有下限，是「低于第三档」的兜底。
var DefaultFishRarityCuts = [3]int64{30, 14, 7}

// FishRarityTierStat 一档的分档现状与重算后结果，用于后台预览。
type FishRarityTierStat struct {
	Tier       int    `json:"tier"`
	Label      string `json:"label"`
	MinCatches int64  `json:"min_catches"` // 进入该档的最小全服渔获数；极罕见恒为 0
	Species    int64  `json:"species"`     // 重算后该档鱼种数
	Before     int64  `json:"before"`      // 重算前该档鱼种数
	MinObs     int64  `json:"min_obs"`     // 重算后该档实际最小全服渔获数
	MaxObs     int64  `json:"max_obs"`     // 重算后该档实际最大全服渔获数
}

// FishRarityRecalcResult 一次重算的执行结果。
type FishRarityRecalcResult struct {
	Changed int64                `json:"changed"` // 实际换了档的鱼种数
	Total   int64                `json:"total"`   // 参与重算的鱼种数（有全服渔获数据的）
	Skipped int64                `json:"skipped"` // 无全服数据、保持原档的鱼种数
	Tiers   []FishRarityTierStat `json:"tiers"`
}

// fishRarityRow 重算的输入行：当前档位 + 全服渔获数（-1 表示没有数据）。
type fishRarityRow struct {
	FishKey string
	Tier    int
	Catches int64
}

// NormalizeFishRarityCuts 校验并规范化阈值：必须严格递减且都 >= 1。
// 返回 false 时调用方应拒绝保存，避免出现「罕见下限高于少见」这类不可解释的分档。
func NormalizeFishRarityCuts(cuts [3]int64) ([3]int64, bool) {
	for i := range cuts {
		if cuts[i] < 1 || cuts[i] > 1_000_000 {
			return cuts, false
		}
		if i > 0 && cuts[i] >= cuts[i-1] {
			return cuts, false
		}
	}
	return cuts, true
}

// FishRarityTierFor 按阈值把一个全服渔获数落到档位。catches < 0 表示无数据。
func FishRarityTierFor(catches int64, cuts [3]int64) (int, bool) {
	if catches < 0 {
		return 0, false
	}
	switch {
	case catches >= cuts[0]:
		return 0, true
	case catches >= cuts[1]:
		return 1, true
	case catches >= cuts[2]:
		return 2, true
	}
	return 3, true
}

// FishRarityThresholds 读当前生效的三档下限。ok=false 表示表为空、用的是默认值。
func (db *DB) FishRarityThresholds(ctx context.Context) ([3]int64, bool, error) {
	out := DefaultFishRarityCuts
	if db == nil || db.SQL == nil {
		return out, false, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `SELECT tier, min_catches FROM fish_rarity_threshold`)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	var seen bool
	for rows.Next() {
		var tier int
		var v int64
		if err := rows.Scan(&tier, &v); err != nil {
			return out, false, err
		}
		if tier >= 0 && tier < len(out) && v > 0 {
			out[tier] = v
			seen = true
		}
	}
	return out, seen, rows.Err()
}

// scanFishRarity 读全量「鱼种 → 当前档位 + 全服渔获数」。
// species_grade 在生产是静态视图，这里只读；LEFT JOIN 落空的鱼会被标成 -1，
// 重算时保持原档不动——没有依据就不改，比按 0 条算成极罕见更安全。
func (db *DB) scanFishRarity(ctx context.Context) ([]fishRarityRow, error) {
	if db == nil || db.SQL == nil {
		return nil, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `
		SELECT e.fish_key, COALESCE(e.rarity_tier, 0), COALESCE(g.catches, -1)
		FROM fish_ecology e
		LEFT JOIN species_grade g ON g.fish_key = e.fish_key
		ORDER BY e.fish_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]fishRarityRow, 0, 256)
	for rows.Next() {
		var r fishRarityRow
		if err := rows.Scan(&r.FishKey, &r.Tier, &r.Catches); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// summarizeFishRarity 把重算结果按档汇总成后台可读的统计。
func summarizeFishRarity(rows []fishRarityRow, cuts [3]int64) ([]FishRarityTierStat, int64, int64) {
	stats := make([]FishRarityTierStat, 4)
	for i := range stats {
		stats[i] = FishRarityTierStat{Tier: i, Label: FishWeightTierLabel(i), MinObs: -1, MaxObs: -1}
	}
	stats[0].MinCatches, stats[1].MinCatches, stats[2].MinCatches = cuts[0], cuts[1], cuts[2]
	stats[3].MinCatches = 0
	var total, skipped int64
	for _, r := range rows {
		stats[clampTier(r.Tier)].Before++
		newTier, ok := FishRarityTierFor(r.Catches, cuts)
		if !ok {
			skipped++
			continue
		}
		total++
		s := &stats[newTier]
		s.Species++
		if s.MinObs < 0 || r.Catches < s.MinObs {
			s.MinObs = r.Catches
		}
		if r.Catches > s.MaxObs {
			s.MaxObs = r.Catches
		}
	}
	return stats, total, skipped
}

// FishRarityPreview 只读预览：按给定阈值算出各档鱼种数，不写库。
func (db *DB) FishRarityPreview(ctx context.Context, cuts [3]int64) ([]FishRarityTierStat, error) {
	rows, err := db.scanFishRarity(ctx)
	if err != nil {
		return nil, err
	}
	stats, _, _ := summarizeFishRarity(rows, cuts)
	return stats, nil
}

// RecalcFishRarity 按阈值重写 rarity_tier，并把阈值一并落库。
//
// 阈值与档位必须在同一个事务里更新：两者若不一致，后台显示的「这一档是 7 条以上」
// 和库里实际的档位就会对不上，后面调权重会一头雾水。
//
// 幂等：重复调用不产生额外写入（只 UPDATE 真正换档的行）。启动时调用可以保证
// 生态档案被 seed 重灌（profile_version 提升）之后仍维持这套口径。
func (db *DB) RecalcFishRarity(ctx context.Context, cuts [3]int64) (FishRarityRecalcResult, error) {
	var res FishRarityRecalcResult
	cuts, ok := NormalizeFishRarityCuts(cuts)
	if !ok {
		return res, fmt.Errorf("稀有度阈值必须严格递减且不小于 1：%v", cuts)
	}
	rows, err := db.scanFishRarity(ctx)
	if err != nil {
		return res, err
	}
	stats, total, skipped := summarizeFishRarity(rows, cuts)

	now := float64(time.Now().UnixNano()) / 1e9
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	for tier, min := range cuts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fish_rarity_threshold(tier, min_catches, updated_ts) VALUES(?,?,?)
			ON CONFLICT(tier) DO UPDATE SET min_catches=excluded.min_catches, updated_ts=excluded.updated_ts`,
			tier, min, now); err != nil {
			return res, fmt.Errorf("写入 %s 档下限: %w", FishWeightTierLabel(tier), err)
		}
	}
	for _, r := range rows {
		newTier, ok := FishRarityTierFor(r.Catches, cuts)
		if !ok || newTier == r.Tier {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE fish_ecology SET rarity_tier=? WHERE fish_key=?`, newTier, r.FishKey); err != nil {
			return res, fmt.Errorf("更新 %s 档位: %w", r.FishKey, err)
		}
		res.Changed++
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.Total, res.Skipped, res.Tiers = total, skipped, stats
	return res, nil
}

// clampTier 防御异常档位值，避免越界写崩 summarize。
func clampTier(t int) int {
	if t < 0 {
		return 0
	}
	if t > 3 {
		return 3
	}
	return t
}
