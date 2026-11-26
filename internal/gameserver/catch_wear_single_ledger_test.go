package gameserver

// 钓获磨损的「单层账」：渔轮与鱼竿外层那格耐久必须由内部零件聚合得出，
// 不能与零件各算一套。历史缺陷见 reel_part_wear.go 单层化注释与
// prepareCatchWear 的说明（conv_5706 外层 0.99302 vs 聚合 0.99915 差 0.61%）。

import (
	"context"
	"math"
	"testing"

	"rf4server/internal/proto"
	"rf4server/internal/store"
)

// singleLedgerReelFixture 造一只装在钓组上的渔轮，并把它挂到 rig_component。
// 返回 rig GUID 与轮 GUID。零件树由出厂装配保证齐全（含摩擦片）。
func singleLedgerReelFixture(t *testing.T) (*Server, *store.DB, context.Context, string, string) {
	t.Helper()
	srv, db, ctx, reelGUID, _ := wearReelFixture(t)
	rig, err := srv.CreateItem(ctx, wearTestUID, "spin_10001_S60L", "backpack", 1)
	if err != nil {
		t.Fatalf("造竿失败: %v", err)
	}
	if err := db.UpsertRigComponentAt(ctx, rig.GUID, catReel, 0, reelGUID, 0); err != nil {
		t.Fatalf("挂轮失败: %v", err)
	}
	return srv, db, ctx, rig.GUID, reelGUID
}

// TestCatchWearReelKeepsSingleLedger 确认「每次钓获」这条通道对渔轮也是单层账：
// 外层耐久必须等于扣完后零件的聚合值，且零件确实被磨掉了。
func TestCatchWearReelKeepsSingleLedger(t *testing.T) {
	srv, _, ctx, rigGUID, reelGUID := singleLedgerReelFixture(t)
	wear, err := srv.prepareCatchWear(ctx, wearTestUID, rigGUID)
	if err != nil {
		t.Fatalf("准备磨损失败: %v", err)
	}
	var change *catchWear
	for i := range wear {
		if wear[i].row != nil && wear[i].row.GUID == reelGUID {
			change = &wear[i]
			break
		}
	}
	if change == nil {
		t.Fatal("渔轮应出现在钓获磨损清单里")
	}
	if change.durability >= change.row.Durability {
		t.Fatalf("渔轮耐久应下降：%.9f -> %.9f", change.row.Durability, change.durability)
	}
	// 字段快照里的耐久必须与 catchWear.durability 一致，否则结算事务会写入两套数字。
	fields, err := FieldsFromJSON(change.fields)
	if err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	if got, ok := fieldWireFloat(fields, change.row.WireClass, csDurability); !ok {
		t.Fatal("快照里读不到耐久字段")
	} else if math.Abs(got-change.durability) > 1e-6 {
		t.Fatalf("快照耐久 %.9f 与 durability %.9f 不一致", got, change.durability)
	}
	// 结算写库后，外层那格必须等于零件聚合值 —— 这就是"单层账"的判据。
	snapshot := *change.row
	snapshot.FieldsJSON = change.fields
	snapshot.Durability = change.durability
	after, err := FieldsFromJSON(snapshot.FieldsJSON)
	if err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	body, ok := srv.reelBodyDurabilityInFields(ctx, &snapshot, after)
	if !ok {
		t.Fatal("应能从零件聚合出本体耐久")
	}
	if math.Abs(change.durability-body) > 1e-9 {
		t.Fatalf("外层耐久必须等于零件聚合：外层 %.9f 聚合 %.9f", change.durability, body)
	}
	// 零件本身也要真的被扣：只改外层属于历史缺陷。
	wornAny := false
	for _, category := range reelWearPartCategories {
		if _, _, dur := srv.reelPartStateFromRow(ctx, &snapshot, category); dur < 1 {
			wornAny = true
		}
	}
	if !wornAny {
		t.Fatal("钓获磨损必须落到轮内零件上，不能只改外层")
	}
}

// TestCatchWearRodKeepsSingleLedger 确认鱼竿侧同样单层：外层 = 竿坯/导环的最弱件。
func TestCatchWearRodKeepsSingleLedger(t *testing.T) {
	srv, _, ctx, _, _ := singleLedgerReelFixture(t)
	rig, err := srv.CreateItem(ctx, wearTestUID, "spin_10001_S60L", "backpack", 1)
	if err != nil {
		t.Fatalf("造竿失败: %v", err)
	}
	wear, err := srv.prepareCatchWear(ctx, wearTestUID, rig.GUID)
	if err != nil {
		t.Fatalf("准备磨损失败: %v", err)
	}
	var change *catchWear
	for i := range wear {
		if wear[i].row != nil && wear[i].row.GUID == rig.GUID {
			change = &wear[i]
			break
		}
	}
	if change == nil {
		t.Fatal("鱼竿应出现在钓获磨损清单里")
	}
	if change.durability >= change.row.Durability {
		t.Fatalf("鱼竿耐久应下降：%.9f -> %.9f", change.row.Durability, change.durability)
	}
	fields, err := FieldsFromJSON(change.fields)
	if err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	body, ok := srv.rodBodyDurabilityInFields(ctx, fields)
	if !ok {
		t.Fatal("应能从竿坯/导环聚合出竿本体耐久")
	}
	if math.Abs(change.durability-body) > 1e-9 {
		t.Fatalf("竿外层耐久必须等于零件聚合：外层 %.9f 聚合 %.9f", change.durability, body)
	}
	// 竿坯与导环至少有一件被扣。子件是本次在快照里物化的，旧行的 FieldsJSON
	// 还没有它们，所以必须从 change.fields 读。
	wornAny := false
	for _, category := range reelRodWearPartCategories {
		if dur, ok := rodPartDurabilityInFields(ctx, srv, fields, category); ok && dur < 1 {
			wornAny = true
		}
	}
	if !wornAny {
		t.Fatal("钓获磨损必须落到竿坯/导环上")
	}
}

// TestCatchWearOtherKindsStayOuterOnly 守住没有内部件的品类仍按外层固定扣：
// 鱼线/鱼钩/浮漂/拟饵没有零件账，改成聚合会直接不磨损。
func TestCatchWearOtherKindsStayOuterOnly(t *testing.T) {
	srv, db, ctx, rigGUID, _ := singleLedgerReelFixture(t)
	line, err := srv.CreateItem(ctx, wearTestUID, "mono_40853", "backpack", 1)
	if err != nil {
		t.Fatalf("造线失败: %v", err)
	}
	if err := db.UpsertRigComponentAt(ctx, rigGUID, catLine, 0, line.GUID, 0); err != nil {
		t.Fatalf("挂线失败: %v", err)
	}
	wear, err := srv.prepareCatchWear(ctx, wearTestUID, rigGUID)
	if err != nil {
		t.Fatalf("准备磨损失败: %v", err)
	}
	found := false
	for _, change := range wear {
		if change.row == nil || change.row.GUID != line.GUID {
			continue
		}
		found = true
		want := math.Max(0, change.row.Durability-wearPerCatch["line"])
		if math.Abs(change.durability-want) > 1e-12 {
			t.Fatalf("鱼线应只按外层固定扣 %.9f，实得 %.9f", want, change.durability)
		}
	}
	if !found {
		t.Fatal("鱼线应出现在钓获磨损清单里")
	}
}
// fieldWireFloat 按 wire class 的字段操作码读回一个 float 字段（U32 承载 float32 位型）。
func fieldWireFloat(fields []proto.Field, class, csName string) (float64, bool) {
	idx := fieldIdxByCS(class, csName)
	if idx < 0 || idx >= len(fields) {
		return 0, false
	}
	switch fieldOpAt(class, idx) {
	case "FLOAT", "DOUBLE":
		return fields[idx].Float, true
	case "U32":
		return float64(proto.F32FromBits(uint32(u64Of(fields[idx].Num)))), true
	}
	return 0, false
}

// rodPartDurabilityInFields 从字段树里读竿侧指定品类零件的耐久。
func rodPartDurabilityInFields(ctx context.Context, srv *Server, fields []proto.Field, category int64) (float64, bool) {
	child, _, _, ok := srv.selectedReelPart(ctx, fields, category)
	if !ok || child == nil {
		return 0, false
	}
	return reelChildDurability(child), true
}
