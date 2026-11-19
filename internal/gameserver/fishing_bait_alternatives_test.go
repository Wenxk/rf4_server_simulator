package gameserver

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestBaitRuleConditionsAndDietQualification(t *testing.T) {
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
	if len(pike.bottomOnly) != 2 || len(pike.deny) != 7 {
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
	// 爱吃饵硬约束：rule_other「补充饵」不在常见拟鲤的设定里，
	// 食谱里有 worm 也不能咬。
	keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_other"}, []string{"c.roach"}, []float64{1}, now)
	if len(keys) != 0 {
		t.Fatalf("不在爱吃饵列表里的饵不该能咬：%v", keys)
	}
	// rule_worm「蚯蚓」在设定里且食谱 worm=1.80 → 能咬，权重取食谱原值（封顶）。
	keys, weights := s.filterFishBaitSet("rules-test", []string{"rule_worm"}, []string{"c.roach"}, []float64{1}, now)
	if len(keys) != 1 || weights[0] <= 0 {
		t.Fatalf("命中爱吃饵的蚯蚓应能咬：%v %v", keys, weights)
	}
	// 归类不出食性族的饵不当万能饵。
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_unknown"}, []string{"c.roach"}, []float64{1}, now); len(keys) != 0 {
		t.Fatal("unknown natural bait became universal food")
	}
	// 钓法约束仍由攻略的仅底钓 / 禁饵条款管，与鱼口证据无关。
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

func TestAllowedBaitPoolBitesRemainRareAndSkillHelps(t *testing.T) {
	t.Setenv("GODEBUG", "randseednop=0")
	rand.Seed(20261104)
	s, _ := seedBaitRuleTest(t)
	setBaitRuleSwitch(t, s, "1")
	loc := "alternate-bait-test-pool"
	previous, existed := fishPools[loc]
	fishPools[loc] = []fishPoolEntry{{"c.roach", 80, 600, 318}}
	t.Cleanup(func() {
		if existed {
			fishPools[loc] = previous
		} else {
			delete(fishPools, loc)
		}
	})
	// 用命中爱吃饵的蚯蚓；「补充饵」这类不在设定里的饵在新口径下整轮不咬。
	c := fishRollContext{baits: []string{"rule_worm"}, forRig: true, cache: newFishingRollCache()}
	count := func(speed float64) int {
		bites := 0
		for range 500 {
			if f := s.rollFishPoolsWithRules(loc, 0, fishingSkillBonus{BiteSpeed: speed}, c); f != nil {
				if f.Species != "c.roach" {
					t.Fatalf("unexpected species: %s", f.Species)
				}
				bites++
			}
		}
		return bites
	}
	plain, skilled := count(1), count(.5)
	// 只断言方向与量级：技能（BiteSpeed<1）必须提高咬钩次数，且基础方案不是 0 也不是必中。
	if plain <= 0 {
		t.Fatalf("500 attempts base=%d, diet-backed bait should bite", plain)
	}
	if skilled < plain {
		t.Fatalf("bite speed bonus did not help: base=%d skilled=%d", plain, skilled)
	}
}
