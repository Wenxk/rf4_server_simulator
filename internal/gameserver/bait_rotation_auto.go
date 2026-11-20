package gameserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"rf4server/internal/store"
)

// 自动轮换：按鱼种固有稀有度，从「爱吃饵」展开池里抽本期开口的饵，
// **把抽中的饵替换成该鱼本期的 allow**。
//
// 不改判鱼主循环：allow 命中本来就是满分档，没抽中的饵自然落到「allow 之外」，
// 由现有证据档位（bait_evidence_low_scale，默认 0.10）降权。auto 只是 allow
// 的一个动态来源，与手工轮换组（soft/strict）互斥。
//
// 参与资格存在独立的 fish_bait_auto_rotation 表（逐鱼开关，键为 fish_key），
// 不写进 fish_bait_override.rotate_mode——那样给没覆盖的鱼开开关就得把内置
// 规则整行复制成覆盖，从此跟不上内置规则表更新。
//
// **抽样单位是「型号 × 形态」而不是单个 item**：客户端里同一款饵有多条 item_def
// （红蚯蚓 10/14/18 三个直径、Lurker 100 八个颜色），按 item 抽会让 3 个名额撞进
// 同一型号（玩家只看到 1 种），或者抽中 14mm 而手里的 10mm 不算开口。所以先把池子
// 聚成组再抽，抽中一组就把该组所有规格/颜色一起放行。
//
// 形态必须进分组键：同为「红虫」，活虫(baitType=2) / 沉底煮饵(1) / Pop-up(8) /
// 颗粒(9) / 人造玉米(10) 是不同产品，钓组与用法都不一样，不能混成一组。
// 味道相同的跨形态饵也**不**合并（客户端数据里也不存在这样的产品线）。
//
// 抽样由 hash(fish_key, 时间桶) 决定：同一周期内所有玩家一致，重启不丢状态，
// 不需要把结果写库。组顺序必须稳定（按基础名排序），否则同桶会抽出不同结果。
const (
	autoPickCommon = 3 // rarity 0 普通
	autoPickRare   = 2 // rarity 1 稀有
	autoPickSuper  = 1 // rarity 2 超稀有

	defRotationAutoSec = 7200.0 // 全局默认周期 120 分钟
	baitPoolTTL        = 10 * time.Minute
)

// baitSpecTail 匹配名字尾部的规格/颜色编号：红虫14、红蚯蚓 18、Lurker 100-04、
// Type 1 4g 003 都会去掉最后一段数字，得到「型号名」。
//
// 必须显式带上 NBSP（U+00A0）与全角空格：客户端大量名称用 NBSP 分隔规格
// （「甜奶油 PopUp\xa016」），而 Go 的 \s 只含 [\t\n\f\r ]，漏掉就会把尺寸留在
// 型号名里，同一系列的 12/16/20 被拆成三个组、只开中签的那个尺寸。
var baitSpecTail = regexp.MustCompile(`(?:[-_\s\x{00a0}\x{3000}]*\d+)$`)

// baitNormName 把客户端名称里的不换行空格/全角空格归一成普通空格。
// 分组、排序、折叠显示都按归一后的写法比较，否则同款饵会因空格码位不同被拆组。
func baitNormName(name string) string {
	return strings.TrimSpace(strings.NewReplacer("\u00a0", " ", "\u3000", " ").Replace(name))
}

// baitBaseName 去掉尾部规格编号。空名原样返回（调用方用 id 兜底）。
// 只剥一层：多剥一层会把型号编号一起吃掉——「Lurker 100-04」剥成「Lurker 100」
// 是对的，再剥成「Lurker」就把 100/200/300/400 四个型号并成了一组。
func baitBaseName(name string) string {
	s := baitNormName(name)
	if n := strings.TrimSpace(baitSpecTail.ReplaceAllString(s, "")); n != "" {
		return n
	}
	return s
}

// baitShapeWord 是「饵型」的可读简写，只在同一基础名有多个形态时用于区分显示。
// 取值来自 item_def.fopbhlmkcpe（天然饵的饵型字段）。
func baitShapeWord(b ruleBait) string {
	if b.category != 27 {
		return ""
	}
	switch b.baitType {
	case 1:
		// fopbhlmkcpe=1 是「下沉煮饵 + 天然饵」混装：boil_* 才是香煮饵，
		// 玉米粒/马铃薯块/湿面包/奶酪块/鱼块这些天然饵也被记成 1，
		// 一律标成「沉底煮饵」会把玉米粒说成煮饵。
		if strings.HasPrefix(b.id, "boil_") {
			return "沉底煮饵"
		}
		return "天然饵"
	case 2, 6, 7:
		return "活饵"
	case 8:
		return "Pop-up"
	case 9:
		return "颗粒"
	case 10:
		return "玉米"
	case 14:
		return "鱼片"
	case 15:
		return "死饵鱼"
	}
	return ""
}

// baitGroup 是一个抽样单位：同一「型号 × 形态」下的所有规格/颜色。
type baitGroup struct {
	base  string // 基础名（去规格后缀）
	shape string // 形态简写，用于同名多形态时区分；空=不标
	label string // 后台折叠显示名，如「红蚯蚓（10/14/18）」
	baits []ruleBait
}

// groupKey 是分组键：基础名 + 品类 + 饵型/拟饵型。
// 饵型进键是为了把「活红虫 / 红虫味沉底煮饵 / 红虫味 Pop-up」分成不同组。
func groupKey(b ruleBait) string {
	return baitBaseName(b.name) + "|" + strconv.FormatInt(b.category, 10) + "|" +
		strconv.FormatInt(b.baitType, 10) + "|" + strconv.FormatInt(b.lureType, 10)
}

// autoQuota 稀有度 -> 本期开口饵数。
func autoQuota(rarity int) int {
	switch rarity {
	case 2:
		return autoPickSuper
	case 1:
		return autoPickRare
	default:
		return autoPickCommon
	}
}

// ruleFingerprint 用于池子缓存失效：allow/deny/bottomOnly 任一变化即视为新规则。
func ruleFingerprint(r fishBaitRule) string {
	h := fnv.New64a()
	for _, group := range [][]string{r.allow, r.deny, r.bottomOnly} {
		for _, t := range group {
			h.Write([]byte(t))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// autoPickGroups 从稳定排序的组列表里确定性抽取 quota 组。
// 组不足 quota 时全部返回；调用方应先判断池子是否值得轮换。
func autoPickGroups(groups []baitGroup, quota int, fishKey string, bucket int64) []baitGroup {
	if quota <= 0 || len(groups) == 0 {
		return nil
	}
	if quota >= len(groups) {
		return append([]baitGroup(nil), groups...)
	}
	h := fnv.New64a()
	h.Write([]byte(fishKey))
	h.Write([]byte{0})
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(bucket))
	h.Write(b[:])
	rnd := rand.New(rand.NewSource(int64(h.Sum64())))
	perm := rnd.Perm(len(groups))
	out := make([]baitGroup, 0, quota)
	for i := 0; i < quota; i++ {
		out = append(out, groups[perm[i]])
	}
	// 排序键必须全序：同名多形态（红虫活饵 / 红虫味 Pop-up）base 相同，
	// 只用 base 比较会因 sort.Slice 不稳定导致跨重启抽签错位。
	sort.Slice(out, func(i, j int) bool {
		if out[i].base != out[j].base {
			return out[i].base < out[j].base
		}
		return out[i].shape < out[j].shape
	})
	return out
}

// buildBaitGroups 把整张饵表按「型号 × 形态」分组，再挑出与 allow 有交集的组。
// 「拟饵」「蠕虫类」「各类饵」这类类目词会展开成实际成员。
//
// 分组让抽样以「种」为单位：红蚯蚓 10/14/18 是一组，Lurker 100 的 8 个颜色是一组；
// 而活红虫与红虫味 Pop-up 因为饵型不同（2 vs 8）分属两组，不混在一起。
//
// **必须先分组再按 allow 过滤，不能反过来**：allow 经常只点中一款具体饵
// （「甜奶油 PopUp 16」「河螯虾14」），按命中项分组就只剩那一款，抽中后只开这个
// 尺寸。先分组、组内任一成员被 allow 命中就整组入池，才能做到「抽中一个型号，
// 整条产品线的规格/颜色一起开口」——这正是抽样单位从 item 改成组的意义。
func buildBaitGroups(baits []ruleBait, r fishBaitRule) []baitGroup {
	allowPre, denyPre := newMatchTerms(r.allow), newMatchTerms(r.deny)
	buckets := map[string][]ruleBait{}
	order := make([]string, 0, 256)
	hit := make(map[string]bool, 256) // 组是否被 allow 命中
	seen := make(map[string]bool, 256)
	for _, b := range baits {
		if len(r.deny) > 0 && b.matchesPre(denyPre) {
			continue // 禁饵不进组：进了也会被 baitBlockedByRule 剔除
		}
		k := groupKey(b)
		// 同名去重：客户端里同一款饵常有多条 item_def（不同 numeric_id）。
		// 去重键必须带上组键：同一个中文名可能是两种产品（「河螯虾 24」既有
		// Pop-up 煮饵也有颗粒），只按名字去重会把其中一个形态整条吃掉。
		dupKey := k + "\x00" + ruleName(b.name)
		if ruleName(b.name) == "" {
			dupKey = k + "\x00\x00" + b.id
		}
		if seen[dupKey] {
			continue
		}
		seen[dupKey] = true

		if _, ok := buckets[k]; !ok {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], b)
		if !hit[k] && b.matchesPre(allowPre) {
			hit[k] = true
		}
	}

	out := make([]baitGroup, 0, len(order))
	for _, k := range order {
		if !hit[k] {
			continue // 这一组没有任何成员在爱吃名单里
		}
		members := buckets[k]
		sort.Slice(members, func(i, j int) bool { return members[i].id < members[j].id })
		g := baitGroup{base: baitBaseName(members[0].name), baits: members}
		g.shape = baitShapeWord(members[0])
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].base != out[j].base {
			return out[i].base < out[j].base
		}
		if out[i].shape != out[j].shape {
			return out[i].shape < out[j].shape
		}
		return out[i].baits[0].id < out[j].baits[0].id
	})
	for i := range out {
		out[i].label = baitGroupLabel(out[i])
	}
	return out
}

// baitGroupLabel 把组折叠成可读名：
//   - 单成员：原名
//   - 多成员：基础名（规格1/规格2/…），超过 3 个规格只列前 3 个 + 共 N 款
//   - 只要有形态词就挂后缀（「玉米（8/14/18/24）· 颗粒」「红虫（10/14）· Pop-up」）：
//     形态决定钓组用法，只在同名多形态时才显示会让人误判「玉米」是玉米粒还是颗粒。
func baitGroupLabel(g baitGroup) string {
	var label string
	if len(g.baits) == 1 {
		label = baitNormName(g.baits[0].name)
	} else {
		specs := make([]string, 0, len(g.baits))
		for _, b := range g.baits {
			s := baitNormName(b.name)
			if strings.HasPrefix(s, g.base) {
				s = strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(s, g.base), "-_ "))
			}
			if s == "" {
				s = b.id
			}
			specs = append(specs, s)
		}
		if len(specs) > 3 {
			label = g.base + "（" + strings.Join(specs[:3], "/") + "… 共" + strconv.Itoa(len(specs)) + "款）"
		} else {
			label = g.base + "（" + strings.Join(specs, "/") + "）"
		}
	}
	if g.shape != "" {
		label += " · " + g.shape
	}
	return label
}

// buildBaitPool 返回展平后的具体饵（调试与兼容用），抽样走 buildBaitGroups。
func buildBaitPool(baits []ruleBait, r fishBaitRule) []ruleBait {
	groups := buildBaitGroups(baits, r)
	total := 0
	for _, g := range groups {
		total += len(g.baits)
	}
	out := make([]ruleBait, 0, total)
	for _, g := range groups {
		out = append(out, g.baits...)
	}
	return out
}

// allRuleBaits 加载全部饵定义（17 拟饵 / 27 天然饵），进程内缓存。
// 判鱼只需要玩家钩上的几款，但自动轮换要展开整张名单，必须全量。
func (s *Server) allRuleBaits(ctx context.Context) []ruleBait {
	s.allBaitMu.RLock()
	if s.allBait != nil && time.Since(s.allBaitAt) < baitPoolTTL {
		v := s.allBait
		s.allBaitMu.RUnlock()
		return v
	}
	s.allBaitMu.RUnlock()

	s.allBaitMu.Lock()
	defer s.allBaitMu.Unlock()
	if s.allBait != nil && time.Since(s.allBaitAt) < baitPoolTTL {
		return s.allBait
	}
	if s.DB == nil {
		return nil
	}
	out := LoadAllRuleBaits(ctx, s.DB)
	s.allBait = out
	s.allBaitAt = time.Now()
	return out
}

// RuleBaitList 是「一次加载的全量饵定义」的导出别名。后台列表要给多条鱼
// 各算一次池子，必须只加载一次再复用；没有这个名字就没法在 admin 包里声明变量。
type RuleBaitList = []ruleBait

// LoadAllRuleBaits 一次读全量饵定义（17 拟饵 / 27 天然饵）。后台算池子时
// 也走这里，保证两边展开口径一致。
func LoadAllRuleBaits(ctx context.Context, db *store.DB) []ruleBait {
	if db == nil {
		return nil
	}
	// ORDER BY 不是性能优化而是确定性要求：组的构建顺序影响同 base 组的相对次序，
	// 进而影响同桶抽签结果；没有它，SQLite 换了查询计划就可能抽出不同饵。
	rows, err := db.SQL.QueryContext(ctx, `SELECT d.item_id, COALESCE(c.name_zh,c.name,''), d.category, COALESCE(d.attrs_json,'{}')
		FROM item_def d LEFT JOIN item_catalog c ON c.item_id=d.item_id
		WHERE d.category IN (17,27) ORDER BY d.item_id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]ruleBait, 0, 4096)
	for rows.Next() {
		var b ruleBait
		var raw string
		if rows.Scan(&b.id, &b.name, &b.category, &raw) != nil {
			continue
		}
		var attrs struct {
			LureType int64 `json:"mbemakijdeh"`
			BaitType int64 `json:"fopbhlmkcpe"`
		}
		if json.Unmarshal([]byte(raw), &attrs) != nil {
			continue
		}
		b.lureType, b.baitType = attrs.LureType, attrs.BaitType
		out = append(out, b)
	}
	return out
}

// autoPoolFor 返回该规则展开后的型号组。缓存键用规则指纹：同一份规则被多条鱼
// 共用（大量鱼种的 allow 完全一致），按指纹存能省掉重复构建。
func (s *Server) autoPoolFor(ctx context.Context, r fishBaitRule) []baitGroup {
	fp := ruleFingerprint(r)
	s.baitPoolMu.RLock()
	if pool, ok := s.baitPool[fp]; ok {
		s.baitPoolMu.RUnlock()
		return pool
	}
	s.baitPoolMu.RUnlock()

	pool := buildBaitGroups(s.allRuleBaits(ctx), r)

	s.baitPoolMu.Lock()
	if s.baitPool == nil {
		s.baitPool = map[string][]baitGroup{}
	}
	s.baitPool[fp] = pool
	s.baitPoolMu.Unlock()
	return pool
}

// invalidateBaitPools 在规则覆盖变化后清空池子缓存。
func (s *Server) invalidateBaitPools() {
	s.baitPoolMu.Lock()
	s.baitPool = nil
	s.baitPoolMu.Unlock()
}

// 全局参数（tunables，管理端可热更）。

func (s *Server) rotationAutoEnabled() bool {
	return s.tunableF("bait_rotation_auto_enabled", 0) > 0
}

func (s *Server) rotationAutoSec() int64 {
	sec := int64(s.tunableF("bait_rotation_auto_sec", defRotationAutoSec))
	if sec < minBaitRotateSec {
		sec = minBaitRotateSec
	}
	return sec
}

// rotationAutoRound 是后台「立即重抽」累计的轮次偏移。抽签只取决于
// hash(fish_key, floor(now/sec))，想马上换一批而不能等周期翻页时，就把偏移 +1：
// 时间桶整体后移一轮，但切换时刻仍按自然网格算（见 autoPickFor 的 nextAt），
// 所以「立刻换一批」和「周期长度不变」能同时成立，也不需要重启或写库存抽签结果。
func (s *Server) rotationAutoRound() int64 {
	return int64(s.tunableF("bait_rotation_auto_round", 0))
}

// autoBucket 是抽签用的时间桶：自然轮次 + 人工偏移。
func autoBucket(now time.Time, sec, round int64) int64 {
	if sec < minBaitRotateSec {
		sec = minBaitRotateSec
	}
	return now.Unix()/sec + round
}

// evidenceLowScale 是「allow 之外、但 companion 在榜」这一档的系数。
// 自动轮换没抽中的饵就落在这里；非 auto 的鱼同样按它降权（2026-11-19 用户口径：
// 由 0.35 降到 0.10，收窄「设定里没写却照样咬」的口子）。
func (s *Server) evidenceLowScale() float64 {
	v := s.tunableF("bait_evidence_low_scale", 0.10)
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return v
}

// baitAutoFlags 读取逐鱼自动轮换开关并缓存；后台写操作会主动失效，这里再兜一层 TTL。
func (s *Server) baitAutoFlags(ctx context.Context) map[string]bool {
	s.baitAutoMu.RLock()
	if s.baitAutoCache != nil && time.Since(s.baitAutoAt) < baitOverrideTTL {
		m := s.baitAutoCache
		s.baitAutoMu.RUnlock()
		return m
	}
	s.baitAutoMu.RUnlock()

	s.baitAutoMu.Lock()
	defer s.baitAutoMu.Unlock()
	if s.baitAutoCache != nil && time.Since(s.baitAutoAt) < baitOverrideTTL {
		return s.baitAutoCache
	}
	m := map[string]bool{}
	if s.DB != nil {
		rows, err := s.DB.ListFishBaitAuto(ctx)
		if err != nil {
			s.log.Warn("读取自动轮换开关失败", "err", err)
		}
		for _, a := range rows {
			if a.Enabled {
				m[a.FishKey] = true
			}
		}
	}
	s.baitAutoCache = m
	s.baitAutoAt = time.Now()
	return m
}

// invalidateBaitAuto 在后台切换开关后清缓存，让改动立即生效。
func (s *Server) invalidateBaitAuto() {
	s.baitAutoMu.Lock()
	s.baitAutoCache = nil
	s.baitAutoAt = time.Time{}
	s.baitAutoMu.Unlock()
}

// autoRotationOn 判断该鱼种是否参与自动轮换：全局总开关 + 逐鱼开关都开。
func (s *Server) autoRotationOn(ctx context.Context, fishKey string) bool {
	if !s.rotationAutoEnabled() {
		return false
	}
	return s.baitAutoFlags(ctx)[fishKey]
}

// autoPickState 是一次自动轮换判定的结果，供后台/调试展示；nil 表示本轮不轮换。
type autoPickState struct {
	picked []baitGroup
	quota  int
	bucket int64
	nextAt int64
}

// autoPickFor 计算该鱼本期的开口饵。返回 nil 的情况：开关未开、
// 组数不足以支撑轮换。
func (s *Server) autoPickFor(ctx context.Context, c fishRollContext, fishKey string, r fishBaitRule, now time.Time) *autoPickState {
	if !s.autoRotationOn(ctx, fishKey) {
		return nil
	}
	groups := s.autoPoolFor(ctx, r)
	quota := autoQuota(s.cachedFishEcology(ctx, c, fishKey).RarityTier)
	if len(groups) <= quota {
		return nil // 型号就这么几种，全开，轮换没有意义
	}
	sec := s.rotationAutoSec()
	round := s.rotationAutoRound()
	return &autoPickState{
		picked: autoPickGroups(groups, quota, fishKey, autoBucket(now, sec, round)),
		quota:  quota,
		bucket: now.Unix()/sec + round,
		// 切换时刻只看自然网格：人工偏移不能把下一次轮换推后一轮。
		nextAt: (now.Unix()/sec + 1) * sec,
	}
}

// applyAutoRotation 把该鱼本期的 allow 换成抽中的型号组。
// 一组里的所有规格/颜色都会写进 allow——同型号不同尺寸在判鱼上完全等价，
// 玩家可以用手里任意规格，不会因为买错 14/18mm 而空军。
// 只改规则内容，后面的判鱼逻辑完全走原路：allow 命中=满分档，
// 其余落到 allow 之外的证据档位（bait_evidence_low_scale）降权。
func (s *Server) applyAutoRotation(ctx context.Context, c fishRollContext, fishKey string, r fishBaitRule, now time.Time) (fishBaitRule, bool) {
	st := s.autoPickFor(ctx, c, fishKey, r, now)
	if st == nil {
		return r, false
	}
	allow := make([]string, 0, len(st.picked)*4)
	seen := map[string]bool{}
	for _, g := range st.picked {
		for _, b := range g.baits {
			name := strings.TrimSpace(b.name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			allow = append(allow, name)
		}
	}
	if len(allow) == 0 {
		return r, false
	}
	r.allow = allow
	return r, true
}

// ExportedAutoRotation 供管理后台展示本期状态（与判鱼同一套计算）。
// baits 由后台一次性加载全量饵定义后传入，fishKey 必须是运行时真正的 fish_key，
// 否则后台显示的开口饵与游戏内不一致。ActiveBaits 是折叠后的组名
// （「红蚯蚓（10/14/18）」），PoolSize 是型号组数而不是 item 数。
// round 必须与判鱼侧同一个偏移，否则后台显示的是上一轮的名单。
func ExportedAutoRotation(fishKey string, baits []ruleBait,
	allow, deny, bottom []string, rarity int, sec, round int64, now time.Time) RotationState {
	r := fishBaitRule{allow: allow, deny: deny, bottomOnly: bottom}
	groups := buildBaitGroups(baits, r)
	quota := autoQuota(rarity)
	state := RotationState{Mode: "auto", Active: -1, IntervalSec: sec,
		Quota: quota, PoolSize: len(groups), Total: len(groups)}
	if sec < minBaitRotateSec || len(groups) <= quota {
		return state
	}
	picked := autoPickGroups(groups, quota, fishKey, autoBucket(now, sec, round))
	names := make([]string, 0, len(picked))
	for _, g := range picked {
		names = append(names, g.label)
	}
	state.Enabled = true
	state.Active = len(names)
	state.ActiveBaits = names
	// 与判鱼一致：下一次切换只看自然网格，人工重抽不改变节奏。
	state.NextSwitch = (now.Unix()/sec + 1) * sec
	return state
}

// ExportedBaitPool 供后台展示展开后的池子（调试「为什么只开这几款」）。
func ExportedBaitPool(baits []ruleBait, allow, deny, bottom []string) []string {
	pool := buildBaitPool(baits, fishBaitRule{allow: allow, deny: deny, bottomOnly: bottom})
	out := make([]string, 0, len(pool))
	for _, b := range pool {
		out = append(out, b.id)
	}
	return out
}
