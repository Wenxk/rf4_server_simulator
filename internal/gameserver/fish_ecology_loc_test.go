package gameserver

import (
	"context"
	"testing"

	"rf4server/internal/store"
)

func floatPtr(v float64) *float64 { return &v }
func int64Ptr(v int64) *int64     { return &v }

// 同一种鱼在不同水域可以有不同的温度窗口；只覆盖温度时水层等字段必须继续继承全局，
// 没有覆盖行的水域必须与全局逐字段相同——这三条共同定义「部分覆盖」语义。
func TestFishEcologyLocOverridesMergeOverGlobal(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_min, layer_pref, layer_max, temp_min_c, temp_pref_c, temp_max_c, activity)
		 VALUES('t.fish', 0.30, 0.55, 0.80, 5, 18, 27, 1.0)`); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFishEcologyLoc(ctx, store.FishEcologyLoc{
		StatLoc: "loc_temp_cold", FishKey: "t.fish", TempPrefC: floatPtr(12),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFishEcologyLoc(ctx, store.FishEcologyLoc{
		StatLoc: "loc_deep", FishKey: "t.fish", LayerPref: floatPtr(0.92),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFishEcologyLoc(ctx, store.FishEcologyLoc{
		StatLoc: "loc_off", FishKey: "t.fish", Enabled: int64Ptr(0),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}

	global := srv.fishEcology(ctx, "t.fish")
	cold := srv.fishEcologyFor(ctx, "loc_temp_cold", "t.fish")
	deep := srv.fishEcologyFor(ctx, "loc_deep", "t.fish")
	off := srv.fishEcologyFor(ctx, "loc_off", "t.fish")
	plain := srv.fishEcologyFor(ctx, "loc_without_override", "t.fish")

	if cold.TempPrefC != 12 {
		t.Fatalf("冷湖水域偏好温度=%v，期望 12", cold.TempPrefC)
	}
	if cold.LayerPref != global.LayerPref || cold.TempMinC != global.TempMinC || cold.Activity != global.Activity {
		t.Fatalf("只覆盖温度不应动其他字段: cold=%+v global=%+v", cold, global)
	}
	if deep.LayerPref != 0.92 {
		t.Fatalf("深水水域水层偏好=%v，期望 0.92", deep.LayerPref)
	}
	if deep.TempPrefC != global.TempPrefC {
		t.Fatalf("只覆盖水层不应动温度: %v vs %v", deep.TempPrefC, global.TempPrefC)
	}
	if off.Enabled {
		t.Fatal("enabled=0 的覆盖水域不应出这种鱼")
	}
	// 无覆盖行 = 与全局完全一致（防「静默改变现有手感」）。
	if plain != global {
		t.Fatalf("无覆盖水域档案与全局不一致: %+v vs %+v", plain, global)
	}
}

// 判鱼缓存键必须带上水域：一次会话内跨水域估算是真实路径（区域估算复用 roll cache），
// 只按 fish_key 缓存会把 A 湖的覆盖串到 B 湖。
func TestCachedFishEcologyKeysByStatLoc(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c) VALUES('t.fish2', 0.55, 18)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(
		`INSERT INTO loc_alias(alias, loc) VALUES('level_a', 'loc_a'), ('level_b', 'loc_b')
		 ON CONFLICT DO NOTHING`); err != nil {
		t.Skipf("loc_alias 结构不匹配，跳过场景名解析用例: %v", err)
	}
	if err := db.UpsertFishEcologyLoc(ctx, store.FishEcologyLoc{
		StatLoc: "loc_a", FishKey: "t.fish2", TempPrefC: floatPtr(9),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}

	c := fishRollContext{cache: newFishingRollCache()}
	atA, atB := c, c
	atA.loc = "level_a"
	atB.loc = "level_b"
	a := srv.cachedFishEcology(ctx, atA, "t.fish2")
	b := srv.cachedFishEcology(ctx, atB, "t.fish2")
	if a.TempPrefC != 9 {
		t.Fatalf("level_a 应命中 loc_a 覆盖（9℃），实得 %v", a.TempPrefC)
	}
	if b.TempPrefC != 18 {
		t.Fatalf("level_b 无覆盖应继承全局（18℃），实得 %v", b.TempPrefC)
	}
}
