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
	return len(rest) >= 2 && rest[0] == '0' && rest[1] >= '0' && rest[1] <= '9'
}

// BaitRuleInfo 一条"鱼吃饵"规则的可读形态（供管理后台展示）。
type BaitRuleInfo struct {
	Allow      []string `json:"allow"`
	Deny       []string `json:"deny"`
	BottomOnly []string `json:"bottom_only"`
	Condition  string   `json:"condition"`
}

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
		allowed := hasRule && baitAllowedByRule(r, rb, nil)
		blocked := hasRule && baitBlockedByRule(r, fishRollContext{}, rb)
		if blocked {
			out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "blocked", Strength: 0, Reason: "规则标明不吃"})
			continue
		}
		if allowed && hasRule {
			out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "bite", Strength: 1, Reason: "规则允许"})
			continue
		}
		if b.Tier >= 3 {
			out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "bite", Strength: 0.6, Reason: "玩家实测常用"})
			continue
		}
		out = append(out, BaitOutcome{BaitID: b.ID, Verdict: "fallback", Strength: 0.1, Reason: "规则未允许，仅保底咬口"})
	}
	return out
}

// ExportedBaitVerdict 单款饵的判定，给后台画「会咬/保底/不吃」三态。
// 与 filterFishBaitSet 同源：规则允许 → 会咬；companion 实证 top 档 → 会咬；
// 实证在榜 → 保底档；规则禁 → 不吃。
// officialPct 只用于后台展示官服占比，不参与结论（2026-11-19 用户口径）。
func ExportedBaitVerdict(fishZh, baitID, baitName string, category, lureType, baitType int64, officialPct float64, tier int) (string, float64, string) {
	r, hasRule := lookupFishBaitRule(fishZh)
	rb := ruleBait{id: baitID, name: baitName, category: category, lureType: lureType, baitType: baitType}
	if hasRule && baitBlockedByRule(r, fishRollContext{}, rb) {
		return "blocked", 0, "规则标明不吃"
	}
	if hasRule && baitAllowedByRule(r, rb, nil) {
		return "bite", 1, "规则允许"
	}
	if tier >= 3 {
		return "bite", 0.6, "玩家实测常用"
	}
	if tier >= 1 {
		return "fallback", 0.10, "实证在榜，仅低权重"
	}
	return "fallback", 0.1, "规则未允许，仅保底咬口"
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
		r := fishBaitRule{allow: strings.Split(strings.TrimSpace(allowed), "/"), condition: strings.TrimSpace(condition)}
		r.deriveConditionFlags()
		if _, denied, ok := strings.Cut(extra, "不吃"); ok {
			denied = strings.FieldsFunc(denied, func(c rune) bool { return c == ')' || c == '；' || c == ';' })[0]
			r.deny = strings.Split(denied, "/")
		}
		if _, only, ok := strings.Cut(extra, "底钓只吃"); ok {
			only = strings.TrimRight(only, ") \r\t")
			r.bottomOnly = strings.Split(only, "/")
		}
		out[ruleName(name)] = r
	}
	return out
}

type ruleBait struct {
	id, name                     string
	category, lureType, baitType int64
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
type matchTerms struct {
	raw []string
	t   []string
	kt  []string
}

func newMatchTerms(terms []string) matchTerms {
	m := matchTerms{raw: terms, t: make([]string, len(terms)), kt: make([]string, len(terms))}
	for i, term := range terms {
		m.t[i] = ruleName(term)
		m.kt[i] = ruleKey(term)
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
	return b.matchOne(term, ruleName(term), ruleKey(term), ruleName(b.name), ruleKey(b.name))
}

func (b ruleBait) matchesAny(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	return b.matchesPre(newMatchTerms(terms))
}

// matchesPre 用预归一化的词表匹配，供池子构建等批量场景使用。
func (b ruleBait) matchesPre(p matchTerms) bool {
	if len(p.raw) == 0 {
		return false
	}
	n, kn := ruleName(b.name), ruleKey(b.name)
	for i, term := range p.raw {
		if b.matchOne(term, p.t[i], p.kt[i], n, kn) {
			return true
		}
	}
	return false
}

// matchOne 是单条「饵 × 规则词」的判定。t/kt 是 term 的两种归一化，
// n/kn 是饵名的两种归一化，由调用方预计算。
func (b ruleBait) matchOne(term, t, kt, n, kn string) bool {
	for _, alias := range baitAliasNorm[b.id] {
		if t == alias {
			return true
		}
	}
	if t == "各类饵" {
		return true
	}
	if t == "拟饵" {
		return b.category == 17
	}
	if t == "活饵类" {
		return b.category == 27 && b.baitType == 4
	}
	if t == "饵鱼类" || t == "饵鱼" || t == "各类饵鱼" {
		return b.category == 27 && (b.baitType == 4 || b.baitType == 15)
	}
	if b.category == 27 {
		switch t {
		case "蠕虫", "蠕虫类":
			return b.baitType == 6
		case "昆虫幼虫":
			return b.baitType == 2
		case "昆虫":
			return b.baitType == 7
		case "鱼肉类":
			return b.baitType == 14 || b.id == "fish_flesh"
		}
	}
	if b.category == 17 {
		switch t {
		case "亮片", "亮片类", "亮片系列饵":
			return b.lureType == 1 || b.lureType == 2
		case "旋转饵", "旋转饵类", "旋转亮片路亚":
			return b.lureType == 1
		case "软饵类", "拟真软饵":
			return b.lureType == 3
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
	if strings.ContainsAny(t, "abcdefghijklmnopqrstuvwxyz") && strings.HasPrefix(n, t) {
		tail := strings.TrimPrefix(n, t)
		return len(tail) > 0 && (tail[0] == '-' || tail[0] == '#' || (t[len(t)-1] < '0' || t[len(t)-1] > '9') && tail[0] >= '0' && tail[0] <= '9')
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
	if err := s.DB.SQL.QueryRowContext(ctx, `SELECT COALESCE(c.name_zh,c.name,''),d.category,COALESCE(d.attrs_json,'{}')
   FROM item_def d LEFT JOIN item_catalog c ON c.item_id=d.item_id WHERE d.item_id=?`, id).Scan(&b.name, &b.category, &raw); err != nil {
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
			if _, ok := overrides[ruleName(name)]; ok {
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
	period := (now.Unix() - worldDayEpochUnix) / (4 * realSecPerGameHour)
	for i, key := range keys {
		if weights[i] <= 0 {
			continue
		}
		r, rot := s.resolveBaitRule(ctx, names[key])
		// 自动轮换：把本期的开口饵替换成 allow，后面的判定完全走原路
		// （allow 命中=满分档，其余落到 allow 之外的证据档位降权）。
		r, autoActive := s.applyAutoRotation(ctx, c, key, r, now)
		// 轮换：按时间桶取当前组；strict 只认当前组，soft 组外降权但仍可咬。
		// 自动抽取开了就不看手工组：两者是同一批 allow 的两种来源，叠加会互相抵消。
		var activeGroup []string
		if !autoActive {
			activeGroup = activeBaitGroup(rot, now)
		}
		strictRotation := activeGroup != nil && rot.mode == "strict"
		if !ruleConditionAllows(r, c) {
			continue
		}
		best := 0.0
		preferred := 0
		hasRule := len(r.allow) > 0
		// 先扫一遍候选池，确认规则是否至少放行了一款饵。整条规则因为翻译差异
		// 一条都没对上时，才允许 companion 证据越级给满档；只要清单里还有
		// 合法饵，证据就不能绕过白名单（红虫 1.08 vs 兜底 0.086 那类问题）。
		allowedFlags := make([]bool, len(baits))
		// 自动轮换生效时 allow 是刚抽出来的有效名单（池子非空），companion
		// 不该再以「规则没匹配上」为由越级给满档，否则轮换会被 tier3 架空。
		anyAllowed := autoActive
		for j, b := range baits {
			if !rotationKeepsBait(activeGroup, strictRotation, b) {
				continue
			}
			allowedFlags[j] = baitAllowedByRule(r, b, activeGroup)
			if allowedFlags[j] {
				anyAllowed = true
			}
		}
		for j, b := range baits {
			if !rotationKeepsBait(activeGroup, strictRotation, b) {
				continue // strict 轮换：组外的饵本轮不咬
			}
			// 禁饵与“底钓只吃”只作用于这一款饵：多钩组上挂一条被禁的饵，
			// 不能把同一杆上其它合法饵一起判死（那会让整条鱼消失）。
			if baitBlockedByRule(r, c, b) {
				continue
			}
			allowed := allowedFlags[j]
			score := 0.0
			full := false
			variation := fishVariation(fmt.Sprintf("%s/%s/%s/%d", loc, key, b.id, period))
			// evidence[j] 实为 companion tier map（1=在榜 2=热门 3=top 档），非计数。
			comp := evidence[j][key]
			// companion 只在「这条鱼没有规则、或规则一条候选饵都没匹配上」时才允许
			// 越级给满档，否则规则表白名单会被证据表架空。anyAllowed 由本轮候选池
			// 决定，因此规则写错字导致整条失效的鱼仍能被证据救回。
			if allowed || (!hasRule || !anyAllowed) && comp >= 3 {
				full = true
				score = 1.0 + 0.5*variation
			} else if comp >= 1 {
				// 「allow 之外但 companion 在榜」的档位。自动轮换没抽中的饵
				// 就落在这里；2026-11-19 由 0.35 降到 0.10。
				score = maxBaitPreference * s.evidenceLowScale()
			} else if b.legalAlternative(r) {
				score = maxBaitPreference * 0.05
			}
			if full {
				preferred++
			}
			if activeGroup != nil {
				score *= rotationScoreMultiplier(activeGroup, b)
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

// baitAllowedByRule 判断一款饵是否在本条规则的允许名单里。
// 当前轮换组的饵视为本轮主推，即使不在静态允许列表里。
func baitAllowedByRule(r fishBaitRule, b ruleBait, activeGroup []string) bool {
	if len(r.allow) == 0 {
		return false
	}
	if b.matchesAny(r.allow) {
		return true
	}
	return activeGroup != nil && b.matchesAny(activeGroup)
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

// 非偏好只在已识别食性族内保留杂口，不把所有天然饵视为通用食物。
func (b ruleBait) legalAlternative(r fishBaitRule) bool {
	if b.category != 27 {
		return false
	}
	var diet string
	switch b.baitType {
	case 2, 6, 7:
		diet = "虫"
	case 8:
		diet = "煮饵"
	case 9:
		diet = "颗粒"
	case 10:
		diet = "玉米"
	case 11, 13:
		diet = "植物"
	case 4, 15:
		diet = "饵鱼"
	case 14:
		diet = "鱼肉"
	}
	if b.id == "fish_flesh" {
		diet = "鱼肉"
	}
	terms := strings.Join(r.allow, "/")
	switch diet {
	case "虫":
		for _, term := range []string{"蠕虫", "蚯蚓", "红虫", "苍蝇", "蛆虫", "毛翅", "树皮甲虫", "昆虫", "地龙", "土蚕", "水蛭", "蜉蝣", "襀翅", "牛虻", "草蜢", "蟋蟀", "蜣螂", "金龟", "龙虱", "蝼蛄"} {
			if strings.Contains(terms, term) {
				return true
			}
		}
	case "植物":
		for _, term := range []string{"面包", "面团", "粥", "玉米", "豌豆", "坚果", "奶酪", "水果", "蜂蜜", "奶油", "椰子", "中立", "自然风味"} {
			if strings.Contains(terms, term) {
				return true
			}
		}
	case "煮饵":
		return strings.Contains(terms, "煮饵") || strings.Contains(terms, "煮成饵") || strings.Contains(strings.ToLower(terms), "boilie") || strings.Contains(strings.ToLower(terms), "popup") || strings.Contains(strings.ToLower(terms), "pop up")
	case "颗粒":
		return strings.Contains(terms, "颗粒") || strings.Contains(strings.ToLower(terms), "pellet")
	case "玉米":
		return strings.Contains(terms, "玉米")
	case "饵鱼":
		return strings.Contains(terms, "饵鱼") || strings.Contains(terms, "活饵")
	case "鱼肉":
		return strings.Contains(terms, "鱼肉") || strings.Contains(terms, "鱼片")
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
