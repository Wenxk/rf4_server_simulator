package gameserver

// 全新玩家在**真实默认参数**下的作钓节奏与手感。
//
// 与 TestPlaytestNewPlayerFlow 的区别：那个用 bite_delay_fixed=0.05 只为把
// 闭环跑通；这里不改任何延迟参数，测的是玩家真正会体验到的等待、鱼种、
// 档位、收益。这是报告里「手感」一节的数据来源。
//
// 关键前提（都是踩过的坑，写在这里避免重犯）：
//   - 地图级 fish_bite_setting 有覆盖时会整体替换全局延迟参数 → 先清；
//   - 会话必须注册进 srv.sessions，否则计时器回调当成断线直接 return；
//   - 浮钓/底钓要吃 rig-settled 12s 门，必须持续发 14/7 帧；
//   - 只能统计本次 14/14/pending 产生的鱼，不读任何历史渔获。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestNewPlayerPacing -v -timeout 60m

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"rf4server/internal/store"
)

// pacingRow 一竿的观测记录。
type pacingRow struct {
	Plan     string  `json:"plan"`
	Species  string  `json:"species"`
	Grade    int64   `json:"grade"`
	WeightG  int64   `json:"weight_g"`
	WaitS    float64 `json:"wait_s"`
	XP       int64   `json:"xp"`
	Silver   int64   `json:"silver"`
	Kept     bool    `json:"kept"`
	TimedOut bool    `json:"timed_out"`
	Loc      string  `json:"loc"`
	Bait     string  `json:"bait"`
	RigType  int64   `json:"rig_type"`
	Note     string  `json:"note,omitempty"`
}

func TestPlaytestNewPlayerPacing(t *testing.T) {
	playtestEnabled(t)
	srv, db, sess := newPlayerTest(t)
	playClearBiteOverrides(t, db)

	// 强制回到真实默认值：其它用例会把 bite_delay_fixed 设成很小的值来提速，
	// 那份修改落在同一个 fresh.db 里，不清就会把「真实等待」测成 0.3s。
	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0", "playtest"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	// 确认当前是默认参数（不固定延迟），并把实际值记进报告
	scale := srv.tunableF("bite_delay_scale", 1)
	maxDelay := srv.tunableF("bite_delay_max", 20)
	lureMax := srv.tunableF("bite_delay_lure_max", 6)
	fixed := srv.tunableF("bite_delay_fixed", 0)
	pace := srv.tunableF("bite_pace_enabled", 0)
	gradeRatio := srv.tunableF("grade_ratio_enabled", 0)
	t.Logf("参数快照：bite_delay_fixed=%.2f（固定延迟，0=不固定） bite_delay_scale=%.2f（延迟倍率） "+
		"bite_delay_max=%.1fs（普通竿上限） bite_delay_lure_max=%.1fs（路亚上限） "+
		"bite_pace_enabled=%.0f（饵效控速开关） grade_ratio_enabled=%.0f（档位按比例）",
		fixed, scale, maxDelay, lureMax, pace, gradeRatio)

	plans := []struct {
		Name    string
		Rod     string
		RigType int64
		Loc     string
		Bait    string
		Seq     []string
		N       int
	}{
		{
			Name: "浮钓·红虫", Rod: "tele_10012_tl16", RigType: 1, Loc: "level_001_lake",
			Bait: "bloodworm",
			// 全新玩家只有裸竿（starter 里没有钩/线/漂），必须自己装配。
			// 不装就等于在一根空竿上测节奏：没有 cat=12 鱼钩 → 写不了
			// bait_hook: → 钓获吃光饵后也写不了 rebait: → 补饵链永远断。
			Seq: []string{"mono_15065", "bob_25165", "msinkset_30015", "orig_20008_14", "bloodworm"},
			N:   10,
		},
		{
			Name: "浮钓·面包", Rod: "tele_10012_tl16", RigType: 1, Loc: "level_001_lake",
			Bait: "bread",
			Seq:  []string{"mono_15065", "bob_25164", "msinkset_30015", "orig_20008_16", "bread"},
			N:    10,
		},
		{
			Name: "路亚·旋转亮片", Rod: "spin_10001_S60L", RigType: 5, Loc: "level_001_lake",
			Bait: "spinner_2713",
			Seq:  []string{"mono_15065", "spinner_2713"},
			N:    10,
		},
	}

	var rows []pacingRow
	for _, p := range plans {
		rod := playerItem(t, db, playUID, p.Rod)
		if rod == nil {
			t.Logf("%s：找不到竿实例，跳过", p.Name)
			continue
		}
		// 真装配：等价客户端 17/2。每套方案都从零开始装，避免上一组残留组件串味。
		topUpBait(t, db, p.Bait, 200)
		clearRigForTest(t, srv, db, playUID, rod.GUID)
		logs, fail := assemble(t, srv, db, playUID, rod.GUID, p.Seq)
		if fail != "" {
			t.Logf("%s：装配失败 %s", p.Name, fail)
			for _, l := range logs {
				t.Logf("      %-22s ok=%-5v %s", l.Item, l.OK, l.Note)
			}
			continue
		}
		comps, _ := db.ListRigComponents(context.Background(), rod.GUID)
		baits := srv.currentBaits(context.Background(), rod.GUID)
		t.Logf("%s：装配完成 组件=%d 钩上饵=%v", p.Name, len(comps), baits)
		if len(baits) == 0 {
			t.Logf("%s：装配后钩上无饵，跳过", p.Name)
			continue
		}
		for i := 0; i < p.N; i++ {
			r := runNewbieRealDelay(t, srv, db, sess, p.Name, rod.GUID, p.RigType, p.Loc, p.Bait)
			rows = append(rows, r)
			if r.TimedOut {
				t.Logf("[%s #%d] 超时无鱼 等%.1fs", p.Name, i+1, r.WaitS)
			} else {
				t.Logf("[%s #%d] %s 档%d %dg 等%.1fs xp=%d 银=%d 入护=%v",
					p.Name, i+1, r.Species, r.Grade, r.WeightG, r.WaitS, r.XP, r.Silver, r.Kept)
			}
		}
	}

	playWrite(t, "../../data/playtest", "newplayer_pacing.json", rows)
	reportPacing(t, rows)
}

// topUpBait 保证某 item_id 的背包库存足够多。
//
// 真实玩家会去买饵；测试不模拟经济链，只保证「测节奏时不会因为饵用完
// 而把结果污染成空竿」。不改变任何判鱼/延迟参数。
func topUpBait(t *testing.T, db *store.DB, itemID string, want int64) {
	t.Helper()
	var have int64
	_ = db.SQL.QueryRow(`SELECT COALESCE(SUM(qty),0) FROM item WHERE uid=? AND item_id=?`,
		playUID, itemID).Scan(&have)
	if have >= want {
		return
	}
	// 先尝试把现有堆补足
	var guid, class, fields string
	if err := db.SQL.QueryRow(`SELECT guid, wire_class, COALESCE(fields_json,'') FROM item
		WHERE uid=? AND item_id=? ORDER BY qty DESC LIMIT 1`, playUID, itemID).
		Scan(&guid, &class, &fields); err != nil {
		return
	}
	fs, err := FieldsFromJSON(fields)
	if err != nil {
		return
	}
	if setStackCountFields(class, fs, want) {
		if out, err := FieldsToJSON(fs); err == nil {
			_, _ = db.SQL.Exec(`UPDATE item SET qty=?, fields_json=? WHERE guid=?`, want, out, guid)
			return
		}
	}
	_, _ = db.SQL.Exec(`UPDATE item SET qty=? WHERE guid=?`, want, guid)
}

// mountBaitForPacing 把指定饵真正挂到钓组上（先拆掉已有的所有饵/拟饵）。
//
// 走生产函数 rigSetComponent（客户端 17/6/13/1 的服务端实现），
// 所以「新玩家能不能换上这个饵」本身也在被测。
func mountBaitForPacing(t *testing.T, srv *Server, db *store.DB, rodGUID, baitItem string) bool {
	t.Helper()
	ctx := context.Background()
	// 先拆掉已有的饵（cat 17 拟饵 / 27 天然饵），否则新饵挂不上去
	rows, err := db.ListRigComponents(ctx, rodGUID)
	if err == nil {
		for _, row := range rows {
			it, _ := db.GetItemByGUID(ctx, row.GUID)
			if it == nil {
				continue
			}
			if it.ItemID == baitItem {
				continue // 已经挂着同款，且还有份数就能继续用
			}
			cat, _ := srv.itemCategoryOf(ctx, it)
			if cat == 17 || cat == 27 {
				_ = srv.rigRemoveComponent(ctx, playUID, rodGUID, row.Slot)
			}
		}
	}
	// 检查是否已挂着目标饵且有份数
	if baits := srv.currentBaits(ctx, rodGUID); len(baits) > 0 && baits[0] == baitItem {
		return true
	}
	// 从背包取一份同款挂上
	var guid string
	if err := db.SQL.QueryRow(`SELECT i.guid FROM item i JOIN item_def d ON d.item_id=i.item_id
		WHERE i.uid=? AND i.item_id=? AND d.category IN (17,27)
		  AND NOT EXISTS (SELECT 1 FROM rig_component rc WHERE rc.guid=i.guid)
		ORDER BY i.slot LIMIT 1`, playUID, baitItem).Scan(&guid); err != nil {
		return false
	}
	res := srv.rigSetComponent(ctx, playUID, rodGUID, 0, guid, 0,
		rigSetOpts{ParentType: familyRod, EnforceCategory: true, SplitStack: true})
	if res == nil || res.Rejected || res.MountedGUID == "" {
		return false
	}
	// 饵不够让下一竿补装时，从背包补满
	srv.autoReloadBaitSlots(rootPlaySess(srv, playUID), rodGUID, "pacing_prepare")
	return len(srv.currentBaits(ctx, rodGUID)) > 0
}

// rootPlaySess 取回已注册的会话（补饵需要推送通道）。
func rootPlaySess(srv *Server, uid int64) *Session {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, s := range srv.sessions {
		if s.Userid == uid && !s.Closed() {
			return s
		}
	}
	return nil
}

// runNewbieRealDelay 一竿完整闭环，不改任何延迟参数。
func runNewbieRealDelay(t *testing.T, srv *Server, db *store.DB, sess *Session,
	plan, rodGUID string, rigType int64, loc, bait string) pacingRow {
	t.Helper()
	r := pacingRow{Plan: plan, Loc: loc, Bait: bait, RigType: rigType}

	st := srv.stateFor(playUID)
	st.mu.Lock()
	st.cancelBiteTimerLocked()
	st.cancelPendingTimerLocked()
	st.GearGuid, st.RigType, st.Loc = rodGUID, rigType, loc
	st.DepthCM, st.Spot = 200, 0
	st.CastPosX, st.CastPosZ, st.HasCastPos = 120, 260, true
	st.PosX, st.PosZ, st.HasPos = 120, 260, true
	st.CastActive, st.Resolved, st.InWater = false, false, false
	st.Pending, st.Armed, st.Hooked, st.PushSent, st.pushing = nil, nil, false, false, nil
	st.TickSeen, st.RigSettled, st.FallSeen, st.NeedBite = false, false, false, false
	st.CastTs, st.FallTs, st.BiteAt = time.Time{}, time.Time{}, time.Time{}
	st.castGeneration++
	st.mu.Unlock()

	keepBefore, _ := db.KeepnetList(context.Background(), playUID)

	_, _ = hCastPrepare(miscCtx(sess, 14, 1, playArgs(rodGUID)))
	_, _ = hCast(miscCtx(sess, 14, 2, liveCastBody(rodGUID, loc, 20, 1)))

	// 持续上报 14/7：浮钓要 RigSettled 才过 rig-settled 门。
	anim := uint32(0x800)
	if rigTypeLureFamily[rigType] {
		anim = 0x80000
	}
	stop := make(chan struct{})
	body := liveWaterBody(anim, loc)
	go func() {
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				hFishingStep(miscCtx(sess, 14, 7, body))
			}
		}
	}()

	// 观察窗按「真实可达的最长等待」定，而不是拍一个 40s：
	//   - 非 pace：浮/底上限 bite_delay_max 20s，路亚上限 bite_delay_lure_max 6s；
	//   - pace 开启（当前线上 bite_pace_enabled=1）：
	//       浮/底按 6~60s clamp，最坏 60s；
	//       路亚好档 1.5~6s，中档 10~60s，差档 20~60s 且**不封顶**，
	//       最坏 60s，且差档开口率仅 pace_lure_prob_bad（默认 0.12），
	//       一竿可能要空掷多次才排上，实际等待可远超 60s。
	// 用 40s 会把 pace 下的路亚差档系统性地误判成空竿。
	limit := 40 * time.Second
	if srv.paceEnabled() {
		limit = 150 * time.Second
	}
	start := time.Now()
	var fish *RolledFish
	for time.Since(start) < limit {
		if f := takePendingFishForTest(srv, playUID); f != nil {
			fish = f
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	r.WaitS = round2(time.Since(start).Seconds())
	if fish == nil {
		r.TimedOut = true
		r.Note = fmt.Sprintf("%.0fs 内无 14/14", limit.Seconds())
		return r
	}

	r.Species, r.Grade, r.WeightG = fish.Species, fish.Grade, fish.WeightG

	hFishingHooked(miscCtx(sess, 14, 11, playArgs(rodGUID, fish.FishGUID)))
	_, _ = hFishingEnd(miscCtx(sess, 14, 4, playArgs(rodGUID)))
	// ⚠️ XP/银币是 14/4 结算那一步才回填到 RolledFish 上的
	//（progress_ops.go:106 `f.XP, f.Silver = r.totalXP, r.silver`）。
	// 在起竿之前就读，拿到的永远是 0。
	r.XP, r.Silver = fish.XP, fish.Silver
	_, _ = hFishingKeep(miscCtx(sess, 14, 5, playArgs(rodGUID, fish.FishGUID)))
	keepAfter, _ := db.KeepnetList(context.Background(), playUID)
	r.Kept = len(keepAfter) > len(keepBefore)
	return r
}

// reportPacing 汇总节奏与收益。
func reportPacing(t *testing.T, rows []pacingRow) {
	t.Helper()
	byPlan := map[string][]pacingRow{}
	for _, r := range rows {
		byPlan[r.Plan] = append(byPlan[r.Plan], r)
	}
	names := make([]string, 0, len(byPlan))
	for k := range byPlan {
		names = append(names, k)
	}
	sort.Strings(names)

	t.Logf("=== 真实参数下的作钓节奏 ===")
	for _, name := range names {
		rs := byPlan[name]
		var waits []float64
		grades := map[int64]int{}
		species := map[string]int{}
		var xpSum, silverSum int64
		ok, timeout := 0, 0
		var wSum int64
		for _, r := range rs {
			if r.TimedOut {
				timeout++
				continue
			}
			ok++
			waits = append(waits, r.WaitS)
			grades[r.Grade]++
			species[r.Species]++
			xpSum += r.XP
			silverSum += r.Silver
			wSum += r.WeightG
		}
		if len(waits) == 0 {
			t.Logf("%-16s 全部超时（%d 竿）", name, timeout)
			continue
		}
		sort.Float64s(waits)
		t.Logf("%-16s 成功%d/%d  等待 均值%.1fs 中位%.1fs 最短%.1fs 最长%.1fs",
			name, ok, len(rs), meanF(waits), quantileF(waits, 0.5), waits[0], waits[len(waits)-1])
		t.Logf("%-16s 档位%v 鱼种%v 均重%.0fg 经验/竿%.0f 银币/竿%.1f",
			"", grades, species, float64(wSum)/float64(ok),
			float64(xpSum)/float64(ok), float64(silverSum)/float64(ok))
	}
}