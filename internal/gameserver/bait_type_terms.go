package gameserver

import "strings"

// 「整类饵」规则词：allow / deny / bottom_only 里可以直接写一个类型名，
// 代表这一类全部饵，不必逐个列饵名。
//
// 例：`吃 颗粒` = 所有 pellet 都算爱吃；自动抽取的池子也就是这一类里的全部型号组，
// 本期开口饵从这一类中抽。deny 写「浮水饵」= 所有 Pop-up 都不吃。
//
// 这张表是**唯一事实源**：
//   - 判鱼匹配（matchOne / matchesPreNorm）用它；
//   - 后台「选择鱼饵」的分类标签与「按大类加入」按钮也从它取，
//     免得出现「后台能选、规则里不认」这种静默失效。
//
// Term 里不能出现 `/`：fishing_bait_rules.txt 用 `/` 分隔规则词。
// Alias 里的词是历史写法（内置规则表在用），只参与匹配，不进后台选择清单。
type BaitTypeTerm struct {
	Term  string   `json:"term"`  // 写进规则的词
	Label string   `json:"label"` // 后台展示名
	Cat   int64    `json:"cat"`   // 27 饵料 / 17 拟饵 / 0 不限
	Subs  []int64  `json:"subs"`  // 二级类型：饵料看 baitType、拟饵看 lureType；空=整个大类
	IDs   []string `json:"-"`     // 额外点名的 item_id（规则表历史特例）
	Alias bool     `json:"-"`     // 历史写法：只匹配、不在选择清单里出现
}

// baitTypeTerms 的顺序就是后台按钮的顺序：先全部，再饵料，再拟饵。
var baitTypeTerms = []BaitTypeTerm{
	{Term: "各类饵", Label: "全部（饵料 + 拟饵）", Cat: 0},

	{Term: "饵料", Label: "饵料（全部天然饵）", Cat: 27},

	{Term: "沉底饵", Label: "煮饵 / 天然饵（沉底）", Cat: 27, Subs: []int64{1}},
	{Term: "虫类", Label: "虫类（幼虫 / 红虫）", Cat: 27, Subs: []int64{2}},
	{Term: "蛙类", Label: "蛙类", Cat: 27, Subs: []int64{3}},
	{Term: "活饵类", Label: "活饵 / 饵鱼", Cat: 27, Subs: []int64{4}},
	{Term: "蚯蚓类", Label: "蚯蚓类", Cat: 27, Subs: []int64{6}},
	{Term: "昆虫类", Label: "昆虫类（成虫）", Cat: 27, Subs: []int64{7}},
	{Term: "浮水饵", Label: "Pop-up 浮水饵", Cat: 27, Subs: []int64{8}},
	{Term: "颗粒", Label: "颗粒", Cat: 27, Subs: []int64{9}},
	{Term: "玉米浮", Label: "玉米浮（人造玉米）", Cat: 27, Subs: []int64{10}},
	{Term: "坚果", Label: "坚果", Cat: 27, Subs: []int64{11}},
	{Term: "钩虾类", Label: "钩虾", Cat: 27, Subs: []int64{12}},
	{Term: "面团类", Label: "面团 / 粥类", Cat: 27, Subs: []int64{13}},
	{Term: "鱼片类", Label: "鱼片", Cat: 27, Subs: []int64{14}},
	{Term: "死饵鱼类", Label: "死饵鱼", Cat: 27, Subs: []int64{15}},
	{Term: "海钓饵", Label: "海钓饵", Cat: 27, Subs: []int64{16}},
	{Term: "Zig浮水球", Label: "Zig 浮水球", Cat: 27, Subs: []int64{17}},

	{Term: "拟饵", Label: "拟饵（全部路亚）", Cat: 17},

	{Term: "旋转亮片", Label: "旋转亮片", Cat: 17, Subs: []int64{1}},
	{Term: "莫斯科路亚", Label: "莫斯科路亚", Cat: 17, Subs: []int64{2}},
	{Term: "软饵类", Label: "泡沫 / 软饵", Cat: 17, Subs: []int64{3}},
	{Term: "Crank摇摆", Label: "Crank 摇摆", Cat: 17, Subs: []int64{4}},
	{Term: "Popper波扒", Label: "Popper 波扒", Cat: 17, Subs: []int64{5}},
	{Term: "Pilker铁板", Label: "Pilker 铁板", Cat: 17, Subs: []int64{6}},
	{Term: "Jerk抽停", Label: "Jerk 抽停", Cat: 17, Subs: []int64{7}},
	{Term: "Tube软管", Label: "Tube 软管", Cat: 17, Subs: []int64{8}},
	{Term: "W-Stick软虫", Label: "W-Stick 软虫", Cat: 17, Subs: []int64{9}},
	{Term: "Mouse鼠型", Label: "Mouse 鼠型", Cat: 17, Subs: []int64{10}},
	{Term: "Fly飞蝇", Label: "Fly 飞蝇", Cat: 17, Subs: []int64{11}},
	{Term: "大号软饵", Label: "大号软饵", Cat: 17, Subs: []int64{12}},
	{Term: "Gummi软鳗", Label: "Gummi 软鳗", Cat: 17, Subs: []int64{13}},
	{Term: "Octopus软饵", Label: "Octopus 软饵", Cat: 17, Subs: []int64{14}},
	{Term: "虾型", Label: "虾型", Cat: 17, Subs: []int64{15}},
	{Term: "Flyingworm飞虫", Label: "Flying worm 飞虫", Cat: 17, Subs: []int64{16}},
	{Term: "鱼卵珠", Label: "鱼卵珠", Cat: 17, Subs: []int64{17}},
	{Term: "Jig铅头", Label: "Jig 铅头", Cat: 17, Subs: []int64{18}},
	{Term: "FlipJig铁板", Label: "Flip Jig 铁板", Cat: 17, Subs: []int64{19}},
	{Term: "Vibra震动", Label: "Vibra 震动", Cat: 17, Subs: []int64{20}},

	// 内置规则表 / 旧后台写过的历史词：只保证继续能匹配，不出现在选择清单里。
	{Term: "昆虫幼虫", Label: "虫类（幼虫 / 红虫）", Cat: 27, Subs: []int64{2}, Alias: true},
	{Term: "蠕虫", Label: "蚯蚓类", Cat: 27, Subs: []int64{6}, Alias: true},
	{Term: "蠕虫类", Label: "蚯蚓类", Cat: 27, Subs: []int64{6}, Alias: true},
	{Term: "昆虫", Label: "昆虫类（成虫）", Cat: 27, Subs: []int64{7}, Alias: true},
	{Term: "饵鱼", Label: "活饵 / 饵鱼 + 死饵鱼", Cat: 27, Subs: []int64{4, 15}, Alias: true},
	{Term: "饵鱼类", Label: "活饵 / 饵鱼 + 死饵鱼", Cat: 27, Subs: []int64{4, 15}, Alias: true},
	{Term: "各类饵鱼", Label: "活饵 / 饵鱼 + 死饵鱼", Cat: 27, Subs: []int64{4, 15}, Alias: true},
	{Term: "鱼肉类", Label: "鱼片", Cat: 27, Subs: []int64{14}, IDs: []string{"fish_flesh"}, Alias: true},
	{Term: "亮片", Label: "旋转亮片 + 莫斯科路亚", Cat: 17, Subs: []int64{1, 2}, Alias: true},
	{Term: "亮片类", Label: "旋转亮片 + 莫斯科路亚", Cat: 17, Subs: []int64{1, 2}, Alias: true},
	{Term: "亮片系列饵", Label: "旋转亮片 + 莫斯科路亚", Cat: 17, Subs: []int64{1, 2}, Alias: true},
	{Term: "旋转饵", Label: "旋转亮片", Cat: 17, Subs: []int64{1}, Alias: true},
	{Term: "旋转饵类", Label: "旋转亮片", Cat: 17, Subs: []int64{1}, Alias: true},
	{Term: "旋转亮片路亚", Label: "旋转亮片", Cat: 17, Subs: []int64{1}, Alias: true},
	{Term: "拟真软饵", Label: "泡沫 / 软饵", Cat: 17, Subs: []int64{3}, Alias: true},
}

// baitTypeTermIndex 用 ruleKey 建索引：规则词里的空格、大小写、全半角差异
// 不影响命中（与饵名匹配同一把归一化）。
var baitTypeTermIndex = func() map[string]int {
	m := make(map[string]int, len(baitTypeTerms))
	for i := range baitTypeTerms {
		m[ruleKey(baitTypeTerms[i].Term)] = i
	}
	return m
}()

// baitTypeTermOf 返回该规则词对应的类目（ok=false 表示它只是普通饵名）。
func baitTypeTermOf(key string) (BaitTypeTerm, bool) {
	if i, ok := baitTypeTermIndex[key]; ok {
		return baitTypeTerms[i], true
	}
	return BaitTypeTerm{}, false
}

// matches 判断一款饵是否属于这个词代表的类型。
func (t BaitTypeTerm) matches(b ruleBait) bool {
	if t.Cat != 0 && b.category != t.Cat {
		return false
	}
	if len(t.Subs) == 0 {
		return true
	}
	for _, s := range t.Subs {
		if b.category == 17 {
			if b.lureType == s {
				return true
			}
		} else if b.baitType == s {
			return true
		}
	}
	for _, id := range t.IDs {
		if b.id == id {
			return true
		}
	}
	return false
}

// ExportedBaitTypeTerms 返回整张表（含历史别名），顺序即展示顺序。
// 后台只用 Alias=false 的条目出按钮，别名只用来给旧规则里的词染色。
func ExportedBaitTypeTerms() []BaitTypeTerm {
	out := make([]BaitTypeTerm, 0, len(baitTypeTerms))
	out = append(out, baitTypeTerms...)
	return out
}

// ExportedBaitTypeLabel 返回某（品类, 二级类型）的展示名，与选择清单里的写法一致。
// 后台分类树与规则词共用一份标签，避免「按钮写颗粒、下拉写鱼子/虾类」。
func ExportedBaitTypeLabel(category, sub int64) (string, bool) {
	for _, t := range baitTypeTerms {
		if t.Alias || t.Cat != category || len(t.Subs) == 0 {
			continue
		}
		for _, s := range t.Subs {
			if s == sub {
				return t.Label, true
			}
		}
	}
	return "", false
}

// ExportedBaitCategoryTerm 返回整个大类的规则词（饵料 / 拟饵）。
func ExportedBaitCategoryTerm(category int64) (string, bool) {
	for _, t := range baitTypeTerms {
		if t.Alias || t.Cat != category || len(t.Subs) != 0 {
			continue
		}
		return t.Term, true
	}
	return "", false
}

// ExportedBaitTypeTermOf 返回某（品类, 二级类型）对应的规则词。
func ExportedBaitTypeTermOf(category, sub int64) (string, bool) {
	for _, t := range baitTypeTerms {
		if t.Alias || t.Cat != category || len(t.Subs) == 0 {
			continue
		}
		for _, s := range t.Subs {
			if s == sub {
				return t.Term, true
			}
		}
	}
	return "", false
}

// IsBaitTypeTerm 判断一个规则词是不是大类词（后台把标签染成另一种颜色用）。
func IsBaitTypeTerm(term string) bool {
	_, ok := baitTypeTermIndex[ruleKey(strings.TrimSpace(term))]
	return ok
}
