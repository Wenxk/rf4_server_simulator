package gameserver

import (
	"testing"
	"time"
)

// 整类词必须按客户端枚举命中，「颗粒」不能退化成同名饵、也不能漏掉别类。
func TestBaitTypeTermsMatchEnum(t *testing.T) {
	pellet := ruleBait{id: "plt_hvz_op14", name: "奇特颗粒 14", category: 27, baitType: 9}
	popup := ruleBait{id: "boil_cream_16", name: "甜奶油 PopUp 16", category: 27, baitType: 8}
	fillet := ruleBait{id: "craft_bait_fillet", name: "鱼片", category: 27, baitType: 14}
	worm := ruleBait{id: "worm_01", name: "红虫", category: 27, baitType: 2}
	lure := ruleBait{id: "aikkila_4g_001", name: "Aikkila 4g-001", category: 17, lureType: 1}
	pilker := ruleBait{id: "bond_125_01", name: "Bond 125-01", category: 17, lureType: 6}

	cases := []struct {
		term string
		bait ruleBait
		want bool
	}{
		// 整类 → 枚举
		{"颗粒", pellet, true},
		{"颗粒", popup, false},
		{"浮水饵", popup, true},
		{"浮水饵", pellet, false},
		{"饵料", pellet, true},
		{"饵料", lure, false},
		{"拟饵", lure, true},
		{"拟饵", pellet, false},
		{"各类饵", lure, true},
		{"各类饵", pellet, true},
		{"鱼片类", fillet, true},
		{"鱼片类", pellet, false},
		{"虫类", worm, true},
		{"虫类", fillet, false},
		{"旋转亮片", lure, true},
		{"旋转亮片", pilker, false},
		// 历史写法（内置规则表在用）必须继续命中
		{"鱼肉类", fillet, true},
		{"昆虫幼虫", worm, true},
		{"昆虫", worm, false},
		{"亮片类", lure, true},
		{"软饵类", pilker, false},
		// 具体饵名照旧：鱼片只是那一款，不是整类
		{"鱼片", fillet, true},
		{"鱼片", pellet, false},
	}
	for _, c := range cases {
		if got := c.bait.matches(c.term); got != c.want {
			t.Errorf("%q × %s（cat=%d）=%v，期望 %v",
				c.term, c.bait.name, c.bait.category, got, c.want)
		}
	}
}

// 整类词写进 allow 后，自动轮换的池子就是这一类，抽签只从这一类里抽。
func TestAutoRotationPicksFromTypeTerm(t *testing.T) {
	baits := []ruleBait{
		{id: "plt_a", name: "奇特颗粒 14", category: 27, baitType: 9},
		{id: "plt_b", name: "吞拿鱼 24", category: 27, baitType: 9},
		{id: "plt_c", name: "大号 20", category: 27, baitType: 9},
		{id: "pop_a", name: "甜奶油 PopUp 16", category: 27, baitType: 8},
	}
	r := fishBaitRule{allow: []string{"颗粒"}}
	groups := buildBaitGroups(baits, r)
	if len(groups) != 3 {
		t.Fatalf("池子组数=%d，期望 3（只含颗粒）：%v", len(groups), groups)
	}
	for _, g := range groups {
		for _, b := range g.baits {
			if b.baitType != 9 {
				t.Fatalf("池子里混进了非颗粒：%s（baitType=%d）", b.name, b.baitType)
			}
		}
	}
	// 超稀有配额 1：从 3 个颗粒型号里抽 1 个，抽中的必须还是颗粒。
	now := time.Unix(1_700_000_000, 0)
	st := ExportedAutoRotation("c.test", baits, []string{"颗粒"}, nil, nil, 2, 600, 0, now)
	if !st.Enabled || len(st.ActiveBaits) != 1 {
		t.Fatalf("轮换状态=%+v，期望启用且抽中 1 款", st)
	}
	if st.PoolSize != 3 || st.Quota != 1 {
		t.Fatalf("池子/配额=%d/%d，期望 3/1", st.PoolSize, st.Quota)
	}
	// 整类进 deny：这一类全部挡掉。
	blocked := 0
	for _, b := range baits {
		if baitBlockedByRule(fishBaitRule{deny: []string{"浮水饵"}}, fishRollContext{}, b) {
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("deny 浮水饵 挡掉 %d 款，期望 1", blocked)
	}
}

// 后台「按大类加入」的清单必须是去重、无 `/`、且与匹配表同一份。
func TestExportedBaitTypeTermsShape(t *testing.T) {
	terms := ExportedBaitTypeTerms()
	if len(terms) < 30 {
		t.Fatalf("大类词只有 %d 个，类型没列全", len(terms))
	}
	seen := map[string]bool{}
	for _, term := range terms {
		if term.Term == "" || term.Label == "" {
			t.Fatalf("空词/空标签：%+v", term)
		}
		if seen[term.Term] {
			t.Fatalf("重复的大类词：%s", term.Term)
		}
		seen[term.Term] = true
		if containsSlash(term.Term) {
			t.Fatalf("规则词不能含 /（规则文件的分隔符）：%s", term.Term)
		}
		if !IsBaitTypeTerm(term.Term) {
			t.Fatalf("清单里的 %q 在匹配表里查不到", term.Term)
		}
		if term.Alias {
			continue // 历史别名只参与匹配/染色，不参与「一键加整类」的反查
		}
		// 每个整类都要有对应的（品类, 二级类型）标签，后台分类树才对得上。
		if term.Cat != 0 && len(term.Subs) == 0 {
			if got, ok := ExportedBaitCategoryTerm(term.Cat); !ok || got != term.Term {
				t.Fatalf("大类 %d 的规则词=%q(ok=%v)，期望 %q", term.Cat, got, ok, term.Term)
			}
		}
		if len(term.Subs) > 0 {
			if got, ok := ExportedBaitTypeTermOf(term.Cat, term.Subs[0]); !ok || got != term.Term {
				t.Fatalf("%d/%d 的规则词=%q(ok=%v)，期望 %q", term.Cat, term.Subs[0], got, ok, term.Term)
			}
		}
	}
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
