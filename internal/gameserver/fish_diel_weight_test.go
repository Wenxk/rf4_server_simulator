package gameserver

import (
	"context"
	"math"
	"testing"
	"time"

	"rf4server/internal/store"
)

// 昼夜系数：活跃时段拿加成（>1 表示加权提升）、非活跃时段拿压制。
// 段位序由 worldclock.SegmentOf 给：0=晨 1=昼 2=暮 3=夜，对应掩码位 1/2/4/8。
func TestFishDielWeightBoostAndPenalty(t *testing.T) {
	const boost, penalty = 1.25, 0.35
	nightOnly := fishEcologyProfile{DielMask: 8} // 仅夜
	dayOnly := fishEcologyProfile{DielMask: 2}   // 仅昼

	cases := []struct {
		name  string
		p     fishEcologyProfile
		hour  int64
		want  float64
		phase string
	}{
		{"夜行鱼·夜", nightOnly, 22, boost, "夜"},
		{"夜行鱼·昼", nightOnly, 12, penalty, "昼"},
		{"日行鱼·昼", dayOnly, 12, boost, "昼"},
		{"日行鱼·夜", dayOnly, 22, penalty, "夜"},
		{"夜行鱼·晨", nightOnly, 7, penalty, "晨"},
		{"夜行鱼·暮", nightOnly, 18, penalty, "暮"},
	}
	for _, c := range cases {
		if got := fishDielWeight(c.p, c.hour, boost, penalty); got != c.want {
			t.Fatalf("%s：%d 点（%s段）期望 %v，实得 %v", c.name, c.hour, c.phase, c.want, got)
		}
	}
}

// 生态总开关默认关，但昼夜层必须独立生效：
// 生态关着时判鱼权重是昼夜系数，而不是原来的硬 1。
func TestDielWeightAppliesWithoutEcology(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, activity)
		 VALUES('t.nightfish', 0.5, 15, 8, 1.0)`); err != nil {
		t.Fatal(err)
	}
	setTunables(t, db, map[string]string{
		"fish_ecology_enabled":       "0",
		"fish_diel_weight_enabled":   "1",
		"fish_diel_active_boost":     "1.25",
		"fish_diel_inactive_penalty": "0.35",
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
	want := fishDielWeight(p, hour, 1.25, 0.35)
	// 生态关掉后只剩昼夜层，但「钓法倾向」是独立的一层：这一杆是浮钓（rigType=1）
	// 打 layer_pref=0.5（中下层）的鱼，按出厂矩阵要乘 0.8。它不是生态的一部分，
	// 所以不与生态开关联动（详见 rig_affinity.go 的说明）。
	want *= srv.rigAffinityWeight(ctx, "t.nightfish", c)

	got := srv.fishBiteEnvironmentWeight(ctx, "t.nightfish", c)
	if got != want {
		t.Fatalf("生态关闭时权重应为昼夜系数×钓法倾向 %v，实得 %v", want, got)
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
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask) VALUES('t.neutral', 0.5, 15, 8)`); err != nil {
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
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_pref_c, diel_mask, enabled) VALUES('t.off', 0.5, 15, 8, 0)`); err != nil {
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

// setTunables 写入运行参数，调用方随后 execAdminCmd(reload_tunables) 刷新缓存。
func setTunables(t *testing.T, db *store.DB, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := db.TunableSet(context.Background(), k, v, "tester"); err != nil {
			t.Fatalf("写 tunable %s: %v", k, err)
		}
	}
}

// 加成按「专一度」分档：只在一个时段出没的鱼拿满额，全天活跃的鱼不加成。
// 这是「夜行鱼夜间该涨得更多」的落点——不是把所有鱼一刀切乘同一个倍率。
func TestDielBoostScalesWithActiveSegments(t *testing.T) {
	const peak = 2.0
	cases := []struct {
		name string
		mask int64
		want float64
	}{
		{"仅夜（专性最强）", 8, 2.0},
		{"晨+暮（两段）", 1 | 4, 1 + 2.0/3},
		{"晨+昼+夜（三段）", 1 | 2 | 8, 1 + 1.0/3},
		{"全天四段", 15, 1},
		{"无档案", 0, 1},
	}
	for _, c := range cases {
		if got := dielActiveBoost(c.mask, peak); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("%s（mask=%d）期望 %.4f，实得 %.4f", c.name, c.mask, c.want, got)
		}
	}
	// 段数越少加成越高，否则「分档」就不成立。
	if !(dielActiveBoost(1|4, peak) > dielActiveBoost(1|2|8, peak)) {
		t.Fatal("两段的加成应高于三段")
	}
	// 纯夜行鱼夜间 ×2.0、白天 ×0.3 ≈ 6.7 倍；旧的一刀切 1.25/0.35 只有 3.6 倍。
	if r := dielActiveBoost(8, peak) / 0.3; r < 6.5 {
		t.Fatalf("夜间/白天对比应约 6.7 倍，实得 %.2f", r)
	}
	// 全天活跃的鱼不该因为开了昼夜权重就改变权重结构。
	if got := fishDielWeight(fishEcologyProfile{DielMask: 15}, 12, peak, 0.3); got != 1 {
		t.Fatalf("全天活跃鱼权重系数应为 1，实得 %v", got)
	}
}
