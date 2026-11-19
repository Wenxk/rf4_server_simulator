package gameserver

import (
	"testing"
	"time"
)

// 爱吃饵设定是鱼口资格：同一食性族里，只有攻略写过的饵才能咬。
// 命中后权重仍由食谱强度决定；官服榜、本图渔获这些人类记录不参与。
func TestBaitPreferenceFollowsAllowList(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	if _, err := db.SQL.Exec(`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999804,'rule_fly',27,'{"fopbhlmkcpe":6}'); INSERT INTO item_catalog(item_id,name_zh) VALUES('rule_fly','另一种蚯蚓')`); err != nil {
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
	worm := weight([]string{"rule_worm"}, now)   // 蚯蚓：常见拟鲤爱吃
	other := weight([]string{"rule_other"}, now) // 补充饵：设定里没有
	fly := weight([]string{"rule_fly"}, now)     // 另一种蚯蚓：设定里没有
	if worm <= 0 {
		t.Fatalf("蚯蚓应能咬常见拟鲤，实得 %f", worm)
	}
	// 食谱里同为 worm 类的三款饵，只有写进 allow 的那款能咬。
	if other != 0 || fly != 0 {
		t.Fatalf("不在爱吃饵列表里的饵应完全剔除：rule_other=%f rule_fly=%f", other, fly)
	}
	// 不在 allow 的饵不参与：组合、去重、槽位顺序都不改变结果。
	if got := weight([]string{"rule_worm", "rule_other"}, now); got != worm {
		t.Fatalf("非偏好饵不应参与加权：%f ≠ %f", got, worm)
	}
	if weight([]string{"rule_worm", "rule_worm"}, now) != worm || weight([]string{"rule_other", "rule_worm"}, now) != worm {
		t.Fatal("duplicates or slot ordering changed bait weight")
	}
	if weight([]string{"rule_worm", "rule_lure"}, now) != 0 || weight([]string{"missing"}, now) != 0 || weight(nil, now) != 0 {
		t.Fatal("combination bypassed banned, invalid or absent bait")
	}
	// 食谱值 1.8 高于偏好上限 1.5：主食饵恒按满分处理，抖动被上限吃掉是预期行为。
	if worm != baitPreferenceCap {
		t.Fatalf("staple bait should sit at the preference cap %f, got %f", baitPreferenceCap, worm)
	}
}
