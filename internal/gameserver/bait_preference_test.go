package gameserver

import (
	"testing"
	"time"
)

func TestFishBaitPreferenceAlternateCombinationAndChange(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	if _, err := db.SQL.Exec(`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999804,'rule_fly',27,'{"fopbhlmkcpe":7}'); INSERT INTO item_catalog(item_id,name_zh) VALUES('rule_fly','苍蝇')`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(worldDayEpochUnix+1500, 0)
	cache := newFishingRollCache()
	weight := func(baits []string, at time.Time) float64 {
		t.Helper()
		keys, w := s.filterFishBaitSet("rules-test", baits, []string{"c.roach"}, []float64{1}, at, fishRollContext{cache: cache})
		if len(keys) == 0 {
			return 0
		}
		return w[0]
	}
	favorite := weight([]string{"rule_worm"}, now)
	if favorite < 1 {
		t.Fatalf("favorite=%f", favorite)
	}
	// 严格 allow：不在名单里的饵直接 0，不再有「保底杂口」。
	if other := weight([]string{"rule_other"}, now); other != 0 {
		t.Fatalf("allow 之外的饵应归零，实得 %f", other)
	}
	if weight([]string{"rule_worm", "rule_fly"}, now) <= favorite {
		t.Fatal("two preferred baits must participate together")
	}
	if weight([]string{"rule_worm", "rule_worm"}, now) != favorite || weight([]string{"rule_fly", "rule_worm"}, now) != weight([]string{"rule_worm", "rule_fly"}, now) {
		t.Fatal("duplicates or slot ordering changed bait preference")
	}
	// 禁饵只作用于它自己那一款：同杆里合法的蚯蚓仍然有效（多钩组不能连坐），
	// 单独挂禁饵、坏 id、空列表则必须一片饵都不给。
	if weight([]string{"rule_lure"}, now) != 0 {
		t.Fatal("banned lure alone must not feed the fish")
	}
	if got := weight([]string{"rule_worm", "rule_lure"}, now); got != favorite {
		t.Fatalf("banned bait must be skipped, not poison the legal one: %f != %f", got, favorite)
	}
	if weight([]string{"missing"}, now) != 0 || weight(nil, now) != 0 {
		t.Fatal("invalid or absent bait must not be scored")
	}
	if favorite == weight([]string{"rule_worm"}, now.Add(600*time.Second)) {
		t.Fatal("bait preference did not vary over game time")
	}
}
