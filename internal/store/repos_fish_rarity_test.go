package store

import (
	"context"
	"math"
	"testing"
)

// 稀有度分档：按全服渔获数的绝对阈值重写 rarity_tier。
//
// 关键约束：依据只能是 species_grade.catches（静态攻略数据），不能是 loc_fish
// （按图分摊、各图尺度差十倍）或 catch_log（本地测试渔获，已被人为调权重污染）。
func TestFishRarityRecalcByAbsoluteThreshold(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.SQL.Exec(sql, args...); err != nil {
			t.Fatalf("执行 %q: %v", sql, err)
		}
	}

	// 初始档位故意写成「四分位残留」的样子：全服 60 条的普通鱼被判极罕见，
	// 只有 5 条的冷门鱼被判常规——这正是旧口径会出现的错配。
	mustExec(`INSERT INTO fish_ecology(fish_key, diel_mask, rarity_tier) VALUES
		('t.common', 15, 3), ('t.mid', 15, 1), ('t.rare', 15, 3), ('t.ultra', 15, 0)`)
	mustExec(`INSERT INTO species_grade(fish_key, catches) VALUES
		('t.common', 60), ('t.mid', 20), ('t.rare', 9), ('t.ultra', 2)`)

	res, err := db.RecalcFishRarity(ctx, [3]int64{30, 14, 7})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 4 || res.Skipped != 0 {
		t.Fatalf("参与 %d / 跳过 %d，期望 4 / 0", res.Total, res.Skipped)
	}
	// t.common 3→0、t.rare 3→2、t.ultra 0→3：共 3 个换档；t.mid 本来就是 1，不动。
	if res.Changed != 3 {
		t.Fatalf("换档 %d 种，期望 3（t.common/t.rare/t.ultra）", res.Changed)
	}

	want := map[string]int{"t.common": 0, "t.mid": 1, "t.rare": 2, "t.ultra": 3}
	for key, tier := range want {
		var got int
		if err := db.SQL.QueryRow(`SELECT rarity_tier FROM fish_ecology WHERE fish_key=?`, key).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tier {
			t.Fatalf("%s 档位=%d，期望 %d", key, got, tier)
		}
	}

	// 分布统计要能对上：常规 1 / 少见 1 / 罕见 1 / 极罕见 1。
	if len(res.Tiers) != 4 {
		t.Fatalf("档位统计 %d 条，期望 4", len(res.Tiers))
	}
	for _, s := range res.Tiers {
		if s.Species != 1 {
			t.Fatalf("tier %d 鱼种数=%d，期望 1", s.Tier, s.Species)
		}
		if s.Tier == 3 {
			if s.MinObs != 2 || s.MaxObs != 2 {
				t.Fatalf("极罕见档区间 %d~%d，期望 2~2", s.MinObs, s.MaxObs)
			}
		}
	}

	// 阈值必须一并落库，且与档位一致。
	cuts, active, err := db.FishRarityThresholds(ctx)
	if err != nil || !active {
		t.Fatalf("阈值读取失败或未激活（err=%v active=%v）", err, active)
	}
	if cuts != [3]int64{30, 14, 7} {
		t.Fatalf("阈值=%v，期望 [30 14 7]", cuts)
	}

	// 幂等：再跑一次不该再产生写入。
	again, err := db.RecalcFishRarity(ctx, [3]int64{30, 14, 7})
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed != 0 {
		t.Fatalf("重复重算又改了 %d 种，期望 0（幂等）", again.Changed)
	}
}

// 没有全服渔获数据的鱼保持原档：没有依据就不改，
// 不能按 catches=0 算成极罕见——那会把 seed 里新增但攻略站还没登记的鱼全打成稀有。
func TestFishRarityKeepsSpeciesWithoutGlobalData(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(`INSERT INTO fish_ecology(fish_key, diel_mask, rarity_tier)
		VALUES('t.noinfo', 15, 1)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.RecalcFishRarity(ctx, [3]int64{30, 14, 7})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Changed != 0 {
		t.Fatalf("跳过 %d / 换档 %d，期望 1 / 0", res.Skipped, res.Changed)
	}
	var tier int
	if err := db.SQL.QueryRow(`SELECT rarity_tier FROM fish_ecology WHERE fish_key='t.noinfo'`).Scan(&tier); err != nil {
		t.Fatal(err)
	}
	if tier != 1 {
		t.Fatalf("无数据鱼档位=%d，期望保持 1", tier)
	}
}

// 预览只读：算分布不能顺手改库。
func TestFishRarityPreviewIsReadOnly(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(`INSERT INTO fish_ecology(fish_key, diel_mask, rarity_tier)
		VALUES('t.a', 15, 3), ('t.b', 15, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO species_grade(fish_key, catches) VALUES('t.a', 50), ('t.b', 3)`); err != nil {
		t.Fatal(err)
	}
	stats, err := db.FishRarityPreview(ctx, [3]int64{30, 14, 7})
	if err != nil {
		t.Fatal(err)
	}
	// 预览里 species 是「重算后」，before 是「现在」。
	if stats[0].Species != 1 || stats[0].Before != 1 {
		t.Fatalf("常规档 preview=%+v", stats[0])
	}
	if stats[3].Species != 1 || stats[3].Before != 1 {
		t.Fatalf("极罕见档 preview=%+v", stats[3])
	}
	var tier int
	if err := db.SQL.QueryRow(`SELECT rarity_tier FROM fish_ecology WHERE fish_key='t.a'`).Scan(&tier); err != nil {
		t.Fatal(err)
	}
	if tier != 3 {
		t.Fatalf("预览改了库（t.a 档位=%d），预览必须只读", tier)
	}
}

// 阈值必须严格递减：罕见下限高于少见下限会让分档不可解释。
func TestFishRarityCutsValidation(t *testing.T) {
	ok := [][3]int64{{30, 14, 7}, {5, 4, 1}, {100, 50, 25}}
	for _, c := range ok {
		if _, valid := NormalizeFishRarityCuts(c); !valid {
			t.Fatalf("%v 应被接受", c)
		}
	}
	bad := [][3]int64{{14, 30, 7}, {30, 30, 7}, {30, 14, 14}, {30, 14, 0}, {-1, 14, 7}}
	for _, c := range bad {
		if _, valid := NormalizeFishRarityCuts(c); valid {
			t.Fatalf("%v 应被拒绝", c)
		}
	}
	// 非法阈值直接拒绝，不能悄悄写库。
	db := openMigratedDB(t)
	if _, err := db.RecalcFishRarity(context.Background(), [3]int64{7, 14, 30}); err == nil {
		t.Fatal("非递减阈值应报错")
	}
}

// 端到端：重算稀有度 → 按档位重设权重 → 名册权重跟着变。
// 这条链就是「根据鱼种本身稀有程度重新初始化基础权重」的实际路径。
func TestFishRarityRecalcDrivesRosterWeights(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.SQL.Exec(sql, args...); err != nil {
			t.Fatalf("执行 %q: %v", sql, err)
		}
	}
	// 旧口径把「全服 60 条」的常见鱼判成极罕见，名册权重只有 5。
	mustExec(`INSERT INTO fish_ecology(fish_key, diel_mask, rarity_tier) VALUES('t.carp', 15, 3)`)
	mustExec(`INSERT INTO species_grade(fish_key, catches) VALUES('t.carp', 60)`)
	mustExec(`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh, in_map_roster) VALUES('甲湖','t.carp','鲤鱼',1)`)
	mustExec(`INSERT INTO loc_fish(loc, fish_key, name_zh, catches) VALUES('甲湖','t.carp','鲤鱼',2)`)

	rosterWeight := func() float64 {
		t.Helper()
		rows, err := db.LocRoster(ctx, "甲湖")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("名册 %d 行，期望 1", len(rows))
		}
		return rows[0].Catches
	}

	if _, err := db.PresetFishBaseWeights(ctx, [4]float64{100, 40, 15, 5}); err != nil {
		t.Fatal(err)
	}
	if got := rosterWeight(); math.Abs(got-5) > 1e-9 {
		t.Fatalf("旧档位下权重=%v，期望 5", got)
	}

	if _, err := db.RecalcFishRarity(ctx, [3]int64{30, 14, 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PresetFishBaseWeights(ctx, [4]float64{100, 40, 15, 5}); err != nil {
		t.Fatal(err)
	}
	if got := rosterWeight(); math.Abs(got-100) > 1e-9 {
		t.Fatalf("重算后权重=%v，期望 100（常规档）", got)
	}
}
