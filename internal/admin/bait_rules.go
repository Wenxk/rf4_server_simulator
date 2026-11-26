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
	FishKey    string   `json:"fish_key"`
	FishZh     string   `json:"fish_zh"`
	Allow      []string `json:"allow"`
	Deny       []string `json:"deny"`
	BottomOnly []string `json:"bottom_only"`
	Condition  string   `json:"condition"`
	Enabled    bool     `json:"enabled"`
}

// handleBaitRuleSave 保存一个鱼种的饵料规则覆盖。
// 主键是 fish_key（00083 迁移）；只传 fish_zh 时由 store 层反查补齐。
func (s *Server) handleBaitRuleSave(w http.ResponseWriter, r *http.Request) {
	var req baitRuleSaveReq
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	fishKey := strings.TrimSpace(req.FishKey)
	fishZh := strings.TrimSpace(req.FishZh)
	if fishKey == "" && fishZh == "" {
		httpError(w, 400, "缺少鱼种")
		return
	}
	if len([]rune(fishZh)) > 40 {
		httpError(w, 400, "鱼种名过长")
		return
	}
	ov := store.FishBaitOverride{
		FishKey:    fishKey,
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
		httpError(w, 400, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "bait_rule_save", ov.FishKey,
		"allow="+strconv.Itoa(len(ov.Allow))+" deny="+strconv.Itoa(len(ov.Deny))+
			" enabled="+strconv.FormatBool(ov.Enabled))
	_ = s.db.QueuePush(ctx, mustJSON(map[string]any{"op": "reload_bait_rules"}))
	writeJSON(w, map[string]any{"ok": true, "fish_key": ov.FishKey})
}

// handleBaitRuleReset 恢复默认：body 带 fish_key（或 fish_zh）删单条，带 all=true 清整表。
func (s *Server) handleBaitRuleReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FishKey string `json:"fish_key"`
		FishZh  string `json:"fish_zh"`
		All     bool   `json:"all"`
	}
	if err := readJSON(r, &req); err != nil {
		req = struct {
			FishKey string `json:"fish_key"`
			FishZh  string `json:"fish_zh"`
			All     bool   `json:"all"`
		}{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var n int64
	var err error
	target := strings.TrimSpace(req.FishKey)
	if req.All {
		n, err = s.db.DeleteAllFishBaitOverrides(ctx)
		target = "*"
	} else {
		if target == "" && strings.TrimSpace(req.FishZh) != "" {
			// 兼容只传中文名的旧调用方：先反查 fish_key。
			if key, _, rerr := s.db.LookupFishKeyByName(ctx, req.FishZh); rerr == nil {
				target = key
			}
		}
		if target == "" {
			httpError(w, 400, "缺少鱼种")
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

// baitCategoryLabel 是大类节点的展示名。
//
// 必须与写进规则的「大类规则词」一致：以前这里硬编码返回「饵料」，
// 而按钮上写的是游戏菜单原文「鱼饵」，同一屏出现两套叫法，
// 用户会以为「饵料」和「鱼饵」是两个不同范围。现在统一取词表正式名。
// 「饵料」只作为旧写法别名保留（bait_type_terms.go 的 Alias），不再当展示名。
func baitCategoryLabel(category int64) string {
	if s, ok := gameserver.ExportedBaitCategoryTerm(category); ok {
		return s
	}
	return "其他"
}

type baitCatalogEntry struct {
	ItemID   string `json:"item_id"`
	NameZh   string `json:"name_zh"`
	Category int64  `json:"category"`
	Sub      int64  `json:"sub"`
	SubLabel string `json:"sub_label"`
	// Brand 是产品系列键（`zmn_spiker2` / `hvz_boils_pp_van`），层级选择器的第三层。
	// 同一 brand 下是同一款产品的不同规格/颜色。
	Brand string `json:"brand"`
	// Kind 是品牌名（`zeiman` / `sorvik` / `Handmade`），层级选择器的第二层。
	// 天然饵（cat=27）大多没有品牌（kind 为空），只有商品饵才有。
	Kind string `json:"kind"`
	// Base 是去掉尾部规格号的基础名（`Aikkila 4g` / `香子兰`），层级选择器的第四层。
	Base string `json:"base"`
	// Shape 是形态词（只有 baitType=8 有）：sink=下沉 / popup=Pop-up。
	Shape string `json:"shape,omitempty"`
	// Unused 表示这款饵在游戏里拿不到（不在任何商店、无制作配方）：
	// baitType=1 里那 6 个香煮成饵基料 + 榨饼。规则编辑器里默认折叠。
	Unused bool `json:"unused,omitempty"`
}

// loadBaitCatalog 读取全部饵料/拟饵及其二级分类，供选择器与分类树使用。
func (s *Server) loadBaitCatalog(ctx context.Context) ([]baitCatalogEntry, error) {
	// 一次性把这些饵的「能否获得」也算出来：在售 或 有配方 就算能拿到。
	// baitType=1 有 7 款孤立数据（六色香煮成饵基料 + 榨饼），游戏里不可见，
	// 放进选择器只会让人以为能钓，见 docs/analysis-bait-category-taxonomy §2.2。
	obtainable := map[string]bool{}
	for _, q := range []string{
		`SELECT DISTINCT item_id FROM shop_item`,
		`SELECT DISTINCT output_id FROM recipe WHERE output_id IS NOT NULL AND output_id<>''`,
	} {
		rows, err := s.db.SQL.QueryContext(ctx, q)
		if err != nil {
			continue // 拿不到就都按「能获得」处理，不因辅助信息缺失而藏饵
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && id != "" {
				obtainable[id] = true
			}
		}
		rows.Close()
	}
	haveObtain := len(obtainable) > 0

	rows, err := s.db.SQL.QueryContext(ctx, `
		SELECT d.item_id, COALESCE(c.name_zh,c.name,''), COALESCE(d.category,0),
		       COALESCE(d.brand,''), COALESCE(d.kind,''), COALESCE(d.attrs_json,'{}')
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
		if err := rows.Scan(&e.ItemID, &e.NameZh, &e.Category, &e.Brand, &e.Kind, &raw); err != nil {
			return nil, err
		}
		var a attrs
		_ = json.Unmarshal([]byte(raw), &a)
		if e.Category == 17 {
			e.Sub = a.LureType
			// 拟饵没有形态细分，靠 lureType 区分的已经是最终分类。
		} else {
			e.Sub = a.BaitType
			if a.BaitType == 8 {
				if gameserver.ExportedBoilIsPopUp(e.Brand) {
					e.Shape = "popup"
				} else {
					e.Shape = "sink"
				}
			}
		}
		e.SubLabel = baitSubLabel(e.Category, e.Sub)
		e.Base = gameserver.ExportedBaitBaseName(e.NameZh)
		if haveObtain && !obtainable[e.ItemID] {
			e.Unused = true
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// handleBaitCategories 返回「大类 → 二级分类 → 品牌 → 型号」树，带每层的数量。
//
// 层级设计对应游戏里的选择方式（用户 2026-11-25 的要求）：
// 拟饵 → 旋转亮片 → 品牌（Zeiman / Syberia…）→ 型号（Spiker #2）→ 具体规格。
// 层级越深收束越窄，最后一层才是单个 item。
func (s *Server) handleBaitCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	all, err := s.loadBaitCatalog(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	type line struct {
		Base  string `json:"base"`
		Count int    `json:"count"`
		// Term 是写进规则的词：型号名本身（判鱼按名称匹配，见 ruleKey）。
		Term string `json:"term"`
		// ScopeTerm 是「带类别限定的写法」`颗粒@鱼子`。前端在「这个型号名跨了
		// 多个类别」时必须用它，否则写进去的词会连带命中别的类别下的同名饵。
		// 只有一个类别含该名时为空，前端仍用 Term（写法更短）。
		ScopeTerm string `json:"scope_term,omitempty"`
		// SharedWith 列出同名的其它类别，例如 ["香煮成饵","颗粒"]。
		// 前端据此提示「这个名字在 3 个类别里都有」，而不是让人以为自己点错了。
		SharedWith []string `json:"shared_with,omitempty"`
		// Unused 表示该型号下全部规格在游戏里都拿不到。
		Unused bool `json:"unused,omitempty"`
	}
	type brand struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Count int    `json:"count"`
		Lines []line `json:"lines"`
	}
	type shape struct {
		Shape string `json:"shape"`
		Label string `json:"label"`
		Term  string `json:"term"`
		Count int    `json:"count"`
	}
	type child struct {
		Sub   int64  `json:"sub"`
		Label string `json:"label"`
		Count int    `json:"count"`
		// Term 是「整类加入」时写进规则的词（如「颗粒」），前端按钮直接用它。
		Term string `json:"term"`
		// Shapes 是同一 Sub 内的形态子项（只有香煮成饵有：下沉 / Pop-up）。
		Shapes []shape `json:"shapes,omitempty"`
		// Brands 是第三层：品牌（拟饵才有），第四层是型号。
		Brands []brand `json:"brands,omitempty"`
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
	// 品牌 → 型号 的聚合要按（大类,子类,品牌,基础名）去重计数
	type bk struct {
		cat, sub int64
		kind     string
	}
	brandCount := map[bk]int{}
	brandLines := map[bk]map[string]*line{}
	brandOrder := map[bk][]string{}
	shapeCount := map[[2]int64]map[string]int{}
	// baseSubs 记录每个型号名出现在哪些二级类别里（键是 (大类, 型号名)）。
	// 型号名会跨类别重名：实测 25 个型号名横跨 ≥2 个二级类别——`鱼子` 在
	// 自然的/香煮成饵/颗粒 三处都有、`香子兰` 在 香煮成饵/颗粒/人造玉米 三处。
	// 前端必须能在重名时写出带限定的词（`颗粒@鱼子`），否则点「颗粒的鱼子」
	// 写进去的 `鱼子` 会把另外 11 款同名的也一起放行。
	baseSubs := map[[2]interface{}]map[int64]bool{}
	for _, e := range all {
		k := bk{e.Category, e.Sub, e.Kind}
		if brandLines[k] == nil {
			brandLines[k] = map[string]*line{}
		}
		brandCount[k]++
		ln := brandLines[k][e.Base]
		if ln == nil {
			ln = &line{Base: e.Base, Term: e.Base, Unused: true}
			brandLines[k][e.Base] = ln
			brandOrder[k] = append(brandOrder[k], e.Base)
		}
		ln.Count++
		if !e.Unused {
			ln.Unused = false
		}
		if e.Shape != "" {
			if shapeCount[[2]int64{e.Category, e.Sub}] == nil {
				shapeCount[[2]int64{e.Category, e.Sub}] = map[string]int{}
			}
			shapeCount[[2]int64{e.Category, e.Sub}][e.Shape]++
		}
		if e.Base != "" {
			// 按「去掉规格号后的型号名」聚合，同名型号才认得出来。
			gk := [2]interface{}{e.Category, e.Base}
			if baseSubs[gk] == nil {
				baseSubs[gk] = map[int64]bool{}
			}
			baseSubs[gk][e.Sub] = true
		}
	}

	// scopeFor 返回该型号名在「本类别」下是否需要用限定写法，
	// 以及它同时出现在哪几个其它类别（供前端提示）。
	//
	// sharedWith 里放的是**类别规则词**（「香煮成饵」）而不是分类树的 label
	// （「香煮成饵（下沉 + Pop-up）」）：提示语要短，且必须与 scope_term 的前缀
	// 是同一个词，否则用户看到「香煮成饵@鱼子」却被告知它跟「香煮成饵（下沉 + Pop-up）」
	// 重名，会以为是两回事。
	scopeFor := func(cat, sub int64, base string) (scopeTerm string, shared []string) {
		subs := baseSubs[[2]interface{}{cat, base}]
		if len(subs) < 2 {
			return "", nil // 只有一个类别含这个名字：不用限定，写法越短越好
		}
		// 术语必须是类别词（判鱼按类别词解析限定），拿不到就退回不限定的写法。
		term, ok := gameserver.ExportedBaitTypeTermOf(cat, sub)
		if !ok || term == "" {
			return "", nil
		}
		for s := range subs {
			if s == sub {
				continue
			}
			if other, ok := gameserver.ExportedBaitTypeTermOf(cat, s); ok && other != "" {
				shared = append(shared, other)
			}
		}
		sort.Strings(shared)
		return gameserver.JoinBaitScope(term, base), shared
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
			c := child{Sub: sub, Label: baitSubLabel(cat, sub), Count: counts[cat][sub], Term: term}
			// 形态子项（下沉 / Pop-up）
			for _, sh := range gameserver.ExportedBaitShapeTerms(cat, sub) {
				c.Shapes = append(c.Shapes, shape{
					Shape: sh.Shape, Label: sh.Label, Term: sh.Term,
					Count: shapeCount[[2]int64{cat, sub}][sh.Shape],
				})
			}
			// 品牌 → 型号（只给带品牌的；天然饵大多没有品牌，留空由前端直出型号）
			kinds := make([]string, 0, 16)
			for k := range brandLines {
				if k.cat == cat && k.sub == sub {
					kinds = append(kinds, k.kind)
				}
			}
			sort.Slice(kinds, func(i, j int) bool {
				a, b := brandCount[bk{cat, sub, kinds[i]}], brandCount[bk{cat, sub, kinds[j]}]
				if a != b {
					return a > b
				}
				return kinds[i] < kinds[j]
			})
			for _, kind := range kinds {
				k := bk{cat, sub, kind}
				bd := brand{Kind: kind, Label: kind, Count: brandCount[k]}
				if bd.Label == "" {
					bd.Label = "无品牌 / 手工"
				}
				for _, base := range brandOrder[k] {
					ln := *brandLines[k][base]
					// 跨类别同名时才填 ScopeTerm / SharedWith，其余留空。
					ln.ScopeTerm, ln.SharedWith = scopeFor(cat, sub, ln.Base)
					bd.Lines = append(bd.Lines, ln)
				}
				sort.SliceStable(bd.Lines, func(i, j int) bool {
					if bd.Lines[i].Count != bd.Lines[j].Count {
						return bd.Lines[i].Count > bd.Lines[j].Count
					}
					return bd.Lines[i].Base < bd.Lines[j].Base
				})
				c.Brands = append(c.Brands, bd)
			}
			n.Children = append(n.Children, c)
		}
		nodes = append(nodes, n)
	}
	// terms 是「按大类加入」按钮的完整清单：顺序、标签、规则词都由 gameserver 定，
	// 前端不再硬编码（以前硬编码 4 个通配词，删改一次就要改两处）。
	terms := make([]map[string]any, 0, 48)
	for _, t := range gameserver.ExportedBaitTypeTerms() {
		cnt := 0
		switch {
		case t.Cat == 0:
			cnt = totals[27] + totals[17]
		case len(t.Subs) == 0:
			cnt = totals[t.Cat]
		default:
			if t.Shape != "" {
				cnt = shapeCount[[2]int64{t.Cat, t.Subs[0]}][t.Shape]
			} else {
				for _, sub := range t.Subs {
					cnt += counts[t.Cat][sub]
				}
			}
		}
		terms = append(terms, map[string]any{
			"term": t.Term, "label": t.Label, "cat": t.Cat, "sub": baitTermSub(t),
			"shape": t.Shape, "count": cnt, "alias": t.Alias,
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

// handleBaitItems 给规则编辑器提供可选饵料清单（只读）。
// 支持按大类/二级分类/品牌/型号/关键字逐层收束：层级选择器的每一层
// 都只是把过滤条件往下叠，最终落到单个 item 或整个型号组。
func (s *Server) handleBaitItems(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	cat, _ := strconv.ParseInt(r.URL.Query().Get("cat"), 10, 64)
	sub, _ := strconv.ParseInt(r.URL.Query().Get("sub"), 10, 64)
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	brand := strings.TrimSpace(r.URL.Query().Get("brand"))
	base := strings.TrimSpace(r.URL.Query().Get("base"))
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
	// matched 是过滤后的命中总数，total 是入库总数。
	// 两者必须分开：前端层级选择器用 matched 显示「该型号共 N 个规格」，
	// 以前 total 直接返回 len(all)，无论怎么收窄都是 4123，会把提示写成「共 4123 个规格」。
	matched := 0
	for _, e := range all {
		if cat != 0 && e.Category != cat {
			continue
		}
		if sub != 0 && e.Sub != sub {
			continue
		}
		if kind != "" && e.Kind != kind {
			continue
		}
		if brand != "" && e.Brand != brand {
			continue
		}
		if base != "" && e.Base != base {
			continue
		}
		if lower != "" &&
			!strings.Contains(strings.ToLower(e.NameZh), lower) &&
			!strings.Contains(strings.ToLower(e.ItemID), lower) {
			continue
		}
		matched++
		// 计数要继续扫完，只截断返回的切片，保证 matched 是全量命中数。
		if len(out) < limit {
			out = append(out, e)
		}
	}
	writeJSON(w, map[string]any{"rows": out, "matched": matched, "total": len(all)})
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
