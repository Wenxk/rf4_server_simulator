package gameserver

import (
	"testing"
	"time"
)

// 爱吃饵硬约束（用户 2026-11-19 口径）：规则表写了「吃 X」的鱼，钩上的饵必须
// 命中 allow 才进候选；鱼种食谱 diet_json 是紧随其后的第二道资格门。
// 攻略 allow 是鱼口设定本身；companion 官服榜、本图渔获、rf4 占比这些
// 「谁钓到过」的人类记录仍然一律不参与判鱼。
func TestBaitBiteIgnoresHumanRecords(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_alias(alias,loc) VALUES('level_905_diet_a','diet-only-a'),('level_905_diet_b','diet-only-b')`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES
			('diet-only-a','c.roach','常见拟鲤',100),('diet-only-a','a_salmon','大西洋鲑鱼',100),
			('diet-only-b','c.roach','常见拟鲤',100),('diet-only-b','a_salmon','大西洋鲑鱼',100)`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999861,'test_corn',27,'{"fopbhlmkcpe":10}')`,
		// 玉米粥在常见拟鲤的「爱吃饵」里；没有中文名就无法与攻略文案比对。
		`INSERT OR REPLACE INTO item_catalog(item_id,name_zh) VALUES('test_corn','玉米粥')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(worldDayEpochUnix+1500, 0)
	// 每次新缓存：否则刚插进去的记录会被上一轮查询缓存挡住。
	weight := func(loc, fish string) float64 {
		t.Helper()
		keys, w := s.filterFishBaitSet(loc, []string{"test_corn"}, []string{fish}, []float64{1}, now,
			fishRollContext{cache: newFishingRollCache()})
		if len(keys) == 0 {
			return 0
		}
		return w[0]
	}

	// 1. 常见拟鲤爱吃玉米粥且食谱有 plant：两个图都没有任何渔获纪录，照样能咬。
	a, b := weight("diet-only-a", "c.roach"), weight("diet-only-b", "c.roach")
	if a <= 0 || b <= 0 {
		t.Fatalf("没有本图纪录不应判死：a=%f b=%f", a, b)
	}
	// 2. 分数与钓场无关：同一鱼种吃同一饵，换片水权重完全一样。
	if a != b {
		t.Fatalf("饵料权重不应随钓场变化：a=%f b=%f", a, b)
	}
	// 3. 大西洋鲑鱼的爱吃饵里没有玉米粥，食谱里也没有 plant → 怎么都钓不到。
	if w := weight("diet-only-a", "a_salmon"); w != 0 {
		t.Fatalf("大西洋鲑鱼不吃玉米粥，应完全钓不到，实得 %f", w)
	}

	// 4. 补满人类记录：本图渔获 + 官服榜 top 档，权重必须一分不变。
	if _, err := db.SQL.Exec(`INSERT INTO loc_bait_fish(loc,bait,fish_key,catches) VALUES('diet-only-a','test_corn','c.roach',12)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO companion_bait_fish(bait_id,fish_key,tier,evidence) VALUES('test_corn','c.roach',3,900)`); err != nil {
		t.Fatal(err)
	}
	if w := weight("diet-only-a", "c.roach"); w != a {
		t.Fatalf("人类记录不应改变鱼口：有记录 %f ≠ 无记录 %f", w, a)
	}
	// 5. 同一鱼种换个图补记录也不影响本图。
	if w := weight("diet-only-b", "c.roach"); w != a {
		t.Fatalf("别的图补记录影响了本图：%f ≠ %f", w, a)
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
// 也不该吃鱼块。恢复硬约束后这两口必须被挡住。
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
