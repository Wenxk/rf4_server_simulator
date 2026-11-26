package gameserver

import (
	"testing"
	"time"
)

// 整类词必须按客户端枚举命中，「颗粒」不能退化成同名饵、也不能漏掉别类。
func TestBaitTypeTermsMatchEnum(t *testing.T) {
	pellet := ruleBait{id: "plt_hvz_op14", name: "奇特颗粒 14", category: 27, baitType: 9}
	popup := ruleBait{id: "boil_cream_16", name: "甜奶油 PopUp 16", category: 27, baitType: 8, brand: "hvz_boils_pp_cream"}
	sink := ruleBait{id: "boil_cream_16b", name: "甜奶油 16", category: 27, baitType: 8, brand: "hvz_boils_cream"}
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
		{"饵料", pellet, true},
		{"饵料", lure, false},
		{"拟饵", lure, true},
		{"拟饵", pellet, false},
		{"各类饵", lure, true},
		{"各类饵", pellet, true},
		{"鱼片", fillet, true},
		{"鱼片", pellet, false},
		{"幼虫", worm, true},
		{"幼虫", fillet, false},
		{"旋转亮片", lure, true},
		{"旋转亮片", pilker, false},

		// baitType=8 的形态细分：下沉 / Pop-up / 两种都要
		{"香煮成饵", popup, true},
		{"香煮成饵", sink, true},
		{"香煮成饵", pellet, false},
		{"下沉香煮成饵", sink, true},
		{"下沉香煮成饵", popup, false},
		{"Pop-up香煮成饵", popup, true},
		{"Pop-up香煮成饵", sink, false},
		// 形态词不能命中非 8 号类型
		{"下沉香煮成饵", pellet, false},
		{"Pop-up香煮成饵", fillet, false},

		// 历史写法（内置规则表在用）必须继续命中
		{"虫类", worm, true},
		{"昆虫幼虫", worm, true},
		{"浮水饵", popup, true},
		{"浮水饵", sink, false},
		{"鱼肉类", fillet, true},
		{"鱼片类", fillet, true},
		{"虫类", fillet, false},
		{"亮片类", lure, true},
		{"软饵类", pilker, false},
		// 旧名与新名指向同一枚举，必须都命中
		{"蚯蚓类", ruleBait{id: "worm_02", name: "蚯蚓", category: 27, baitType: 6}, true},
		{"蠕虫", ruleBait{id: "worm_02", name: "蚯蚓", category: 27, baitType: 6}, true},
		{"昆虫类", ruleBait{id: "fly_01", name: "苍蝇", category: 27, baitType: 7}, true},
		{"昆虫", ruleBait{id: "fly_01", name: "苍蝇", category: 27, baitType: 7}, true},
		{"钩虾类", ruleBait{id: "gamm_01", name: "淡水钩虾", category: 27, baitType: 12}, true},
		{"小型甲壳类", ruleBait{id: "gamm_01", name: "淡水钩虾", category: 27, baitType: 12}, true},
		{"活饵类", ruleBait{id: "live_01", name: "欧鲌", category: 27, baitType: 4}, true},
		{"活的", ruleBait{id: "live_01", name: "欧鲌", category: 27, baitType: 4}, true},
	}
	for _, c := range cases {
		if got := c.bait.matches(c.term); got != c.want {
			t.Errorf("%q × %s（cat=%d bt=%d，brand=%q）=%v，期望 %v",
				c.term, c.bait.name, c.bait.category, c.bait.baitType, c.bait.brand, got, c.want)
		}
	}
}

// 香煮成饵的下沉 / Pop-up 只能靠 brand 的 `_pp_` 分辨。
// 这个判定被钉成样例：库里 578 款 baitType=8 中 Pop-up 恰好 222 款、下沉 356 款，
// 与名称里的 PopUp / Pop-Ups / Pop Ups 标记 100% 吻合（零例外）。
// 详见 docs/analysis-bait-category-taxonomy-2026-11-25.md §2.1。
func TestBoilIsPopUpBrandMark(t *testing.T) {
	popupBrands := []string{
		"hvz_boils_pp_van", // 香子兰 Pop-Ups 12
		"op_boils_pp_vanila", // 香子兰 Pop-Up 14
		"cb_boilies_pp_basegreen",
		"cb_boilies_pp_caviar",
		"cb_boilies_pp_baseorange",
	}
	for _, b := range popupBrands {
		if !boilIsPopUp(b) {
			t.Errorf("%q 应判定为 Pop-up", b)
		}
	}
	sinkBrands := []string{
		"hvz_boils_van", // 香子兰 12
		"op_boils_van", // 香子兰 14
		"cb_boilies_basegreen",
		"cb_boilies_baseorange",
		"cb_boilies_caviar",
		"",               // 无 brand：按下沉处理
		"hvz_pel_vanila", // 颗粒，brand 里没有 _pp_
	}
	for _, b := range sinkBrands {
		if boilIsPopUp(b) {
			t.Errorf("%q 不应判定为 Pop-up", b)
		}
	}
}

// 分类树的反查必须跳过形态词，形态词单独列出来。
func TestBaitShapeTermsSeparated(t *testing.T) {
	// baitType=8 的默认标签/规则词是不带形态的那个
	if got, ok := ExportedBaitTypeTermOf(27, 8); !ok || got != "香煮成饵" {
		t.Fatalf("baitType=8 的规则词=%q(ok=%v)，期望「香煮成饵」", got, ok)
	}
	if got, ok := ExportedBaitTypeLabel(27, 8); !ok || got == "" {
		t.Fatalf("baitType=8 的标签为空（ok=%v）", ok)
	}
	shapes := ExportedBaitShapeTerms(27, 8)
	if len(shapes) != 2 {
		t.Fatalf("baitType=8 的形态词=%d 个，期望 2（下沉 / Pop-up）：%+v", len(shapes), shapes)
	}
	terms := map[string]bool{}
	for _, s := range shapes {
		terms[s.Term] = true
		if s.Shape != "sink" && s.Shape != "popup" {
			t.Fatalf("形态词的 Shape 取值非法：%+v", s)
		}
	}
	if !terms["下沉香煮成饵"] || !terms["Pop-up 香煮成饵"] {
		t.Fatalf("形态词清单不对：%+v", terms)
	}
	// 无空格写法不单独登记别名：ruleKey 会去掉连字符与空格，
	// 「Pop-up香煮成饵」与正式名归一化后是同一个键，能直接命中。
	if !IsBaitTypeTerm("Pop-up香煮成饵") || !IsBaitTypeTerm("popup 香煮成饵") {
		t.Fatalf("Pop-up 香煮成饵 的无空格/小写写法应能命中正式名")
	}
	// 其它类型没有形态词
	for _, sub := range []int64{1, 2, 4, 20} {
		if got := ExportedBaitShapeTerms(17, sub); len(got) != 0 {
			t.Fatalf("cat=17 sub=%d 不该有形态词：%+v", sub, got)
		}
	}
}

// 整类词写进 allow 后，自动轮换的池子就是这一类，抽签只从这一类里抽。
func TestAutoRotationPicksFromTypeTerm(t *testing.T) {
	baits := []ruleBait{
		{id: "plt_a", name: "奇特颗粒 14", category: 27, baitType: 9},
		{id: "plt_b", name: "吞拿鱼 24", category: 27, baitType: 9},
		{id: "plt_c", name: "大号 20", category: 27, baitType: 9},
		{id: "pop_a", name: "甜奶油 PopUp 16", category: 27, baitType: 8, brand: "hvz_boils_pp_cream"},
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
	// 只写「下沉香煮成饵」：那款 Pop-up 不能进池子。
	sinkOnly := buildBaitGroups(baits, fishBaitRule{allow: []string{"下沉香煮成饵"}})
	if len(sinkOnly) != 0 {
		t.Fatalf("allow 只有下沉香煮成饵，池子应为空，实得 %d 组：%v", len(sinkOnly), sinkOnly)
	}
	popOnly := buildBaitGroups(baits, fishBaitRule{allow: []string{"Pop-up香煮成饵"}})
	if len(popOnly) != 1 {
		t.Fatalf("allow 只有 Pop-up香煮成饵，池子应只有 1 组，实得 %d 组", len(popOnly))
	}
	// 整类进 deny：这一类全部挡掉。
	blocked := 0
	for _, b := range baits {
		if baitBlockedByRule(fishBaitRule{deny: []string{"香煮成饵"}}, fishRollContext{}, b) {
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("deny 香煮成饵 挡掉 %d 款，期望 1", blocked)
	}
}

// 归一化后不能碰撞：ruleKey 会丢弃空格/连字符/下划线/斜杠等，
// 一旦别名与正式名归一化后同键，后写入的别名会顶掉正式名的索引项，
// 结果正式名被当成 Alias 从后台选择清单里消失（2026-11-25 踩过：
// 「Pop-up香煮成饵」别名顶掉了正式名「Pop-up 香煮成饵」）。
func TestBaitTypeTermsNoKeyCollision(t *testing.T) {
	byKey := map[string]string{}
	for _, term := range ExportedBaitTypeTerms() {
		k := ruleKey(term.Term)
		if prev, ok := byKey[k]; ok {
			t.Fatalf("「%s」与「%s」归一化后同为 %q，会互相顶掉索引", prev, term.Term, k)
		}
		byKey[k] = term.Term
	}
	// 正式名必须能从索引里查到，且查到的不是别名。
	for _, term := range ExportedBaitTypeTerms() {
		if term.Alias {
			continue
		}
		got, ok := baitTypeTermOf(ruleKey(term.Term))
		if !ok {
			t.Fatalf("正式名「%s」在索引里查不到", term.Term)
		}
		if got.Alias {
			t.Fatalf("正式名「%s」被别名顶掉了（查到的 Label=%q）", term.Term, got.Label)
		}
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
		// 形态词不是该类型的「代表词」，反查要跳过它。
		if term.Shape != "" {
			continue
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

// 正式名必须严格取自游戏菜单，不能再出现「虫类 / 蚯蚓类 / 昆虫类」这类自造词。
// 这条守卫是用户 2026-11-25 明确要求的：分类必须按游戏来，不许自己造类别。
func TestBaitTypeTermsUseGameNames(t *testing.T) {
	// 游戏「鱼饵」菜单里的项（见 docs/analysis-bait-category-taxonomy §2）
	// 取值来自客户端 resources.assets 的 kStorageSlotNameBaits* 简中列，
	// 不是我们自己起的名字。
	gameBaitNames := map[string]bool{
		"自然的": true, "幼虫": true, "蛙类": true, "活的": true, "蠕虫": true,
		"昆虫": true, "香煮成饵": true, "下沉香煮成饵": true, "Pop-up 香煮成饵": true,
		"颗粒": true, "人造玉米": true, "坚果": true, "小型甲壳类": true,
		"粥和面团": true, "鱼片": true, "死鱼": true, "海饵": true, "ZIG钓组泡沫": true,
	}
	// 明确禁止的自造词（它们曾经是正式名）
	banned := map[string]bool{"虫类": true, "蚯蚓类": true, "昆虫类": true, "沉底饵": true}

	for _, term := range ExportedBaitTypeTerms() {
		if term.Alias || term.Cat != 27 || len(term.Subs) == 0 {
			continue
		}
		if banned[term.Term] {
			t.Errorf("「%s」是代码自造词，游戏里没有这个分类，不得作为正式名", term.Term)
		}
		if !gameBaitNames[term.Term] {
			t.Errorf("天然饵正式名「%s」不在游戏菜单里，请核对来源", term.Term)
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