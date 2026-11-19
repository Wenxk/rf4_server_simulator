package store

import (
	"context"
	"fmt"
	"time"
)

// 基础权重档位化：按 fish_ecology.rarity_tier（= species_rarity 的固有稀有度，
// 普通/稀有/超稀有）给所有水域一套统一的权重基准。
//
// 为什么不写 loc_fish.catches：loc_fish 属于 StaticTables，运行时被建成指向只读
// 静态库的 TEMP VIEW，并有 authorizer 拦写。攻略原值必须原样保留，所以预设放在
// 独立的运行时表 fish_weight_preset，由 LocRoster 优先读取。
//
// 清空该表即回滚到攻略原值；后台手工覆盖（fish_weight_override）仍是最外层，优先于它。

// FishWeightTierStat 一档稀有度的现状与预设值，用于后台确认弹窗。
type FishWeightTierStat struct {
	Tier    int     `json:"tier"`
	Label   string  `json:"label"`
	Weight  float64 `json:"weight"`   // 当前生效的档位权重
	Species int64   `json:"species"`  // 该档鱼种数（全服）
	Rows    int64   `json:"rows"`     // 影响到的「水域 × 鱼种」行数
	AvgBase float64 `json:"avg_base"` // 攻略站原值在该档的平均渔获数（对比用）
}

// FishWeightPresetResult 一次重设的执行结果。
type FishWeightPresetResult struct {
	Updated int64                `json:"updated"`
	Tiers   []FishWeightTierStat `json:"tiers"`
}

// FishWeightPresetWeights 读当前预设的 3 档权重；表为空或某档缺失时该档为 0。
// 返回的切片下标即 rarity_tier（0 普通 / 1 稀有 / 2 超稀有）。
func (db *DB) FishWeightPresetWeights(ctx context.Context) ([FishRarityTiers]float64, bool, error) {
	var out [FishRarityTiers]float64
	var any bool
	if db == nil || db.SQL == nil {
		return out, false, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `SELECT rarity_tier, weight FROM fish_weight_preset`)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var tier int
		var w float64
		if err := rows.Scan(&tier, &w); err != nil {
			return out, false, err
		}
		if tier >= 0 && tier < len(out) {
			out[tier] = w
			any = true
		}
	}
	return out, any, rows.Err()
}

// FishWeightPresetStats 只读预览：每档多少鱼、影响多少行、攻略原值均值多少。
func (db *DB) FishWeightPresetStats(ctx context.Context, weights [FishRarityTiers]float64) ([]FishWeightTierStat, error) {
	if db == nil || db.SQL == nil {
		return nil, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `
		SELECT e.rarity_tier,
		       COUNT(DISTINCT e.fish_key),
		       COUNT(w.fish_key),
		       COALESCE(AVG(l.catches), 0)
		FROM fish_ecology e
		LEFT JOIN loc_wiki_fish w ON w.fish_key = e.fish_key
		LEFT JOIN loc_fish l ON l.loc = w.loc AND l.fish_key = w.fish_key
		GROUP BY e.rarity_tier ORDER BY e.rarity_tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FishWeightTierStat{}
	for rows.Next() {
		var s FishWeightTierStat
		if err := rows.Scan(&s.Tier, &s.Species, &s.Rows, &s.AvgBase); err != nil {
			return nil, err
		}
		s.Label = FishRarityLabel(s.Tier)
		if s.Tier >= 0 && s.Tier < len(weights) {
			s.Weight = weights[s.Tier]
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PresetFishBaseWeights 写入 3 档预设权重（同档覆盖）。weights 下标即 rarity_tier。
// 值 <=0 的档位会被跳过（保留原值），避免把某档鱼直接抹成不出现——
// 要封掉某档鱼应该用手工覆盖或生态覆盖的 enabled=0。
func (db *DB) PresetFishBaseWeights(ctx context.Context, weights [FishRarityTiers]float64) (FishWeightPresetResult, error) {
	var res FishWeightPresetResult
	if db == nil || db.SQL == nil {
		return res, nil
	}
	stats, err := db.FishWeightPresetStats(ctx, weights)
	if err != nil {
		return res, err
	}
	res.Tiers = stats

	now := float64(time.Now().UnixNano()) / 1e9
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	for tier, w := range weights {
		if w <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fish_weight_preset(rarity_tier, weight, updated_ts) VALUES(?,?,?)
			ON CONFLICT(rarity_tier) DO UPDATE SET weight=excluded.weight, updated_ts=excluded.updated_ts`,
			tier, w, now); err != nil {
			return res, fmt.Errorf("写入 %s 档权重: %w", FishRarityLabel(tier), err)
		}
		res.Updated++
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	return res, nil
}

// ClearFishWeightPreset 清空预设，基础权重回到攻略站原值（loc_fish.catches）。
func (db *DB) ClearFishWeightPreset(ctx context.Context) (int64, error) {
	if db == nil || db.SQL == nil {
		return 0, nil
	}
	r, err := db.SQL.ExecContext(ctx, `DELETE FROM fish_weight_preset`)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
