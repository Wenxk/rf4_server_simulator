package gameserver

// 判鱼合规审计：验证「上来的鱼是否都符合当前参数设定」。
//
// 不是核对参数值，而是拿真实判鱼产出的鱼去反查每一层硬约束：
//
//	L1 饵料 allow 规则（fish_bait_rules_enabled=1 → 严格 allow，名单外权重 0）
//	L2 硬性鱼口门水层地板（bite_gate_enabled=1 → 超界只乘 bite_gate_layer_floor）
//	L3 昼夜禁口（fish_diel_inactive_block=1 → 曲线 <0.5 整段禁口）
//	L4 适温响应（高斯，容差 max(2.2,(max-min)/2)）
//	L5 鱼群分布（distributionMultiplier：v<0.3 → 权重 0）
//
// 全部复用生产函数，不自算口径——自算就变成「用我的理解验证我的理解」。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestBiteLegality -v -timeout 60m
//
// 产出 data/playtest/bite_legality.json

import (
	"context"
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"rf4server/internal/store"
)

// legalCase 一种判鱼配置：地图 × 钓法 × 饵。
type legalCase struct {
	Name    string `json:"name"`
	Rod     string `json:"rod_item"`
	RigType int64  `json:"rig_type"`
	Loc     string `json:"loc"`
	Bait    string `json:"bait"`
	Seq     []string
}

// legalSample 一次判鱼的完整留证。
type legalSample struct {
	Case string `json:"case"`
	Loc  string `json:"loc"`
	Bait string `json:"bait"`
	Rig  int64  `json:"rig_type"`

	Hit bool `json:"hit"`

	// 命中时的鱼
	Species string `json:"species,omitempty"`
	Grade   int64  `json:"grade,omitempty"`
	WeightG int64  `json:"weight_g,omitempty"`

	// 判鱼上下文（命中/未命中都记）
	Layer     float64 `json:"layer"`
	WaterTemp float64 `json:"water_temp_c"`
	Hour      int64   `json:"game_hour"`
	BiteUnix  int64   `json:"bite_unix"`
	DepthCM   int64   `json:"depth_cm"`

	// 逐层判定
	EmptyReason string `json:"empty_reason,omitempty"`

	// 命中鱼的合规自检
	BaitRuleOK        bool    `json:"bait_rule_ok"`
	BaitRuleNote      string  `json:"bait_rule_note,omitempty"`
	LayerOK           bool    `json:"layer_ok"`
	LayerNote         string  `json:"layer_note,omitempty"`
	DielOK            bool    `json:"diel_ok"`
	DielNote          string  `json:"diel_note,omitempty"`
	TempResponse      float64 `json:"temp_response"`
	TempOK            bool    `json:"temp_ok"`
	Candidates        int     `json:"candidates"`
	RosterSize        int     `json:"roster_size"`
	ChosenWeightShare float64 `json:"chosen_weight_share"`
}

// TestPlaytestBiteLegality 大规模判鱼合规审计。
func TestPlaytestBiteLegality(t *testing.T) {
	playtestEnabled(t)
	srv, db, _ := newPlayerTest(t)
	playClearBiteOverrides(t, db)
	// 关掉 fixed，但不动任何判鱼开关——要测的就是当前生效的配置。
	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0", "legality"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	gates := biteGatesSnapshot(t, db)
	t.Logf("判鱼约束开关快照：%s", gates)

	ctx := context.Background()
	cases := []legalCase{
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

	const roundsPerCase = 120
	var samples []legalSample

	for _, cs := range cases {
		rod := playerItem(t, db, playUID, cs.Rod)
		if rod == nil {
			t.Logf("%s：找不到竿", cs.Name)
			continue
		}
		topUpBait(t, db, cs.Bait, 400)
		clearRigForTest(t, srv, db, playUID, rod.GUID)
		if _, fail := assemble(t, srv, db, playUID, rod.GUID, cs.Seq); fail != "" {
			t.Logf("%s：装配失败 %s", cs.Name, fail)
			continue
		}
		topUpBait(t, db, cs.Bait, 400)

		statLoc := srv.cachedResolveStatLoc(ctx, fishRollContext{}, cs.Loc)
		roster, _ := srv.cachedLocRoster(ctx, fishRollContext{}, statLoc)
		t.Logf("%s：statLoc=%s 名册=%d 种", cs.Name, statLoc, len(roster))

		// ✅ 关键：必须跨 24 游戏小时 × 多水层扫描。
		// 单点时间跑 120 轮只能验证「这一刻」的配置，昼夜/适温/分布三层
		// 约束都测不到（实测 360 次全落在同一游戏小时 1、同一水温 12.5℃）。
		// 每个 (小时, 水层) 组合跑 1 次，共 24 × 3 = 72 次覆盖全天。
		for _, layerSpec := range layerSpecsFor(cs.RigType) {
			for hr := int64(0); hr < 24; hr++ {
				// 保证钩上有饵
				if baits := srv.currentBaits(ctx, rod.GUID); len(baits) == 0 {
					if !mountBaitForPacing(t, srv, db, rod.GUID, cs.Bait) {
						t.Logf("%s：无法挂饵，提前结束", cs.Name)
						break
					}
				}
				s := rollOneLegalAt(srv, cs, rod.GUID, len(roster), hr, layerSpec)
				samples = append(samples, s)
			}
		}
		t.Logf("%s：完成 %d 轮（24 小时 × %d 水层）", cs.Name, 24*len(layerSpecsFor(cs.RigType)), len(layerSpecsFor(cs.RigType)))
	}

	playWrite(t, "../../data/playtest", "bite_legality.json", samples)
	reportLegality(t, samples, srv, db)
}

// layerSpec 一个水层测试点。
type layerSpec struct {
	DepthCM int64
	Name    string
}

// layerSpecsFor 按钓法给出要测的水层：浮钓扫浅/中/深三档，
// 路亚按拟饵名义泳层（由 effectiveLureLayer 决定，depthCM 不参与）。
func layerSpecsFor(rigType int64) []layerSpec {
	if rigTypeLureFamily[rigType] {
		return []layerSpec{{DepthCM: 0, Name: "拟饵泳层"}}
	}
	return []layerSpec{
		{DepthCM: 80, Name: "浅(0.16)"},
		{DepthCM: 200, Name: "中(0.40)"},
		{DepthCM: 465, Name: "底(0.93)"},
	}
}

// gameTimeAt 构造「今天第 hr 游戏小时」的时间戳。
// 判鱼用的 now 决定：昼夜曲线、鱼群分布 period、天气。
func gameTimeAt(hr int64) time.Time {
	const protocolEpoch = int64(1438387200)
	const realSecPerGH = int64(150)
	// 取当前所在游戏日的零点，再加 hr 个游戏小时
	now := time.Now().Unix()
	dayStart := protocolEpoch + ((now-protocolEpoch)/(24*realSecPerGH))*(24*realSecPerGH)
	return time.Unix(dayStart+hr*realSecPerGH+10, 0)
}

// rollOneLegalAt 在指定游戏小时与钓深下判一次鱼。
// 通过 fishRollContext 注入 now/depthCM，因此昼夜、适温、分布三层都真实生效。
func rollOneLegalAt(srv *Server, cs legalCase, rodGUID string, rosterSize int, hr int64, ls layerSpec) legalSample {
	ctx := context.Background()
	at := gameTimeAt(hr)

	s := legalSample{
		Case: cs.Name, Loc: cs.Loc, Bait: cs.Bait, Rig: cs.RigType,
		DepthCM: ls.DepthCM, BiteUnix: at.Unix(), Hour: hr,
		RosterSize: rosterSize,
	}

	// 与生产同口径算水温：layer 取该钓法实际判鱼水层
	rc := fishRollContext{rigType: cs.RigType, depthCM: ls.DepthCM}
	s.Layer = srv.rigWaterLayer(rc)
	air := 15.0
	if v, ok := mapWeatherProfileForTest(cs.Loc, at); ok {
		air = v
	}
	_, temp := waterTempsAtLayer(air, hr, at.Unix(), s.Layer)
	s.WaterTemp = temp

	bonus := neutralFishingSkillBonus()
	rollCtx := fishRollContext{now: at, depthCM: ls.DepthCM}
	f := srv.rollFishForRig(ctx, playUID, rodGUID, cs.Loc, cs.Bait, bonus, rollCtx)
	if f == nil {
		s.Hit = false
		s.EmptyReason = "判鱼返回 nil"
		return s
	}
	s.Hit = true
	s.Species, s.Grade, s.WeightG = f.Species, f.Grade, f.WeightG

	// ---- 合规自检，全部走生产函数 ----
	eco := srv.cachedFishEcology(ctx, fishRollContext{}, f.Species)

	// L1 饵料 allow
	s.BaitRuleOK, s.BaitRuleNote = baitRuleVerdict(ctx, srv, srv.DB, f.Species, cs.Bait)

	// L2 水层：硬性鱼口门超界只乘地板，不剔除 → 「超界但出鱼」是一种真实但
	// 极小概率的杂口。这里记录是否超界，用来核对地板倍数是否真的压低到预期。
	lo, hi := eco.LayerMin-biteGateLayerTol, eco.LayerMax+biteGateLayerTol
	s.LayerOK = s.Layer >= lo && s.Layer <= hi
	if !s.LayerOK {
		s.LayerNote = fmt.Sprintf("钓层%.2f 超鱼层[%.2f,%.2f]（±0.15容差）", s.Layer, eco.LayerMin, eco.LayerMax)
	}

	// L3 昼夜：直接走生产的 fishDielWeight
	dw := fishDielWeight(eco, hr)
	s.DielOK = dw >= 0.5
	if !s.DielOK {
		s.DielNote = fmt.Sprintf("%d时 昼夜权重%.2f<0.5 diel_mask=%d 曲线=%s", hr, dw, eco.DielMask, eco.DielCurve)
	}

	// L4 适温
	tol := math.Max(2.2, (eco.TempMaxC-eco.TempMinC)/2)
	z := (temp - eco.TempPrefC) / tol
	thermal := 0.35 + 0.65*math.Exp(-0.5*z*z)
	s.TempResponse = math.Max(0.15, math.Min(1.25, thermal))
	s.TempOK = s.TempResponse >= 0.5

	return s
}

// baitRuleVerdict 用生产规则判定这尾鱼 + 这款饵是否被 allow。
func baitRuleVerdict(ctx context.Context, srv *Server, db *store.DB, fishKey, baitItem string) (bool, string) {
	names, ok := srv.cachedFishNames(ctx, fishRollContext{})
	if !ok {
		return true, "鱼名表读取失败，跳过"
	}
	r := srv.resolveBaitRule(ctx, fishKey, names[fishKey])
	if len(r.allow) == 0 {
		return true, "该鱼种无 allow 名单（不受限）"
	}
	b, ok := srv.cachedBaitDefinition(ctx, fishRollContext{}, baitItem)
	if !ok {
		return true, "饵定义缺失，跳过"
	}
	if baitBlockedByRule(r, fishRollContext{}, b) {
		return false, fmt.Sprintf("饵「%s」被 deny", baitItem)
	}
	if !baitAllowedByRule(r, b) {
		return false, fmt.Sprintf("饵「%s」不在 allow 名单（%d 项）", baitItem, len(r.allow))
	}
	return true, ""
}

// biteGatesSnapshot 记录本次审计时生效的判鱼开关。
func biteGatesSnapshot(t *testing.T, db *store.DB) string {
	t.Helper()
	keys := []string{
		"fish_bait_rules_enabled", "fish_weather_enabled", "fish_distribution_enabled",
		"bite_gate_enabled", "bite_gate_layer_floor", "fish_diel_inactive_block",
		"fish_weight_cap", "hook_entry_cap", "groundbait_enabled",
	}
	var out string
	for _, k := range keys {
		var v string
		err := db.SQL.QueryRow(`SELECT value FROM tunables WHERE key=?`, k).Scan(&v)
		if err != nil {
			v = "(默认)"
		}
		out += fmt.Sprintf("%s=%s ", shortKey(k), v)
	}
	return out
}

func shortKey(k string) string {
	switch k {
	case "fish_bait_rules_enabled":
		return "饵料规则"
	case "fish_weather_enabled":
		return "天气"
	case "fish_distribution_enabled":
		return "鱼群分布"
	case "bite_gate_enabled":
		return "硬性鱼口门"
	case "bite_gate_layer_floor":
		return "水层地板"
	case "fish_diel_inactive_block":
		return "昼夜禁口"
	case "fish_weight_cap":
		return "重量上限"
	case "hook_entry_cap":
		return "钩入口上限"
	case "groundbait_enabled":
		return "打窝"
	}
	return k
}

// gameHourOf 游戏内小时（与 worldclock 同源）。
func gameHourOf(tm time.Time) int64 {
	const protocolEpoch = int64(1438387200)
	const realSecPerGH = int64(150)
	secs := tm.Unix() - protocolEpoch
	if secs < 0 {
		secs = 0
	}
	return (secs / realSecPerGH) % 24
}

// mapWeatherProfileForTest 取地图当前气温（后台同源，与判鱼用同一套模板）。
func mapWeatherProfileForTest(loc string, now time.Time) (float64, bool) {
	p, ok := WeatherPreviewForMap(loc, now.Unix(), 0, 0)
	if !ok || p == nil || len(p.Hours) == 0 {
		return 0, false
	}
	h := int(gameHourOf(now))
	if h >= len(p.Hours) {
		h = len(p.Hours) - 1
	}
	return p.Hours[h].AirTempC, true
}

// reportLegality 汇总合规率与违规明细。
func reportLegality(t *testing.T, samples []legalSample, srv *Server, db *store.DB) {
	t.Helper()
	byCase := map[string][]legalSample{}
	for _, s := range samples {
		byCase[s.Case] = append(byCase[s.Case], s)
	}
	names := make([]string, 0, len(byCase))
	for k := range byCase {
		names = append(names, k)
	}
	sort.Strings(names)

	t.Logf("=== 判鱼合规审计 ===")
	var totalHits, totalSamples int
	var vBait, vLayer, vDiel, vTemp int
	for _, n := range names {
		rs := byCase[n]
		hits := 0
		species := map[string]int{}
		grades := map[int64]int{}
		var sumW int64
		for _, s := range rs {
			totalSamples++
			if !s.Hit {
				continue
			}
			hits++
			totalHits++
			species[s.Species]++
			grades[s.Grade]++
			sumW += s.WeightG
			if !s.BaitRuleOK {
				vBait++
				t.Logf("  ⚠️ 饵料违规 %s：%s  饵=%s  %dg", n, s.Species, s.Bait, s.WeightG)
			}
			if !s.LayerOK {
				vLayer++
				t.Logf("  ⚠️ 水层超界 %s：%s  %s  %dg", n, s.Species, s.LayerNote, s.WeightG)
			}
			if !s.DielOK {
				vDiel++
				t.Logf("  ⚠️ 昼夜禁口 %s：%s  %s  %dg", n, s.Species, s.DielNote, s.WeightG)
			}
			if !s.TempOK {
				vTemp++
				t.Logf("  ⚠️ 极不适温 %s：%s  响应=%.2f  水温=%.1f℃  %dg", n, s.Species, s.TempResponse, s.WaterTemp, s.WeightG)
			}
		}
		if hits == 0 {
			t.Logf("%-16s 命中 0/%d", n, len(rs))
			continue
		}
		t.Logf("%-16s 命中 %d/%d  鱼种%v  档位%v  均重%dg", n, hits, len(rs), species, grades, sumW/int64(hits))
	}

	t.Logf("")
	t.Logf("=== 违规汇总（共 %d 条渔获 / %d 次判鱼）===", totalHits, totalSamples)
	t.Logf("  饵料规则违规  %d  (%.2f%%)", vBait, pct(vBait, totalHits))
	t.Logf("  水层超界      %d  (%.2f%%)", vLayer, pct(vLayer, totalHits))
	t.Logf("  昼夜禁口      %d  (%.2f%%)", vDiel, pct(vDiel, totalHits))
	t.Logf("  极不适温      %d  (%.2f%%)", vTemp, pct(vTemp, totalHits))
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}
