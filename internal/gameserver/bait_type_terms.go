package gameserver

import "strings"

// 「整类饵」规则词：allow / deny / bottom_only 里可以直接写一个类型名，
// 代表这一类全部饵，不必逐个列饵名。
//
// 例：`吃 颗粒` = 所有 pellet 都算爱吃；自动抽取的池子也就是这一类里的全部型号组，
// 本期开口饵从这一类中抽。deny 写「Pop-up 香煮成饵」= 所有 Pop-up 都不吃。
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
	// Shape 在同一 Sub 内再分形态：只有 baitType=8（香煮成饵）用到，
	// "sink"=下沉（brand 不含 _pp_）/ "popup"=Pop-up（brand 含 _pp_）。
	// 空=不按形态过滤。详见 docs/analysis-bait-category-taxonomy-2026-11-25.md §2.1。
	Shape string   `json:"shape,omitempty"`
	IDs   []string `json:"-"`     // 额外点名的 item_id（规则表历史特例）
	Alias bool     `json:"-"`     // 历史写法：只匹配、不在选择清单里出现
}

// baitTypeTerms 的顺序就是后台按钮的顺序：先全部，再鱼饵，再拟饵。
//
// **所有正式名都是客户端本地化表的原文，不是我们自己起的**。
// 来源：`rf4_x64_Data/resources.assets` 里的多语言资源表，
// 鱼饵键 `kStorageSlotNameBaits*`、拟饵键 `kLureType*`（第 16 列＝简中）。
// 提取方法与逐项对照见 docs/analysis-bait-category-taxonomy-2026-11-25.md §3。
//
// 判定「哪个 baitType 对应哪个键」的依据是静态库实测内容：
//   baitType=4 的 18 款全是 `*_lf` 活鱼（欧鲌/河鲈/拟鲤…）  → kStorageSlotNameBaitsLiveCreature「活的」
//   baitType=15 的 18 款全是 `fb_d_*` 死鱼（死鲭鱼/死鲱鱼…）→ kStorageSlotNameBaitsDeadFish「死鱼」
// 早前把这两个词写反过（活饵类指 4、死饵鱼类指 15 但标签互换），已按数据更正。
var baitTypeTerms = []BaitTypeTerm{
	{Term: "各类饵", Label: "全部（鱼饵 + 拟饵）", Cat: 0},

	{Term: "鱼饵", Label: "鱼饵（全部天然饵）", Cat: 27},

	// 鱼饵 16 类：名称取自 kStorageSlotNameBaits*（简中列）。
	// 曾经的 `虫类`/`蚯蚓类`/`昆虫类` 是代码自造词，游戏里没有这三个概念，
	// 已全部降为 Alias（只参与老规则匹配，不出现在选择清单里）。
	{Term: "自然的", Label: "自然的", Cat: 27, Subs: []int64{1}},
	{Term: "幼虫", Label: "幼虫", Cat: 27, Subs: []int64{2}},
	{Term: "蛙类", Label: "蛙类", Cat: 27, Subs: []int64{3}},
	{Term: "活的", Label: "活的", Cat: 27, Subs: []int64{4}},
	{Term: "蠕虫", Label: "蠕虫", Cat: 27, Subs: []int64{6}},
	{Term: "昆虫", Label: "昆虫", Cat: 27, Subs: []int64{7}},
	// baitType=8 在库里是一个枚举，但游戏把同一口味的两个形态并列成两项菜单：
	// 下沉（356 款）/ Pop-up（222 款），判定依据是 brand 含 `_pp_`，578/578 零例外。
	// 两个词都指向同一个 Sub，靠 Shape 再分；只写「香煮成饵」= 两种形态都要。
	{Term: "香煮成饵", Label: "香煮成饵（下沉 + Pop-up）", Cat: 27, Subs: []int64{8}},
	{Term: "下沉香煮成饵", Label: "下沉香煮成饵", Cat: 27, Subs: []int64{8}, Shape: "sink"},
	{Term: "Pop-up 香煮成饵", Label: "Pop-up 香煮成饵", Cat: 27, Subs: []int64{8}, Shape: "popup"},
	{Term: "颗粒", Label: "颗粒", Cat: 27, Subs: []int64{9}},
	{Term: "人造玉米", Label: "人造玉米", Cat: 27, Subs: []int64{10}},
	{Term: "坚果", Label: "坚果", Cat: 27, Subs: []int64{11}},
	{Term: "小型甲壳类", Label: "小型甲壳类", Cat: 27, Subs: []int64{12}},
	{Term: "粥和面团", Label: "粥和面团", Cat: 27, Subs: []int64{13}},
	{Term: "鱼片", Label: "鱼片", Cat: 27, Subs: []int64{14}},
	{Term: "死鱼", Label: "死鱼", Cat: 27, Subs: []int64{15}},
	{Term: "海饵", Label: "海饵", Cat: 27, Subs: []int64{16}},
	{Term: "ZIG钓组泡沫", Label: "ZIG 钓组泡沫", Cat: 27, Subs: []int64{17}},

	{Term: "拟饵", Label: "拟饵（全部路亚）", Cat: 17},

	// 拟饵 20 类：名称取自 kLureType*（简中列），与 lureType 数值一一对应。
	// 这解决了「菜单 19 项 vs lureType 20 种」的疑问：
	//   菜单「旋转亮片路亚」= kLureTypeSpinner      = lureType 1
	//   菜单「旋转饵」    = kLureTypeSpinnerBait    = lureType 20
	// 两者是不同枚举，不是同一类的两种叫法。
	{Term: "旋转亮片路亚", Label: "旋转亮片路亚", Cat: 17, Subs: []int64{1}},
	{Term: "勺子路亚", Label: "勺子路亚", Cat: 17, Subs: []int64{2}},
	{Term: "软饵", Label: "软饵", Cat: 17, Subs: []int64{3}},
	{Term: "手工鱼形饵", Label: "手工鱼形饵", Cat: 17, Subs: []int64{4}},
	{Term: "表水摇摆鱼形诱饵", Label: "表水摇摆鱼形诱饵", Cat: 17, Subs: []int64{5}},
	{Term: "柳叶亮片", Label: "柳叶亮片", Cat: 17, Subs: []int64{6}},
	{Term: "抽停饵", Label: "抽停饵", Cat: 17, Subs: []int64{7}},
	{Term: "硅胶管", Label: "硅胶管", Cat: 17, Subs: []int64{8}},
	{Term: "维基虫", Label: "维基虫", Cat: 17, Subs: []int64{9}},
	{Term: "假啮齿动物", Label: "假啮齿动物", Cat: 17, Subs: []int64{10}},
	{Term: "飞蝇", Label: "飞蝇", Cat: 17, Subs: []int64{11}},
	{Term: "巨大软饵", Label: "巨大软饵", Cat: 17, Subs: []int64{12}},
	{Term: "Gummi Makk", Label: "Gummi Makk", Cat: 17, Subs: []int64{13}},
	{Term: "八爪鱼", Label: "八爪鱼", Cat: 17, Subs: []int64{14}},
	{Term: "虾", Label: "虾", Cat: 17, Subs: []int64{15}},
	{Term: "硅树脂海虫", Label: "硅树脂海虫", Cat: 17, Subs: []int64{16}},
	{Term: "吸引成分", Label: "吸引成分", Cat: 17, Subs: []int64{17}},
	{Term: "死鱼铅头钩", Label: "死鱼铅头钩", Cat: 17, Subs: []int64{18}},
	{Term: "带毛汲钩", Label: "带毛汲钩", Cat: 17, Subs: []int64{19}},
	{Term: "旋转饵", Label: "旋转饵", Cat: 17, Subs: []int64{20}},

	// 内置规则表 / 旧后台写过的历史词：只保证继续能匹配，不出现在选择清单里。
	// 2026-11-25 改用客户端原文后，被替换下来的旧正式名与自己的过渡名全落到这里。
	// 大类级的旧叫法：游戏菜单里没有「饵料」这个词，鱼饵侧的正式名是「鱼饵」，
	// 但后台覆盖数据与早期规则确实写过「饵料」，留作别名兜底。
	{Term: "饵料", Label: "鱼饵（旧大类名）", Cat: 27, Alias: true},
	{Term: "天然饵", Label: "鱼饵（旧大类名）", Cat: 27, Alias: true},
	{Term: "路亚", Label: "拟饵（旧大类名）", Cat: 17, Alias: true},
	{Term: "沉底饵", Label: "自然的（旧名）", Cat: 27, Subs: []int64{1}, Alias: true},
	{Term: "虫类", Label: "幼虫（旧名）", Cat: 27, Subs: []int64{2}, Alias: true},
	{Term: "昆虫幼虫", Label: "幼虫（旧名）", Cat: 27, Subs: []int64{2}, Alias: true},
	{Term: "活饵类", Label: "活的（旧名）", Cat: 27, Subs: []int64{4}, Alias: true},
	{Term: "蚯蚓类", Label: "蠕虫（旧名）", Cat: 27, Subs: []int64{6}, Alias: true},
	{Term: "蠕虫类", Label: "蠕虫（旧名）", Cat: 27, Subs: []int64{6}, Alias: true},
	{Term: "昆虫类", Label: "昆虫（旧名）", Cat: 27, Subs: []int64{7}, Alias: true},
	// 注意：不要再加「Pop-up香煮成饵」这种无空格写法当别名。ruleKey 会去掉全部空白，
	// 它与正式名 `Pop-up 香煮成饵` 归一化后是同一个键，后写入的别名会顶掉正式名的索引项，
	// 结果正式名被当成 Alias 从后台选择清单里消失。无空格写法照样能命中，不需要单独登记。
	{Term: "浮水饵", Label: "Pop-up 香煮成饵（旧名）", Cat: 27, Subs: []int64{8}, Shape: "popup", Alias: true},
	{Term: "玉米浮", Label: "人造玉米（旧名）", Cat: 27, Subs: []int64{10}, Alias: true},
	{Term: "钩虾类", Label: "小型甲壳类（旧名）", Cat: 27, Subs: []int64{12}, Alias: true},
	{Term: "面团类", Label: "粥和面团（旧名）", Cat: 27, Subs: []int64{13}, Alias: true},
	{Term: "鱼片类", Label: "鱼片（旧名）", Cat: 27, Subs: []int64{14}, Alias: true},
	{Term: "死饵鱼类", Label: "死鱼（旧名）", Cat: 27, Subs: []int64{15}, Alias: true},
	{Term: "海钓饵", Label: "海饵（旧名）", Cat: 27, Subs: []int64{16}, Alias: true},
	{Term: "Zig浮水球", Label: "ZIG 钓组泡沫（旧名）", Cat: 27, Subs: []int64{17}, Alias: true},
	// 历史合并写法：旧规则表用「饵鱼类」一次覆盖活鱼 + 死鱼两种鱼形饵。
	// 现在两者各有正式名（活的 / 死鱼），合并写法只作为别名保留。
	// 注意 `饵鱼` 本身不能再用作合并词：客户端把 LiveFish 也译作「饵鱼」，
	// 而库里 baitType=15（死鱼）才是 `fb_d_*` 那批，见下方说明。
	{Term: "饵鱼类", Label: "活的 + 死鱼（旧合并写法）", Cat: 27, Subs: []int64{4, 15}, Alias: true},
	{Term: "各类饵鱼", Label: "活的 + 死鱼（旧合并写法）", Cat: 27, Subs: []int64{4, 15}, Alias: true},
	{Term: "饵鱼", Label: "活的 + 死鱼（旧合并写法，规则表在用）", Cat: 27, Subs: []int64{4, 15}, Alias: true},
	{Term: "鱼肉类", Label: "鱼片（旧名）", Cat: 27, Subs: []int64{14}, IDs: []string{"fish_flesh"}, Alias: true},

	// 拟饵旧名：早期后台与规则表用过，全部只参与匹配。
	{Term: "旋转亮片", Label: "旋转亮片路亚（旧名）", Cat: 17, Subs: []int64{1}, Alias: true},
	{Term: "莫斯科路亚", Label: "勺子路亚（旧名）", Cat: 17, Subs: []int64{2}, Alias: true},
	{Term: "亮片", Label: "旋转亮片路亚 + 勺子路亚", Cat: 17, Subs: []int64{1, 2}, Alias: true},
	{Term: "亮片类", Label: "旋转亮片路亚 + 勺子路亚", Cat: 17, Subs: []int64{1, 2}, Alias: true},
	{Term: "亮片系列饵", Label: "旋转亮片路亚 + 勺子路亚", Cat: 17, Subs: []int64{1, 2}, Alias: true},
	{Term: "旋转饵类", Label: "旋转亮片路亚（旧名）", Cat: 17, Subs: []int64{1}, Alias: true},
	{Term: "软饵类", Label: "软饵（旧名）", Cat: 17, Subs: []int64{3}, Alias: true},
	{Term: "拟真软饵", Label: "软饵（旧名）", Cat: 17, Subs: []int64{3}, Alias: true},
	{Term: "Crank摇摆", Label: "手工鱼形饵（旧名）", Cat: 17, Subs: []int64{4}, Alias: true},
	{Term: "Popper波扒", Label: "表水摇摆鱼形诱饵（旧名）", Cat: 17, Subs: []int64{5}, Alias: true},
	{Term: "Pilker铁板", Label: "柳叶亮片（旧名）", Cat: 17, Subs: []int64{6}, Alias: true},
	{Term: "Jerk抽停", Label: "抽停饵（旧名）", Cat: 17, Subs: []int64{7}, Alias: true},
	{Term: "Tube软管", Label: "硅胶管（旧名）", Cat: 17, Subs: []int64{8}, Alias: true},
	{Term: "W-Stick软虫", Label: "维基虫（旧名）", Cat: 17, Subs: []int64{9}, Alias: true},
	{Term: "Mouse鼠型", Label: "假啮齿动物（旧名）", Cat: 17, Subs: []int64{10}, Alias: true},
	{Term: "Fly飞蝇", Label: "飞蝇（旧名）", Cat: 17, Subs: []int64{11}, Alias: true},
	{Term: "大号软饵", Label: "巨大软饵（旧名）", Cat: 17, Subs: []int64{12}, Alias: true},
	{Term: "Gummi软鳗", Label: "Gummi Makk（旧名）", Cat: 17, Subs: []int64{13}, Alias: true},
	{Term: "Octopus软饵", Label: "八爪鱼（旧名）", Cat: 17, Subs: []int64{14}, Alias: true},
	{Term: "虾型", Label: "虾（旧名）", Cat: 17, Subs: []int64{15}, Alias: true},
	{Term: "Flyingworm飞虫", Label: "硅树脂海虫（旧名）", Cat: 17, Subs: []int64{16}, Alias: true},
	{Term: "硅胶海虫", Label: "硅树脂海虫（菜单旧译）", Cat: 17, Subs: []int64{16}, Alias: true},
	{Term: "鱼卵珠", Label: "吸引成分（旧名，实际是珠子/亮片挂件）", Cat: 17, Subs: []int64{17}, Alias: true},
	{Term: "Jig铅头", Label: "死鱼铅头钩（旧名）", Cat: 17, Subs: []int64{18}, Alias: true},
	{Term: "FlipJig铁板", Label: "带毛汲钩（旧名）", Cat: 17, Subs: []int64{19}, Alias: true},
	// `带毛汲钩(jig)` 是早期自造的写法（括号是代码加的，游戏原文只有「带毛汲钩」）。
	// **不能让它继续当正式名**：类别词会写进 fishing_bait_rules.txt，而解析器把
	// 第一个 `(` 之后的全部内容当作「不吃 / 底钓只吃」的限定段，
	// 于是 `带毛汲钩(jig)@Finesse Micro Jig 1/8 oz` 会把后面的条目整段截断
	// （2026-11-25 实测：14 条鱼的 27 款饵静默丢失）。
	// 这里保留为别名，只为兼容历史后台数据里可能存过的旧写法。
	{Term: "带毛汲钩(jig)", Label: "带毛汲钩（旧名）", Cat: 17, Subs: []int64{19}, Alias: true},
	{Term: "Vibra震动", Label: "旋转饵（旧名）", Cat: 17, Subs: []int64{20}, Alias: true},
}

// baitTypeTermIndex 用 ruleKey 建索引：规则词里的空格、大小写、全半角差异
// 不影响命中（与饵名匹配同一把归一化）。
//
// **索引里同一 key 只能有一个词**：ruleKey 会丢掉空格与连字符，
// 因此别名与正式名一旦归一化后同键，后写入的会静默顶掉正式名。
// TestBaitTypeTermsNoKeyCollision 守着这条。
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
	if t.Shape != "" {
		// 形态词（下沉 / Pop-up）只在 baitType=8 上有意义，其它类型直接不匹配，
		// 否则「Pop-up 香煮成饵」会命中所有 8 号以外的饵。
		if b.category != 27 || b.baitType != 8 {
			return false
		}
		if boilIsPopUp(b.brand) != (t.Shape == "popup") {
			return false
		}
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

// boilIsPopUp 判断一款香煮成饵是不是 Pop-up 形态。
//
// 静态库里 baitType=8 的 578 款同时包含下沉与浮水两种产品，枚举上没有区分，
// 唯一可靠的标记在 brand：Pop-up 产品的 brand 带 `_pp_` 段。
// 实测 578 款按此判定与名称里的 PopUp / Pop-Ups / Pop Ups 标记 100% 吻合（零例外），
// 对照样本见 docs/analysis-bait-category-taxonomy-2026-11-25.md §2.1。
func boilIsPopUp(brand string) bool {
	if brand == "" {
		return false
	}
	b := strings.ToLower(brand)
	return strings.Contains(b, "_pp_") || strings.Contains(b, "_pp") && strings.HasSuffix(b, "_pp")
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
//
// 同一 Sub 可能对应多个词（baitType=8 有「香煮成饵 / 下沉香煮成饵 / Pop-up 香煮成饵」），
// 这里固定返回**不带形态词**的那个，作为分类树的默认标签；形态子项由
// ExportedBaitShapeTerms 单独提供。
func ExportedBaitTypeLabel(category, sub int64) (string, bool) {
	for _, t := range baitTypeTerms {
		if t.Alias || t.Shape != "" || t.Cat != category || len(t.Subs) == 0 {
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

// ExportedBaitCategoryTerm 返回整个大类的规则词（鱼饵 / 拟饵）。
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
// 与 ExportedBaitTypeLabel 同口径：跳过带形态词的条目，返回覆盖整类的那个词。
func ExportedBaitTypeTermOf(category, sub int64) (string, bool) {
	for _, t := range baitTypeTerms {
		if t.Alias || t.Shape != "" || t.Cat != category || len(t.Subs) == 0 {
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

// ExportedBaitShapeTerms 返回某（品类, 二级类型）下的形态子词。
// 目前只有 baitType=8（香煮成饵）有两项：下沉 / Pop-up。
// 库里这两者是同一个枚举，只能靠 brand 的 `_pp_` 标记区分（578/578 零例外），
// 因此必须作为独立的可选词暴露出去，否则后台无法只选其中一种。
func ExportedBaitShapeTerms(category, sub int64) []BaitTypeTerm {
	var out []BaitTypeTerm
	for _, t := range baitTypeTerms {
		if t.Alias || t.Shape == "" || t.Cat != category {
			continue
		}
		for _, s := range t.Subs {
			if s == sub {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// ExportedBoilIsPopUp 判断一款香煮成饵是不是 Pop-up 形态（后台画层级用）。
// 与运行时判鱼同一个函数，避免「后台说这是 Pop-up、判鱼当成下沉」。
func ExportedBoilIsPopUp(brand string) bool { return boilIsPopUp(brand) }

// IsBaitTypeTerm 判断一个规则词是不是大类词（后台把标签染成另一种颜色用）。
func IsBaitTypeTerm(term string) bool {
	_, ok := baitTypeTermIndex[ruleKey(strings.TrimSpace(term))]
	return ok
}