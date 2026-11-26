package gameserver

import (
	"testing"
	"time"
)

// 证据口径（用户 2026-11-19）：判定只认规则表 allow 与 companion 饵效档位。
// 本图渔获（loc_bait_fish）与 rf4records 官服占比都不参与——两者都只说明
// 「谁钓到过」，接进判定会让同一鱼种同一款饵在不同钓场/不同数据批次下漂移。
func TestBaitScoreIgnoresLocalCatchesAndOfficialShare(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_alias(alias,loc) VALUES('level_905_diet_a','diet-only-a')`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES
			('diet-only-a','c.roach','常见拟鲤',100),('diet-only-a','a_salmon','大西洋鲑鱼',100)`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999861,'test_corn',27,'{"fopbhlmkcpe":10}')`,
		`INSERT OR REPLACE INTO item_catalog(item_id,name_zh) VALUES('test_corn','玉米粥')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(worldDayEpochUnix+1500, 0)
	// 每次新缓存：否则刚插进去的记录会被上一轮查询缓存挡住。
	weight := func(fish string) float64 {
		t.Helper()
		keys, w := s.filterFishBaitSet("diet-only-a", []string{"test_corn"}, []string{fish}, []float64{1}, now,
			fishRollContext{cache: newFishingRollCache()})
		if len(keys) == 0 {
			return 0
		}
		return w[0]
	}

	// 大西洋鲑鱼的「爱吃饵」里没有玉米粥：基线必须为 0（钓不到）。
	base := weight("a_salmon")
	if base != 0 {
		t.Fatalf("规则未允许的饵基线应为 0，实得 %f", base)
	}
	// 补本图渔获记录：不改变判定。
	if _, err := db.SQL.Exec(`INSERT INTO loc_bait_fish(loc,bait,fish_key,catches) VALUES('diet-only-a','test_corn','a_salmon',12)`); err != nil {
		t.Fatal(err)
	}
	if w := weight("a_salmon"); w != base {
		t.Fatalf("本图渔获不应参与判定：有记录 %f ≠ 无记录 %f", w, base)
	}
	// 补官服占比记录：同样不改变判定（rf4 只作后台参考）。
	if _, err := db.SQL.Exec(`INSERT INTO rf4records_catch
		(fish_name_en,bait_name,weight_g,catch_date,waterbody,fetched_at,fish_key,bait_id)
		VALUES('Atlantic salmon','Cornmeal porridge',1200,'01.01.26','Lake','0','a_salmon','test_corn')`); err != nil {
		t.Fatal(err)
	}
	if w := weight("a_salmon"); w != base {
		t.Fatalf("官服占比不应参与判定：有纪录 %f ≠ 无纪录 %f", w, base)
	}
	// companion 饵效实证 tier3：有 allow 规则时不能绕过白名单。
	if _, err := db.SQL.Exec(`INSERT INTO companion_bait_fish(bait_id,fish_key,tier,evidence) VALUES('test_corn','a_salmon',3,900)`); err != nil {
		t.Fatal(err)
	}
	if w := weight("a_salmon"); w != base {
		t.Fatalf("有 allow 规则时 companion 不应绕过白名单：%f ≠ 基线 %f", w, base)
	}
}

// 食谱里的低值（0.2 ≈ 偶尔吃一口）不影响 allow 准入：命中 allow 就进候选，
// 具体权重由 allow 与 companion 决定，不再走食性强度。
//
// 测试数据必须与真实库同口径：data/rf4server.db 的「玉米粥」（item_id=porridge_corn）
// 是 `fopbhlmkcpe`=13（粥和面团）。早前这里写 10（人造玉米），规则表改成分类型写法
// `粥和面团@玉米粥` 之后类别对不上，被测鱼会被判死——那是测试数据与真实库不符，
// 不是规则写错。
func TestBaitLowDietValueStillAllowed(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_alias(alias,loc) VALUES('level_906_diet','diet-low')`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES('diet-low','c.roach','常见拟鲤',100)`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999862,'test_corn2',27,'{"fopbhlmkcpe":13}')`,
		`INSERT OR REPLACE INTO item_catalog(item_id,name_zh) VALUES('test_corn2','玉米粥')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	keys, w := s.filterFishBaitSet("diet-low", []string{"test_corn2"}, []string{"c.roach"}, []float64{1},
		time.Unix(worldDayEpochUnix+1500, 0), fishRollContext{cache: newFishingRollCache()})
	if len(keys) != 1 || w[0] <= 0 {
		t.Fatalf("命中爱吃饵的玉米粥不该被判死：keys=%v w=%v", keys, w)
	}
}

// 用户 2026-11-19 实测的两条鱼：青蛙设定里只有苍蝇、镜鲤设定里没有苍蝇，
// 也不该吃鱼块：两款饵都不在 allow、也没有 companion 实证，必须被挡住。
func TestFrogRejectsFishPiecesAndMirrorCarpRejectsFly(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('rules-test','m_carp','镜鲤')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES
			(999871,'fly',27,'{"fopbhlmkcpe":7}'),(999872,'fish_pieces',27,'{"fopbhlmkcpe":1}')`,
		`INSERT OR REPLACE INTO item_catalog(item_id,name_zh) VALUES('fly','苍蝇'),('fish_pieces','鱼块')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(worldDayEpochUnix+1500, 0)
	call := func(bait string) []string {
		keys, _ := s.filterFishBaitSet("rules-test", []string{bait},
			[]string{"frog", "m_carp"}, []float64{1, 1}, now,
			fishRollContext{cache: newFishingRollCache()})
		return keys
	}
	// 苍蝇：青蛙爱吃，镜鲤的设定里没有。
	if got := call("fly"); len(got) != 1 || got[0] != "frog" {
		t.Fatalf("苍蝇只该钓到青蛙，实得 %v", got)
	}
	// 鱼块：青蛙设定里没有，镜鲤设定里也没有。
	if got := call("fish_pieces"); len(got) != 0 {
		t.Fatalf("鱼块不该钓到青蛙或镜鲤，实得 %v", got)
	}
}
