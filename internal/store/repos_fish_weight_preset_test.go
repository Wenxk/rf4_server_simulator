package store

import (
	"context"
	"math"
	"testing"
)

// 基础权重档位化：预设表优先于攻略原值，清空即回滚。
//
// 关键约束：loc_fish 在生产是只读静态视图（TEMP VIEW + authorizer），所以预设必须
// 落在独立表里、由 LocRoster 读取时优先取用。这里锁住的就是那条读取链。
//
// 档位只有三档（0 普通 / 1 稀有 / 2 超稀有），数值来自鱼种固有稀有度表
// species_rarity，不再是按渔获量算出来的四档。
func TestFishWeightPresetOverridesRoster(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.SQL.Exec(sql, args...); err != nil {
			t.Fatalf("执行 %q: %v", sql, err)
		}
	}

	mustExec(`INSERT INTO fish_ecology(fish_key, diel_mask, rarity_tier) VALUES
		('t.common', 15, 0), ('t.rare', 8, 1), ('t.ultra', 15, 2)`)
	// 甲湖：三条鱼都有攻略原值；乙湖：t.rare 只有名册没有渔获统计（模拟 178 条缺口）。
	mustExec(`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh, in_map_roster) VALUES
		('甲湖','t.common','普通鱼',1), ('甲湖','t.rare','稀有鱼',1),
		('甲湖','t.ultra','超稀有鱼',1), ('乙湖','t.rare','稀有鱼',1)`)
	mustExec(`INSERT INTO loc_fish(loc, fish_key, name_zh, catches) VALUES
		('甲湖','t.common','普通鱼',7),
		('甲湖','t.rare','稀有鱼',3),
		('甲湖','t.ultra','超稀有鱼',1)`)

	roster := func(loc string) map[string]float64 {
		t.Helper()
		rows, err := db.LocRoster(ctx, loc)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, r := range rows {
			out[r.FishKey] = r.Catches
		}
		return out
	}

	// 预设表为空（迁移只写了默认 3 行，先清掉模拟「没启用」）：走攻略原值。
	if _, err := db.ClearFishWeightPreset(ctx); err != nil {
		t.Fatal(err)
	}
	base := roster("甲湖")
	if base["t.common"] != 7 || base["t.rare"] != 3 || base["t.ultra"] != 1 {
		t.Fatalf("未启用预设时应走攻略原值，实得 %v", base)
	}
	// 名册有、loc_fish 没有的鱼仍按 COALESCE 兜底为 1——这正是要修的缺口。
	if got := roster("乙湖")["t.rare"]; got != 1 {
		t.Fatalf("未启用预设时缺口行应为兜底 1，实得 %v", got)
	}

	// 启用档位预设。
	res, err := db.PresetFishBaseWeights(ctx, [FishRarityTiers]float64{100, 30, 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != int64(FishRarityTiers) {
		t.Fatalf("写入 %d 档，期望 %d", res.Updated, FishRarityTiers)
	}
	want := map[string]float64{"t.common": 100, "t.rare": 30, "t.ultra": 5}
	for key, w := range want {
		if got := roster("甲湖")[key]; math.Abs(got-w) > 1e-9 {
			t.Fatalf("%s 权重=%v，期望 %v", key, got, w)
		}
	}
	// 缺口行现在也拿到档位权重，不再恒为 1。
	if got := roster("乙湖")["t.rare"]; math.Abs(got-30) > 1e-9 {
		t.Fatalf("乙湖缺口行权重=%v，期望 30", got)
	}

	// 改档位值立即生效，不需要重算任何行。
	if _, err := db.PresetFishBaseWeights(ctx, [FishRarityTiers]float64{200, 60, 10}); err != nil {
		t.Fatal(err)
	}
	if got := roster("甲湖")["t.rare"]; math.Abs(got-60) > 1e-9 {
		t.Fatalf("改档位后稀有鱼权重=%v，期望 60", got)
	}

	// 清空即回滚到攻略原值。
	n, err := db.ClearFishWeightPreset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(FishRarityTiers) {
		t.Fatalf("清除 %d 档，期望 %d", n, FishRarityTiers)
	}
	back := roster("甲湖")
	if back["t.common"] != 7 || back["t.rare"] != 3 {
		t.Fatalf("回滚后应恢复攻略原值，实得 %v", back)
	}
}

// 没有生态档案的鱼不该被预设强行赋值：JOIN 落空时退回攻略原值。
func TestFishWeightPresetSkipsSpeciesWithoutTier(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh, in_map_roster)
		VALUES('甲湖','t.notier','无档案',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO loc_fish(loc, fish_key, name_zh, catches)
		VALUES('甲湖','t.notier','无档案',9)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PresetFishBaseWeights(ctx, [FishRarityTiers]float64{100, 30, 5}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.LocRoster(ctx, "甲湖")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Catches != 9 {
		t.Fatalf("无 tier 的鱼应保持攻略原值 9，实得 %+v", rows)
	}
}

// 预览只读：算档位分布不能顺手改库；预设表为空时 active=false。
func TestFishWeightPresetStatsIsReadOnly(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(`INSERT INTO fish_ecology(fish_key, diel_mask, rarity_tier) VALUES
		('t.a', 15, 0), ('t.b', 15, 0), ('t.c', 8, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh, in_map_roster) VALUES
		('甲湖','t.a','A',1), ('乙湖','t.a','A',1), ('甲湖','t.c','C',1)`); err != nil {
		t.Fatal(err)
	}
	stats, err := db.FishWeightPresetStats(ctx, [FishRarityTiers]float64{100, 30, 5})
	if err != nil {
		t.Fatal(err)
	}
	byTier := map[int]FishWeightTierStat{}
	for _, s := range stats {
		byTier[s.Tier] = s
	}
	if got := byTier[0]; got.Species != 2 || got.Rows != 2 || got.Weight != 100 {
		t.Fatalf("普通档=%+v，期望 2 种 / 2 行 / 权重 100", got)
	}
	if got := byTier[2]; got.Species != 1 || got.Rows != 1 || got.Weight != 5 {
		t.Fatalf("超稀有档=%+v，期望 1 种 / 1 行 / 权重 5", got)
	}
	if _, active, err := db.FishWeightPresetWeights(ctx); err != nil || !active {
		t.Fatalf("迁移默认写入 %d 档，active 应为 true（err=%v）", FishRarityTiers, err)
	}
	if _, err := db.ClearFishWeightPreset(ctx); err != nil {
		t.Fatal(err)
	}
	if _, active, err := db.FishWeightPresetWeights(ctx); err != nil || active {
		t.Fatalf("清空后 active 应为 false（err=%v）", err)
	}
}
