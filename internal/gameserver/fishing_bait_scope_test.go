package gameserver

import (
	"strings"
	"testing"
)

// TestBaitScopeSplit 锁定 `限定词@饵名` 的拆解规则。
//
// 这条机制存在的原因：型号名会跨类别重名。实测 4123 款饵里有 25 个型号名
// 横跨 ≥2 个二级类别（`鱼子` 在 自然的/香煮成饵/颗粒 三处、`香子兰` 在
// 香煮成饵/颗粒/人造玉米 三处）。只写 `鱼子` 会命中全部 14 款，
// 表达不了「只要香煮成饵的鱼子」。
func TestBaitScopeSplit(t *testing.T) {
	for _, c := range []struct {
		term, scope, name string
		why               string
	}{
		{"香煮成饵@鱼子", "香煮成饵", "鱼子", "标准限定写法"},
		{"颗粒@鱼子", "颗粒", "鱼子", "限制到颗粒"},
		{"拟饵@虾", "拟饵", "虾", "拟饵侧同样适用"},
		{"下沉香煮成饵@鱼子", "下沉香煮成饵", "鱼子", "形态词也能当限定"},
		{"香煮成饵（下沉 + Pop-up）@鱼子", "香煮成饵", "鱼子", "允许用展示名当限定词"},
		{"鱼子", "", "鱼子", "不含分隔符 = 普通饵名"},
		{"@鱼子", "", "@鱼子", "左边为空，不当限定"},
		{"香煮成饵@", "", "香煮成饵@", "右边为空，不当限定"},
		{"不存在的类别@鱼子", "", "不存在的类别@鱼子", "左边不是已知类别词，不当限定（保护带 @ 的真实饵名）"},
		{"Finesse Micro Jig 1/8 oz 002", "", "Finesse Micro Jig 1/8 oz 002", "带斜杠的饵名不受影响"},
	} {
		scope, name, ok := SplitBaitScope(c.term)
		if ok {
			if c.scope == "" {
				t.Fatalf("%q 应判为普通饵名（%s），实际拆成 scope=%q name=%q", c.term, c.why, scope, name)
			}
			if scope != c.scope || name != c.name {
				t.Fatalf("%q 拆分 = (%q, %q)，期望 (%q, %q)（%s）", c.term, scope, name, c.scope, c.name, c.why)
			}
			continue
		}
		if c.scope != "" {
			t.Fatalf("%q 应拆成 (%q, %q)，实际没拆开（%s）", c.term, c.scope, c.name, c.why)
		}
	}
	// 反向：JoinBaitScope 必须被 SplitBaitScope 还原
	for _, c := range [][2]string{{"香煮成饵", "鱼子"}, {"颗粒", "香子兰"}, {"拟饵", "虾"}} {
		joined := JoinBaitScope(c[0], c[1])
		if !strings.Contains(joined, baitScopeSep) {
			t.Fatalf("JoinBaitScope(%q,%q)=%q 缺少分隔符", c[0], c[1], joined)
		}
		scope, name, ok := SplitBaitScope(joined)
		if !ok || scope != c[0] || name != c[1] {
			t.Fatalf("Join→Split 不闭环：%q → (%q,%q,ok=%v)", joined, scope, name, ok)
		}
	}
	// 空参数不能拼出孤立分隔符（会变成一个永远匹配不上的死词）
	if got := JoinBaitScope("", "鱼子"); got != "鱼子" {
		t.Fatalf("JoinBaitScope(\"\",\"鱼子\")=%q，期望 \"鱼子\"", got)
	}
	if got := JoinBaitScope("颗粒", ""); got != "" {
		t.Fatalf("JoinBaitScope(\"颗粒\",\"\")=%q，期望空串", got)
	}
}

// TestBaitScopeNarrowsSameNameAcrossCategories 是本机制的核心断言：
// 限定写法必须**收窄**同名饵的命中范围，而不是原样放过。
//
// 数据（实测）：叫「鱼子」的饵共 14 款——
//
//	自然的(1)   caviar
//	香煮成饵(8) 鱼子10/16/20/26、鱼子 Pop Ups 10/16/20/26
//	颗粒(9)     鱼子 14/18/24
//	人造玉米(10) 鱼子 红色 / 鱼子 黄色
func TestBaitScopeNarrowsSameNameAcrossCategories(t *testing.T) {
	bait := func(name string, cat, subtype int64) ruleBait {
		b := ruleBait{name: name, category: cat}
		if cat == 17 {
			b.lureType = subtype
		} else {
			b.baitType = subtype
		}
		return b
	}
	cases := []struct {
		bait ruleBait
		why  string
	}{
		{bait("鱼子", 27, 1), "自然的鱼子"},
		{bait("鱼子10", 27, 8), "香煮成饵鱼子"},
		{bait("鱼子 14", 27, 9), "颗粒鱼子"},
		{bait("鱼子 红色", 27, 10), "人造玉米鱼子"},
	}

	// 无限定：14 款全部命中（这是当前行为，保留不变，不能因为加了机制就改口径）
	for _, c := range cases {
		if !c.bait.matches("鱼子") {
			t.Fatalf("无限定的「鱼子」应命中 %s，实际不命中", c.why)
		}
	}

	// 有量化：只有目标类别命中
	for _, scope := range []struct {
		term string
		want []int64 // 命中的 baitType
	}{
		{"香煮成饵@鱼子", []int64{8}},
		{"颗粒@鱼子", []int64{9}},
		{"人造玉米@鱼子", []int64{10}},
		{"自然的@鱼子", []int64{1}},
	} {
		for _, c := range cases {
			hit := c.bait.matches(scope.term)
			shouldHit := false
			for _, w := range scope.want {
				if c.bait.baitType == w {
					shouldHit = true
				}
			}
			if hit != shouldHit {
				t.Fatalf("%q 对 %s（baitType=%d）命中=%v，期望 %v",
					scope.term, c.why, c.bait.baitType, hit, shouldHit)
			}
		}
	}

	// 反向守卫：限定词必须真的起作用，不能把整类全放行。
	// 写「颗粒@鱼子」时应只命中叫鱼子的颗粒，不该命中同类的其它颗粒。
	if bait("某颗粒 6mm", 27, 9).matches("颗粒@鱼子") {
		t.Fatal("「颗粒@鱼子」不该命中其它颗粒，说明只判了类别、没判名字")
	}
	// 也不能只判名字不判类别（那样等于无限定）
	if bait("鱼子10", 27, 8).matches("颗粒@鱼子") {
		t.Fatal("「颗粒@鱼子」不该命中香煮成饵的鱼子，说明只判了名字、没判类别")
	}
}

// TestBaitScopePreMatchesSameAsDirect 保证批量路径（matchesPre，池子构建用）
// 与单条路径（matches，判鱼用）口径完全一致。
//
// 两套路径写过不同的历史 bug：单条改了、批量没改，表现是「后台抽查签能抽到、
// 判鱼拒收」这种只在一半场景出现的静默失效。
func TestBaitScopePreMatchesSameAsDirect(t *testing.T) {
	terms := []string{
		"鱼子",                           // 无限定
		"香煮成饵@鱼子",                      // 限定香煮成饵
		"颗粒@鱼子",                        // 限定颗粒
		"颗粒",                           // 整类词
		"拟饵@虾",                         // 拟饵侧限定
		"Finesse Micro Jig 1/8 oz 002", // 带斜杠的普通饵名
	}
	pre := newMatchTerms(terms)
	baits := []ruleBait{
		{name: "鱼子", category: 27, baitType: 1},
		{name: "鱼子10", category: 27, baitType: 8},
		{name: "鱼子 14", category: 27, baitType: 9},
		{name: "鱼子 红色", category: 27, baitType: 10},
		{name: "玉米 6mm", category: 27, baitType: 9},
		{name: "虾 3寸", category: 17, lureType: 15},
		{name: "Finesse Micro Jig 1/8 oz 002", category: 17, lureType: 3},
		{name: "无关饵", category: 27, baitType: 9},
	}
	for _, b := range baits {
		want := b.matchesAny(terms)
		got := b.matchesPre(pre)
		if want != got {
			t.Fatalf("%q（cat%d/bait%d/lure%d）批量=%v 单条=%v，两条路径口径必须一致",
				b.name, b.category, b.baitType, b.lureType, got, want)
		}
	}
	// 顺带固定几条语义，避免上面的「一致」变成两边一起错。
	// 这里必须用**只含限定词**的词表：上面的 terms 里还有无限定的 `鱼子`，
	// 拿它去断言「不该命中」会一直命中那条，测不出限定有没有生效。
	onlyScope := newMatchTerms([]string{"香煮成饵@鱼子"})
	if !(ruleBait{name: "鱼子10", category: 27, baitType: 8}).matchesPre(onlyScope) {
		t.Fatal("批量路径应命中 香煮成饵@鱼子")
	}
	if (ruleBait{name: "鱼子 14", category: 27, baitType: 9}).matchesPre(onlyScope) {
		t.Fatal("批量路径不该把颗粒的鱼子算进 香煮成饵@鱼子")
	}
	if (ruleBait{name: "鱼子", category: 27, baitType: 1}).matchesPre(onlyScope) {
		t.Fatal("批量路径不该把自然的鱼子算进 香煮成饵@鱼子")
	}
}

// TestBaitScopeTermsNeverContainSeparator 守卫：类别词的正式名与别名里不能出现 `@`。
//
// 一旦出现，`splitBaitScope` 会把类别词自身也当成「限定词+名字」拆开，
// 导致整类词静默失效——写「颗粒」结果一条都不命中，界面上完全看不出来。
func TestBaitScopeTermsNeverContainSeparator(t *testing.T) {
	for _, term := range []string{"各类饵", "鱼饵", "拟饵", "颗粒", "香煮成饵", "下沉香煮成饵", "Pop-up 香煮成饵"} {
		if strings.Contains(term, baitScopeSep) {
			t.Fatalf("类别词 %q 含分隔符 %q，会让整类匹配失效", term, baitScopeSep)
		}
	}
	for _, tr := range baitTypeTerms {
		if strings.Contains(tr.Term, baitScopeSep) {
			t.Fatalf("类别词表里的 %q 含分隔符 %q", tr.Term, baitScopeSep)
		}
	}
}
