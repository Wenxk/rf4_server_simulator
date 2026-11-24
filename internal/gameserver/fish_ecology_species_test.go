package gameserver

import (
	"context"
	"testing"

	"rf4server/internal/store"
)

// 三层合并顺序必须是 资产基线 → 鱼种覆盖 → 水域覆盖。
// 用同一个字段（temp_pref_c）在两层都设不同值，能直接照出谁压谁。
func TestFishEcologySpeciesOverridesMergeBetweenBaselineAndLoc(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_min, layer_pref, layer_max, temp_min_c, temp_pref_c, temp_max_c, activity)
		 VALUES('t.burbot', 0.30, 0.55, 0.80, 10.2, 18.9, 27.1, 0.87)`); err != nil {
		t.Fatal(err)
	}
	// 鱼种层：把江鳕的窗口改成现实值 4/10/14，水层不动。
	if err := db.UpsertFishEcologySpecies(ctx, store.FishEcologySpecies{
		FishKey:  "t.burbot",
		TempMinC: floatPtr(4), TempPrefC: floatPtr(10), TempMaxC: floatPtr(14),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	// 水域层：在某张图上再压成 8℃。
	if err := db.UpsertFishEcologyLoc(ctx, store.FishEcologyLoc{
		StatLoc: "loc_cold", FishKey: "t.burbot", TempPrefC: floatPtr(8),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}

	// 只有鱼种层生效的图：温度用 4/10/14，水层仍继承基线。
	plain := srv.fishEcologyFor(ctx, "loc_plain", "t.burbot")
	if plain.TempMinC != 4 || plain.TempPrefC != 10 || plain.TempMaxC != 14 {
		t.Fatalf("鱼种层未生效：%v/%v/%v，期望 4/10/14", plain.TempMinC, plain.TempPrefC, plain.TempMaxC)
	}
	if plain.LayerPref != 0.55 || plain.Activity != 0.87 {
		t.Fatalf("鱼种层只给了温度，水层/活跃度应继承基线：%v/%v", plain.LayerPref, plain.Activity)
	}

	// 有水域层的图：水域层压过鱼种层，鱼种层其余字段仍在。
	cold := srv.fishEcologyFor(ctx, "loc_cold", "t.burbot")
	if cold.TempPrefC != 8 {
		t.Fatalf("水域层应压过鱼种层（8℃），实得 %v", cold.TempPrefC)
	}
	if cold.TempMinC != 4 || cold.TempMaxC != 14 {
		t.Fatalf("水域层只覆盖了偏好温度，上下限应保留鱼种层：%v/%v", cold.TempMinC, cold.TempMaxC)
	}

	// statLoc 为空（位置未知）：只跳过水域层，鱼种层照常合并。
	// 这是关键——水温窗口属于物种本身，不该因为定位缺失退回合成基线。
	noloc := srv.fishEcologyFor(ctx, "", "t.burbot")
	if noloc.TempPrefC != 10 {
		t.Fatalf("定位缺失时鱼种层仍须生效（10℃），实得 %v", noloc.TempPrefC)
	}
}

// 鱼种层能覆盖的字段比水域层宽：底质/水色/动作/水流/光照/清澈度/攻击性
// 只在鱼种层有位置，必须能从表里读进合并结果（否则后台存了也不生效）。
func TestFishEcologySpeciesCoversWideFields(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, temp_pref_c) VALUES('t.wide', 18)`); err != nil {
		t.Fatal(err)
	}
	substrate := `{"mud":1.4}`
	color := `{"green":1.2}`
	action := `{"jerk":1.1}`
	if err := db.UpsertFishEcologySpecies(ctx, store.FishEcologySpecies{
		FishKey:       "t.wide",
		SubstrateJSON: &substrate, ColorJSON: &color, ActionJSON: &action,
		CurrentPref: floatPtr(0.8), LightPref: floatPtr(0.2),
		ClarityPref: floatPtr(0.9), Aggression: floatPtr(1.6),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	p := srv.fishEcologyFor(ctx, "", "t.wide")
	if p.SubstrateJSON != substrate {
		t.Fatalf("底质=%q，期望 %q", p.SubstrateJSON, substrate)
	}
	if p.ColorJSON != color {
		t.Fatalf("水色=%q，期望 %q", p.ColorJSON, color)
	}
	if p.ActionJSON != action {
		t.Fatalf("拟饵动作=%q，期望 %q", p.ActionJSON, action)
	}
	if p.CurrentPref != 0.8 || p.LightPref != 0.2 || p.ClarityPref != 0.9 || p.Aggression != 1.6 {
		t.Fatalf("宽字段未合并: current=%v light=%v clarity=%v aggression=%v",
			p.CurrentPref, p.LightPref, p.ClarityPref, p.Aggression)
	}
}

// 缓存失效必须把鱼种层一起清掉，否则后台改完要等进程重启。
func TestInvalidateClearsSpeciesCache(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, temp_pref_c) VALUES('t.inv', 18)`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFishEcologySpecies(ctx, store.FishEcologySpecies{
		FishKey: "t.inv", TempPrefC: floatPtr(7),
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	if got := srv.fishEcologyFor(ctx, "", "t.inv").TempPrefC; got != 7 {
		t.Fatalf("首次载入得到 %v，期望 7", got)
	}

	srv.invalidateFishEcologyCache()
	if srv.fishEcologySpeciesCache != nil {
		t.Fatal("invalidate 没清掉鱼种层缓存")
	}
	// 清完不 reload 时必须退回资产基线，不能残留旧值。
	if got := srv.fishEcologyFor(ctx, "", "t.inv").TempPrefC; got != 18 {
		t.Fatalf("清缓存后退回基线得到 %v，期望 18", got)
	}
}
