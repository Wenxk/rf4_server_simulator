package gameserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

//go:embed fishing_bait_rules.txt
var fishBaitRuleText string

type fishBaitRule struct {
	allow, deny []string
	bottomOnly  []string
	condition   string
	// 攻略前置条件里可核对的部分：海图区号列表（chartAny 表示只要求
	// “在海图上，区域不限”），以及顺水漂流、jig 阶梯、饵在水中运动三个动作要求。
	chartZones []int
	chartAny   bool
	needDrift  bool
	needJig    bool
	needMotion bool
	// needBoat 表示攻略要求“使用有效船票并已上船”。
	needBoat bool
	// jigOrMotion 用于“出现 jig 阶梯，或采用路亚钓法且已上船、饵在水中运动”
	// 这类二选一前置条件，不能拆成同时要求。船是二选一里的一条路，不单独封鱼。
	jigOrMotion bool
}

var fishBaitRules = parseFishBaitRules(fishBaitRuleText)

// ruleVariantRune 把异体字归一到规则表使用的写法。客户端中文名与规则表
// 之间长期存在「鮟鱇 / 𩽾𩾌」这类同物异写，只差一个码位就会让整条鱼
// 在严格模式下失去规则（表现为该鱼吃什么都被拒）。
var ruleVariantRune = map[rune]rune{
	0x9b9f: 0x29f7e, // 鮟 -> 𩽾
	0x9c47: 0x29f8c, // 鱇 -> 𩾌
	0x9c79: 0x9c79,  // 鱹 占位（保留自身）
}

// lookupFishBaitRule 按鱼名找规则：先精确命中，再退一步去掉结尾的「鱼」
// 再试（客户端「大西洋鲑」vs 规则「大西洋鲑鱼」、数据库「泥鳅鱼」vs
// 规则「泥鳅」）。两个方向都只试一次，不做模糊匹配，避免误配同科鱼。
func lookupFishBaitRule(fishZh string) (fishBaitRule, bool) {
	if r, ok := fishBaitRules[ruleName(fishZh)]; ok {
		return r, true
	}
	key := ruleName(fishZh)
	if strings.HasSuffix(key, "鱼") {
		if r, ok := fishBaitRules[strings.TrimSuffix(key, "鱼")]; ok {
			return r, true
		}
	}
	if r, ok := fishBaitRules[key+"鱼"]; ok {
		return r, true
	}
	return fishBaitRule{}, false
}

const maxBaitPreference = 1.5 * 1.15

func ruleName(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\ufeff', '(', ')', '（', '）':
			return -1
		case '鯡':
			return '鲱'
		case '鮭':
			return '鲑'
		case '鰻':
			return '鳗'
		case '鳌':
			return '螯'
		}
		if v, ok := ruleVariantRune[r]; ok {
			return v
		}
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// ruleKey 是 matchKey 的“去排版”版本：连字符、下划线、斜杠、逗号、点号、
// NBSP 与全角空格全部丢弃，全角数字/字母转半角。客户端里同一款饵的写法
// 并不统一（`Natural Squid - 23 - 01`、`中性的 PopUp\xa010`、
// `旋转饵１号\u3000004`），只靠 ruleName 永远匹配不到。
func ruleKey(s string) string {
	var b strings.Builder
	for _, r := range ruleName(s) {
		switch {
		case r == '\u00a0' || r == '\u3000' || r == '-' || r == '_' || r == '/' || r == ',' || r == '.' || r == '*' || r == ':':
			continue
		case r >= '０' && r <= '９':
			b.WriteRune(r - '０' + '0')
			continue
		case r >= 'Ａ' && r <= 'Ｚ':
			b.WriteRune(r - 'Ａ' + 'a')
			continue
		case r >= 'ａ' && r <= 'ｚ':
			b.WriteRune(r - 'ａ' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ruleBaitCanon 把规则文本里的用词映射到客户端实际名称前缀（去排版后比较）。
// 只放“同一个东西被两种叫法”的情况，不做语义猜测。
var ruleBaitCanon = map[string]string{
	"中立":        "中性",
	"中立类":       "中性",
	"鱼肉":        "鱼块",
	"鱼肉块":       "鱼块",
	"面包屑":       "湿面包",
	"河鲈饵":       "河鲈",
	"河鲈饵鱼":      "河鲈",
	"梭鲈饵":       "梭鲈",
	"gummimakk": "supergummimakk",
}

// keyDigitBoundaryOK 防止「型号 1」误配「型号 1000」。以数字结尾的规则词，
// 若在名称里紧接数字只接受三位型号（001/004/…）这种家族写法。
func keyDigitBoundaryOK(term, name string) bool {
	if len(term) == 0 || len(name) <= len(term) {
		return true
	}
	last := term[len(term)-1]
	if last < '0' || last > '9' {
		return true
	}
	rest := name[len(term):]
	if rest[0] < '0' || rest[0] > '9' {
		return true
	}
	return isZeroPaddedIndex(rest)
}

// isZeroPaddedIndex 判断「规则词之后剩下的那截」是不是规格编号。
//
// 客户端把同一型号的不同规格写成补零的三位编号：`Baby Craw 3.5 001`、
// `Salmon T1 Crank 10 013`。写了型号名 `Baby Craw 3.5` 就应该覆盖它全部规格，
// 因此「以 0 开头的纯数字」视为规格号，允许前缀命中。
//
// 反向要求：`Hunter 1` 后面接 `000`（即 `Hunter 1000`）不是规格号，那是另一个型号。
// 数据里不存在全零编号（4123 款实测 0 例），所以「至少含一个非零数字」即判是：
// `001`/`012`/`010` 都算规格号，`000`/`00` 不算。
//
// 注意不要写成「次位必须是 1-9」——那会把 `001`（次位是 0）也否掉，
// 而 `001` 恰恰是最常见的规格号。这个错误曾让 37 个以数字结尾的型号组零命中。
func isZeroPaddedIndex(rest string) bool {
	if len(rest) < 2 {
		return false
	}
	if rest[0] != '0' {
		return false
	}
	// 全部为 0 时是产品名的一部分（`Hunter 1000` 的 `000`），不是规格号。
	for i := 1; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false // 混进非数字，交给调用方的其它分支
		}
		if rest[i] != '0' {
			return true
		}
	}
	return false
}

// onlyDigits 判断整段都是数字（已由 ruleKey 去掉排版字符）。
func onlyDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// BaitRuleInfo 一条"鱼吃饵"规则的可读形态（供管理后台展示）。
type BaitRuleInfo struct {
	Allow      []string `json:"allow"`
	Deny       []string `json:"deny"`
	BottomOnly []string `json:"bottom_only"`
	Condition  string   `json:"condition"`
}

// ExportedBaitRuleKey 把鱼种中文名归一成规则表/覆盖表的查找键。
// 后台要按「与判鱼同一把键」去匹配 fish_bait_override，否则「泥鳅鱼 / 泥鳅」
// 这类写法差异会让后台算出的自动轮换名单和游戏里不一致。
func ExportedBaitRuleKey(fishZh string) string { return ruleName(fishZh) }

// ExportedBaitRules 按中文鱼名查规则表，供管理后台只读展示。
// 返回 ok=false 表示该鱼在规则表里没有条目（严格模式下只能靠饵效证据放行）。
func ExportedBaitRules(fishZh string) (BaitRuleInfo, bool) {
	r, ok := lookupFishBaitRule(fishZh)
	if !ok {
		return BaitRuleInfo{}, false
	}
	return BaitRuleInfo{Allow: r.allow, Deny: r.deny, BottomOnly: r.bottomOnly, Condition: r.condition}, true
}

// BaitOutcome 是后台「实际能不能咬」的判定结果，四档与运行时判鱼一一对应。
// 纯展示用途：后台拿到它就能把「这款饵现在到底会不会上鱼」直接画出来，
// 不必让玩家去理解 strict 内部分档。
type BaitOutcome struct {
	BaitID   string  `json:"bait_id"`
	Verdict  string  `json:"verdict"`  // bite=会咬 / fallback=仅保底 / blocked=被规则挡掉
	Strength float64 `json:"strength"` // 1=首选, 0.6=可用, 0.1=保底, 0=不咬
	Reason   string  `json:"reason"`   // 中文说明，直接展示
}

// ExportedBaitOutcomes 给出一批饵料在指定鱼上的实际判定。
// 返回顺序与入参一致；minShare 与 OfficialPc 仅为兼容旧调用保留，
// 官服占比不参与判定（2026-11-19 用户口径）。
func ExportedBaitOutcomes(fishZh string, baits []struct {
	ID         string
	NameZh     string
	Category   int64
	LureType   int64
	BaitType   int64
	OfficialPc float64
	Tier       int
}, minShare float64) []BaitOutcome {
	r, hasRule := lookupFishBaitRule(fishZh)
	out := make([]BaitOutcome, 0, len(baits))
	for _, b := range baits {
		rb := ruleBait{id: b.ID, name: b.NameZh, category: b.Category, lureType: b.LureType, baitType: b.BaitType}
		if hasRule {
			if baitBlockedByRule(r, fishRollContext{}, rb) {
				out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "blocked", Strength: 0, Reason: "规则标明不吃"})
				continue
			}
			if baitAllowedByRule(r, rb) {
				out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "bite", Strength: 1, Reason: "规则允许"})
				continue
			}
			out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "blocked", Strength: 0, Reason: "不在本期 allow"})
			continue
		}
		if b.Tier >= 3 {
			out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "bite", Strength: 0.6, Reason: "玩家实测常用"})
			continue
		}
		if b.Tier >= 1 {
			out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "fallback", Strength: 0.1, Reason: "实证在榜，仅低权重"})
			continue
		}
		out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "blocked", Strength: 0, Reason: "无规则且无实证"})
	}
	return out
}

// ExportedBaitVerdict 单款饵的判定，给后台画「会咬/保底/不吃」三态。
// 与 filterFishBaitSet 同源：有规则时只认 allow（deny 优先），不再看 companion
// 越级或食性硬门；无规则时才按 companion 证据兜底。
func ExportedBaitVerdict(allow, deny, bottom []string, baitID, baitName string, category, lureType, baitType int64, tier int) (string, float64, string) {
	r := fishBaitRule{allow: cleanRuleTerms(allow), deny: cleanRuleTerms(deny), bottomOnly: cleanRuleTerms(bottom)}
	rb := ruleBait{id: baitID, name: baitName, category: category, lureType: lureType, baitType: baitType}
	hasRule := len(r.allow) > 0
	if hasRule {
		if baitBlockedByRule(r, fishRollContext{}, rb) {
			return "blocked", 0, "规则标明不吃"
		}
		if baitAllowedByRule(r, rb) {
			return "bite", 1, "规则允许"
		}
		return "blocked", 0, "不在本期 allow"
	}
	if tier >= 3 {
		return "bite", 0.6, "玩家实测常用"
	}
	if tier >= 1 {
		return "fallback", 0.10, "实证在榜，仅低权重"
	}
	return "blocked", 0, "无规则且无实证"
}

// ExportedBaitRuleCount 规则表条目数（前端展示用）。
func ExportedBaitRuleCount() int { return len(fishBaitRules) }

// deriveConditionFlags 从规则文本解析出判鱼要核对的硬条件：
// 海图区号、顺水漂流、jig 阶梯、饵在水中运动、已上船。
// 内置规则与后台覆盖共用同一套解析，避免两条路径判定不一致。
func (r *fishBaitRule) deriveConditionFlags() {
	r.chartZones, r.chartAny = fishChartCondition(r.condition)
	r.needDrift = strings.Contains(r.condition, "顺水漂流")
	needJig := strings.Contains(r.condition, "jig阶梯")
	needMotion := strings.Contains(r.condition, "饵在水中运动")
	r.needJig, r.needMotion, r.needBoat, r.jigOrMotion = false, false, false, false
	if needJig && needMotion && strings.Contains(r.condition, "或") {
		r.jigOrMotion = true
		return
	}
	r.needJig, r.needMotion = needJig, needMotion
	r.needBoat = strings.Contains(r.condition, "已上船")
}

// splitBaitTerms 按 "/" 切分饵名词表，支持用 "\/" 转义饵名里的真实斜杠。
//
// 为什么需要转义：部分饵的官方名自带斜杠——`Finesse Micro Jig 1/8 oz 002`、
// `Brilliance Colorado 1/4 oz 001`、`Super GummiMakk 8/0 - 03`。这些名字若原样
// 写进规则表，会被当成两个词（`Finesse Micro Jig 1` + `8 oz 002`），拆完以后
// 两个碎片都匹配不上完整饵名（前者的 ruleKey 以数字 1 结尾，按
// keyDigitBoundaryOK 只认三位型号；后者根本不是前缀），于是"吃"变成"不吃"。
// 历史上规则表靠"恰好第一片是前缀"蒙对过几次，但那是巧合，换个型号就失效——
// 2026-11-25 合并后台覆盖时就踩了：363 万组判定里有 40 组因此翻转。
//
// 所以规则表支持写 `1\/8 oz`，解析时还原成 `1/8 oz`，与覆盖表的 JSON 口径等价。
func splitBaitTerms(s string) []string {
	if !strings.Contains(s, `\/`) {
		return strings.Split(s, "/")
	}
	parts := strings.Split(strings.ReplaceAll(s, `\/`, "\x00"), "/")
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], "\x00", "/")
	}
	return parts
}

// joinBaitTerms 是 splitBaitTerms 的逆运算，写规则表时用。
func joinBaitTerms(terms []string) string {
	esc := make([]string, len(terms))
	for i, t := range terms {
		esc[i] = strings.ReplaceAll(t, "/", `\/`)
	}
	return strings.Join(esc, "/")
}

// baitScopeSep 是「类别限定词」与「饵名/型号名」之间的分隔符，写法 `颗粒@鱼子`。
//
// 为什么必须有这个机制：型号名会跨类别重名。实测 4123 款饵里有 25 个型号名
// 横跨 ≥2 个二级类别——`鱼子` 在 自然的(1)/香煮成饵(4)/颗粒(3) 三处都有，
// `香子兰` 更是横跨 香煮成饵(6)/颗粒(4)/人造玉米(1)。只写 `鱼子` 会命中全部 14 款，
// 无法表达「只要香煮成饵的鱼子」。后台层级选择器点得再细，写进规则的词也表达不了。
//
// 为什么选 `@`：实测 4123 个饵的官方名与全部型号名里 `@` 出现 0 次（`>`/`｜`/`·`
// 同样 0 次，但 `@` 在中文输入下最顺手，且不会与「系列饵」后缀等现有语法混淆）。
// 同时 `ruleKey` 不去掉 `@`，所以限定词不会被归一化吃掉。
const baitScopeSep = "@"

// splitBaitScope 拆 `限定词@饵名`：ok=false 表示这只是一个普通饵名。
//
// 判定必须双向都成立才认作限定：
//  1. `@` 左边能解析成一个类别词（baitTypeTerms，如「香煮成饵」「颗粒」「拟饵」）；
//  2. `@` 右边非空。
//
// 只判「含 @」是不够的：饵名里原则上可能出现 `@`（现在是 0 款，但以后可能变），
// 那样会被误拆成「限定词不存在 → 匹配失败 → 原本能吃的饵变不吃」。
func splitBaitScope(term string) (scope BaitTypeTerm, name string, ok bool) {
	i := strings.Index(term, baitScopeSep)
	if i <= 0 || i >= len(term)-1 {
		return BaitTypeTerm{}, "", false
	}
	left := strings.TrimSpace(term[:i])
	name = strings.TrimSpace(term[i+len(baitScopeSep):])
	if left == "" || name == "" {
		return BaitTypeTerm{}, "", false
	}
	// 限定词本身必须是已知类别词，否则不当限定（避免误拆带 @ 的真实饵名）。
	if cat, found := baitTypeTermOf(ruleKey(left)); found {
		return cat, name, true
	}
	// 也允许用展示名（Label）当限定词：后台按钮显示的是 Label，
	// 两者在少数条目上不同（如「香煮成饵（下沉 + Pop-up）」）。
	for _, t := range baitTypeTerms {
		if t.Alias {
			continue
		}
		if ruleKey(t.Label) == ruleKey(left) {
			return t, name, true
		}
	}
	return BaitTypeTerm{}, "", false
}

// JoinBaitScope 拼出类别限定写法，供后台选择器生成规则词。
// 例：JoinBaitScope("香煮成饵", "鱼子") == "香煮成饵@鱼子"。
func JoinBaitScope(scopeTerm, name string) string {
	scopeTerm, name = strings.TrimSpace(scopeTerm), strings.TrimSpace(name)
	if scopeTerm == "" || name == "" {
		return name
	}
	return scopeTerm + baitScopeSep + name
}

// SplitBaitScope 是 JoinBaitScope 的逆，供后台把已有规则词还原成「限定词 + 名字」，
// 这样界面上能正确显示「这个标签是限定过的」，并在高亮时区分同名项。
func SplitBaitScope(term string) (scopeTerm, name string, ok bool) {
	cat, n, found := splitBaitScope(term)
	if !found {
		return "", strings.TrimSpace(term), false
	}
	return cat.Term, n, true
}

func parseFishBaitRules(raw string) map[string]fishBaitRule {
	out := map[string]fishBaitRule{}
	for _, line := range strings.Split(raw, "\n") {
		name, tail, ok := strings.Cut(line, "  吃 ")
		if !ok {
			continue
		}
		bait, condition, ok := strings.Cut(tail, "  前置条件 ")
		if !ok {
			continue
		}
		bait = strings.NewReplacer("（", "(", "）", ")").Replace(bait)
		allowed, extra, _ := strings.Cut(bait, "(")
		r := fishBaitRule{allow: splitBaitTerms(strings.TrimSpace(allowed)), condition: strings.TrimSpace(condition)}
		r.deriveConditionFlags()
		if _, denied, ok := strings.Cut(extra, "不吃"); ok {
			denied = strings.FieldsFunc(denied, func(c rune) bool { return c == ')' || c == '；' || c == ';' })[0]
			r.deny = splitBaitTerms(denied)
		}
		if _, only, ok := strings.Cut(extra, "底钓只吃"); ok {
			only = strings.TrimRight(only, ") \r\t")
			r.bottomOnly = splitBaitTerms(only)
		}
		out[ruleName(name)] = r
	}
	return out
}

type ruleBait struct {
	id, name                     string
	category, lureType, baitType int64
	// brand 是产品系列键（`zmn_spiker2` / `hvz_boils_pp_van` 这种）。
	// 唯一用途是区分香煮成饵的下沉 / Pop-up 两种形态：库里 baitType=8
	// 把两者记成同一个枚举，只有 brand 里带 `_pp_` 能分辨（578/578 零例外）。
	brand string
}

// baitNameAliases 是「同一个饵在攻略里有多种叫法」的别名表。
// 放在包级而不是每次 matches 里新建：判鱼每次要跑 鱼×饵×allow词 量级的匹配，
// 逐次分配这个 map 是纯浪费（2026-11-19 跑全量覆盖统计时实测卡住 4 分钟）。
var baitNameAliases = map[string][]string{
	"muckworm":    {"红蚯蚓"},
	"worm_sur":    {"粪便蠕虫", "苏尔斯基粪蚯蚓"},
	"worm_volh":   {"粪便蠕虫", "伏尔霍夫斯基粪蚯蚓"},
	"worm_asurv":  {"粪便蠕虫", "阿斯特拉罕粪蚯蚓"},
	"dough_sweet": {"甜的面团"},
	"caddisfly":   {"毛翅目幼虫"},
	"baitfrog":    {"青蛙"},
	"fish_flesh":  {"鱼肉块"},
	// 客户端只有一件鱼肉类制品（fish_pieces / 鱼块，baitType=1 杂类），
	// 攻略文本却分写"鱼肉/鱼肉块/鱼片"——不归一的话，像江鳕、鳗鱼、葛氏鲈塘鳢
	// 这些明写"吃鱼块/鱼肉"的鱼反而认不出鱼块。
	"fish_pieces": {"鱼肉", "鱼肉块", "鱼肉类", "鱼片", "鱼块"},
	"bread":       {"面包屑", "湿面包"},
	"perch_lf":    {"河鲈饵", "河鲈饵鱼", "河鲈"},
}

// matchTerms 是预归一化的规则词表。构建池子要对 4123 款饵逐一匹配同一条
// 规则，逐次重算 ruleName/ruleKey 会把同一批词归一化上万遍（实测占九成开销）。
// 词表按规则归一化一次，饵自身归一化一次，之后全是纯字符串比较。
//
// termIdx 把「大类词」预先解析成 baitTypeTerms 下标（-1=普通饵名）：
// 批量匹配时直接按枚举比较，不必每款饵再查一次哈希表。
//
// scopeIdx / nameRaw / nameT / nameKT 承载「类别限定词」`颗粒@鱼子`：
// 限定词本身也要匹配类别（否则 `颗粒@鱼子` 会命中所有叫鱼子的饵），
// 所以拆成几段——scopeIdx 是限定词下标（-1=该词没有限定），
// nameRaw 是拆出来的饵名原文（matchOne 要用它跟型号名做精确比较），
// nameT/nameKT 是它的两种归一化。
type matchTerms struct {
	raw      []string
	t        []string
	kt       []string
	termIdx  []int32
	scopeIdx []int32 // 限定词在 baitTypeTerms 的下标；-1 = 无限定
	nameRaw  []string
	nameT    []string
	nameKT   []string
}

func newMatchTerms(terms []string) matchTerms {
	m := matchTerms{
		raw:      terms,
		t:        make([]string, len(terms)),
		kt:       make([]string, len(terms)),
		termIdx:  make([]int32, len(terms)),
		scopeIdx: make([]int32, len(terms)),
		nameRaw:  make([]string, len(terms)),
		nameT:    make([]string, len(terms)),
		nameKT:   make([]string, len(terms)),
	}
	for i, term := range terms {
		m.t[i] = ruleName(term)
		m.kt[i] = ruleKey(term)
		m.termIdx[i] = -1
		m.scopeIdx[i] = -1
		if idx, ok := baitTypeTermIndex[m.kt[i]]; ok {
			m.termIdx[i] = int32(idx)
		}
		// 类别限定写法 `颗粒@鱼子`：先把限定词与饵名拆开，各自归一化。
		// 拆开后必须**同时**过两道判定（属于该类 + 名字匹配），顺序无关。
		if scope, name, ok := splitBaitScope(term); ok {
			for j := range baitTypeTerms {
				if baitTypeTerms[j].Term == scope.Term && baitTypeTerms[j].Alias == scope.Alias &&
					baitTypeTerms[j].Cat == scope.Cat && baitTypeTerms[j].Shape == scope.Shape {
					m.scopeIdx[i] = int32(j)
					break
				}
			}
			if m.scopeIdx[i] >= 0 {
				m.nameRaw[i] = name
				m.nameT[i] = ruleName(name)
				m.nameKT[i] = ruleKey(name)
				// 拆成功以后，termIdx 不能再生效：`颗粒@鱼子` 的整串不是类别词，
				// 让它回落成普通饵名会去匹配「名字叫颗粒@鱼子」的饵（0 款）→ 静默零命中。
				m.termIdx[i] = -1
			}
		}
	}
	return m
}

// baitAliasNorm 是别名表的归一化形式，避免每次匹配都重算 ruleName。
var baitAliasNorm = func() map[string][]string {
	m := map[string][]string{}
	for id, list := range baitNameAliases {
		for _, alias := range list {
			m[id] = append(m[id], ruleName(alias))
		}
	}
	return m
}()

func (b ruleBait) matches(term string) bool {
	kt := ruleKey(term)
	// 类别限定写法 `颗粒@鱼子`：先确认属于该类别，再按名字匹配。
	// 两道都要过——只判类别会把该类的全部颗粒都放行，只判名字会把三种鱼子都放行。
	if scope, name, ok := splitBaitScope(term); ok {
		if !scope.matches(b) {
			return false
		}
		return b.matchOne(name, ruleName(name), ruleKey(name), ruleName(b.name), ruleKey(b.name))
	}
	// 大类词先判：写「颗粒」就是要整类，不能退化成「名字叫颗粒的那款饵」。
	if cat, ok := baitTypeTermOf(kt); ok {
		return cat.matches(b)
	}
	return b.matchOne(term, ruleName(term), kt, ruleName(b.name), ruleKey(b.name))
}

func (b ruleBait) matchesAny(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	return b.matchesPre(newMatchTerms(terms))
}

// matchesPre 用预归一化的词表匹配，供池子构建等批量场景使用。
func (b ruleBait) matchesPre(p matchTerms) bool {
	return b.matchesPreNorm(p, ruleName(b.name), ruleKey(b.name))
}

// matchesPreNorm 同上，但饵名的两种归一化由调用方提供（已有缓存时不重算）。
func (b ruleBait) matchesPreNorm(p matchTerms, n, kn string) bool {
	if len(p.raw) == 0 {
		return false
	}
	for i, term := range p.raw {
		// 类别限定写法 `颗粒@鱼子`：类别 + 名字两道都要过。
		if idx := p.scopeIdx[i]; idx >= 0 {
			if baitTypeTerms[idx].matches(b) {
				if b.matchOne(p.nameRaw[i], p.nameT[i], p.nameKT[i], n, kn) {
					return true
				}
			}
			continue
		}
		if idx := p.termIdx[i]; idx >= 0 {
			if baitTypeTerms[idx].matches(b) {
				return true
			}
			continue
		}
		if b.matchOne(term, p.t[i], p.kt[i], n, kn) {
			return true
		}
	}
	return false
}

// matchOne 是单条「饵 × 规则词」的判定（只处理具体饵名/型号）。
// 大类词（拟饵、颗粒、浮水饵…）不在这里判：调用方先用 baitTypeTermOf 分流，
// 统一交给 BaitTypeTerm.matches，避免两处各写一套枚举映射。
// t/kt 是 term 的两种归一化，n/kn 是饵名的两种归一化，由调用方预计算。
func (b ruleBait) matchOne(term, t, kt, n, kn string) bool {
	for _, alias := range baitAliasNorm[b.id] {
		if t == alias {
			return true
		}
	}
	for _, suffix := range []string{"系列饵", "系列", "类"} {
		if strings.HasSuffix(t, suffix) {
			stem := strings.TrimSuffix(t, suffix)
			if strings.HasPrefix(n, stem) {
				// 型号 1 不能匹配型号 1000；颜色编号通常由连字符分隔。
				tail := strings.TrimPrefix(n, stem)
				if len(stem) > 0 && len(tail) > 0 && stem[len(stem)-1] >= '0' && stem[len(stem)-1] <= '9' && tail[0] >= '0' && tail[0] <= '9' {
					return false
				}
				return true
			}
			return false
		}
	}
	if n == t {
		return true
	}
	// 规则词等于「该饵去掉尾部规格号后的型号名」时直接命中。
	//
	// 这一条必须复用 baitBaseName：后台层级选择器的「型号」层、自动轮换的分组、
	// 判鱼的命中范围三者是同一个概念，只能有一把尺子。否则会出现
	// 「后台点了 Spiker #2、抽查签也按 Spiker #2 分组、判鱼却一条都不认」的静默失效。
	//
	// 覆盖的写法差异：规格编号补零（`Spiker #2 001`）、带小数点（`Baby Craw 3.5 001`）、
	// 规格用连字符（`板条路亚 120-1`）、NBSP 分隔（`Baby Tube\u00a02.4\u00a0002`）。
	// 这些名字靠下面的字符串前缀判定都判不出来，只有走「去规格号」才一致。
	//
	// 用 base 的两种归一化（原样 / 去排版）各比一次：界面上写「Spiker #2」，
	// 而库里的 base 是 `Spiker #2`，两边排版写法可能不同。
	if b.name != "" {
		bb := baitBaseName(b.name)
		if bb != "" && (term == bb || kt == ruleKey(bb)) {
			return true
		}
		// 型号名本身还带一层规格号时（`Gummi Makk 10` 对规则词 `Gummi Makk`），
		// 允许「base 以规则词开头且剩余部分是数字/连字符」——这是同一产品线的
		// 不同尺寸型号（Gummi Makk 10 / 12），不是不同产品。
		if bn := ruleKey(bb); bn != "" && len(bn) > len(kt) && strings.HasPrefix(bn, kt) {
			rest := bn[len(kt):]
			if rest[0] >= '0' && rest[0] <= '9' && onlyDigits(rest) {
				return true
			}
		}
	}
	// 规则词以数字结尾时不能直接放弃：大量型号名以型号号/尺寸结尾
	// （`Baby Craw 3.5`、`Salmon T1 Crank 10`、`Hornet #3`、`Fire Gummi Eel 12/0`），
	// 它们的规格是「补零编号」`001`/`002`…，属于正常的前缀扩展。
	// 早前这里直接 return false，导致这 37 个型号组无论怎么写规则都零命中
	// （实测 873 组里 37 组，见 docs/analysis-bait-category-taxonomy §5）。
	if strings.ContainsAny(t, "abcdefghijklmnopqrstuvwxyz") && strings.HasPrefix(n, t) {
		tail := strings.TrimPrefix(n, t)
		if len(tail) == 0 {
			return false
		}
		if tail[0] == '-' || tail[0] == '#' {
			return true
		}
		if t[len(t)-1] < '0' || t[len(t)-1] > '9' {
			return tail[0] >= '0' && tail[0] <= '9'
		}
		return isZeroPaddedIndex(tail)
	}
	// 去排版后再比一次：连字符/空格/NBSP/全角字符造成的“看起来一样但对不上”。
	// 规则词可能带“类/饵/系列/系列饵”后缀，先剥掉再比，并对同义叫法做改写
	// （中立↔中性、鱼肉↔鱼块、面包屑↔湿面包、梭鲈饵↔梭鲈、Gummi Makk↔Super GummiMakk）。
	if kn != "" {
		cands := []string{kt}
		if c, ok := ruleBaitCanon[kt]; ok {
			cands = append(cands, c)
		}
		for _, suffix := range []string{"系列饵", "系列", "类", "饵"} {
			if strings.HasSuffix(kt, suffix) {
				if stem := strings.TrimSuffix(kt, suffix); stem != "" {
					cands = append(cands, stem)
					if c, ok := ruleBaitCanon[stem]; ok {
						cands = append(cands, c)
					}
				}
				break
			}
		}
		for _, c := range cands {
			if c == "" {
				continue
			}
			if c == kn || (strings.HasPrefix(kn, c) && keyDigitBoundaryOK(c, kn)) {
				return true
			}
		}
	}
	return false
}

// filterFishBaits 对喜好饵和其他饵分别加权，明确禁饵仍然排除。
func (s *Server) filterFishBaits(loc, bait string, keys []string, weights []float64) ([]string, []float64) {
	return s.filterFishBaitSet(loc, []string{bait}, keys, weights, time.Now())
}

func (s *Server) loadRuleBait(ctx context.Context, id string) (ruleBait, bool) {
	b := ruleBait{id: id}
	var raw string
	if err := s.DB.SQL.QueryRowContext(ctx, `SELECT COALESCE(c.name_zh,c.name,''),d.category,COALESCE(d.brand,''),COALESCE(d.attrs_json,'{}')
   FROM item_def d LEFT JOIN item_catalog c ON c.item_id=d.item_id WHERE d.item_id=?`, id).Scan(&b.name, &b.category, &b.brand, &raw); err != nil {
		return ruleBait{}, false
	}
	if b.category != 17 && b.category != 27 {
		return ruleBait{}, false
	}
	var attrs struct {
		LureType int64 `json:"mbemakijdeh"`
		BaitType int64 `json:"fopbhlmkcpe"`
	}
	if json.Unmarshal([]byte(raw), &attrs) != nil {
		return ruleBait{}, false
	}
	b.lureType, b.baitType = attrs.LureType, attrs.BaitType
	return b, true
}

func (s *Server) loadFishNames(ctx context.Context) (map[string]string, bool) {
	names := map[string]string{}
	// 先取覆盖，再开游标：连接池可能只有一条连接，游标未关时再查询会互相等待。
	overrides := s.baitOverrides(ctx)
	rows, err := s.DB.SQL.QueryContext(ctx, `SELECT fish_key,fish_zh FROM loc_wiki_fish
		UNION SELECT fish_key,name_zh FROM loc_fish`)
	if err != nil {
		return names, false
	}
	defer rows.Close()
	for rows.Next() {
		var key, name string
		if rows.Scan(&key, &name) == nil {
			if _, known := lookupFishBaitRule(name); known {
				names[key] = name
				continue
			}
			// 后台新增规则覆盖的鱼种同样参与严格模式判鱼。
			// 覆盖以 fish_key 为主键，先按 key 命中，再退回规范化中文名。
			if _, ok := overrides.byKey[key]; ok {
				names[key] = name
				continue
			}
			if _, ok := overrides.byName[ruleName(name)]; ok {
				names[key] = name
			}
		}
	}
	return names, true
}

func (s *Server) filterFishBaitSet(loc string, ids []string, keys []string, weights []float64, now time.Time, contexts ...fishRollContext) ([]string, []float64) {
	if s.DB == nil {
		return nil, nil
	}
	c := rollContext(contexts)
	ctx := s.dbCtx()
	var baits []ruleBait
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		b, ok := s.cachedBaitDefinition(ctx, c, id)
		if !ok {
			// 单个饵的静态库缺行（历史 id 改写、脏 item_id）不能连坐整池，
			// 否则一支竿上只要有一个坏饵，整杆就是空竿。
			continue
		}
		baits = append(baits, b)
	}
	if len(baits) == 0 {
		return nil, nil
	}
	c.lureBait = false
	for _, b := range baits {
		if b.category == 17 {
			c.lureBait = true
			break
		}
	}
	names, namesOK := s.cachedFishNames(ctx, c)
	if !namesOK {
		return nil, nil
	}
	// 证据只有 companion 档位一套（2026-11-19 用户口径）：rf4 官服占比只作后台
	// 参考、不参与判定；本图渔获（loc_bait_fish）同样不参与。同一鱼种同一款饵
	// 在任何钓场得分一致，不再随本地谁钓到过而漂移。
	evidence := make([]map[string]int64, len(baits))
	for i, b := range baits {
		evidence[i] = s.cachedBaitTiers(ctx, c, b.id)
	}
	outKeys := make([]string, 0, len(keys))
	outWeights := make([]float64, 0, len(keys))
	period := (now.Unix() - worldProtocolEpochUnix) / (4 * realSecPerGameHour)
	for i, key := range keys {
		if weights[i] <= 0 {
			continue
		}
		// 覆盖表主键是 fish_key，中文名只作兜底：names[key] 可能取到同名变体，
		// 单靠名字查会漏掉覆盖。
		r := s.resolveBaitRule(ctx, key, names[key])
		// 自动轮换：把本期的开口饵替换成 allow。吃口资格只认这份 allow，
		// 不再有 companion 越级或食性硬门兜底。
		r, _ = s.applyAutoRotation(ctx, c, key, r, now)
		if !ruleConditionAllows(r, c) {
			continue
		}
		best := 0.0
		preferred := 0
		hasRule := len(r.allow) > 0
		for j, b := range baits {
			// 禁饵与“底钓只吃”只作用于这一款饵：多钩组上挂一条被禁的饵，
			// 不能把同一杆上其它合法饵一起判死（那会让整条鱼消失）。
			if baitBlockedByRule(r, c, b) {
				continue
			}
			score := 0.0
			variation := fishVariation(fmt.Sprintf("%s/%s/%s/%d", loc, key, b.id, period))
			if hasRule {
				// 严格 allow：当前生效名单之外的饵一律 0，不再看 companion 档位。
				if !baitAllowedByRule(r, b) {
					continue
				}
				score = 1.0 + 0.5*variation
				preferred++
			} else {
				// 没有规则（兜底鱼池/未收录鱼）才看 companion 证据；
				// 有规则时证据不能绕过 allow。
				comp := evidence[j][key]
				if comp >= 3 {
					score = 1.0 + 0.5*variation
					preferred++
				} else if comp >= 1 {
					score = maxBaitPreference * s.evidenceLowScale()
				}
			}
			best = math.Max(best, score)
		}
		if best <= 0 {
			continue
		}
		if preferred > 1 {
			// 多款偏好饵叠加收益要收敛：原来固定 ×1.15 与饵数量无关，
			// 挂 2 款和挂 20 款拿同样的加成。改成每多一款 +5%，上限 +15%。
			best *= 1 + math.Min(0.15, 0.05*float64(preferred-1))
		}
		outKeys = append(outKeys, key)
		outWeights = append(outWeights, weights[i]*best)
	}
	return outKeys, outWeights
}

// fishChartCondition 解析攻略里的海图前置条件：“海图120号区域”给出区号列表，
// “海图，区域不限”只要求当前在海图上。区号数字与客户端水域 systemID 编号同源。
func fishChartCondition(condition string) ([]int, bool) {
	if !strings.Contains(condition, "海图") {
		return nil, false
	}
	var zones []int
	for i := 0; i < len(condition); {
		if condition[i] < '0' || condition[i] > '9' {
			i++
			continue
		}
		j := i
		for j < len(condition) && condition[j] >= '0' && condition[j] <= '9' {
			j++
		}
		zone, err := strconv.Atoi(condition[i:j])
		if err == nil && zone > 0 {
			zones = append(zones, zone)
		}
		i = j
	}
	return zones, len(zones) == 0
}

// ruleConditionAllows 只用客户端给得出的证据判断攻略前置条件：海图条件要有能
// 定位的抛竿落点，拟饵动作条件要有钓组动作上报。天然饵不套用“使用拟饵时”的
// 动作和船条件；拿不到证据时保持中性，避免把没有对应上报的钓法一律判成不合格。
func ruleConditionAllows(r fishBaitRule, c fishRollContext) bool {
	if r.chartAny || len(r.chartZones) > 0 {
		if c.runtime == nil {
			return true
		}
		if !c.runtime.chartKnown {
			return false
		}
		if len(r.chartZones) > 0 && c.runtime.zoneKnown {
			inside := false
			for _, zone := range r.chartZones {
				if zone == c.runtime.zone {
					inside = true
					break
				}
			}
			if !inside {
				return false
			}
		}
	}
	if !c.lureBait {
		return true
	}
	if r.needBoat && c.boatKnown && !c.onBoat {
		return false
	}
	if !lureMotionEvidence(c.lureMask) {
		return true
	}
	if r.jigOrMotion {
		if c.lureMask&lureMotionJigBit != 0 {
			return true
		}
		if c.lureMask&lureMotionInWaterMask == 0 {
			return false
		}
		if c.boatKnown && !c.onBoat {
			return false
		}
		return true
	}
	if r.needDrift && c.lureMask&lureMotionDriftBit == 0 {
		return false
	}
	if r.needJig && c.lureMask&lureMotionJigBit == 0 {
		return false
	}
	if r.needMotion && c.lureMask&lureMotionInWaterMask == 0 {
		return false
	}
	return true
}

// baitAllowedByRule 判断一款饵是否在当前生效的 allow 名单里。
// 自动轮换已把本期开口饵写进 r.allow，因此这里不再接受额外的轮换组参数。
func baitAllowedByRule(r fishBaitRule, b ruleBait) bool {
	if len(r.allow) == 0 {
		return false
	}
	return b.matchesAny(r.allow)
}

// baitBlockedByRule 判断一款饵是否被禁饵或「底钓只吃」挡下。
// 只看这一款饵，不牵连同杆其它合法饵。
func baitBlockedByRule(r fishBaitRule, c fishRollContext, b ruleBait) bool {
	if bottomRigTypes[c.rigType] && len(r.bottomOnly) > 0 && !b.matchesAny(r.bottomOnly) {
		return true
	}
	for _, term := range r.deny {
		if b.matches(term) {
			return true
		}
	}
	return false
}

// 技能只提高已有的正兴趣，不能放行禁饵或不适用的拟饵。
func fishingBaitInterest(interest float64, bonus fishingSkillBonus) float64 {
	if interest <= 0 {
		return 0
	}
	multiplier := 1.0
	if bonus.BiteSpeed > 0 && !math.IsNaN(bonus.BiteSpeed) && !math.IsInf(bonus.BiteSpeed, 0) {
		multiplier = clampFloat(1/bonus.BiteSpeed, 1, 2)
	}
	limit := 1.0
	if interest <= .1 {
		limit = .1
	}
	return math.Min(limit, interest*multiplier)
}
