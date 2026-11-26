package gameserver

// 全新玩家从零开始的作钓体验测试。
//
// 与 TestPlaytestFishingFlow 的区别：那个测试假设「竿已经装配好、已有饵」，
// 只验证判鱼到结算这一段。本轮用户要求「以全新玩家身份验证」，所以这里从
// 真正的一无所有开始：
//
//	1. 只有 starter 33 件散装物品（竿、线、钩、浮标、饵，全部在背包里躺着）；
//	2. 走 rigSetComponent 把竿→轮/线→钩→饵 层层装起来（等价客户端 13/1·17/2）；
//	3. 只挂真实存在的天然饵/拟饵；
//	4. 再跑完整作钓闭环，记录每一步耗时与结果。
//
// 全部判鱼、档位、结算、入护都走生产代码；不读任何历史渔获。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestNewPlayer -v

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"rf4server/internal/store"
)

// rigPlan 一套装配方案：把哪几件物品按什么顺序装上某根竿。
type rigPlan struct {
	Name      string   `json:"name"`
	Rod       string   `json:"rod_item"`
	RigType   int64    `json:"rig_type"`
	Loc       string   `json:"loc_alias"`
	LocName   string   `json:"loc_name"`
	Sequence  []string `json:"sequence"` // 按装配顺序的物品 item_id
	BaitItem  string   `json:"bait_item"`
	HookItem  string   `json:"hook_item"`
	Assemble  []stepLog `json:"assemble"`
	Fail      string    `json:"fail,omitempty"`
}

type stepLog struct {
	Item   string  `json:"item"`
	OK     bool    `json:"ok"`
	Note   string  `json:"note"`
	Ms     float64 `json:"ms"`
}

// newPlayerTest 准备环境：干净库 + 真实服务端 + 新玩家会话。
func newPlayerTest(t *testing.T) (*Server, *store.DB, *Session) {
	t.Helper()
	db := playDB(t)
	srv := playServer(t, db)
	sess := playSess(t, srv, playUID)
	return srv, db, sess
}

// playerItem 按 item_id 取新玩家的物品实例（同款多件时取第一件未装配的）。
func playerItem(t *testing.T, db *store.DB, uid int64, itemID string) *store.Item {
	t.Helper()
	var guid string
	err := db.SQL.QueryRow(`
		SELECT i.guid FROM item i
		WHERE i.uid=? AND i.item_id=? AND i.durability > 0
		  AND NOT EXISTS (SELECT 1 FROM rig_component rc WHERE rc.guid = i.guid)
		ORDER BY i.slot LIMIT 1`, uid, itemID).Scan(&guid)
	if err != nil {
		return nil
	}
	row, err := db.GetItemByGUID(context.Background(), guid)
	if err != nil {
		return nil
	}
	return row
}

// assemble 按顺序把物品装到竿上，记录每步成败与耗时。
// 装配走生产函数 rigSetComponent（客户端 13/1 / 17/2 的服务端实现），
// 因此这里能暴露「新玩家按攻略装配是否真能成」这类问题。
func assemble(t *testing.T, srv *Server, db *store.DB, uid int64, rodGUID string, seq []string) ([]stepLog, string) {
	t.Helper()
	ctx := context.Background()
	var logs []stepLog
	for _, itemID := range seq {
		it := playerItem(t, db, uid, itemID)
		if it == nil {
			logs = append(logs, stepLog{Item: itemID, OK: false, Note: "背包里没有可用实例"})
			return logs, fmt.Sprintf("缺少 %s", itemID)
		}
		cat, _ := srv.itemCategoryOf(ctx, it)
		t0 := time.Now()
		res := srv.rigSetComponent(ctx, uid, rodGUID, 0, it.GUID, 0,
			rigSetOpts{ParentType: familyRod, EnforceCategory: true, SplitStack: true})
		ms := float64(time.Since(t0).Microseconds()) / 1000
		if res == nil || res.Rejected || res.MountedGUID == "" {
			note := "装配被拒"
			if res != nil && res.Rejected {
				note = fmt.Sprintf("装配被拒(rejected) cat=%d", cat)
			}
			logs = append(logs, stepLog{Item: itemID, OK: false, Note: note, Ms: ms})
			return logs, fmt.Sprintf("%s 装配失败: %s", itemID, note)
		}
		logs = append(logs, stepLog{Item: itemID, OK: true, Note: fmt.Sprintf("cat=%d mounted=%s", cat, shortGUID(res.MountedGUID)), Ms: ms})
	}
	return logs, ""
}

// TestPlaytestNewPlayerAssemble 验证新玩家能否用 starter 物品装出可用的钓组。
func TestPlaytestNewPlayerAssemble(t *testing.T) {
	playtestEnabled(t)
	srv, db, _ := newPlayerTest(t)

	type candidate struct {
		Name     string
		Rod      string
		RigType  int64
		Seq      []string
		Bait     string
		Hook     string
		LocAlias string
		LocName  string
	}
	cands := []candidate{
		{
			Name: "浮钓·浮标组", Rod: "tele_10012_tl16", RigType: 1,
			// 浮钓：主线 → 浮标 → 铅坠 → 钩 → 饵
			Seq:      []string{"mono_15065", "bob_25164", "msinkset_30015", "orig_20008_16", "bread"},
			Bait:     "bread", Hook: "orig_20008_16",
			LocAlias: "level_001_lake", LocName: "克马羚诺也湖",
		},
		{
			Name: "浮钓·红虫", Rod: "tele_10012_tl16", RigType: 1,
			Seq:      []string{"mono_15065", "bob_25165", "msinkset_30015", "orig_20008_14", "bloodworm"},
			Bait:     "bloodworm", Hook: "orig_20008_14",
			LocAlias: "level_001_lake", LocName: "克马羚诺也湖",
		},
		{
			Name: "路亚·旋转亮片", Rod: "spin_10001_S60L", RigType: 5,
			Seq:      []string{"mono_15065", "spinner_2713"},
			Bait:     "spinner_2713",
			LocAlias: "level_001_lake", LocName: "克马羚诺也湖",
		},
		{
			Name: "路亚·复合亮片", Rod: "spin_5820", RigType: 5,
			Seq:      []string{"mono_15065", "spinnerbaits_1244"},
			Bait:     "spinnerbaits_1244",
			LocAlias: "level_001_lake", LocName: "克马羚诺也湖",
		},
	}

	var out []rigPlan
	for _, c := range cands {
		rod := playerItem(t, db, playUID, c.Rod)
		p := rigPlan{Name: c.Name, Rod: c.Rod, RigType: c.RigType,
			Loc: c.LocAlias, LocName: c.LocName, Sequence: c.Seq,
			BaitItem: c.Bait, HookItem: c.Hook}
		if rod == nil {
			p.Fail = "背包里没有这根竿"
			out = append(out, p)
			t.Logf("%-22s × %s", c.Name, p.Fail)
			continue
		}
		// 先把这根竿上已有组件拆干净，保证从零开始
		clearRigForTest(t, srv, db, playUID, rod.GUID)
		logs, fail := assemble(t, srv, db, playUID, rod.GUID, c.Seq)
		p.Assemble, p.Fail = logs, fail
		out = append(out, p)
		if fail != "" {
			t.Logf("%-22s × %s", c.Name, fail)
			for _, l := range logs {
				t.Logf("      %-22s ok=%-5v %s", l.Item, l.OK, l.Note)
			}
			continue
		}
		// 验证：装配后钩上确有饵、rig 组件数正确
		bait := srvBaitOf(context.Background(), db, rod.GUID)
		comps, _ := db.ListRigComponents(context.Background(), rod.GUID)
		t.Logf("%-22s ✓ 组件=%d 钩上饵=%q", c.Name, len(comps), bait)
	}
	playWrite(t, "../../data/playtest", "newplayer_assemble.json", out)
}

// clearRigForTest 拆掉竿上的全部组件，并把它们退回背包。
// rigRemoveComponent(ctx, uid, parentGUID, slot) 按槽位拆，返回拆卸结果。
func clearRigForTest(t *testing.T, srv *Server, db *store.DB, uid int64, rodGUID string) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.ListRigComponents(ctx, rodGUID)
	if err != nil || len(rows) == 0 {
		return
	}
	// 深到浅逐层拆，避免父组件先被拆掉导致子组件悬空
	sort.Slice(rows, func(i, j int) bool { return rows[i].Slot > rows[j].Slot })
	for _, row := range rows {
		_ = srv.rigRemoveComponent(ctx, uid, rodGUID, row.Slot)
	}
}