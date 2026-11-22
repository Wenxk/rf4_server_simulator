package gameserver

import (
	"math"
	"testing"
	"time"
)

func TestBaitRuleConditionsAndStrictAllow(t *testing.T) {
	conditional := 0
	for _, rule := range fishBaitRules {
		if rule.condition != "" && rule.condition != "无" {
			conditional++
		}
	}
	if conditional != 82 {
		t.Fatalf("parsed prerequisites = %d", conditional)
	}
	pike := fishBaitRules[ruleName("常见狗鱼")]
	// deny 只剩 Gummi Makk：另外六个词（维基虫/吸引成分/硅胶海虫/八爪鱼/
	// 巨大软饵/柳叶亮片）在客户端与静态库里都没有对应商品，属规则文本笔误，已删。
	if len(pike.bottomOnly) != 2 || len(pike.deny) != 1 {
		t.Fatalf("pike restrictions = %+v", pike)
	}
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('rules-test','n.pike','常见狗鱼')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999805,'rule_unknown',27,'{}'),(999806,'rule_flesh',27,'{"fopbhlmkcpe":14}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('rule_unknown','未知天然饵'),('rule_flesh','鱼肉块')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_other"}, []string{"c.roach"}, []float64{1}, now); len(keys) != 0 {
		t.Fatal("allow 之外的饵必须归零")
	}
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_unknown"}, []string{"c.roach"}, []float64{1}, now); len(keys) != 0 {
		t.Fatal("unknown natural bait became universal food")
	}
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_worm"}, []string{"n.pike"}, []float64{1}, now, fishRollContext{rigType: 12}); len(keys) != 0 {
		t.Fatal("bottom pike accepted worms")
	}
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_flesh"}, []string{"n.pike"}, []float64{1}, now, fishRollContext{rigType: 12}); len(keys) != 1 {
		t.Fatal("bottom pike rejected fish flesh")
	}
}

func TestAlternativeBaitInterestUsesSkillWithoutBypassingExclusions(t *testing.T) {
	for _, tc := range []struct{ interest, speed, want float64 }{
		{0, .5, 0}, {.05, 1, .05}, {.05, .8, .0625}, {.05, .1, .1}, {.08, .5, .1}, {.8, .5, 1},
	} {
		if got := fishingBaitInterest(tc.interest, fishingSkillBonus{BiteSpeed: tc.speed}); math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("interest=%g speed=%g: got %g want %g", tc.interest, tc.speed, got, tc.want)
		}
	}
}
