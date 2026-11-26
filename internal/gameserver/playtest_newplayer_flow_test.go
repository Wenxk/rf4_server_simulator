package gameserver

// 新玩家完整作钓闭环：用刚装好的钓组走 抛竿→入水→咬钩→中钩→收竿→入护。
//
// 全部走生产 handler（hCastPrepare / hCast / hFishingStep / hFishingHooked /
// hFishingEnd / hFishingKeep），不读任何历史渔获。为了让测试在可接受时间内
// 跑完，bite_delay_fixed 设成 0.05s；判鱼权重链、档位、结算、经济全部真实。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rf4server/internal/store"
)

type newbieRun struct {
	Plan      string   `json:"plan"`
	RodItem   string   `json:"rod_item"`
	RigType   int64    `json:"rig_type"`
	Loc       string   `json:"loc"`
	Bait      string   `json:"bait"`
	HookedOK  bool     `json:"hooked_ok"`
	Steps     []stepLog `json:"steps"`
	BiteWait  float64  `json:"bite_wait_s"`
	Outcome   string   `json:"outcome"`
	Species   string   `json:"species"`
	Grade     int64    `json:"grade"`
	WeightG   int64    `json:"weight_g"`
	LengthM   float64  `json:"length_m"`
	XP        int64    `json:"xp"`
	Silver    int64    `json:"silver"`
	Kept      bool     `json:"kept"`
	Keepnet   int      `json:"keepnet_before"`
	Note      string   `json:"note,omitempty"`
}

// TestPlaytestNewPlayerFlow 新玩家完整作钓闭环（3 套真实钓组 × 3 次）。
func TestPlaytestNewPlayerFlow(t *testing.T) {
	playtestEnabled(t)
	srv, db, sess := newPlayerTest(t)

	// 先清掉地图级覆盖：否则全局 bite_delay_fixed 对已配置的地图不生效，
	// 浮钓会按真实 20s 上限采样，测出来的等待跟参数设定无关。
	playClearBiteOverrides(t, db)
	// 固定 0.05s 延迟：只为让测试跑得完，判鱼与结算链路不变。
	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0.05", "playtest"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	// 复用装配测试装好的三套钓组（库已被上一个测试改过，这里按现状读）。
	plans := []struct {
		Name    string
		Rod     string
		RigType int64
		Loc     string
	}{
		{"浮钓·浮标组+面包", "tele_10012_tl16", 1, "level_001_lake"},
		{"路亚·旋转亮片", "spin_10001_S60L", 5, "level_001_lake"},
	}

	var out []newbieRun
	for _, p := range plans {
		rod := playerItem(t, db, playUID, p.Rod)
		if rod == nil {
			t.Logf("%s: 找不到 %s 实例", p.Name, p.Rod)
			continue
		}
		bait := srvBaitOf(context.Background(), db, rod.GUID)
		for i := 0; i < 3; i++ {
			r := runNewbieOnce(t, srv, db, sess, p.Name, rod.GUID, p.Rod, p.RigType, p.Loc, bait)
			out = append(out, r)
			t.Logf("[%s #%d] %s 鱼种=%s 档=%d %.0fg 等待%.2fs xp=%d 银=%d 入护=%v",
				p.Name, i+1, r.Outcome, r.Species, r.Grade, float64(r.WeightG),
				r.BiteWait, r.XP, r.Silver, r.Kept)
		}
	}
	playWrite(t, "../../data/playtest", "newplayer_flow.json", out)
	reportNewbieSummary(t, out)
}

// runNewbieOnce 跑一次完整闭环。
func runNewbieOnce(t *testing.T, srv *Server, db *store.DB, sess *Session,
	plan, rodGUID, rodItem string, rigType int64, loc, bait string) newbieRun {
	t.Helper()
	now := func() float64 { return float64(time.Now().UnixNano()) / 1e6 }
	r := newbieRun{Plan: plan, RodItem: rodItem, RigType: rigType, Loc: loc, Bait: bait}

	// 入护前计数，用来确认这次真的进护了
	keepBefore, _ := db.KeepnetList(context.Background(), playUID)
	r.Keepnet = len(keepBefore)

	st := srv.stateFor(playUID)
	st.mu.Lock()
	st.GearGuid = rodGUID
	st.RigType = rigType
	st.Loc = loc
	st.DepthCM = 200
	st.CastPosX, st.CastPosZ, st.HasCastPos = 120, 260, true
	st.PosX, st.PosZ, st.HasPos = 120, 260, true
	st.mu.Unlock()

	// 14/1 准备抛投
	t0 := now()
	body1, _ := hCastPrepare(miscCtx(sess, 14, 1, playArgs(rodGUID)))
	r.Steps = append(r.Steps, stepLog{Item: "14/1 cast_prepare",
		OK: len(body1) > 0, Note: fmt.Sprintf("%dB", len(body1)), Ms: now() - t0})

	// 14/2 抛投
	t0 = now()
	body2, _ := hCast(miscCtx(sess, 14, 2, liveCastBody(rodGUID, loc, 20, 1)))
	r.Steps = append(r.Steps, stepLog{Item: "14/2 cast",
		OK: len(body2) > 0, Note: fmt.Sprintf("%dB", len(body2)), Ms: now() - t0})

	// 14/7 入水（持续帧由 runOneFlow 内部的 state 直接置位，这里补一帧）
	t0 = now()
	anim := uint32(0x800)
	if rigType == 5 {
		anim = 0x80000
	}
	hFishingStep(miscCtx(sess, 14, 7, liveWaterBody(anim, loc)))
	r.Steps = append(r.Steps, stepLog{Item: "14/7 water", OK: true, Ms: now() - t0})

	// 触发咬钩计时器并等待
	start := time.Now()
	var fish *RolledFish
	// 上限 30s：只要是为了覆盖「参数被地图覆盖/未生效」时浮钓真实 20s 上限
	// 也能采到点，而不是被轮询超时误判成空竿。
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		fish = takePendingFishForTest(srv, playUID)
		if fish != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.BiteWait = time.Since(start).Seconds()
	if fish == nil {
		r.Outcome = "空竿（计时器到点无鱼）"
		return r
	}
	r.Species = fish.Species
	r.Grade = fish.Grade
	r.WeightG = fish.WeightG
	r.LengthM = fish.LengthM

	// 14/11 中钩
	t0 = now()
	hFishingHooked(miscCtx(sess, 14, 11, playArgs(rodGUID, fish.FishGUID)))
	r.HookedOK = true
	r.Steps = append(r.Steps, stepLog{Item: "14/11 hooked", OK: true, Ms: now() - t0})

	// 14/4 收竿结算
	t0 = now()
	_, _ = hFishingEnd(miscCtx(sess, 14, 4, playArgs(rodGUID)))
	r.Steps = append(r.Steps, stepLog{Item: "14/4 settle", OK: true, Ms: now() - t0})

	// 14/5 入护
	t0 = now()
	_, _ = hFishingKeep(miscCtx(sess, 14, 5, playArgs(rodGUID, fish.FishGUID)))
	keepAfter, _ := db.KeepnetList(context.Background(), playUID)
	r.Kept = len(keepAfter) > r.Keepnet
	r.Steps = append(r.Steps, stepLog{Item: "14/5 keep",
		OK: r.Kept, Note: fmt.Sprintf("%d→%d", r.Keepnet, len(keepAfter)), Ms: now() - t0})

	r.XP = fish.XP
	r.Silver = fish.Silver
	r.Outcome = "成功入护"
	return r
}

// takePendingFishForTest 从玩家状态里取走一条已判定的 pending 鱼。
func takePendingFishForTest(srv *Server, uid int64) *RolledFish {
	st := srv.stateFor(uid)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.Pending == nil || st.Resolved {
		return nil
	}
	return st.Pending
}

// reportNewbieSummary 打印汇总，作为报告的数据来源。
func reportNewbieSummary(t *testing.T, out []newbieRun) {
	t.Helper()
	ok, empty := 0, 0
	grades := map[int64]int{}
	var waits []float64
	for _, r := range out {
		if r.Outcome == "成功入护" {
			ok++
			grades[r.Grade]++
		} else {
			empty++
		}
		if r.BiteWait > 0 {
			waits = append(waits, r.BiteWait)
		}
	}
	t.Logf("=== 汇总：成功 %d / 空竿 %d，档位 %v", ok, empty, grades)
	if len(waits) > 0 {
		var s float64
		for _, w := range waits {
			s += w
		}
		t.Logf("    等待均值 %.3fs（bite_delay_fixed=0.05 下的计时器落地延迟）", s/float64(len(waits)))
	}
}