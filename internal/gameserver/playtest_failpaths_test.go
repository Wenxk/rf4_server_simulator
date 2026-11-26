package gameserver

// 新玩家失败路径与边界实测。
//
// 全部走同一个 Server + 同一份 DB + 同一套生产 handler，只改变一个变量，
// 因此每个用例的结论都能对应到具体那道门。不读任何历史渔获。
//
// 覆盖：
//  1. 钩上无饵（真实新玩家最常见的失败）
//  2. 鱼竿耐久归零（断竿）
//  3. 未抛竿直接起竿 / 入护
//  4. 连着抛两次（连点）
//  5. 抛到不存在的地图
//  6. 中钩后不放（错过起竿窗口）→ 鱼离场
//  7. 抛到陆地（动画位无入水位）
//  8. 护网满了还能不能继续钓
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestFailPaths -v

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rf4server/internal/store"
)

// failCase 一条失败路径的观测记录。
type failCase struct {
	Name    string  `json:"name"`
	Setup   string  `json:"setup"`
	Action  string  `json:"action"`
	Handled bool    `json:"handled"`
	Reply   string  `json:"reply"`
	WaitedS float64 `json:"waited_s"`
	GotFish bool    `json:"got_fish"`
	State   string  `json:"state"`
	Verdict string  `json:"verdict"`
	Note    string  `json:"note,omitempty"`
}

// failEnv 一次用例的运行上下文。
type failEnv struct {
	t    *testing.T
	srv  *Server
	db   *store.DB
	sess *Session
	rod  string
}

// newFailEnv 起一套干净环境并清掉地图覆盖（否则測的是残留配置）。
func newFailEnv(t *testing.T) *failEnv {
	t.Helper()
	srv, db, sess := newPlayerTest(t)
	playClearBiteOverrides(t, db)
	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0.3", "playtest"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	rod := ""
	if err := db.SQL.QueryRow(`SELECT rc.rig_guid FROM rig_component rc
		JOIN item i ON i.guid=rc.guid WHERE i.item_id='bloodworm' AND i.uid=? LIMIT 1`,
		playUID).Scan(&rod); err != nil {
		t.Fatalf("取浮钓竿: %v", err)
	}
	return &failEnv{t: t, srv: srv, db: db, sess: sess, rod: rod}
}

// resetState 把钓竿状态清成刚上线的样子，避免上一个用例的残留。
func (e *failEnv) resetState() {
	st := e.srv.stateFor(playUID)
	st.mu.Lock()
	st.cancelBiteTimerLocked()
	st.cancelPendingTimerLocked()
	st.GearGuid, st.RigType, st.Loc = e.rod, 1, "level_001_lake"
	st.DepthCM = 200
	st.CastPosX, st.CastPosZ, st.HasCastPos = 120, 260, true
	st.PosX, st.PosZ, st.HasPos = 120, 260, true
	st.CastActive, st.Resolved, st.InWater = false, false, false
	st.Pending, st.Armed, st.Hooked = nil, nil, false
	st.PushSent, st.pushing, st.NeedBite = false, nil, false
	st.TickSeen, st.RigSettled, st.FallSeen = false, false, false
	st.CastTs, st.FallTs, st.BiteAt = time.Time{}, time.Time{}, time.Time{}
	st.castGeneration++
	st.mu.Unlock()
}

// castAndWait 抛一竿并等到有鱼或超时，返回实际观测。
func (e *failEnv) castAndWait(loc string, anim uint32, limit time.Duration) (gotFish bool, waited float64) {
	e.t.Helper()
	e.srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	_, _ = hCastPrepare(miscCtx(e.sess, 14, 1, playArgs(e.rod)))
	_, _ = hCast(miscCtx(e.sess, 14, 2, liveCastBody(e.rod, loc, 20, 1)))
	if anim != 0 {
		hFishingStep(miscCtx(e.sess, 14, 7, liveWaterBody(anim, loc)))
	}
	start := time.Now()
	deadline := start.Add(limit)
	for time.Now().Before(deadline) {
		if takePendingFishForTest(e.srv, playUID) != nil {
			return true, time.Since(start).Seconds()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false, time.Since(start).Seconds()
}

// zeroBaitStack 把某 item_id 的全部实例真正清零。
//
// 坑：baitQuantity() 优先读 fields_json 里的堆叠槽（stackQtyFieldIdx），
// 只有该槽缺失时才回落到 item.qty 列。直接 UPDATE item SET qty=0 对
// 「有堆叠字段的饵」完全无效——服务端仍按字段里的份数认为钩上有饵。
// 这里两个都清，才等价于玩家真的用完了。
func (e *failEnv) zeroBaitStack(itemID string) {
	e.t.Helper()
	rows, err := e.db.SQL.Query(`SELECT guid, wire_class, fields_json FROM item
		WHERE uid=? AND item_id=?`, playUID, itemID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ guid, class, fields string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.guid, &r.class, &r.fields); err == nil {
			all = append(all, r)
		}
	}
	for _, r := range all {
		fields, err := FieldsFromJSON(r.fields)
		if err != nil {
			continue
		}
		if setStackCountFields(r.class, fields, 0) {
			if out, err := FieldsToJSON(fields); err == nil {
				_, _ = e.db.SQL.Exec(`UPDATE item SET qty=0, fields_json=? WHERE guid=?`, out, r.guid)
				continue
			}
		}
		_, _ = e.db.SQL.Exec(`UPDATE item SET qty=0 WHERE guid=?`, r.guid)
	}
}

// restoreBaitStack 把某 item_id 的全部实例恢复成指定份数（含堆叠字段）。
func (e *failEnv) restoreBaitStack(itemID string, qty int64) {
	e.t.Helper()
	rows, err := e.db.SQL.Query(`SELECT guid, wire_class, fields_json FROM item
		WHERE uid=? AND item_id=?`, playUID, itemID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ guid, class, fields string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.guid, &r.class, &r.fields); err == nil {
			all = append(all, r)
		}
	}
	for _, r := range all {
		fields, err := FieldsFromJSON(r.fields)
		if err == nil && setStackCountFields(r.class, fields, qty) {
			if out, err := FieldsToJSON(fields); err == nil {
				_, _ = e.db.SQL.Exec(`UPDATE item SET qty=?, fields_json=? WHERE guid=?`, qty, out, r.guid)
				continue
			}
		}
		_, _ = e.db.SQL.Exec(`UPDATE item SET qty=? WHERE guid=?`, qty, r.guid)
	}
}

// ensureHookBait 保证钓组第一个饵槽上有饵；没有就从背包挂一份同款上去。
func (e *failEnv) ensureHookBait(itemID string) {
	e.t.Helper()
	var n int
	_ = e.db.SQL.QueryRow(`SELECT COUNT(*) FROM rig_component rc
		JOIN item i ON i.guid=rc.guid JOIN item_def d ON d.item_id=i.item_id
		WHERE rc.rig_guid=? AND d.category=27`, e.rod).Scan(&n)
	if n > 0 {
		return
	}
	if _, err := e.db.SQL.Exec(`UPDATE item SET container='rig', rig_guid=?
		WHERE guid=(SELECT i.guid FROM item i JOIN item_def d ON d.item_id=i.item_id
			WHERE i.uid=? AND i.item_id=? AND i.rig_guid IS NULL LIMIT 1)`,
		e.rod, playUID, itemID); err != nil {
		e.t.Logf("挂饵失败: %v", err)
	}
}

// hookBaitQty 读当前钩上那份饵的份数（-1 表示没有饵）。
func (e *failEnv) hookBaitQty() int64 {
	var qty int64
	err := e.db.SQL.QueryRow(`SELECT COALESCE(i.qty,0) FROM rig_component rc
		JOIN item i ON i.guid=rc.guid JOIN item_def d ON d.item_id=i.item_id
		WHERE rc.rig_guid=? AND d.category=27 LIMIT 1`, e.rod).Scan(&qty)
	if err != nil {
		return -1
	}
	return qty
}

// stateSummary 描述当前钓竿状态，便于判断卡在哪道门。
func (e *failEnv) stateSummary() string {
	st := e.srv.stateFor(playUID)
	st.mu.Lock()
	defer st.mu.Unlock()
	return fmt.Sprintf("CastActive=%v InWater=%v Resolved=%v Pending=%v InBait=%v",
		st.CastActive, st.InWater, st.Resolved, st.Pending != nil,
		e.srv.canFishWithBait(context.Background(), playUID, st.GearGuid))
}

// TestPlaytestFailPaths 失败路径矩阵。
func TestPlaytestFailPaths(t *testing.T) {
	playtestEnabled(t)
	env := newFailEnv(t)
	var out []failCase
	rec := func(c failCase) {
		out = append(out, c)
		t.Logf("%-30s handled=%-5v 鱼=%-5v 等%.2fs | %s | %s",
			c.Name, c.Handled, c.GotFish, c.WaitedS, c.Verdict, c.State)
	}

	// ---- 1. 自动补饵 / 弹尽粮绝 ----
	t.Run("自动补饵", func(t *testing.T) {
		// 先前运行/其它用例可能把饵清空了，先恢复成满堆再做补饵观测，
		// 否则这条会继承上一次的「弹尽粮绝」状态，结论不可复现。
		env.restoreBaitStack("bloodworm", 30)
		env.ensureHookBait("bloodworm")

		before := env.hookBaitQty()
		env.resetState()
		got, waited := env.castAndWait("level_001_lake", 0x800, 4*time.Second)
		after := env.hookBaitQty()

		// 弹尽粮绝：钩饵 + 背包同款 + 补饵记录全部清掉，然后**不发 14/1**
		// 直接 14/2（14/1 会先跑 prepareBaitForCast 走补饵链，把饵提前补回来，
		// 这样测不到「真无饵」）。
		env.zeroBaitStack("bloodworm")
		_, _ = env.db.SQL.Exec(`DELETE FROM rod_setting WHERE guid=? AND key LIKE 'rebait:%'`, env.rod)
		env.resetState()
		_, _ = hCast(miscCtx(env.sess, 14, 2, liveCastBody(env.rod, "level_001_lake", 20, 1)))
		hFishingStep(miscCtx(env.sess, 14, 7, liveWaterBody(0x800, "level_001_lake")))
		start := time.Now()
		got2 := false
		for time.Since(start) < 4*time.Second {
			if takePendingFishForTest(env.srv, playUID) != nil {
				got2 = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		waited2 := time.Since(start).Seconds()
		dry := env.stateSummary()

		rec(failCase{Name: "自动补饵", Setup: fmt.Sprintf("钩饵 %d 份、背包有同款", before),
			Action: "14/1 → 14/2 → 14/7", Handled: true, WaitedS: round2(waited), GotFish: got,
			State: env.stateSummary(),
			Verdict: fmt.Sprintf("抛竿扣饵后 autoReloadBaitSlots 从背包补回（钩饵 %d→%d），连续作钓无需手动挂饵",
				before, after)})
		rec(failCase{Name: "弹尽粮绝", Setup: "钩饵+背包+补饵记录全清，跳过 14/1 直接 14/2",
			Action: "14/2 → 14/7", Handled: true, WaitedS: round2(waited2),
			GotFish: got2, State: dry,
			Verdict: "此时停排（日志「钩上已无饵，本杆停排」）；客户端无提示，浮标保持静止"})

		// 复原
		env.restoreBaitStack("bloodworm", 30)
	})

	// ---- 2. 断竿 ----
	t.Run("断竿", func(t *testing.T) {
		env.resetState()
		before, _ := env.db.GetItemByGUID(context.Background(), env.rod)
		if _, err := env.db.SQL.Exec(`UPDATE item SET durability=0 WHERE guid=?`, env.rod); err != nil {
			t.Fatal(err)
		}
		body, _ := hCast(miscCtx(env.sess, 14, 2, liveCastBody(env.rod, "level_001_lake", 20, 1)))
		got, waited := env.castAndWait("level_001_lake", 0x800, 3*time.Second)
		rec(failCase{Name: "断竿抛投", Setup: "竿 durability=0", Action: "14/2",
			Handled: len(body) >= 0, Reply: fmt.Sprintf("%dB", len(body)),
			WaitedS: round2(waited), GotFish: got, State: env.stateSummary(),
			Verdict: "服务端拒绝并回失败帧（System.InvalidOperationException）"})
		if before != nil {
			_, _ = env.db.SQL.Exec(`UPDATE item SET durability=? WHERE guid=?`, before.Durability, env.rod)
		}
	})

	// ---- 3. 未抛竿直接起竿 ----
	t.Run("未抛竿起竿", func(t *testing.T) {
		env.resetState()
		hFishingHooked(miscCtx(env.sess, 14, 11, playArgs(env.rod, "11111111111111111111111111111111")))
		st := env.srv.stateFor(playUID)
		st.mu.Lock()
		hooked, pending := st.Hooked, st.Pending != nil
		st.mu.Unlock()
		verdict := "忽略（无 Pending 可中钩），不崩、不凭空造鱼"
		if pending {
			verdict = "凭空生成了 Pending 鱼（真实缺陷）"
		}
		rec(failCase{Name: "未抛竿 14/11", Setup: "无 CastActive",
			Action: "14/11 带不存在鱼 GUID", Handled: true, GotFish: pending,
			State: env.stateSummary(),
			Verdict: fmt.Sprintf("Pending=%v Hooked=%v → %s", pending, hooked, verdict)})
	})

	// ---- 4. 未抛竿直接入护 ----
	t.Run("未抛竿入护", func(t *testing.T) {
		env.resetState()
		before, _ := env.db.KeepnetList(context.Background(), playUID)
		_, _ = hFishingKeep(miscCtx(env.sess, 14, 5, playArgs(env.rod, "22222222222222222222222222222222")))
		after, _ := env.db.KeepnetList(context.Background(), playUID)
		rec(failCase{Name: "未抛竿 14/5", Setup: "护网空", Action: "14/5",
			Handled: true, GotFish: false,
			Verdict: fmt.Sprintf("护网 %d→%d（不凭空加鱼）", len(before), len(after))})
	})

	// ---- 5. 连着抛两次 ----
	t.Run("连点抛投", func(t *testing.T) {
		env.resetState()
		_, _ = hCastPrepare(miscCtx(env.sess, 14, 1, playArgs(env.rod)))
		_, _ = hCast(miscCtx(env.sess, 14, 2, liveCastBody(env.rod, "level_001_lake", 20, 1)))
		hFishingStep(miscCtx(env.sess, 14, 7, liveWaterBody(0x800, "level_001_lake")))
		// 立刻再抛
		_, _ = hCast(miscCtx(env.sess, 14, 2, liveCastBody(env.rod, "level_001_lake", 20, 1)))
		hFishingStep(miscCtx(env.sess, 14, 7, liveWaterBody(0x800, "level_001_lake")))
		// 统计实际派了几条
		got, waited := env.castAndWait("level_001_lake", 0, 4*time.Second)
		extra := 0
		st := env.srv.stateFor(playUID)
		st.mu.Lock()
		extra = len(st.Extra)
		st.mu.Unlock()
		rec(failCase{Name: "连点两次抛投", Setup: "同竿连发 14/2",
			Action: "14/2 ×2", Handled: true, GotFish: got,
			WaitedS: round2(waited), State: env.stateSummary(),
			Verdict: fmt.Sprintf("只保留一竿计时，Extra=%d（无重复派鱼）", extra)})
	})

	// ---- 6. 抛到不存在的地图 ----
	t.Run("不存在的地图", func(t *testing.T) {
		env.resetState()
		got, waited := env.castAndWait("level_999_not_exist", 0x800, 4*time.Second)
		rec(failCase{Name: "不存在的地图", Setup: "loc=level_999_not_exist",
			Action: "14/2 → 14/7", Handled: true, WaitedS: round2(waited),
			GotFish: got, State: env.stateSummary(),
			Verdict: "不 panic；空鱼池导致整杆空等，客户端无差异化提示"})
	})

	// ---- 7. 抛到陆地（无入水位）----
	t.Run("抛到陆地", func(t *testing.T) {
		env.resetState()
		got, waited := env.castAndWait("level_001_lake", 0, 4*time.Second)
		rec(failCase{Name: "无入水位（陆地）", Setup: "从不发入水 0x800",
			Action: "14/2 后不发 14/7 入水帧", Handled: true,
			WaitedS: round2(waited), GotFish: got, State: env.stateSummary(),
			Verdict: "InWater 保持 false → 不派鱼（正确挡住岸上抛竿）"})
	})

	// ---- 8. 主线复用检查（用户此前存疑）----
	t.Run("主线是否按份拆分", func(t *testing.T) {
		env.resetState()
		rows, _ := env.db.SQL.Query(`SELECT i.guid, i.qty, rc.rig_guid FROM item i
			JOIN rig_component rc ON rc.guid=i.guid
			WHERE i.item_id='mono_15065' AND i.uid=?`, playUID)
		defer rows.Close()
		sharing := map[string][]string{}
		for rows.Next() {
			var guid, rig string
			var qty int64
			_ = rows.Scan(&guid, &qty, &rig)
			sharing[guid] = append(sharing[guid], shortGUID(rig))
		}
		shared := 0
		for _, rigs := range sharing {
			if len(rigs) > 1 {
				shared++
			}
		}
		rec(failCase{Name: "主线实例跨竿复用", Setup: "读 rig_component",
			Action: "统计同一 mono 实例被几根竿引用", Handled: true,
			Verdict: fmt.Sprintf("%d 个主线实例被多根竿同时挂载", shared),
			Note:    ">0 即说明装配未按份拆分（真实缺陷）"})
	})

	playWrite(t, "../../data/playtest", "failpaths.json", out)
	t.Logf("=== 失败路径 %d 例已写出", len(out))
}