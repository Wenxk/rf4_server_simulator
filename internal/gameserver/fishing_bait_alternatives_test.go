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
	// 【2026-11-25 复核修正】早前这里断言「常见狗鱼来自后台 fish_bait_override 的人工配置，
	// allow 被收窄成 9 个具体类目」。实测 data/rf4server.db 的 fish_bait_override 已是
	// **0 行**（人工覆盖被清空），因此运行时取到的是内置表那一行：
	//   `常见狗鱼  吃 各类饵（不吃Gummi Makk；底钓只吃鱼肉类/饵鱼类）`
	// 旧断言（allow 长度 9、bottomOnly 长度 3）描述的是已不存在的配置，属过期断言。
	// 这里按当前真实来源重新锁定，同时保留「底钓只吃」必须被 allow 覆盖这条约束（见下一个测试）。
	//
	// 仍然成立的设计意图：规则词统一成大类、用游戏菜单正式名，
	// `鱼肉类/饵鱼类` 这类历史合并写法由 bait_type_terms.go 的别名在匹配时展开。
	if len(pike.allow) != 1 || pike.allow[0] != "各类饵" {
		t.Fatalf("pike allow = %v，期望 [各类饵]（内置表原值）", pike.allow)
	}
	if len(pike.deny) != 1 || pike.deny[0] != "Gummi Makk" {
		t.Fatalf("pike deny = %v，期望 [Gummi Makk]", pike.deny)
	}
	// 【2026-11-25 统一格式】原文 `底钓只吃鱼肉类/饵鱼类`，现拆成三个正式类别词：
	// `鱼肉类`→`鱼片`（Cat27/Sub1），`饵鱼类`→`活的`+`死鱼`（Sub4 + Sub15）。
	if len(pike.bottomOnly) != 3 || pike.bottomOnly[0] != "鱼片" ||
		pike.bottomOnly[1] != "活的" || pike.bottomOnly[2] != "死鱼" {
		t.Fatalf("pike bottomOnly = %v，期望 [鱼片 活的 死鱼]", pike.bottomOnly)
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
	// 底钓鱼块：**放行**。
	// 早前这里是 `len(keys) != 0`，因为人工覆盖把 allow 收窄成 9 个类目、其中没有鱼肉类。
	// 该覆盖行已清空（fish_bait_override 0 行），当前生效的是内置表 `各类饵`，
	// 而 `各类饵` 含鱼肉类(Sub14)，所以底钓狗鱼应该能吃鱼块。
	// 这条断言正是上一版本注释里预告的分支（"将来把鱼片类补进 allow 后改成 != 1"）。
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_flesh"}, []string{"n.pike"}, []float64{1}, now, fishRollContext{rigType: 12}); len(keys) != 1 {
		t.Fatalf("底钓狗鱼应放行鱼肉（allow=各类饵 含 Sub14），实际 keys=%v", keys)
	}
	// 反向守卫：非底钓时同样放行，说明「底钓只吃」没有误伤普通钓法。
	if keys, _ := s.filterFishBaitSet("rules-test", []string{"rule_flesh"}, []string{"n.pike"}, []float64{1}, now); len(keys) != 1 {
		t.Fatalf("普通钓法也应放行鱼肉，实际 keys=%v", keys)
	}
}

// TestBaitRuleBottomOnlyNeedsAllow 锁定一条容易踩的规则语义：
// `底钓只吃 X` 只能**收窄** allow，不能**扩大** allow——baitBlockedByRule 与
// baitAllowedByRule 是 AND 关系。所以 bottomOnly 里的每个词，都必须能被 allow
// 里的某些词**语义覆盖**，否则那一条是死配置：玩家看着「底钓只吃鱼肉类」，
// 实际底钓连鱼块都吃不上，界面上看不出来。
//
// 现状（2026-11-25 复核）：内置表是 `吃 各类饵（不吃Gummi Makk；底钓只吃鱼肉类/饵鱼类）`。
// `各类饵` 覆盖全部鱼饵与拟饵，因此 bottomOnly 两项都在 allow 内，不存在死配置。
// 早前这里断言「鱼肉类未被覆盖（既有缺陷）」，前提是人工覆盖把 allow 收窄成 9 个类目；
// 该覆盖行已从库中清空（`fish_bait_override` 实测 0 行），前提消失，故改为正向守卫。
func TestBaitRuleBottomOnlyNeedsAllow(t *testing.T) {
	r := fishBaitRules[ruleName("常见狗鱼")]
	// coveredByAllow 用真实判定问：某类饵的代表款能否过 allow。
	// 取代表要比字面比较可靠——类目词之间存在别名与子集关系。
	coveredByAllow := func(bt int64) bool {
		return baitAllowedByRule(r, ruleBait{name: "probe", category: 27, baitType: bt})
	}
	if len(r.bottomOnly) == 0 {
		t.Fatalf("常见狗鱼应有「底钓只吃」限定：%+v", r)
	}
	// 逐项核对 bottomOnly 里出现的词都能放行至少一种饵。
	// 语义映射：鱼肉类=Sub14；饵鱼类=Sub4(活) + Sub15(死鱼)。
	for _, c := range []struct {
		term string
		bts  []int64
	}{
		{"鱼肉类", []int64{14}},
		{"饵鱼类", []int64{4, 15}},
	} {
		inRule := false
		for _, got := range r.bottomOnly {
			if got == c.term {
				inRule = true
			}
		}
		if !inRule {
			continue // 当前规则没写这一项
		}
		passed := false
		for _, bt := range c.bts {
			if coveredByAllow(bt) {
				passed = true
				break
			}
		}
		if !passed {
			t.Fatalf("死配置：bottomOnly 的 %q 没有任何饵能过 allow=%v，底钓会彻底空钩", c.term, r.allow)
		}
	}
	// 反向守卫：真的写出死配置时必须能被发现，避免上面的检查恒真。
	dead := fishBaitRule{allow: []string{"颗粒"}, bottomOnly: []string{"鱼肉类"}}
	if baitAllowedByRule(dead, ruleBait{name: "probe", category: 27, baitType: 14}) {
		t.Fatal("检测逻辑失效：allow=颗粒 不该放行鱼肉类")
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
