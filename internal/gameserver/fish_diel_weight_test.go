package gameserver

import (
	"context"
	"testing"
	"time"

	"rf4server/internal/store"
)

// 昼夜曲线的解析、边界、位图回退与资产自洽由 fish_diel_curve_test.go 覆盖；
// 这里只钉「昼夜层与生态总开关的解耦」这类集成行为。

// 生态总开关默认关，但昼夜层必须独立生效：
// 生态关着时判鱼权重是昼夜系数，而不是原来的硬 1。
func TestDielWeightAppliesWithoutEcology(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, diel_curve, activity)
		 VALUES('t.nightfish', 0.5, 15, 1, '[1,0.25,0.12,0.6]', 1.0)`); err != nil {
		t.Fatal(err)
	}
	setTunables(t, db, map[string]string{
		"fish_ecology_enabled":     "0",
		"fish_diel_weight_enabled": "1",
	})
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}

	c := fishRollContext{
		loc: "loc_test", rigType: 1, cache: newFishingRollCache(),
		now: time.Unix(1_700_000_000, 0),
	}
	p := srv.cachedFishEcology(ctx, c, "t.nightfish")
	_, hour := worldDayAndHour(c.now.Unix())
	want := fishDielWeight(p, hour)

	got := srv.fishBiteEnvironmentWeight(ctx, "t.nightfish", c)
	if got != want {
		t.Fatalf("生态关闭时权重应等于昼夜系数 %v（不再有钓法乘子），实得 %v", want, got)
	}
	if got == 1 {
		t.Fatal("昼夜层默认开启时不应返回中性 1，否则夜行鱼在夜里不会变多")
	}
}

// 昼夜开关也关掉时回到完全中性（与改动前的生态全关行为一致）。
func TestDielWeightDisabledIsNeutral(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, diel_curve)
		 VALUES('t.neutral', 0.5, 15, 1, '[1,0.25,0.12,0.6]')`); err != nil {
		t.Fatal(err)
	}
	setTunables(t, db, map[string]string{
		"fish_ecology_enabled":     "0",
		"fish_diel_weight_enabled": "0",
	})
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	c := fishRollContext{loc: "loc_test", rigType: 1, cache: newFishingRollCache(), now: time.Unix(1_700_000_000, 0)}
	if got := srv.fishBiteEnvironmentWeight(ctx, "t.neutral", c); got != 1 {
		t.Fatalf("昼夜与生态都关时应为 1，实得 %v", got)
	}
}

// 「全局不出鱼」只属于生态总开关：生态关着时，enabled=0 的鱼不该被昼夜层抹掉，
// 否则会静默改变既有候选集。
func TestDielWeightDoesNotHonourGlobalEnabled(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, diel_curve, enabled)
		 VALUES('t.off', 0.5, 15, 1, '[1,0.25,0.12,0.6]', 0)`); err != nil {
		t.Fatal(err)
	}
	setTunables(t, db, map[string]string{
		"fish_ecology_enabled":     "0",
		"fish_diel_weight_enabled": "1",
	})
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	c := fishRollContext{loc: "loc_test", rigType: 1, cache: newFishingRollCache(), now: time.Unix(1_700_000_000, 0)}
	if got := srv.fishBiteEnvironmentWeight(ctx, "t.off", c); got == 0 {
		t.Fatal("生态关闭时昼夜层不应把 enabled=0 的鱼权重归零")
	}
}

// 曲线是「接近不出但不置零」：低于下限的值必须钳到 0.08，
// 否则低活跃段会真的把该鱼从候选池里抹掉。
func TestDielCurveClampsToMinimum(t *testing.T) {
	p := fishEcologyProfile{DielCurve: "[1,0.01,0,0.2]"}
	if got := fishDielWeight(p, 12); got != fishDielCurveMin {
		t.Fatalf("昼段 0 权重应钳到 %v，实得 %v", fishDielCurveMin, got)
	}
	if got := fishDielWeight(p, 7); got != fishDielCurveMin {
		t.Fatalf("晨段 0.01 应钳到 %v，实得 %v", fishDielCurveMin, got)
	}
}

// setTunables 写入运行参数，调用方随后 execAdminCmd(reload_tunables) 刷新缓存。
func setTunables(t *testing.T, db *store.DB, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := db.TunableSet(context.Background(), k, v, "tester"); err != nil {
			t.Fatalf("写 tunable %s: %v", k, err)
		}
	}
}
