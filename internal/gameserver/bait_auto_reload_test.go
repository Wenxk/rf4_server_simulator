package gameserver

// 自动补饵回归（用户口径 2026-11-20）：鱼把钩饵吃掉、杆还插在水里时，
// 必须当场补挂同款再继续钓。
//
// 背景：吐饵（14/12 f0=1）扣掉钩饵的 4/21 和鱼离场是同一帧到的，客户端侧
// 完全看不出钩已经空了；玩家只会觉得"这条没上钩，还能继续等"。而服务端
// 下一轮咬钩会卡在 canFishWithBait 上——置 Resolved 直接返回，不发鱼也
// 不打日志，一杆就此静默死亡（实机 20:55:03 吐饵后静默 4 分钟）。

import (
	"testing"

	"rf4server/internal/proto"
	"rf4server/internal/wiregen"
)

func TestSpitAutoReloadsBaitAndKeepsRodFishing(t *testing.T) {
	s, db := newRigTestServer(t)
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopFishingRecoveryTimers(s) })
	sess, _ := newTunnelProbe(t, s)
	s.sessions[sess.ID] = sess
	uid := sess.Userid
	if _, err := db.EnsureProfile(t.Context(), uid); err != nil {
		t.Fatal(err)
	}

	rod := baitTestRod(t, s)
	mountHookForBaitTest(t, s, rod, 0)
	// 挂饵时已从来源堆预留 1 份：5 → 4。
	source, child := baitTestMount(t, s, rod, "worm_01", 5, 350)

	fish := &RolledFish{FishGUID: newFishGUID(), Species: "crucian", Grade: 1,
		WeightG: 500, LengthM: .25, BaitsKnown: true, BaitGUIDs: child}
	st := s.fishingStateForRod(uid, rod)
	st.mu.Lock()
	st.GearGuid = rod
	st.Loc = "unknown-map"
	st.Bait = "worm_01"
	st.CastActive, st.InWater = true, true
	st.Pending, st.PushSent, st.Hooked = fish, true, true
	st.Resolved = false
	st.mu.Unlock()
	t.Cleanup(func() {
		st.mu.Lock()
		st.cancelBiteTimerLocked()
		st.cancelPendingTimerLocked()
		st.mu.Unlock()
	})

	tree := rigRequestTree(t, proto.Guid(rod), proto.Guid(fish.FishGUID),
		wiregen.MustMakeObj("lhicjpmglbo", map[int]*proto.Value{0: proto.U32(1)}))
	if _, ok := hFishingLeft(miscCtx(sess, 14, 12, tree)); !ok {
		t.Fatal("14/12 spit was not handled")
	}

	// 一扣一补：来源堆 4 → 3（补饵再从背包预留一份），钩上必须重新有同款。
	if row, _ := db.GetItemByGUID(t.Context(), source); row == nil || baitQuantity(row) != 3 {
		t.Fatalf("spit + auto reload should leave 3 in the source stack, got %+v", row)
	}
	if got := s.currentBaits(t.Context(), rod); len(got) != 1 || got[0] != "worm_01" {
		t.Fatalf("hook bait after spit = %v, want one worm_01", got)
	}
	// 补饵完成必须清掉 rebait 记录，否则下次收竿会再补一份。
	var pendingRecords int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM rod_setting WHERE guid=? AND key LIKE 'rebait:%'`, rod).Scan(&pendingRecords); err != nil {
		t.Fatal(err)
	}
	if pendingRecords != 0 {
		t.Fatalf("auto reload left %d pending rebait records", pendingRecords)
	}
}

// 库存没有同款时不能凭空变出饵：钩保持空，rebait 记录留给下一次抛竿。
func TestSpitWithoutStockLeavesHookEmpty(t *testing.T) {
	s, db := newRigTestServer(t)
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopFishingRecoveryTimers(s) })
	sess, _ := newTunnelProbe(t, s)
	s.sessions[sess.ID] = sess
	uid := sess.Userid
	if _, err := db.EnsureProfile(t.Context(), uid); err != nil {
		t.Fatal(err)
	}

	rod := baitTestRod(t, s)
	mountHookForBaitTest(t, s, rod, 0)
	// 只挂 1 份：扣掉之后背包里没有同款可补。
	_, child := baitTestMount(t, s, rod, "worm_01", 1, 350)

	fish := &RolledFish{FishGUID: newFishGUID(), Species: "crucian", Grade: 1,
		WeightG: 500, LengthM: .25, BaitsKnown: true, BaitGUIDs: child}
	st := s.fishingStateForRod(uid, rod)
	st.mu.Lock()
	st.GearGuid = rod
	st.Loc = "unknown-map"
	st.Bait = "worm_01"
	st.CastActive, st.InWater = true, true
	st.Pending, st.PushSent, st.Hooked = fish, true, true
	st.Resolved = false
	st.mu.Unlock()
	t.Cleanup(func() {
		st.mu.Lock()
		st.cancelBiteTimerLocked()
		st.cancelPendingTimerLocked()
		st.mu.Unlock()
	})

	tree := rigRequestTree(t, proto.Guid(rod), proto.Guid(fish.FishGUID),
		wiregen.MustMakeObj("lhicjpmglbo", map[int]*proto.Value{0: proto.U32(1)}))
	if _, ok := hFishingLeft(miscCtx(sess, 14, 12, tree)); !ok {
		t.Fatal("14/12 spit was not handled")
	}

	if got := s.currentBaits(t.Context(), rod); len(got) != 0 {
		t.Fatalf("hook bait without stock = %v, want empty", got)
	}
	// 记录必须留着：下一次 prepareBaitForCast 还要按它补。
	var pendingRecords int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM rod_setting WHERE guid=? AND key LIKE 'rebait:%'`, rod).Scan(&pendingRecords); err != nil {
		t.Fatal(err)
	}
	if pendingRecords == 0 {
		t.Fatal("rebait record should survive when the bag has no matching bait")
	}
}
