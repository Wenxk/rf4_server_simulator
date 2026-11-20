package gameserver

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"rf4server/internal/store"
)

// seedAutoRotation 造一条 6 款饵的鱼，并打开它的自动轮换开关
// （fish_bait_auto_rotation，与 rotate_mode 无关）。
func seedAutoRotation(t *testing.T, rarity int) (*Server, *store.DB) {
	t.Helper()
	s, db := seedBaitRuleTest(t)
	ctx := context.Background()
	ids := []string{"auto_b1", "auto_b2", "auto_b3", "auto_b4", "auto_b5", "auto_b6"}
	names := []string{"测试饵一", "测试饵二", "测试饵三", "测试饵四", "测试饵五", "测试饵六"}
	for i, id := range ids {
		if _, err := db.SQL.Exec(`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(?,?,27,'{"fopbhlmkcpe":6}')`, 999910+i, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL.Exec(`INSERT INTO item_catalog(item_id,name_zh) VALUES(?,?)`, id, names[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL.Exec(`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('rules-test','auto_fish','自动鱼')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO fish_ecology(fish_key,rarity_tier) VALUES('auto_fish',?)`, rarity); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFishBaitOverride(ctx, store.FishBaitOverride{
		FishZh: "自动鱼", Allow: names, Enabled: true,
	}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFishBaitAuto(ctx, "auto_fish", true, "test"); err != nil {
		t.Fatal(err)
	}
	s.invalidateBaitOverrides()
	s.invalidateBaitAuto()
	// 生态档案是进程级缓存，种完数据必须作废，否则读到的还是旧快照（rarity=0）。
	s.invalidateFishEcologyCache()
	setTunable(t, s, "bait_rotation_auto_enabled", "1")
	return s, db
}

func setTunable(t *testing.T, s *Server, key, value string) {
	t.Helper()
	if err := s.DB.TunableSet(context.Background(), key, value, "test"); err != nil {
		t.Fatal(err)
	}
	s.tunMu.Lock()
	s.tunAt = time.Time{}
	s.tunMu.Unlock()
}

// baitScore 单钩单饵跑一遍判鱼，返回该鱼拿到的权重。
func baitScore(s *Server, baitID string, now time.Time) float64 {
	_, w := s.filterFishBaitSet("level_900_rules", []string{baitID}, []string{"auto_fish"},
		[]float64{1}, now, fishRollContext{cache: newFishingRollCache()})
	if len(w) == 0 {
		return 0
	}
	return w[0]
}

func countOpenBaits(s *Server, now time.Time) (open, closed int) {
	for i := 1; i <= 6; i++ {
		if baitScore(s, "auto_b"+strconv.Itoa(i), now) > 0.4 {
			open++
		} else {
			closed++
		}
	}
	return
}

func TestAutoRotationQuotaByRarity(t *testing.T) {
	cases := []struct{ rarity, want int }{{0, 3}, {1, 2}, {2, 1}}
	now := time.Unix(worldDayEpochUnix+1500, 0)
	for _, c := range cases {
		s, _ := seedAutoRotation(t, c.rarity)
		open, closed := countOpenBaits(s, now)
		if open != c.want || closed != 6-c.want {
			t.Fatalf("rarity=%d 期望 %d 款开口，实得 开口%d/未开%d", c.rarity, c.want, open, closed)
		}
	}
}

// 未抽中的饵必须落到「allow 之外」，不会保留满分档。
func TestAutoRotationUnpickedDropsOutOfAllow(t *testing.T) {
	s, _ := seedAutoRotation(t, 0)
	now := time.Unix(worldDayEpochUnix+1500, 0)
	var openScore, closedScore float64
	for i := 1; i <= 6; i++ {
		if v := baitScore(s, "auto_b"+strconv.Itoa(i), now); v > 0.4 {
			openScore = v
		} else if v > closedScore {
			closedScore = v
		}
	}
	if openScore == 0 {
		t.Fatal("没有开口饵，抽样未生效")
	}
	// 测试里没有 companion 证据，未抽中的饵会直接出鱼（0 分）。
	// 若以后给它们补了 tier1~2 证据，则落到 bait_evidence_low_scale 档（默认 0.10）。
	if closedScore >= openScore*0.5 {
		t.Fatalf("未抽中的饵没有被压下去: open=%f closed=%f", openScore, closedScore)
	}
}

func TestAutoRotationDeterministicWithinBucket(t *testing.T) {
	s, _ := seedAutoRotation(t, 0)
	a := time.Unix(worldDayEpochUnix+1500, 0)
	b := a.Add(30 * time.Second) // 同一时间桶（周期 2 小时）
	pick := func(now time.Time) []string {
		var out []string
		for i := 1; i <= 6; i++ {
			if baitScore(s, "auto_b"+strconv.Itoa(i), now) > 0.4 {
				out = append(out, "auto_b"+strconv.Itoa(i))
			}
		}
		return out
	}
	pa, pb := pick(a), pick(b)
	if len(pa) != len(pb) {
		t.Fatalf("同一时间桶内结果不稳定: %v vs %v", pa, pb)
	}
	for i := range pa {
		if pa[i] != pb[i] {
			t.Fatalf("同一时间桶内结果不稳定: %v vs %v", pa, pb)
		}
	}
	// 跨多个桶后至少有一次与当前不同（否则等于没轮换）。
	diff := false
	for k := int64(1); k <= 6 && !diff; k++ {
		pc := pick(a.Add(time.Duration(k) * 2 * time.Hour))
		if len(pc) != len(pa) {
			diff = true
			break
		}
		for i := range pa {
			if pa[i] != pc[i] {
				diff = true
				break
			}
		}
	}
	if !diff {
		t.Fatalf("连续 6 个周期抽到同一批饵，轮换没有生效: %v", pa)
	}
}

func TestAutoRotationDisabledFallsBack(t *testing.T) {
	s, _ := seedAutoRotation(t, 0)
	setTunable(t, s, "bait_rotation_auto_enabled", "0")
	open, _ := countOpenBaits(s, time.Unix(worldDayEpochUnix+1500, 0))
	if open != 6 {
		t.Fatalf("总开关关闭时应全部按满分档放行，实得开口 %d 款", open)
	}
}

// 逐鱼开关关掉、总开关仍开着时，该鱼回退成完整名单（不再抽签）。
func TestAutoRotationPerFishOffFallsBack(t *testing.T) {
	s, db := seedAutoRotation(t, 0)
	ctx := context.Background()
	if err := db.SetFishBaitAuto(ctx, "auto_fish", false, "test"); err != nil {
		t.Fatal(err)
	}
	s.invalidateBaitAuto()
	open, _ := countOpenBaits(s, time.Unix(worldDayEpochUnix+1500, 0))
	if open != 6 {
		t.Fatalf("逐鱼开关关闭后应全部按满分档放行，实得开口 %d 款", open)
	}
}

// 池子不足配额时不轮换：宁可全开，也不要把仅有几款饵也关掉。
func TestAutoRotationSmallPoolKeepsAll(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	ctx := context.Background()
	names := []string{"小池饵一", "小池饵二"}
	for i, id := range []string{"sp_b1", "sp_b2"} {
		if _, err := db.SQL.Exec(`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(?,?,27,'{"fopbhlmkcpe":6}')`, 999930+i, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL.Exec(`INSERT INTO item_catalog(item_id,name_zh) VALUES(?,?)`, id, names[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL.Exec(`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('rules-test','small_fish','小池鱼')`); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFishBaitOverride(ctx, store.FishBaitOverride{
		FishZh: "小池鱼", Allow: names, Enabled: true,
	}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFishBaitAuto(ctx, "small_fish", true, "test"); err != nil {
		t.Fatal(err)
	}
	s.invalidateBaitOverrides()
	s.invalidateBaitAuto()
	s.invalidateFishEcologyCache()
	setTunable(t, s, "bait_rotation_auto_enabled", "1")
	now := time.Unix(worldDayEpochUnix+1500, 0)
	for i, id := range []string{"sp_b1", "sp_b2"} {
		_, w := s.filterFishBaitSet("level_900_rules", []string{id}, []string{"small_fish"},
			[]float64{1}, now, fishRollContext{cache: newFishingRollCache()})
		if len(w) == 0 || w[0] < 0.4 {
			t.Fatalf("%s 应保持满分档（池子只有 2 款，不轮换），实得 %v", names[i], w)
		}
	}
}

func TestAutoPickGroupsStable(t *testing.T) {
	groups := make([]baitGroup, 0, 7)
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		groups = append(groups, baitGroup{base: id, baits: []ruleBait{{id: id, name: id}}})
	}
	p1 := autoPickGroups(groups, 3, "fish", 100)
	p2 := autoPickGroups(groups, 3, "fish", 100)
	if len(p1) != 3 || len(p2) != 3 {
		t.Fatalf("配额应为 3，实得 %d / %d", len(p1), len(p2))
	}
	for i := range p1 {
		if p1[i].base != p2[i].base {
			t.Fatalf("同种子结果不一致: %v vs %v", groupBases(p1), groupBases(p2))
		}
	}
	// 组不足配额时全开
	if got := autoPickGroups(groups[:2], 3, "fish", 100); len(got) != 2 {
		t.Fatalf("组不足应全开，实得 %v", groupBases(got))
	}
	// 不同桶应给出不同结果（至少有一次）
	same := 0
	for i := 1; i <= 8; i++ {
		p := autoPickGroups(groups, 3, "fish", int64(100+i))
		eq := len(p) == len(p1)
		if eq {
			for j := range p {
				if p[j].base != p1[j].base {
					eq = false
					break
				}
			}
		}
		if eq {
			same++
		}
	}
	if same == 8 {
		t.Fatal("8 个周期结果完全相同，抽样没有随机性")
	}
}

// 同一型号的不同规格必须同组：抽中一组要整组放行。
func TestBaitGroupMergesSpecs(t *testing.T) {
	baits := []ruleBait{
		{id: "boils_op_rdw10", name: "红蚯蚓 10", category: 27, baitType: 8},
		{id: "boils_op_rdw14", name: "红蚯蚓 14", category: 27, baitType: 8},
		{id: "boils_op_rdw18", name: "红蚯蚓 18", category: 27, baitType: 8},
		{id: "muckworm", name: "红蚯蚓", category: 27, baitType: 6},
		{id: "boils_op_blw14", name: "红虫14", category: 27, baitType: 8},
		{id: "bloodworm", name: "红虫", category: 27, baitType: 2},
	}
	groups := buildBaitGroups(baits, fishBaitRule{allow: []string{"红蚯蚓", "红虫"}})
	if len(groups) != 4 {
		t.Fatalf("应聚成 4 组（煮饵红蚯蚓/活红蚯蚓/煮饵红虫/活红虫），实得 %d: %v", len(groups), groupBases(groups))
	}
	for _, g := range groups {
		switch g.base {
		case "红蚯蚓":
			if len(g.baits) == 3 && !strings.Contains(g.label, "10") {
				t.Fatalf("三个尺寸应同组并折叠显示，实得 %q", g.label)
			}
		case "红虫":
			if len(g.baits) == 2 {
				t.Fatalf("红虫14 不应和活红虫同组")
			}
		}
	}
}

func groupBases(groups []baitGroup) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.base)
	}
	return out
}

func TestAutoQuota(t *testing.T) {
	if autoQuota(0) != 3 || autoQuota(1) != 2 || autoQuota(2) != 1 {
		t.Fatalf("稀有度配额映射错误: %d/%d/%d", autoQuota(0), autoQuota(1), autoQuota(2))
	}
}

// 人工「立即重抽」= 轮次偏移 +1：同一时刻换一批开口饵，但周期长度与下次自然
// 切换时刻都不变（否则点一次按钮就把下次轮换推后一整轮）。
func TestAutoRotationRoundOffsetKeepsCadence(t *testing.T) {
	now := time.Unix(worldDayEpochUnix+1500, 0)
	const sec = int64(1800)
	// 名字不能带尾部阿拉伯数字：型号分组会把末尾规格号剥掉，六款会被并成一组。
	names := []string{"重抽饵甲", "重抽饵乙", "重抽饵丙", "重抽饵丁", "重抽饵戊", "重抽饵己"}
	baits := make([]ruleBait, 0, len(names))
	for i, n := range names {
		baits = append(baits, ruleBait{id: "reroll_b" + strconv.Itoa(i), name: n, category: 27})
	}
	base := ExportedAutoRotation("reroll_fish", baits, names, nil, nil, 0, sec, 0, now)
	if !base.Enabled || len(base.ActiveBaits) != autoPickCommon {
		t.Fatalf("池子够大时应启用轮换并开 %d 组，实得 enabled=%v active=%d",
			autoPickCommon, base.Enabled, len(base.ActiveBaits))
	}
	changed := false
	for round := int64(1); round <= 6; round++ {
		st := ExportedAutoRotation("reroll_fish", baits, names, nil, nil, 0, sec, round, now)
		if st.NextSwitch != base.NextSwitch {
			t.Fatalf("偏移 %d 改了下一次切换时刻：%d != %d", round, st.NextSwitch, base.NextSwitch)
		}
		if strings.Join(st.ActiveBaits, "|") != strings.Join(base.ActiveBaits, "|") {
			changed = true
		}
	}
	if !changed {
		t.Fatal("连换 6 轮开口饵都没变化，偏移没有进抽样")
	}
	back := ExportedAutoRotation("reroll_fish", baits, names, nil, nil, 0, sec, 0, now)
	if strings.Join(back.ActiveBaits, "|") != strings.Join(base.ActiveBaits, "|") {
		t.Fatal("偏移归零后没有回到原批次")
	}
}

// 型号名剥离：NBSP 分隔的尺寸要吃掉，型号编号只能剥一层。
func TestBaitBaseNameStripsOneSpec(t *testing.T) {
	cases := map[string]string{
		"甜奶油 PopUp\u00a016": "甜奶油 PopUp", // NBSP 分隔：\s 不含 U+00A0，漏了会拆组
		"河螯虾14":             "河螯虾",
		"玉米 14":             "玉米",
		"Lurker 100-04":     "Lurker 100", // 再剥一层就会把 100/200/300 并成「Lurker」
		"红蚯蚓":               "红蚯蚓",        // 没有规格后缀时原样返回
	}
	for in, want := range cases {
		if got := baitBaseName(in); got != want {
			t.Fatalf("baitBaseName(%q)=%q，应为 %q", in, got, want)
		}
	}
}

// allow 只点中一款具体饵时，抽中该组必须把同型号其它规格一起开口。
// 反例（按 allow 命中项分组）会让「甜奶油 PopUp 16」只开 16 这一颗。
func TestBaitGroupExpandsWholeSeries(t *testing.T) {
	baits := []ruleBait{
		{id: "boils_sb_sc_pu12", name: "甜奶油 PopUp\u00a012", category: 27, baitType: 8},
		{id: "boils_sb_sc_pu16", name: "甜奶油 PopUp\u00a016", category: 27, baitType: 8},
		{id: "boils_sb_sc_pu20", name: "甜奶油 PopUp\u00a020", category: 27, baitType: 8},
	}
	groups := buildBaitGroups(baits, fishBaitRule{allow: []string{"甜奶油 PopUp\u00a016"}})
	if len(groups) != 1 {
		t.Fatalf("应只有 1 组，实得 %d: %v", len(groups), groupBases(groups))
	}
	if len(groups[0].baits) != 3 {
		t.Fatalf("allow 只写了 16，组里应有 12/16/20，实得 %d", len(groups[0].baits))
	}
	if !strings.Contains(groups[0].label, "12") || !strings.Contains(groups[0].label, "Pop-up") {
		t.Fatalf("折叠名应带上尺寸与形态，实得 %q", groups[0].label)
	}
}

// 同名不同形态是两种产品（「河螯虾 24」既有 Pop-up 也有颗粒），
// 按名字去重会先把 Pop-up 记进 seen，再来的颗粒 24 被当成重复丢掉。
func TestBaitGroupKeepsSameNameDifferentShape(t *testing.T) {
	baits := []ruleBait{
		// 加载顺序按 item_id：boils_* 在 plt_* 之前，正是踩坑顺序。
		{id: "boils_sb_craw24", name: "河螯虾 24", category: 27, baitType: 8},
		{id: "plt_cb_cwf14", name: "河螯虾14", category: 27, baitType: 9},
		{id: "plt_cb_cwf18", name: "河螯虾 18", category: 27, baitType: 9},
		{id: "plt_cb_cwf24", name: "河螯虾 24", category: 27, baitType: 9},
	}
	groups := buildBaitGroups(baits, fishBaitRule{allow: []string{"河螯虾14"}})
	if len(groups) != 1 {
		t.Fatalf("只有颗粒组被 allow 命中，应成 1 组，实得 %d: %v", len(groups), groupBases(groups))
	}
	if got := len(groups[0].baits); got != 3 {
		t.Fatalf("颗粒组应含 14/18/24，实得 %d（同名去重把颗粒 24 吃掉了）", got)
	}
	if groups[0].shape != "颗粒" {
		t.Fatalf("组形态应为颗粒，实得 %q", groups[0].shape)
	}
}
