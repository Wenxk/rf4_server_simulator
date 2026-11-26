package gameserver

import (
	"context"
	"math"
	"testing"

	"rf4server/internal/store"
)

func TestHookFamilyForRealModelIDs(t *testing.T) {
	tests := map[string]string{
		"triple_20686":  "treble",
		"jhead_20001":   "jighead",
		"offset_20007":  "offset",
		"woffset_20001": "offset",
		"mhook_20870":   "marine",
		"mdhook_20932":  "marine",
		"gmhook_21205":  "marine",
		"c_hook_20712":  "carp",
		"clas_20042":    "classic",
		"orig_20008_1":  "classic",
		"hook_20001":    "other",
	}
	for itemID, want := range tests {
		if got := hookFamilyFor(itemID); got != want {
			t.Fatalf("hookFamilyFor(%q)=%q，期望 %q", itemID, got, want)
		}
	}
}

func TestSyncHookBiteProfilesUsesRealStaticData(t *testing.T) {
	s, db := newStaticRigTestServer(t)
	ctx := context.Background()
	defs, err := db.ListItemDefsByCategory(ctx, catHook)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.SyncHookBiteProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(defs) || n < 1000 {
		t.Fatalf("同步型号数=%d，静态定义=%d", n, len(defs))
	}

	p, ok := s.hookBiteProfileFor("triple_20686")
	if !ok {
		t.Fatal("真实型号 triple_20686 未进入运行时缓存")
	}
	if p.Size != 25 || math.Abs(p.LengthMM-18) > 1e-6 ||
		math.Abs(p.StrengthKg-98) > 1e-6 || p.Quality != 100 ||
		p.Family != "treble" || p.Brand != "brsrk_ultra_tr_f" {
		t.Fatalf("真实型号物理字段错误: %+v", p)
	}
	if p.SolidScale != 1 || p.HoldScale != 1 ||
		p.SmallFishFloor != 0 || p.OverCapacityHoldFloor != 0 {
		t.Fatalf("默认调参值不应改变现有手感: %+v", p)
	}
}

func TestApplyHookBiteProfileDefaultsAndOverrides(t *testing.T) {
	spec := gearPhysicsSpec{
		HookPresent: true, HookItemID: "triple_test",
		HookSize: 28, HookLengthMM: 24,
	}
	baseSolid, baseHold := hookSolidHold(100, spec)
	gotSolid, gotHold := applyHookBiteProfile(100, spec, store.HookBiteProfile{
		SolidScale: 1, HoldScale: 1,
	}, baseSolid, baseHold)
	if gotSolid != baseSolid || gotHold != baseHold {
		t.Fatalf("默认倍率改变了旧手感: base=(%f,%f) got=(%f,%f)",
			baseSolid, baseHold, gotSolid, gotHold)
	}

	gotSolid, _ = applyHookBiteProfile(100, spec, store.HookBiteProfile{
		SolidScale: 0.1, SmallFishFloor: 0.42,
	}, baseSolid, baseHold)
	if math.Abs(gotSolid-0.42) > 1e-9 {
		t.Fatalf("小鱼咬口下限未生效: %f", gotSolid)
	}

	overSolid, overHold := hookSolidHold(200_000, spec)
	_, gotHold = applyHookBiteProfile(200_000, spec, store.HookBiteProfile{
		HoldScale: 0.5, OverCapacityHoldFloor: 0.65,
	}, overSolid, overHold)
	if math.Abs(gotHold-0.65) > 1e-9 {
		t.Fatalf("超载保持下限未生效: %f", gotHold)
	}

	gotSolid, gotHold = applyHookBiteProfile(100, spec, store.HookBiteProfile{
		SolidScale: 3, HoldScale: 3,
	}, 0.96, 0.98)
	if gotSolid != 1 || gotHold != 1 {
		t.Fatalf("概率必须限幅到 1: (%f,%f)", gotSolid, gotHold)
	}
	gotSolid, gotHold = applyHookBiteProfile(100, spec, store.HookBiteProfile{
		SolidScale: 0, HoldScale: 0,
	}, 0.96, 0.98)
	if gotSolid != 0 || gotHold != 0 {
		t.Fatalf("后台设置为 0 必须真正关闭概率: (%f,%f)", gotSolid, gotHold)
	}
}

func TestHookDisplaySizeMappingIsCompleteAndOneToOne(t *testing.T) {
	// 34 档刻度全都要有展示号，且展示号不能重复——重复意味着两档钩子会被显示成
	// 同一个号，玩家按号选钩就会选错。
	if len(hookDisplaySizes) != 34 {
		t.Fatalf("刻度档位=%d，期望 34", len(hookDisplaySizes))
	}
	seen := map[string]int64{}
	for size, display := range hookDisplaySizes {
		if display == "" {
			t.Fatalf("刻度 %d 缺少展示钩号", size)
		}
		if prev, ok := seen[display]; ok {
			t.Fatalf("刻度 %d 与 %d 都映射到 %q", prev, size, display)
		}
		seen[display] = int64(size)
	}
	// 逆运算必须能换回去，后台按游戏钩号筛选依赖它。
	for size, display := range hookDisplaySizes {
		if got := hookSizeForDisplay(display); got != int64(size) {
			t.Fatalf("hookSizeForDisplay(%q)=%d，期望 %d", display, got, size)
		}
	}
	if hookDisplaySize(-1) != "" || hookDisplaySize(34) != "" {
		t.Fatal("越界刻度必须返回空串，不能臆造钩号")
	}
	if hookSizeForDisplay("S99") != -1 || hookSizeForDisplay("") != -1 {
		t.Fatal("未收录写法必须返回 -1")
	}
	// 相邻档位的钩长必须单调递增：展示号是给人看的，但如果它跟钩长不同向，
	// 「大号钩钩大鱼」这条直觉就反了。
	prevLen := -1.0
	for size := int64(0); size < 34; size++ {
		length := hookWeightCapG(0, size)
		if length < prevLen {
			t.Fatalf("刻度 %d 的容量回落，映射顺序有误", size)
		}
		prevLen = length
	}
}

// 真实静态库回归：每个钩子都要落一个游戏钩号，且与物品名里的钩号一致。
// 名字里能解析出钩号的样本必须 100% 吻合，解析不出来（如 Stinger Orig）的
// 允许存在，但不能反过来——名字与映射冲突就是硬错。
func TestHookDisplaySizeMatchesStaticItemNames(t *testing.T) {
	s, _ := newStaticRigTestServer(t)
	ctx := context.Background()
	if _, err := s.SyncHookBiteProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.ListHookBiteProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 1000 {
		t.Fatalf("钩子档案只有 %d 条，静态数据没加载全", len(rows))
	}
	missing := 0
	for _, p := range rows {
		if p.DisplaySize == "" {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("%d 个钩子没有游戏钩号", missing)
	}
	// 抽查几个玩家实际在用的型号，钉住「库里刻度 → 界面 S 号」的对照。
	checks := map[string]string{
		"clas_20042":    "S20",
		"clas_20043":    "S18",
		"clas_20044":    "S16",
		"clas_20045":    "S14",
		"orig_20008_16": "S16",
		"orig_20008_1":  "S1",
		"triple_20686":  "S4/0",
	}
	for itemID, want := range checks {
		p, ok := s.hookBiteProfileFor(itemID)
		if !ok {
			t.Fatalf("型号 %s 未进入缓存", itemID)
		}
		if p.DisplaySize != want {
			t.Fatalf("%s 的展示钩号=%q，期望 %q（内部刻度 %d，钩长 %.2fmm）",
				itemID, p.DisplaySize, want, p.Size, p.LengthMM)
		}
	}
}

func TestMountedHookCarriesItemIDIntoPhysicsSpec(t *testing.T) {
	s, _ := newRigTestServer(t)
	ctx := context.Background()
	const uid = int64(1000001)
	rod, err := s.CreateItem(ctx, uid, "spin_10001_S60L", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := s.CreateItem(ctx, uid, "triple_20686", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	sess := newProbeSess(s)
	mountGearForTest(t, s, sess, rod.GUID, hook.GUID)

	spec := s.loadGearSpecForRig(ctx, s.rigGearIDs(ctx, uid, rod.GUID), rod.GUID)
	if !spec.HookPresent || spec.HookItemID != "triple_20686" {
		t.Fatalf("装配钩型号未进入物理裁决: %+v", spec)
	}
}
