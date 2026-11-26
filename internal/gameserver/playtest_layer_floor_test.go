package gameserver

// 水层地板参数（bite_gate_layer_floor）扫描：量化「超活跃水层被捕获」的概率，
// 并反推该参数的设定是否合理。
//
// 方法不靠采样猜：判鱼链每一层都由生产代码写进 fishRollTrace 快照，
// 这里读 trace 的 final 层（真正参与抽签的权重向量），按生产函数
// cachedFishEcology + rigWaterLayer 判定每个候选是否超出该鱼种的活跃水层，
//
//	超层抽签概率 = Σ(超界候选 final 权重) / Σ(全部 final 权重)
//
// 这是**精确值**，不是抽样估计——同一 (方案, 钓层, 游戏小时) 只判一次鱼就够，
// 因此可以把 24 小时 × 多水层 × 多地板值 全组合跑完。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestLayerFloorSweep -v -timeout 30m
//
// 产出 data/playtest/layer_floor_sweep.json

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"rf4server/internal/store"
)

// probeCase 一次权重探针的配置。
type probeCase struct {
	Name    string
	Rod     string
	RigType int64
	Loc     string
	Bait    string
	Seq     []string
}

// layerProbe 单个 (方案 × 钓层 × 游戏小时 × 地板值) 的权重画像。
type layerProbe struct {
	Case  string  `json:"case"`
	Layer string  `json:"layer"`
	Hour  int64   `json:"game_hour"`
	Floor float64 `json:"floor_given"`

	Target    float64 `json:"target_layer"`
	WaterTemp float64 `json:"water_temp_c"`

	// 候选分类（按生产判定）
	TotalW float64 `json:"total_weight"`
	OutW   float64 `json:"outlay_weight"`
	InW    float64 `json:"inlayer_weight"`
	OutP   float64 `json:"outlay_share"`

	// 明细
	OutKeys []string `json:"outlay_keys,omitempty"`
	InKeys  []string `json:"inlayer_keys,omitempty"`
	OutN    int      `json:"outlay_n"`
	InN     int      `json:"inlayer_n"`
	AllowN  int      `json:"allow_n"`

	// 地板倍数实测：从 trace 的 roster→gate 权重比值反推
	// （1.0=未被裁，<1=被裁，用于交叉验证自算判定没有走偏）
	GateCutKeys []string `json:"gate_cut_keys,omitempty"`

	// 逐层存活明细：每个判鱼阶段之后「界内候选」还剩几个/多少权重。
	// 用来定位「本来有界内候选、后来被哪一层清掉」——这是 100% 超层的唯一成因。
	InByStage  map[string]int     `json:"in_by_stage"`
	InWByStage map[string]float64 `json:"in_w_by_stage"`
	// Stages 是本次实际打点的阶段名（zone / bait_tier 未介入时不出现）。
	Stages []string `json:"stages,omitempty"`
	// LastInStage 最后一个界内候选仍存活的阶段；InClearedBy 把它清零的那一层。
	LastInStage string `json:"last_in_stage,omitempty"`
	InClearedBy string `json:"in_cleared_by,omitempty"`
}

// TestPlaytestLayerFloorSweep 地板参数扫描。
func TestPlaytestLayerFloorSweep(t *testing.T) {
	playtestEnabled(t)
	srv, db, _ := newPlayerTest(t)
	playClearBiteOverrides(t, db)
	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0", "floor-sweep"); err != nil {
		t.Fatal(err)
	}
	// 记下原始地板值，结束时还原。
	// ⚠️ 必须区分「原本没有这行（走代码默认 0.01）」与「原本有值」：
	// 直接把读到的空串写回去会留下 value="" 的脏行，下一次跑测试
	// readTunable 就会读成"存在"，于是"还原"把脏值固化下来。
	origFloor, hadFloor := readTunable(t, db, "bite_gate_layer_floor")
	origGate, hadGate := readTunable(t, db, "bite_gate_enabled")
	t.Logf("原水层地板 bite_gate_layer_floor=%q（存在=%v） 原开关 bite_gate_enabled=%q（存在=%v）",
		origFloor, hadFloor, origGate, hadGate)
	t.Cleanup(func() {
		bg := context.Background()
		restoreTunable(bg, t, db, "bite_gate_layer_floor", origFloor, hadFloor)
		restoreTunable(bg, t, db, "bite_gate_enabled", origGate, hadGate)
		srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	})

	cases := []probeCase{
		{
			Name: "浮钓·红虫", Rod: "tele_10012_tl16", RigType: 1, Loc: "level_001_lake", Bait: "bloodworm",
			Seq: []string{"mono_15065", "bob_25165", "msinkset_30015", "orig_20008_14", "bloodworm"},
		},
		{
			Name: "浮钓·面包", Rod: "tele_10012_tl16", RigType: 1, Loc: "level_001_lake", Bait: "bread",
			Seq: []string{"mono_15065", "bob_25164", "msinkset_30015", "orig_20008_16", "bread"},
		},
		{
			Name: "路亚·旋转亮片", Rod: "spin_10001_S60L", RigType: 5, Loc: "level_001_lake", Bait: "spinner_2713",
			Seq: []string{"mono_15065", "spinner_2713"},
		},
	}

	// 地板取值：从「完全杜绝」到「完全不裁」，含当前默认 0.01
	floors := []float64{0, 0.001, 0.005, 0.01, 0.03, 0.05, 0.10, 0.20, 0.50, 1.0}

	// 每套方案先装配好，之后切换地板值只改 tunable，不重装
	type prepared struct {
		pc     probeCase
		rodGUID string
		roster int
	}
	var preps []prepared
	for _, cs := range cases {
		rod := playerItem(t, db, playUID, cs.Rod)
		if rod == nil {
			t.Logf("%s：找不到竿", cs.Name)
			continue
		}
		topUpBait(t, db, cs.Bait, 2000)
		clearRigForTest(t, srv, db, playUID, rod.GUID)
		if _, fail := assemble(t, srv, db, playUID, rod.GUID, cs.Seq); fail != "" {
			t.Logf("%s：装配失败 %s", cs.Name, fail)
			continue
		}
		topUpBait(t, db, cs.Bait, 2000)
		ctx := context.Background()
		statLoc := srv.cachedResolveStatLoc(ctx, fishRollContext{}, cs.Loc)
		roster, _ := srv.cachedLocRoster(ctx, fishRollContext{}, statLoc)
		t.Logf("%s：statLoc=%s 名册=%d 种", cs.Name, statLoc, len(roster))
		preps = append(preps, prepared{pc: cs, rodGUID: rod.GUID, roster: len(roster)})
	}
	if len(preps) == 0 {
		t.Fatal("没有任何方案装配成功")
	}

	var all []layerProbe
	for _, fl := range floors {
		if err := db.TunableSet(t.Context(), "bite_gate_enabled", "1", "floor-sweep"); err != nil {
			t.Fatal(err)
		}
		if err := db.TunableSet(t.Context(), "bite_gate_layer_floor", trimFloat(fl), "floor-sweep"); err != nil {
			t.Fatal(err)
		}
		srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
		conf := srv.biteGateConfig()
		if math.Abs(conf.layerFloor-fl) > 1e-9 || !conf.enabled {
			t.Fatalf("地板值未生效：写入 %.4f，读到 enabled=%v floor=%.4f", fl, conf.enabled, conf.layerFloor)
		}

		var round []layerProbe
		for _, p := range preps {
			for _, ls := range layerSpecsFor(p.pc.RigType) {
				for hr := int64(0); hr < 24; hr++ {
					if pr, ok := probeLayersAt(srv, p.pc, ls, hr, fl); ok {
						round = append(round, pr)
					}
				}
			}
		}
		all = append(all, round...)
		t.Logf("地板=%.3f 完成 %d 个探针", fl, len(round))
	}

	playWrite(t, "../../data/playtest", "layer_floor_sweep.json", all)
	reportLayerFloor(t, all, floors)

	// ---- 跨图普适性验证 ----
	// 单图结论不足以反推参数：克马羚诺也湖只有 20 种鱼，allow 结构可能是特例。
	// 这里换 5 张不同规模/生态的内陆图，只扫「当前地板 0.01」，看 100% 被迫超层
	// 是不是普遍现象，还是新手图的个案。
	sweepCrossMaps(t, srv, db)
}

// crossMap 一张待验证的地图。
type crossMap struct {
	Loc  string
	Note string
}

// sweepCrossMaps 在多张地图上复算「界内候选是否为空」。
// 不看具体鱼种，只看结构：这个 (图, 饵, 钓层) 组合下有没有任何界内合法候选。
func sweepCrossMaps(t *testing.T, srv *Server, db *store.DB) {
	t.Helper()
	const cur = 0.01
	if err := db.TunableSet(t.Context(), "bite_gate_enabled", "1", "cross-map"); err != nil {
		t.Fatal(err)
	}
	if err := db.TunableSet(t.Context(), "bite_gate_layer_floor", trimFloat(cur), "cross-map"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	// ⚠️ Loc 必须是 loc_alias 的 slug（level_xxx），中文名是 loc_alias.loc。
	// 直接传中文名会让 cachedResolveStatLoc 返回空 → 名册取不到 → 探针静默跳过。
	maps := []crossMap{
		{"level_001_lake", "新手图 20 种（基准）"},
		{"level_009_ahtuba", "阿赫图巴河 54 种，最大内陆图"},
		{"level_011_ladoga_02", "拉多加湖群岛 42 种"},
		{"level_008_sura_river", "苏拉河 37 种"},
		{"level_014_carp_lake", "琥珀湖 36 种"},
		{"level_010_belaya", "白河 29 种"},
	}
	baits := []struct{ Item, Note string }{
		{"bread", "面包（谷物料）"},
		{"bloodworm", "红虫（荤饵）"},
	}

	type row struct {
		Map     string  `json:"map"`
		MapNote string  `json:"map_note"`
		Bait    string  `json:"bait"`
		Layer   string  `json:"layer"`
		HoursOK int     `json:"hours_with_inlayer"`
		Hours   int     `json:"hours_total"`
		OutP    float64 `json:"mean_outlay_share"`
		Verdict string  `json:"verdict"`
	}
	var out []row

	t.Logf("")
	t.Logf("=== 跨图结构验证（地板 0.01，浮钓三水层 × 24 小时）===")
	t.Logf("%-16s %-12s %-10s %8s %10s  %s", "地图", "饵", "钓层", "界内小时", "超层占比", "判定")

	for _, m := range maps {
		for _, b := range baits {
			pc := probeCase{
				Name: m.Loc + "/" + b.Item, Rod: "tele_10012_tl16", RigType: 1,
				Loc: m.Loc, Bait: b.Item,
				Seq: []string{"mono_15065", "bob_25165", "msinkset_30015", "orig_20008_14", b.Item},
			}
			for _, ls := range layerSpecsFor(1) {
				ok, total := 0, 0
				var shareSum float64
				for hr := int64(0); hr < 24; hr++ {
					p, got := probeLayersAt(srv, pc, ls, hr, cur)
					if !got {
						continue
					}
					total++
					if p.InN > 0 {
						ok++
					}
					shareSum += p.OutP
				}
				if total == 0 {
					// 静默跳过是上一版的坑：地图名传错（中文名 vs slug）时
					// 名册取不到，探针一个都不产出，报告里却看不出缺了什么。
					t.Errorf("%s/%s/%s：24 小时全是空探针，检查地图 slug 是否可解析", m.Loc, b.Item, ls.Name)
					continue
				}
				mean := shareSum / float64(total)
				verdict := "正常"
				switch {
				case ok == 0:
					verdict = "⚠️ 全时段被迫超层"
				case ok <= total/2:
					verdict = "⚠️ 过半时段被迫超层"
				case mean > 0.3:
					verdict = "⚠️ 超层占比偏高"
				}
				r := row{Map: m.Loc, MapNote: m.Note, Bait: b.Item, Layer: ls.Name,
					HoursOK: ok, Hours: total, OutP: mean, Verdict: verdict}
				out = append(out, r)
				t.Logf("%-16s %-12s %-10s %5d/%-3d %9.2f%%  %s",
					m.Loc, b.Item, ls.Name, ok, total, mean*100, verdict)
			}
		}
	}
	playWrite(t, "../../data/playtest", "layer_floor_crossmap.json", out)
}

// probeLayersAt 在指定钓层与游戏小时判一次鱼，读 trace 的 final 权重向量，
// 按生产口径把候选分成「界内 / 超界」两堆。
func probeLayersAt(srv *Server, pc probeCase, ls layerSpec, hr int64, floorGave float64) (layerProbe, bool) {
	ctx := context.Background()
	at := gameTimeAt(hr)

	rc := fishRollContext{rigType: pc.RigType, depthCM: ls.DepthCM}
	target := srv.rigWaterLayer(rc)

	air := 15.0
	if v, ok := mapWeatherProfileForTest(pc.Loc, at); ok {
		air = v
	}
	_, temp := waterTempsAtLayer(air, hr, at.Unix(), target)

	rec := newFishRollTrace()
	c := fishRollContext{
		loc:     pc.Loc,
		uid:     playUID,
		now:     at,
		baits:   []string{pc.Bait},
		rigType: pc.RigType,
		depthCM: ls.DepthCM,
		forRig:  true,
		skill:   neutralFishingSkillBonus(),
		trace:   rec,
	}
	srv.rollFishFromDBWithRules(pc.Loc, pc.Bait, 0, c.skill, c)

	final := lastTraceStage(rec)
	if len(final) == 0 {
		return layerProbe{}, false
	}

	pr := layerProbe{
		Case: pc.Name, Layer: ls.Name, Hour: hr, Floor: floorGave,
		Target: target, WaterTemp: temp,
	}
	for key, w := range final {
		if w <= 0 {
			continue
		}
		pr.TotalW += w
		pr.AllowN++
		eco := srv.cachedFishEcology(ctx, c, key)
		lo, hi := eco.LayerMin-biteGateLayerTol, eco.LayerMax+biteGateLayerTol
		if target < lo || target > hi {
			pr.OutW += w
			pr.OutKeys = append(pr.OutKeys, key)
			pr.OutN++
		} else {
			pr.InW += w
			pr.InKeys = append(pr.InKeys, key)
			pr.InN++
		}
	}
	if pr.TotalW <= 0 {
		return layerProbe{}, false
	}
	pr.OutP = pr.OutW / pr.TotalW
	sort.Strings(pr.OutKeys)
	sort.Strings(pr.InKeys)
	pr.GateCutKeys = gateCutKeysFromTrace(rec)

	// 逐层归因：每个阶段之后界内候选的存活情况。
	// 注意 zone / bait_tier 两层只在命中条件时才 step，没打点就代表
	// 「这一层没介入」，不能当成「候选被清空」——用空切片区分。
	pr.InByStage = map[string]int{}
	pr.InWByStage = map[string]float64{}
	pr.Stages = nil
	for _, st := range rec.stages {
		n, w := 0, 0.0
		for key, v := range st.w {
			if v <= 0 {
				continue
			}
			eco := srv.cachedFishEcology(ctx, c, key)
			if target >= eco.LayerMin-biteGateLayerTol && target <= eco.LayerMax+biteGateLayerTol {
				n++
				w += v
			}
		}
		pr.InByStage[st.name] = n
		pr.InWByStage[st.name] = w
		pr.Stages = append(pr.Stages, st.name)
	}
	// 把界内候选被清零的那一刻定出来：第一个「打了点且界内=0」的阶段。
	for _, name := range pr.Stages {
		if pr.InByStage[name] > 0 {
			pr.LastInStage = name
		}
	}
	for i, name := range pr.Stages {
		if pr.InByStage[name] == 0 && i > 0 {
			pr.InClearedBy = name
			break
		}
	}
	return pr, true
}

// lastTraceStage 取 trace 最后一层的权重 map。
func lastTraceStage(t *fishRollTrace) map[string]float64 {
	if t == nil || len(t.stages) == 0 {
		return nil
	}
	return t.stages[len(t.stages)-1].w
}

// gateCutKeysFromTrace 从 roster→gate 两层的权重比值反推哪些候选被水层地板裁过。
// 纯交叉验证：若与 probeLayersAt 里自算的判定完全一致，说明判定口径没走偏。
func gateCutKeysFromTrace(t *fishRollTrace) []string {
	if t == nil {
		return nil
	}
	var roster, gate map[string]float64
	for _, st := range t.stages {
		switch st.name {
		case rollStageRoster:
			roster = st.w
		case rollStageGate:
			gate = st.w
		}
	}
	if roster == nil || gate == nil {
		return nil
	}
	var out []string
	for key, w0 := range roster {
		if w0 <= 0 {
			continue
		}
		if w1, ok := gate[key]; ok && w1 < w0-1e-12 {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// reportLayerFloor 汇总「地板值 → 超层抽签概率」曲线。
func reportLayerFloor(t *testing.T, all []layerProbe, floors []float64) {
	t.Helper()

	// ---- 速查表：当前默认 0.01 下，各 (方案, 钓层) 的超层概率 ----
	const cur = 0.01
	t.Logf("")
	t.Logf("=== 当前设定（地板 %.2f）下的超活跃水层捕获概率 ===", cur)
	t.Logf("%-16s %-10s %6s %6s %6s %8s %8s  %s",
		"方案", "钓层", "界内", "超界", "被测小时", "超层占比", "水温℃", "判定")

	type cell struct {
		caseName, layer string
		inN, outN       int
		hours           int
		shareSum        float64
		tempSum         float64
		alwaysOut       bool
		neverOut        bool
	}
	cells := map[string]*cell{}
	for _, p := range all {
		if math.Abs(p.Floor-cur) > 1e-9 {
			continue
		}
		k := p.Case + "|" + p.Layer
		c := cells[k]
		if c == nil {
			c = &cell{caseName: p.Case, layer: p.Layer, alwaysOut: true, neverOut: true}
			cells[k] = c
		}
		c.hours++
		c.shareSum += p.OutP
		c.tempSum += p.WaterTemp
		c.inN += p.InN
		c.outN += p.OutN
		if p.OutP < 0.999 {
			c.alwaysOut = false
		}
		if p.OutP > 0.001 {
			c.neverOut = false
		}
	}
	var keys []string
	for k := range cells {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := cells[k]
		verdict := "稀疏杂口"
		switch {
		case c.neverOut:
			verdict = "完全不出（地板彻底阻断）"
		case c.alwaysOut:
			verdict = "⚠️ 100% 被迫超层（无界内候选）"
		case c.shareSum/float64(c.hours) > 0.3:
			verdict = "⚠️ 偏高"
		case c.shareSum/float64(c.hours) > 0.1:
			verdict = "中等"
		}
		t.Logf("%-16s %-10s %6d %6d %8d %7.2f%% %7.1f  %s",
			c.caseName, c.layer, c.inN, c.outN, c.hours,
			c.shareSum/float64(c.hours)*100, c.tempSum/float64(c.hours), verdict)
	}

	// ---- 扫描曲线：地板值 → 平均超层占比 ----
	t.Logf("")
	t.Logf("=== 地板值扫描（24 游戏小时均值）===")
	// 按 (方案, 钓层) 分组，每个地板值给一个平均
	type curveKey struct{ c, l string }
	curves := map[curveKey]map[float64][]float64{}
	for _, p := range all {
		ck := curveKey{p.Case, p.Layer}
		m := curves[ck]
		if m == nil {
			m = map[float64][]float64{}
			curves[ck] = m
		}
		m[p.Floor] = append(m[p.Floor], p.OutP)
	}
	var cks []curveKey
	for k := range curves {
		cks = append(cks, k)
	}
	sort.Slice(cks, func(i, j int) bool {
		if cks[i].c != cks[j].c {
			return cks[i].c < cks[j].c
		}
		return cks[i].l < cks[j].l
	})

	// 表头
	hdr := fmt.Sprintf("%-16s %-10s", "方案", "钓层")
	for _, fl := range floors {
		hdr += fmt.Sprintf(" %7s", trimFloat(fl))
	}
	t.Logf("%s", hdr)
	for _, ck := range cks {
		line := fmt.Sprintf("%-16s %-10s", ck.c, ck.l)
		for _, fl := range floors {
			vs := curves[ck][fl]
			if len(vs) == 0 {
				line += fmt.Sprintf(" %7s", "-")
				continue
			}
			var s float64
			for _, v := range vs {
				s += v
			}
			line += fmt.Sprintf(" %6.2f%%", s/float64(len(vs))*100)
		}
		t.Logf("%s", line)
	}

	// ---- 关键分组：有界内候选 vs 无界内候选 ----
	t.Logf("")
	t.Logf("=== 结构性对比（当前 0.01）===")
	var hasIn, noIn []layerProbe
	for _, p := range all {
		if math.Abs(p.Floor-cur) > 1e-9 {
			continue
		}
		if p.InN > 0 {
			hasIn = append(hasIn, p)
		} else {
			noIn = append(noIn, p)
		}
	}
	summarizeOutlay(t, "有界内合法候选", hasIn)
	summarizeOutlay(t, "无界内合法候选（被迫超层）", noIn)

	// 地板有效性：在「有界内候选」的场景下，地板值是否线性生效
	t.Logf("")
	t.Logf("=== 地板有效性检验（只统计有界内候选的探针）===")
	t.Logf("%-8s %10s %10s %10s", "地板", "界内权重", "超界权重", "超层占比")
	for _, fl := range floors {
		var inW, outW float64
		n := 0
		for _, p := range all {
			if math.Abs(p.Floor-fl) > 1e-9 || p.InN == 0 {
				continue
			}
			inW += p.InW
			outW += p.OutW
			n++
		}
		if n == 0 {
			t.Logf("%-8s %10s %10s %10s", trimFloat(fl), "-", "-", "-")
			continue
		}
		tot := inW + outW
		share := 0.0
		if tot > 0 {
			share = outW / tot * 100
		}
		t.Logf("%-8.3f %10.2e %10.2e %9.3f%%", fl, inW, outW, share)
	}
}

func summarizeOutlay(t *testing.T, title string, ps []layerProbe) {
	t.Helper()
	if len(ps) == 0 {
		t.Logf("%s：无样本", title)
		return
	}
	var shares []float64
	var outW, inW float64
	for _, p := range ps {
		shares = append(shares, p.OutP)
		outW += p.OutW
		inW += p.InW
	}
	sort.Float64s(shares)
	tot := outW + inW
	agg := 0.0
	if tot > 0 {
		agg = outW / tot * 100
	}
	t.Logf("%s：%d 个探针，超层占比 均值 %.2f%% / 中位 %.2f%% / 最小 %.2f%% / 最大 %.2f%%（合并权重口径 %.2f%%）",
		title, len(ps), meanOf(shares)*100, quantile(shares, .5)*100,
		shares[0]*100, shares[len(shares)-1]*100, agg)
}

// readTunable 读原始 tunable 值（区分「存在」与「缺失走代码默认」）。
func readTunable(t *testing.T, db *store.DB, key string) (string, bool) {
	t.Helper()
	var v string
	err := db.SQL.QueryRow(`SELECT value FROM tunables WHERE key=?`, key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// restoreTunable 精确还原：原本存在的写回原值，原本不存在的删掉这行，
// 让下轮读取重新走代码默认。留 value="" 的脏行会污染后续所有测试。
// restoreTunable 精确还原一个 tunable：value 与 updated_by 两列都恢复原样。
//
// ⚠️ 不能用 TuableSet(key, val, "restore")：那会把 updated_by 留成 "restore"，
// 值虽然对了，库却已经不是原状（第一阶段踩过，靠手工修库才清掉）。
//
// existed=false 时必须 DELETE：写回空串会留下 value="" 的脏行，
// 下次 readTunable 读成「存在」，于是把脏值当成原值固化下来。
func restoreTunable(ctx context.Context, t *testing.T, db *store.DB, key, val string, existed bool) {
	t.Helper()
	if !existed {
		if _, err := db.SQL.ExecContext(ctx, `DELETE FROM tunables WHERE key=?`, key); err != nil {
			t.Logf("清理 %s 失败: %v", key, err)
		}
		return
	}
	// 原 updated_by 也一并还原；取不到就退回 admin（建库时的来源）。
	// 列名是 updated_ts（REAL，不是 updated_at）—— 写错列名会让 UPDATE 报
	// "no such column"，而错误被下面的 t.Logf 吞掉，表现为「还原静默失效」。
	var by string
	_ = db.SQL.QueryRowContext(ctx, `SELECT COALESCE(updated_by,'') FROM tunables WHERE key=?`, key).Scan(&by)
	if by == "" || by == "restore" || by == "floor-sweep" || by == "fishability" {
		by = "admin"
	}
	if _, err := db.SQL.ExecContext(ctx,
		`UPDATE tunables SET value=?, updated_by=? WHERE key=?`, val, by, key); err != nil {
		t.Logf("还原 %s 失败: %v", key, err)
	}
}

// trimFloat 把浮点写成不带多余零的短串（写 tunable 用）。
func trimFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = time.Now