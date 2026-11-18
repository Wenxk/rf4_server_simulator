package gameserver

import (
	"context"
	"testing"
	"time"

	"rf4server/internal/store"
)

// 钓法倾向层：钓组类型 → 钓法族、鱼种水层 → 四档、矩阵乘子原样生效。
//
// 这一层存在的理由（实机抽样 2026-11-18）：钓法水层原本只有几何公式，底钓恒 0.93、
// 浮钓按有效饵深，方向对但玩家看不见也调不了；底钓 100% 出底层鱼、浮钓仍有 64%
// 是底层鱼。现在补一层显式可配的乘子，本文件锁住三件事：分类不漏、边界不错、
// 后台填多少就生效多少。

func TestRigFamilyOfCoversRigTypes(t *testing.T) {
	cases := []struct {
		rig  int64
		want string
	}{
		{1, store.RigFamilyFloat},   // rig_FloatSimple
		{21, store.RigFamilyFloat},  // rig_FloatWaggler
		{30, store.RigFamilyFloat},  // rig_FloatCarpHair
		{12, store.RigFamilyBottom}, // rig_BottomSimple
		{24, store.RigFamilyBottom}, // rig_BottomHair
		{18, store.RigFamilyBottom}, // rig_BottomFeedRocket（打窝器，仍属沉底族）
		{5, store.RigFamilyLure},    // rig_Lure
		{9, store.RigFamilyLure},    // rig_Carolina
		{33, store.RigFamilyFly},    // rig_Fly
		{50, store.RigFamilyMarine}, // rig_MarinePilker（同时也在路亚族里，海钓优先）
		{55, store.RigFamilyMarine}, // rig_MarineBottomClassic
		{99, ""},                    // 未知钓组：不做偏向
	}
	for _, c := range cases {
		if got := rigFamilyOf(c.rig); got != c.want {
			t.Fatalf("钓组 %d 应归入 %q，实得 %q", c.rig, c.want, got)
		}
	}
	// 50~54 同时在 rigTypeLureFamily 里，必须先判海钓：否则船竿铁板会被当成岸抛路亚。
	if rigFamilyOf(51) != store.RigFamilyMarine {
		t.Fatal("海钓族必须优先于路亚族")
	}
}

func TestFishLayerBandBoundaries(t *testing.T) {
	cases := []struct {
		pref float64
		want string
	}{
		{0.17, store.LayerBandSurface},
		{0.2999, store.LayerBandSurface},
		{0.30, store.LayerBandMidUpper}, // 边界归更深一档
		{0.4999, store.LayerBandMidUpper},
		{0.50, store.LayerBandMidLower},
		{0.7199, store.LayerBandMidLower},
		{0.72, store.LayerBandBottom},
		{0.94, store.LayerBandBottom},
	}
	for _, c := range cases {
		if got := fishLayerBand(c.pref); got != c.want {
			t.Fatalf("layer_pref=%v 应为 %q，实得 %q", c.pref, c.want, got)
		}
	}
}

// 后台改矩阵 → 判鱼侧取到的就是那个数（键拼法必须两处一致）。
func TestRigAffinityWeightFollowsMatrix(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, activity)
		 VALUES('t.benthic', 0.9, 15, 15, 1.0)`); err != nil {
		t.Fatal(err)
	}
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	c := fishRollContext{loc: "loc_test", rigType: 24, cache: newFishingRollCache()}
	if got := srv.rigAffinityWeight(ctx, "t.benthic", c); got != 1.35 {
		t.Fatalf("默认矩阵下底钓×底层应为 1.35，实得 %v", got)
	}

	if _, err := db.SaveRigAffinity(ctx, []store.RigAffinityWeight{
		{RigFamily: store.RigFamilyBottom, LayerBand: store.LayerBandBottom, Weight: 2.0},
	}); err != nil {
		t.Fatal(err)
	}
	// 缓存 30 秒，直接清掉模拟"改完过了一个 TTL"。
	srv.rigAffMu.Lock()
	srv.rigAffAt = time.Time{}
	srv.rigAffMu.Unlock()
	if got := srv.rigAffinityWeight(ctx, "t.benthic", c); got != 2.0 {
		t.Fatalf("改完应立刻取到 2.0，实得 %v", got)
	}

	// 浮钓打同一条底层鱼：走的是浮钓×底层那一格。
	c.rigType = 1
	if got := srv.rigAffinityWeight(ctx, "t.benthic", c); got != 0.6 {
		t.Fatalf("浮钓×底层应为 0.6，实得 %v", got)
	}

	// 判不出钓法族时保持中性，不能凭空压权重。
	c.rigType = 99
	if got := srv.rigAffinityWeight(ctx, "t.benthic", c); got != 1 {
		t.Fatalf("未知钓组应中性 1，实得 %v", got)
	}
}

// 开关关掉时整层中性：回滚只需要改一个参数，不用清表。
func TestRigAffinityDisabledIsNeutral(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, activity)
		 VALUES('t.benthic', 0.9, 15, 15, 1.0)`); err != nil {
		t.Fatal(err)
	}
	setTunables(t, db, map[string]string{"rig_affinity_enabled": "0"})
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if err := srv.reloadFishEcologyCache(ctx); err != nil {
		t.Fatal(err)
	}
	c := fishRollContext{loc: "loc_test", rigType: 24, cache: newFishingRollCache()}
	if got := srv.rigAffinityWeight(ctx, "t.benthic", c); got != 1 {
		t.Fatalf("开关关闭时应为 1，实得 %v", got)
	}
}
