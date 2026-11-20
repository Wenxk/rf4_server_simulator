package gameserver

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// 分布层的写死昼夜表删除后（2026-11-18），夜行鱼的"白天压下去"全靠生态档案的
// diel_curve / diel_mask。这里把已知的夜行/昼行鱼钉在资产上，防止哪天重新生成
// defs 时把曲线配反（夜行鱼白天满权重）——那会让梅花鲈在大中午照常出货。
//
// 掩码位：1 夜 2 晨 4 昼 8 昏（与上游 fishActivityForHour、曲线 [夜,晨,昼,昏] 同序）。
const (
	dielNight int64 = 1
	dielDawn  int64 = 2
	dielDay   int64 = 4
	dielDusk  int64 = 8
)

func loadEcologyDefs(t *testing.T) map[string]fishEcologyProfile {
	t.Helper()
	var profiles []fishEcologyProfile
	if err := json.Unmarshal(fishEcologyDefsJSON, &profiles); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]fishEcologyProfile, len(profiles))
	for _, p := range profiles {
		out[p.FishKey] = p
	}
	return out
}

// TestNocturnalFishExcludeDaylight：严格夜行鱼夜间峰值、白天接近不出。
// 数据来自 rf4info 实测（见 diel_source），所以这里只钉「方向」不钉具体数值：
// 大鲶鱼类（w_catfish_a / bullhead_syb / barb_toni）社区样本里白天也有活动，
// 曲线白天段能到 0.6~0.9，不能拿「掩码必须排除昼位」去卡它们。
func TestNocturnalFishExcludeDaylight(t *testing.T) {
	defs := loadEcologyDefs(t)
	strict := []string{"ruffe", "ruffe_n", "eel", "burbot", "w_catfish", "fhcatfish"}
	for _, key := range strict {
		p, ok := defs[key]
		if !ok {
			t.Errorf("%s：生态档案缺失", key)
			continue
		}
		if p.DielMask&dielDay != 0 {
			t.Errorf("%s：夜行鱼掩码 %d 含昼位，白天会被当成活跃时段", key, p.DielMask)
		}
		if p.DielMask&dielNight == 0 {
			t.Errorf("%s：夜行鱼掩码 %d 不含夜位", key, p.DielMask)
		}
		curve := dielCurveWeights(p.DielCurve)
		if len(curve) != 4 {
			t.Errorf("%s：缺昼夜曲线", key)
			continue
		}
		if curve[0] != 1 || !(curve[2] < 0.5) {
			t.Errorf("%s：曲线 %v 应为夜间峰值且白天明显衰减", key, curve)
		}
	}
	// 白天也有活动的大型鲶鱼：只要求夜间仍是峰值，不要求白天被压到很低。
	for _, key := range []string{"w_catfish_a", "bullhead_syb", "barb_toni"} {
		p, ok := defs[key]
		if !ok {
			t.Errorf("%s：生态档案缺失", key)
			continue
		}
		curve := dielCurveWeights(p.DielCurve)
		if len(curve) != 4 || curve[0] != 1 {
			t.Errorf("%s：曲线 %v 应以夜间为峰值", key, curve)
		}
	}
}

// TestDiurnalFishBiteInDaylight：白天摄食的鱼（欧鲌、红鳍鱼）昼段必须是峰值。
// 不要求排除昏段：两种鱼昏段还有 0.54~0.56 的尾巴，傍晚仍会出。
func TestDiurnalFishBiteInDaylight(t *testing.T) {
	defs := loadEcologyDefs(t)
	for _, key := range []string{"c_bleak", "rudd"} {
		p, ok := defs[key]
		if !ok {
			t.Errorf("%s：生态档案缺失", key)
			continue
		}
		if p.DielMask&dielDay == 0 {
			t.Errorf("%s：昼行鱼掩码 %d 不含昼位", key, p.DielMask)
		}
		if p.DielMask&dielNight != 0 {
			t.Errorf("%s：掩码 %d 含夜位，夜里不会被压下去", key, p.DielMask)
		}
		curve := dielCurveWeights(p.DielCurve)
		if len(curve) != 4 || curve[2] != 1 {
			t.Errorf("%s：昼段权重应为峰值 1，实得 %v", key, curve)
		}
	}
}

// TestDielWeightSeparatesNightAndDay：同一时刻，夜行鱼与昼行鱼的方向必须相反。
// 曲线口径下峰值恒为 1，只压低不抬高，所以「白天压到多低」完全由数据决定。
func TestDielWeightSeparatesNightAndDay(t *testing.T) {
	defs := loadEcologyDefs(t)
	noon, midnight := int64(12), int64(23)

	ruffe := defs["ruffe"] // 纯夜行
	if got := fishDielWeight(ruffe, midnight); got != 1 {
		t.Fatalf("梅花鲈深夜系数应为峰值 1，实得 %v", got)
	}
	if got, want := fishDielWeight(ruffe, noon), dielCurveWeights(ruffe.DielCurve)[2]; math.Abs(got-want) > 1e-9 {
		t.Fatalf("梅花鲈正午系数应为 %v，实得 %v", want, got)
	}
	rudd := defs["rudd"] // 昼行
	if got := fishDielWeight(rudd, noon); got != 1 {
		t.Fatalf("红鳍鱼正午系数应为峰值 1，实得 %v", got)
	}
	if got, want := fishDielWeight(rudd, midnight), dielCurveWeights(rudd.DielCurve)[0]; math.Abs(got-want) > 1e-9 {
		t.Fatalf("红鳍鱼深夜系数应为 %v，实得 %v", want, got)
	}
	// 曲线是乘性权重：任何鱼、任何时段都不该超过 1。
	for _, key := range []string{"ruffe", "rudd", "eel", "burbot", "perch"} {
		for hour := int64(0); hour < 24; hour++ {
			if got := fishDielWeight(defs[key], hour); got > 1 {
				t.Fatalf("%s 在 %d 点的系数 %v 超过 1", key, hour, got)
			}
		}
	}
	// 没有曲线的旧档案回退位图：命中 1、未命中 0.35。
	pureNight := fishEcologyProfile{FishKey: "pure-night", DielMask: dielNight}
	if got := fishDielWeight(pureNight, midnight); got != 1 {
		t.Fatalf("无曲线·夜位命中应为 1，实得 %v", got)
	}
	if got := fishDielWeight(pureNight, noon); got != .35 {
		t.Fatalf("无曲线·昼位未命中应为 0.35，实得 %v", got)
	}
}

// TestDistributionIgnoresGameHour：分布层不再读游戏小时——同一格、同一散布
// 周期内，换个时刻（不跨周期）结果不变；跨周期才变。若有人把昼夜塞回分布层，
// 这条会先失败。
func TestDistributionIgnoresGameHour(t *testing.T) {
	area := "map-z/3/5"
	// 12 点与 13 点同属一个四小时散布周期。
	noon := time.Unix(worldDayEpochUnix+12*realSecPerGameHour, 0)
	later := time.Unix(worldDayEpochUnix+13*realSecPerGameHour, 0)
	if a, b := distributionMultiplier(area, "ruffe", noon), distributionMultiplier(area, "ruffe", later); a != b {
		t.Fatalf("同一散布周期内乘子应一致，正午 %v、一小时 %v", a, b)
	}
}
