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
	// companion 饵效实证 tier3：允许越级给满档（规则一条都没匹配上时）。
	if _, err := db.SQL.Exec(`INSERT INTO companion_bait_fish(bait_id,fish_key,tier,evidence) VALUES('test_corn','a_salmon',3,900)`); err != nil {
		t.Fatal(err)
	}
	if w := weight("a_salmon"); w <= base {
		t.Fatalf("companion tier3 应参与判定并抬权重，实得 %f（基线 %f）", w, base)
	}
}

// 食谱里的低值（0.2 ≈ 偶尔吃一口）属于"能吃但少"：baitDietStrength 必须返回
// ok=true 和食谱原值，命中 allow 后权重天然低于主食饵。
func TestBaitDietLowValueStillEdible(t *testing.T) {
	diet := map[string]float64{"plant": 0.2, "worm": 2.03}
	corn := ruleBait{id: "test_corn", name: "玉米粥", category: 27, baitType: 10}
	worm := ruleBait{id: "rule_worm", name: "蚯蚓", category: 27, baitType: 6}
	low, ok := baitDietStrength(diet, corn)
	if !ok || low != 0.2 {
		t.Fatalf("食谱里写了 plant=0.2 就是能吃：value=%f ok=%v", low, ok)
	}
	high, ok := baitDietStrength(diet, worm)
	if !ok || high != 2.03 {
		t.Fatalf("主食饵应读到食谱原值：value=%f ok=%v", high, ok)
	}
	if high <= low {
		t.Fatalf("食谱值更高的一方权重应更高：%f 应 > %f", high, low)
	}
	// 命中 allow 的低值饵仍要能进候选，不能因为值低被判死。
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_alias(alias,loc) VALUES('level_906_diet','diet-low')`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES('diet-low','c.roach','常见拟鲤',100)`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999862,'test_corn2',27,'{"fopbhlmkcpe":10}')`,
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
