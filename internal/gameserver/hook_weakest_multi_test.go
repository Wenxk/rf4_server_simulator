package gameserver

import (
	"context"
	"testing"
)

// 多钩钓组（rig 59 海竿三钩位 250/251/252）的断件归属。
//
// 客户端遍历渔具链，每个鱼钩各自报告自己的承力上限，取 limit 最小的那件
// 塞进 17/9（FishingSet.cs:6017）。所以服务端兜底裁决时也必须把每一枚钩
// 都当候选，不能只认主钩：否则客户端上报的是支线钩，服务端却扣掉主钩，
// 玩家 UI 上会看到另一枚钩凭空消失。
//
// 这里用「主钩 660kg 强钩 + 支线钩 3.15kg 弱钩 + 13.6kg 主线 + 4.09kg 竿」
// 构造：客户端会报支线弱钩；兜底裁决也该判支线弱钩，不能判成主钩。
func TestWeakestHookPicksAuxiliaryHookNotMainHook(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	const uid = int64(1000001)

	rod, err := srv.CreateItem(ctx, uid, "boatr_10222_SBH80_100", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !srv.rigSetType(ctx, uid, rod.GUID, 59) {
		t.Fatal("设置多钩钓组类型失败")
	}
	line, err := srv.CreateItem(ctx, uid, "mono_40853", "backpack", 1) // 13.6kg
	if err != nil {
		t.Fatal(err)
	}
	strongHook, err := srv.CreateItem(ctx, uid, "triple_21070", "backpack", 1) // 660kg 主钩
	if err != nil {
		t.Fatal(err)
	}
	weakHook, err := srv.CreateItem(ctx, uid, "orig_20008_18", "backpack", 1) // 3.15kg 支线钩
	if err != nil {
		t.Fatal(err)
	}
	sess := newProbeSess(srv)
	mountGearForTest(t, srv, sess, rod.GUID, line.GUID)
	for i, guid := range []string{strongHook.GUID, weakHook.GUID} {
		res := srv.rigSetComponent(ctx, uid, rod.GUID, catHook, guid, int64(250+i),
			rigSetOpts{ParentType: familyRod, EnforceCategory: true})
		if res == nil || res.Rejected || res.MountedGUID == "" {
			t.Fatalf("装配第 %d 枚钩失败: %+v", i, res)
		}
	}
	// 多钩同品类都落 slot=12，靠 ordinal 区分；取首条即主钩。
	if mainHook, err := db.GetItemGUIDByRigSlot(ctx, rod.GUID, catHook); err != nil {
		t.Fatal(err)
	} else if mainHook != strongHook.GUID {
		t.Fatalf("主钩（ordinal 最小）应是强钩，实得 %s", shortGUID(mainHook))
	}
	rows, err := db.ListRigComponents(ctx, rod.GUID)
	if err != nil {
		t.Fatal(err)
	}
	var hooks []string
	for _, r := range rows {
		if r.Slot == catHook {
			hooks = append(hooks, r.GUID)
		}
	}
	if len(hooks) != 2 {
		t.Fatalf("应装两枚钩，实得 %d: %+v", len(hooks), hooks)
	}

	// 兜底裁决：上报件传空，强制走 resolveGearBreak。
	decision, err := srv.resolveGearBreak(ctx, uid, rod.GUID, "")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Part != "hook" {
		t.Fatalf("最弱环应判钩，实得 part=%q kg-part=%+v", decision.Part, decision)
	}
	if decision.GUID != weakHook.GUID {
		t.Fatalf("最弱环应判支线弱钩 %s，实得 %s（主钩 %s）",
			shortGUID(weakHook.GUID), shortGUID(decision.GUID), shortGUID(strongHook.GUID))
	}

	// 端到端：客户端上报支线钩断，落库的必须是这枚支线钩，主钩还在。
	outcome, err := srv.breakLineOrRod(ctx, uid, rod.GUID, weakHook.GUID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.ComponentBroken || outcome.BrokenGUID != weakHook.GUID {
		t.Fatalf("应断支线钩 %s: %+v", shortGUID(weakHook.GUID), outcome)
	}
	if row, _ := db.GetItemByGUID(ctx, weakHook.GUID); row != nil {
		t.Fatalf("断掉的支线钩应销毁，实得 %+v", row)
	}
	if row, _ := db.GetItemByGUID(ctx, strongHook.GUID); row == nil {
		t.Fatal("主钩不该被连坐：客户端上报的是支线钩")
	}
	if mainHook, err := db.GetItemGUIDByRigSlot(ctx, rod.GUID, catHook); err != nil {
		t.Fatal(err)
	} else if mainHook != strongHook.GUID {
		t.Fatalf("主钩位应仍是强钩 %s，实得 %s",
			shortGUID(strongHook.GUID), shortGUID(mainHook))
	}
}

// 钩已进承力链的唯一真实路径：客户端上报的件直接落库（不重算最弱环），
// 因为客户端自己就是按最弱环选件的（FishingSet.cs:6017 取 limit 最小者）。
// 这里锁定"强钩 + 弱线"下上报钩仍然断钩：服务端不拿最弱环去推翻客户端。
func TestReportedHookBreakIsTrustedOverWeakerLine(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	const uid = int64(1000001)

	rod, err := srv.CreateItem(ctx, uid, "tele_10012_tl16", "backpack", 1) // 4.09kg 竿
	if err != nil {
		t.Fatal(err)
	}
	// 5.0kg 主线：比钩强，但比竿弱；一旦按最弱环重算就会误判成断线。
	line, err := srv.CreateItem(ctx, uid, "mono_15095", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	strongHook, err := srv.CreateItem(ctx, uid, "clas_20163", "backpack", 1) // 13.3kg 强钩
	if err != nil {
		t.Fatal(err)
	}
	sess := newProbeSess(srv)
	mountGearForTest(t, srv, sess, rod.GUID, line.GUID)
	mountGearForTest(t, srv, sess, rod.GUID, strongHook.GUID)

	// 前置：确认这条钓组的最弱环确实是主线，不是钩。
	spec := srv.loadGearSpecForRig(ctx, srv.rigGearIDs(ctx, uid, rod.GUID), rod.GUID)
	if spec.HookStrengthKg <= spec.LineStrengthKg {
		t.Fatalf("用例前提不成立：钩应强于主线，hook=%f line=%f",
			spec.HookStrengthKg, spec.LineStrengthKg)
	}

	outcome, err := srv.breakLineOrRod(ctx, uid, rod.GUID, strongHook.GUID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.ComponentBroken || outcome.BrokenGUID != strongHook.GUID {
		t.Fatalf("应采信客户端上报断钩: %+v", outcome)
	}
	if outcome.LineBroken {
		t.Fatalf("不应按最弱环改判断线: %+v", outcome)
	}
	if row, _ := db.GetItemByGUID(ctx, strongHook.GUID); row != nil {
		t.Fatalf("断钩后钩应销毁，实得 %+v", row)
	}
	if row, _ := db.GetItemByGUID(ctx, line.GUID); row == nil {
		t.Fatal("断钩不应连带销毁主线")
	}
	if row, _ := db.GetItemByGUID(ctx, rod.GUID); row == nil || row.Durability <= 0 {
		t.Fatalf("断钩不应损坏鱼竿: %+v", row)
	}
}
