package store

import (
	"context"
	"testing"
)

// 钓法倾向矩阵：出厂值随迁移写入，保存/恢复可往返，越界值必须被拒。
//
// 这张表的键只有 20 个（5 钓法族 × 4 水层档），所以测试重点不是"能不能存"，
// 而是"存进去的键能不能被判鱼侧按同一套拼法取回来"——两边键不一致时判鱼会静默退回 1.0，
// 后台改了像没改。RigAffinityKey 就是为此存在的唯一拼接口径。
func TestRigAffinityDefaultsAndRoundTrip(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()

	loaded, defaultsOnly, err := db.LoadRigAffinity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaultsOnly {
		t.Fatal("迁移应已写入出厂矩阵，不该走默认兜底")
	}
	if len(loaded) != len(RigFamilyOrder)*len(LayerBandOrder) {
		t.Fatalf("矩阵应有 %d 格，实得 %d", len(RigFamilyOrder)*len(LayerBandOrder), len(loaded))
	}
	// 出厂语义：底钓偏底层、浮钓/飞钓偏表层、跨两档以上明显压制。
	if got := loaded[RigAffinityKey(RigFamilyBottom, LayerBandBottom)]; got <= 1 {
		t.Fatalf("底钓×底层应 >1，实得 %g", got)
	}
	if got := loaded[RigAffinityKey(RigFamilyFloat, LayerBandSurface)]; got <= 1 {
		t.Fatalf("浮钓×表层应 >1，实得 %g", got)
	}
	if got := loaded[RigAffinityKey(RigFamilyBottom, LayerBandSurface)]; got >= 0.5 {
		t.Fatalf("底钓×表层应明显 <1，实得 %g", got)
	}

	// 保存三格，未给的行保持原值。
	before := loaded[RigAffinityKey(RigFamilyLure, LayerBandBottom)]
	n, err := db.SaveRigAffinity(ctx, []RigAffinityWeight{
		{RigFamilyBottom, LayerBandBottom, 1.9},
		{RigFamilyFloat, LayerBandSurface, 2.2},
		{RigFamilyFly, LayerBandMidLower, 0.55},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("应写入 3 格，实得 %d", n)
	}
	loaded, _, err = db.LoadRigAffinity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded[RigAffinityKey(RigFamilyBottom, LayerBandBottom)]; got != 1.9 {
		t.Fatalf("底钓×底层应为 1.9，实得 %g", got)
	}
	if got := loaded[RigAffinityKey(RigFamilyLure, LayerBandBottom)]; got != before {
		t.Fatalf("未提交的格子不该变：%g -> %g", before, got)
	}

	// 越界与未知键必须报错，不能静默写入（判鱼侧取不到就等于没生效）。
	if _, err := db.SaveRigAffinity(ctx, []RigAffinityWeight{{RigFamilyBottom, LayerBandBottom, 0.01}}); err == nil {
		t.Fatal("低于下限应报错")
	}
	if _, err := db.SaveRigAffinity(ctx, []RigAffinityWeight{{RigFamilyBottom, LayerBandBottom, 9}}); err == nil {
		t.Fatal("高于上限应报错")
	}
	if _, err := db.SaveRigAffinity(ctx, []RigAffinityWeight{{"trawler", LayerBandBottom, 1}}); err == nil {
		t.Fatal("未知钓法族应报错")
	}
	if _, err := db.SaveRigAffinity(ctx, []RigAffinityWeight{{RigFamilyBottom, "abyss", 1}}); err == nil {
		t.Fatal("未知水层档应报错")
	}

	// 恢复出厂：先前改的三格回到默认。
	if _, err := db.RestoreRigAffinity(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = db.LoadRigAffinity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	def := RigAffinityDefaultMap()
	for _, d := range RigAffinityDefaults() {
		if got := loaded[d.Key()]; got != d.Weight {
			t.Fatalf("%s 恢复后应为 %g，实得 %g", d.Key(), d.Weight, got)
		}
	}
	if len(def) != len(loaded) {
		t.Fatalf("恢复后格数应回到 %d，实得 %d", len(def), len(loaded))
	}
}

// 矩阵视图必须补齐缺失格：读不到的行按出厂值展示，避免后台出现空白输入框。
func TestRigAffinityMatrixViewFillsGaps(t *testing.T) {
	view := RigAffinityMatrixView(map[string]float64{
		RigAffinityKey(RigFamilyBottom, LayerBandBottom): 2.0,
	})
	if len(view) != len(RigFamilyOrder)*len(LayerBandOrder) {
		t.Fatalf("应为 20 格，实得 %d", len(view))
	}
	byKey := map[string]float64{}
	for _, r := range view {
		byKey[r.Key()] = r.Weight
	}
	if byKey[RigAffinityKey(RigFamilyBottom, LayerBandBottom)] != 2.0 {
		t.Fatal("已存的行应保留原值")
	}
	def := RigAffinityDefaultMap()
	if byKey[RigAffinityKey(RigFamilyFly, LayerBandSurface)] != def[RigAffinityKey(RigFamilyFly, LayerBandSurface)] {
		t.Fatal("缺失的行应补出厂值")
	}
}
