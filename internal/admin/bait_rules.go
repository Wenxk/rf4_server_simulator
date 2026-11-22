package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"rf4server/internal/gameserver"
	"rf4server/internal/store"
)

// 覆盖写入上限：避免一次提交过大把判鱼路径拖慢。
const maxBaitRuleTerms = 40

type baitRuleSaveReq struct {
	FishZh     string   `json:"fish_zh"`
	Allow      []string `json:"allow"`
	Deny       []string `json:"deny"`
	BottomOnly []string `json:"bottom_only"`
	Condition  string   `json:"condition"`
	Enabled    bool     `json:"enabled"`
}

// handleBaitRuleSave 保存一个鱼种的饵料规则覆盖。
func (s *Server) handleBaitRuleSave(w http.ResponseWriter, r *http.Request) {
	var req baitRuleSaveReq
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	fishZh := strings.TrimSpace(req.FishZh)
	if fishZh == "" {
		httpError(w, 400, "缺少鱼种中文名")
		return
	}
	if len([]rune(fishZh)) > 40 {
		httpError(w, 400, "鱼种名过长")
		return
	}
	ov := store.FishBaitOverride{
		FishZh:     fishZh,
		Allow:      cleanTerms(req.Allow),
		Deny:       cleanTerms(req.Deny),
		BottomOnly: cleanTerms(req.BottomOnly),
		Condition:  strings.TrimSpace(req.Condition),
		Enabled:    req.Enabled,
	}
	if len(ov.Allow) > maxBaitRuleTerms || len(ov.Deny) > maxBaitRuleTerms || len(ov.BottomOnly) > maxBaitRuleTerms {
		httpError(w, 400, "单个列表最多 "+strconv.Itoa(maxBaitRuleTerms)+" 项")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.SaveFishBaitOverride(ctx, ov, adminOf(r)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "bait_rule_save", fishZh,
		"allow="+strconv.Itoa(len(ov.Allow))+" deny="+strconv.Itoa(len(ov.Deny))+
			" enabled="+strconv.FormatBool(ov.Enabled))
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_bait_rules"}))
	writeJSON(w, map[string]any{"ok": true})
}

// handleBaitRuleReset 恢复默认：body 带 fish_zh 删单条，带 all=true 清整表。
func (s *Server) handleBaitRuleReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FishZh string `json:"fish_zh"`
		All    bool   `json:"all"`
	}
	if err := readJSON(r, &req); err != nil {
		req = struct {
			FishZh string `json:"fish_zh"`
			All    bool   `json:"all"`
		}{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var n int64
	var err error
	target := strings.TrimSpace(req.FishZh)
	if req.All {
		n, err = s.db.DeleteAllFishBaitOverrides(ctx)
		target = "*"
	} else {
		if target == "" {
			httpError(w, 400, "缺少鱼种中文名")
			return
		}
		n, err = s.db.DeleteFishBaitOverride(ctx, target)
	}
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "bait_rule_reset", target,
		"rows="+strconv.FormatInt(n, 10))
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_bait_rules"}))
	writeJSON(w, map[string]any{"ok": true, "removed": n})
}

// 饵料/拟饵的二级分类：直接取客户端枚举（饵料 baitType、拟饵 lureType）。
// 展示名与「大类规则词」统一由 gameserver 的 baitTypeTerms 表提供：后台按钮上
// 写「颗粒」，写进规则的就是「颗粒」，不会再出现两套叫法。
func baitSubLabel(category, sub int64) string {
	if s, ok := gameserver.ExportedBaitTypeLabel(category, sub); ok {
		return s
	}
	return "其他"
}

func baitCategoryLabel(category int64) string {
	if category == 17 {
		return "拟饵"
	}
	return "饵料"
}

type baitCatalogEntry struct {
	ItemID   string `json:"item_id"`
	NameZh   string `json:"name_zh"`
	Category int64  `json:"category"`
	Sub      int64  `json:"sub"`
	SubLabel string `json:"sub_label"`
}

// loadBaitCatalog 读取全部饵料/拟饵及其二级分类，供选择器与分类树使用。
func (s *Server) loadBaitCatalog(ctx context.Context) ([]baitCatalogEntry, error) {
	rows, err := s.db.SQL.QueryContext(ctx, `
		SELECT d.item_id, COALESCE(c.name_zh,c.name,''), COALESCE(d.category,0), COALESCE(d.attrs_json,'{}')
		FROM item_def d LEFT JOIN item_catalog c ON c.item_id=d.item_id
		WHERE d.category IN (17,27)
		GROUP BY d.item_id, d.category
		ORDER BY COALESCE(c.name_zh,c.name,''), d.item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type attrs struct {
		BaitType int64 `json:"fopbhlmkcpe"`
		LureType int64 `json:"mbemakijdeh"`
	}
	out := make([]baitCatalogEntry, 0, 4096)
	for rows.Next() {
		var e baitCatalogEntry
		var raw string
		if err := rows.Scan(&e.ItemID, &e.NameZh, &e.Category, &raw); err != nil {
			return nil, err
		}
		var a attrs
		_ = json.Unmarshal([]byte(raw), &a)
		if e.Category == 17 {
			e.Sub = a.LureType
		} else {
			e.Sub = a.BaitType
		}
		e.SubLabel = baitSubLabel(e.Category, e.Sub)
		out = append(out, e)
	}
	return out, rows.Err()
}

// handleBaitCategories 返回「大类 → 二级分类」树，带每种分类的数量。
func (s *Server) handleBaitCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	all, err := s.loadBaitCatalog(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	type child struct {
		Sub   int64  `json:"sub"`
		Label string `json:"label"`
		Count int    `json:"count"`
		// Term 是「整类加入」时写进规则的词（如「颗粒」），前端按钮直接用它。
		Term string `json:"term"`
	}
	type node struct {
		Key      int64   `json:"key"`
		Label    string  `json:"label"`
		Count    int     `json:"count"`
		Term     string  `json:"term"`
		Children []child `json:"children"`
	}
	counts := map[int64]map[int64]int{}
	totals := map[int64]int{}
	for _, e := range all {
		if counts[e.Category] == nil {
			counts[e.Category] = map[int64]int{}
		}
		counts[e.Category][e.Sub]++
		totals[e.Category]++
	}
	nodes := make([]node, 0, 2)
	for _, cat := range []int64{27, 17} {
		catTerm, _ := gameserver.ExportedBaitCategoryTerm(cat)
		n := node{Key: cat, Label: baitCategoryLabel(cat), Count: totals[cat], Term: catTerm}
		subs := make([]int64, 0, len(counts[cat]))
		for sub := range counts[cat] {
			subs = append(subs, sub)
		}
		sort.Slice(subs, func(i, j int) bool { return subs[i] < subs[j] })
		for _, sub := range subs {
			term, _ := gameserver.ExportedBaitTypeTermOf(cat, sub)
			n.Children = append(n.Children, child{
				Sub: sub, Label: baitSubLabel(cat, sub), Count: counts[cat][sub], Term: term,
			})
		}
		nodes = append(nodes, n)
	}
	// terms 是「按大类加入」按钮的完整清单：顺序、标签、规则词都由 gameserver 定，
	// 前端不再硬编码（以前硬编码 4 个通配词，删改一次就要改两处）。
	terms := make([]map[string]any, 0, 40)
	for _, t := range gameserver.ExportedBaitTypeTerms() {
		cnt := 0
		switch {
		case t.Cat == 0:
			cnt = totals[27] + totals[17]
		case len(t.Subs) == 0:
			cnt = totals[t.Cat]
		default:
			for _, sub := range t.Subs {
				cnt += counts[t.Cat][sub]
			}
		}
		terms = append(terms, map[string]any{
			"term": t.Term, "label": t.Label, "cat": t.Cat, "sub": baitTermSub(t),
			"count": cnt, "alias": t.Alias,
		})
	}
	writeJSON(w, map[string]any{"categories": nodes, "terms": terms})
}

// baitTermSub 返回该大类词对应的二级类型（0=整个大类）；前端据此决定
// 「选了这个分类时该显示哪些整类按钮」。
func baitTermSub(t gameserver.BaitTypeTerm) int64 {
	if len(t.Subs) == 0 {
		return 0
	}
	return t.Subs[0]
}

// handleBaitItems 给规则编辑器提供可选饵料清单（只读），支持按大类/二级分类/关键字过滤。
func (s *Server) handleBaitItems(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	cat, _ := strconv.ParseInt(r.URL.Query().Get("cat"), 10, 64)
	sub, _ := strconv.ParseInt(r.URL.Query().Get("sub"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	all, err := s.loadBaitCatalog(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	lower := strings.ToLower(q)
	out := make([]baitCatalogEntry, 0, limit)
	for _, e := range all {
		if cat != 0 && e.Category != cat {
			continue
		}
		if sub != 0 && e.Sub != sub {
			continue
		}
		if lower != "" &&
			!strings.Contains(strings.ToLower(e.NameZh), lower) &&
			!strings.Contains(strings.ToLower(e.ItemID), lower) {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, map[string]any{"rows": out, "total": len(all)})
}

// baitRulesEnabled 规则开关是否打开：关闭时规则不参与判鱼，后台需要提示。
func (s *Server) baitRulesEnabled(ctx context.Context) bool {
	var v string
	if err := s.db.SQL.QueryRowContext(ctx, `SELECT value FROM tunables WHERE key='fish_bait_rules_enabled'`).Scan(&v); err != nil {
		return false
	}
	return strings.TrimSpace(v) == "1"
}

// handleBaitAutoRotation 切换某鱼种的自动轮换开关。
// 参数是 fish_key（不是中文名）：开关表以 fish_key 为主键，不受「泥鳅 / 泥鳅鱼」
// 这类按水域改名以及 BOM 脏字符影响。开关只存「要不要轮换」，不改规则内容。
func (s *Server) handleBaitAutoRotation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FishKey string `json:"fish_key"`
		Enabled bool   `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	key := strings.TrimSpace(req.FishKey)
	if key == "" {
		httpError(w, 400, "缺少 fish_key")
		return
	}
	if len([]rune(key)) > 64 {
		httpError(w, 400, "fish_key 过长")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.db.SetFishBaitAuto(ctx, key, req.Enabled, adminOf(r)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	action := "bait_auto_off"
	if req.Enabled {
		action = "bait_auto_on"
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), action, key, "")
	// 主动通知判鱼侧清缓存；没收到也会在 15 秒 TTL 内自然生效。
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_bait_auto"}))
	writeJSON(w, map[string]any{"ok": true, "fish_key": key, "enabled": req.Enabled})
}

// autoRotationGlobalOn 读自动轮换总开关（tunables）。
func (s *Server) autoRotationGlobalOn(ctx context.Context) bool {
	var v string
	if err := s.db.SQL.QueryRowContext(ctx,
		`SELECT value FROM tunables WHERE key='bait_rotation_auto_enabled'`).Scan(&v); err != nil {
		return false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return err == nil && f > 0
}

// autoRotationSec 读自动轮换的全局周期（秒）。
func (s *Server) autoRotationSec(ctx context.Context) int64 {
	var v string
	if err := s.db.SQL.QueryRowContext(ctx, `SELECT value FROM tunables WHERE key='bait_rotation_auto_sec'`).Scan(&v); err != nil {
		return 7200
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 7200
	}
	sec := int64(f)
	if sec < 60 {
		sec = 60
	}
	return sec
}

// baitAutoRoundTunable 是「立即重抽」累计的轮次偏移（判鱼侧同一键）。
const baitAutoRoundTunable = "bait_rotation_auto_round"

// autoRotationRound 读人工重抽偏移；缺省/坏值按 0 处理（等于没点过重抽）。
func (s *Server) autoRotationRound(ctx context.Context) int64 {
	v, ok, err := s.db.TunableGet(ctx, baitAutoRoundTunable)
	if err != nil || !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// handleBaitAutoReroll 立即重抽一轮：把轮次偏移 +1。
// 抽签是 hash(fish_key, floor(now/周期))，改不了 now，所以用偏移让时间桶整体后移；
// 切换时刻仍按自然网格，周期长度不变，重启也不会退回上一轮。
func (s *Server) handleBaitAutoReroll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	next := s.autoRotationRound(ctx) + 1
	if err := s.db.TunableSet(ctx, baitAutoRoundTunable, strconv.FormatInt(next, 10), adminOf(r)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "bait_auto_reroll", baitAutoRoundTunable, strconv.FormatInt(next, 10))
	// 判鱼侧的 run 参数有 30 秒缓存，主动推一条让它立刻重读。
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_tunables"}))
	writeJSON(w, map[string]any{"ok": true, "round": next})
}

// applyFishConfigBaitPick 给开了自动轮换的鱼种补上「本期抽到哪几款饵」。
//
// 钓场页原先只写一句「按稀有度每期重抽」，看不出版本期内到底开什么饵。
// 抽签必须与判鱼完全一致（同一份规则、同一份池子、同一个时间桶 + 人工偏移），
// 所以这里直接调 gameserver 的导出函数，而不是让前端自己再算一遍。
// 只对 BaitAuto 的行构池，单个水域几十条，成本与「鱼饵偏好」页同一套逻辑。
func (s *Server) applyFishConfigBaitPick(ctx context.Context, cfg *store.AdminFishConfig) {
	if cfg == nil || len(cfg.Fish) == 0 {
		return
	}
	anyAuto := false
	for i := range cfg.Fish {
		if cfg.Fish[i].BaitAuto {
			anyAuto = true
			break
		}
	}
	if !anyAuto {
		return
	}
	baits := gameserver.LoadAllRuleBaits(ctx, s.db)
	// 覆盖按归一后的鱼名建表（与判鱼 resolveBaitRule 的查法一致）；
	// 停用的覆盖不参与判鱼，这里也不能拿来算抽签。
	overrides := map[string]store.FishBaitOverride{}
	if list, err := s.db.ListFishBaitOverrides(ctx); err == nil {
		for _, o := range list {
			if o.Enabled {
				overrides[gameserver.ExportedBaitRuleKey(o.FishZh)] = o
			}
		}
	}
	sec := s.autoRotationSec(ctx)
	round := s.autoRotationRound(ctx)
	globalOn := s.autoRotationGlobalOn(ctx)
	now := time.Now()
	for i := range cfg.Fish {
		row := &cfg.Fish[i]
		if !row.BaitAuto {
			continue
		}
		allow, deny, bottom := baitRuleTerms(row.NameZh, overrides)
		st := gameserver.ExportedAutoRotation(row.FishKey, baits, allow, deny, bottom,
			row.RarityTier, sec, round, now)
		row.BaitPick = &store.BaitAutoPick{
			Enabled:     st.Enabled,
			GlobalOff:   !globalOn,
			Quota:       st.Quota,
			PoolSize:    st.PoolSize,
			ActiveBaits: st.ActiveBaits,
			NextSwitch:  st.NextSwitch,
			IntervalSec: sec,
		}
	}
}

// baitRuleTerms 取该鱼种当前生效的 allow/deny/bottom：启用的覆盖优先，
// 否则回落内置规则表（与判鱼 resolveBaitRule 同口径）。
func baitRuleTerms(nameZh string, overrides map[string]store.FishBaitOverride) (allow, deny, bottom []string) {
	if o, ok := overrides[gameserver.ExportedBaitRuleKey(nameZh)]; ok {
		return o.Allow, o.Deny, o.BottomOnly
	}
	if info, ok := gameserver.ExportedBaitRules(nameZh); ok {
		return info.Allow, info.Deny, info.BottomOnly
	}
	return nil, nil, nil
}

// fishRarity 取鱼种固有稀有度（0 普通 / 1 稀有 / 2 超稀有）。
// fish_ecology 以 fish_key 为主键（水域覆盖在 fish_ecology_loc），一行即可。
func (s *Server) fishRarity(ctx context.Context, fishKey string) int {
	var v int
	if err := s.db.SQL.QueryRowContext(ctx,
		`SELECT rarity_tier FROM fish_ecology WHERE fish_key=?`, fishKey).Scan(&v); err != nil {
		return 0
	}
	return v
}

func cleanTerms(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// baitShareMinCount 是后台占比展示的最小样本口径：官服纪录里单饵样本量
// 低于该值的比例不计入（默认 5，可用 tunable 覆盖）。该数据只作参考展示，
// 不参与判鱼。
func (s *Server) baitShareMinCount(ctx context.Context) int {
	var v string
	if err := s.db.SQL.QueryRowContext(ctx,
		`SELECT value FROM tunables WHERE key='bait_share_min_count'`).Scan(&v); err != nil {
		return 5
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 5
	}
	return n
}
