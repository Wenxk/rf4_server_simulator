package admin

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"rf4server/internal/gameserver"
	"rf4server/internal/store"
)

// baitPrefRow 一条鱼的饵料偏好摘要（管理后台只读展示 + 可编辑覆盖）。
//
// 2026-11-24 裁剪：删掉「偏好口（按类）」「饵效证据」「吃不吃」三列的字段。
// 三者都不参与判鱼，只是后台展示：
//   - 偏好口来自 fish_ecology.diet_json，唯一运行时用途是咬钩节奏打分，
//     而 bite_pace_enabled（饵效控速总开关，默认 0）是关的；
//   - 吃不吃是 applyBiteVerdict 现算的一句话结论，不写回任何链路；
//   - 饵效证据（companion_bait_fish.tier）确实仍在护鱼网 E 值与鱼体重档位生效，
//     但那两处是运行时自带逻辑、不需要后台维护，故只从界面移除、计算保持不变。
type baitPrefRow struct {
	FishKey  string          `json:"fish_key"`
	NameZh   string          `json:"name_zh"`
	HasRule  bool            `json:"has_rule"`
	Allow    []string        `json:"allow"`       // 规则表"吃"列表
	Deny     []string        `json:"deny"`        // 规则表"不吃"列表
	BottomOh []string        `json:"bottom_only"` // 底钓只吃
	Cond     string          `json:"condition"`
	Official []baitPrefShare `json:"official"`  // 官方纪录里的饵料占比（rf4records_catch），仅参考
	InStrict bool            `json:"in_strict"` // 严格模式下是否还能咬（只看 allow）

	// 鱼口画像（fish_ecology，与判鱼 G 层同表同源）。
	// 2026-11-25：水层（Layer）连「水域」（Locs）一起搬到「生态基线」页签，
	// 两者都不是饵料属性。这里保留字段但不再输出 Locs——需要时从
	// /api/fish/ecology-species 取，那里是按鱼种维度展示的正主。
	Layer  [3]float64 `json:"layer"`  // min/pref/max（0水面~1水底），仅编辑器对照用
	QualG  int64      `json:"qual_g"` // 合格档体重（钩容量适配计算用）
	TrophG int64      `json:"trophy_g"`

	// 可编辑覆盖：source=override 表示该鱼已被后台规则覆盖。
	Source       string                   `json:"source"` // default | override
	Enabled      bool                     `json:"enabled"`
	AutoRotation bool                     `json:"auto_rotation"`
	Rotation     gameserver.RotationState `json:"rotation"`
}

// baitPrefShare 官方渔获纪录中该饵占该鱼的比例，仅作参考展示。
type baitPrefShare struct {
	BaitID string  `json:"bait_id"`
	Pct    float64 `json:"pct"`
	Count  int     `json:"count"`
}

// handleBaitPrefs 鱼饵偏好总览：每条鱼一行，含规则表条目。
//
// 查询参数：
//
//	q     按 fish_key / 中文名模糊匹配
//	loc   只看某个水域出现过的鱼
//	limit 默认 400，最多 2000
func (s *Server) handleBaitPrefs(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	loc := strings.TrimSpace(r.URL.Query().Get("loc"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 400
	}
	like := "%" + q + "%"

	// 1) 鱼清单 + 中文名（优先 wiki 名，退回 loc_fish.name_zh）
	rows, err := s.db.SQL.QueryContext(r.Context(), `
		SELECT x.fish_key, COALESCE(x.name_zh, x.fish_key)
		FROM (
			SELECT w.fish_key,
			       COALESCE(MAX(NULLIF(w.fish_zh,'')), MAX(NULLIF(l.name_zh,'')), w.fish_key) AS name_zh
			FROM loc_wiki_fish w
			LEFT JOIN loc_fish l ON l.fish_key = w.fish_key
			WHERE (? = '' OR w.fish_key LIKE ? OR w.fish_zh LIKE ?)
			  AND (? = '' OR w.loc = ?)
			GROUP BY w.fish_key
		) x
		ORDER BY x.fish_key
		LIMIT ?`, q, like, like, loc, loc, limit)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	type fishEntry struct{ key, name string }
	fish := make([]fishEntry, 0, 256)
	for rows.Next() {
		var k, n string
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			httpError(w, 500, err.Error())
			return
		}
		fish = append(fish, fishEntry{k, n})
	}
	rows.Close()

	// 2) 官方饵料占比：rf4records_catch 聚合，仅作参考展示，不参与判鱼
	// （按鱼算分母，单饵样本量低于 bait_share_min_count 的不计入）。
	shareByFish := map[string][]baitPrefShare{}
	minCount := s.baitShareMinCount(r.Context())
	if shRows, serr := s.db.SQL.QueryContext(r.Context(), `
		SELECT fish_key, bait_id, cnt, tot FROM (
			SELECT fish_key, bait_id, count(*) AS cnt,
			       sum(count(*)) OVER (PARTITION BY fish_key) AS tot
			FROM rf4records_catch WHERE fish_key <> '' AND bait_id <> ''
			GROUP BY fish_key, bait_id
		) WHERE cnt >= ?`, minCount); serr == nil {
		type pair struct {
			fk, bait string
			pct      float64
			cnt      int
		}
		all := map[string][]pair{}
		for shRows.Next() {
			var fk, bait string
			var cnt, tot int
			if shRows.Scan(&fk, &bait, &cnt, &tot) == nil && tot > 0 {
				all[fk] = append(all[fk], pair{fk, bait, 100 * float64(cnt) / float64(tot), cnt})
			}
		}
		shRows.Close()
		for fk, list := range all {
			sort.Slice(list, func(i, j int) bool { return list[i].pct > list[j].pct })
			if len(list) > 8 {
				list = list[:8]
			}
			for _, p := range list {
				shareByFish[fk] = append(shareByFish[fk], baitPrefShare{BaitID: p.bait, Pct: mathRound1(p.pct), Count: p.cnt})
			}
		}
	}

	// 3) 水域归属已移到「生态基线」页签（fish_ecology_species.go 查同一张表），
	//    这里不再重复查——同一份 loc_wiki_fish 扫两遍，2000 行时是白花的时间。

	// 4) 后台覆盖：有覆盖时以覆盖为准，并把轮换状态一起带出去。
	// 覆盖表主键是 fish_key（00083），按 key 索引，不再按中文名。
	overrides := map[string]store.FishBaitOverride{}
	if list, oerr := s.db.ListFishBaitOverrides(r.Context()); oerr == nil {
		for _, o := range list {
			overrides[o.FishKey] = o
		}
	}
	// 4.5) 自动轮换开关（按 fish_key，与覆盖解耦）：没有覆盖的鱼也能开。
	autoOn := map[string]bool{}
	if autoRows, aerr := s.db.ListFishBaitAuto(r.Context()); aerr == nil {
		for _, a := range autoRows {
			if a.Enabled {
				autoOn[a.FishKey] = true
			}
		}
	}
	// 3.5) 水层画像（fish_ecology，判鱼 G 层同源）+ 档位体重（fish_grade_lines）
	layerByFish := map[string][3]float64{}
	if ecoRows, eerr := s.db.SQL.QueryContext(r.Context(),
		`SELECT fish_key, layer_min, layer_pref, layer_max FROM fish_ecology`); eerr == nil {
		for ecoRows.Next() {
			var fk string
			var lmin, lpref, lmax float64
			if ecoRows.Scan(&fk, &lmin, &lpref, &lmax) == nil {
				layerByFish[fk] = [3]float64{lmin, lpref, lmax}
			}
		}
		ecoRows.Close()
	}
	gradeByFish := map[string][2]int64{}
	if gRows, gerr := s.db.SQL.QueryContext(r.Context(),
		`SELECT fish_key, qualified_g, trophy_g FROM fish_grade_lines`); gerr == nil {
		for gRows.Next() {
			var fk string
			var q, t int64
			if gRows.Scan(&fk, &q, &t) == nil {
				gradeByFish[fk] = [2]int64{q, t}
			}
		}
		gRows.Close()
	}

	// 自动轮换要把 allow 展开成具体饵，全量饵定义只加载一次；否则 200 条鱼
	// 各查一遍 item_def（每遍 4000+ 行）会把后台列表拖成几秒。
	var autoBaits gameserver.RuleBaitList
	globalAutoOn := false
	autoRound := int64(0)
	if len(autoOn) > 0 {
		autoBaits = gameserver.LoadAllRuleBaits(r.Context(), s.db)
		globalAutoOn = s.autoRotationGlobalOn(r.Context())
		// 人工「立即重抽」的偏移，必须和判鱼侧用同一个值，否则表格显示的是上一轮。
		autoRound = s.autoRotationRound(r.Context())
	}

	now := time.Now()
	out := make([]baitPrefRow, 0, len(fish))
	for _, f := range fish {
		row := baitPrefRow{FishKey: f.key, NameZh: f.name, Source: "default"}
		if l, ok := layerByFish[f.key]; ok {
			row.Layer = l
		}
		if g, ok := gradeByFish[f.key]; ok {
			row.QualG, row.TrophG = g[0], g[1]
		}
		if info, ok := gameserver.ExportedBaitRules(f.name); ok {
			row.HasRule = true
			row.Allow = info.Allow
			row.Deny = info.Deny
			row.BottomOh = info.BottomOnly
			row.Cond = info.Condition
		}
		if o, ok := overrides[f.key]; ok {
			row.Source, row.Enabled = "override", o.Enabled
			row.HasRule = true
			row.Allow, row.Deny = o.Allow, o.Deny
			row.BottomOh, row.Cond = o.BottomOnly, o.Condition
		}
		row.AutoRotation = autoOn[f.key]
		// 自动轮换：本期开口饵由同一套 exported 抽签算出；手工轮换组已删除。
		if row.AutoRotation {
			row.Rotation = gameserver.ExportedAutoRotation(f.key, autoBaits,
				row.Allow, row.Deny, row.BottomOh, s.fishRarity(r.Context(), f.key),
				s.autoRotationSec(r.Context()), autoRound, now)
			// 逐鱼开关勾了、但总开关没开：抽签结果照算，游戏内不生效。
			if !globalAutoOn {
				row.Rotation.GlobalOff = true
			}
		}
		row.Official = shareByFish[f.key]
		// 严格模式判定：覆盖行被停用时该鱼不参与；有 allow 才可能在严格模式下开口。
		// tier（饵效实证）仍在护鱼网与体重档位生效，但那是运行时自带逻辑、
		// 后台不维护，也不参与这一判定，所以这里只看 allow。
		if row.Source == "override" && !row.Enabled {
			row.InStrict = false
		} else {
			row.InStrict = len(row.Allow) > 0
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FishKey < out[j].FishKey })

	writeJSON(w, map[string]any{
		"total":         len(out),
		"rule_count":    gameserver.ExportedBaitRuleCount(),
		"rows":          out,
		"rules_enabled": s.baitRulesEnabled(r.Context()),
		// 整类规则词：钓场页要把「颗粒」这类词标成「整类」，与具体饵名区分开。
		"type_terms": gameserver.ExportedBaitTypeTerms(),
		"now":        now.Unix(),
	})
}

func mathRound1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
