package gameserver

import (
	"encoding/json"
	"testing"
	"time"
)

// 分布层的写死昼夜表删除后（2026-11-18），夜行鱼的“白天压下去”全靠生态档案的
// diel_mask。这里把已知的夜行/昼行鱼钉在资产上，防止哪天重新生成 defs 时把掩码
// 改回“晨/昼/夜”这种既漏暮又含昼的形状——那会让梅花鲈这类鱼在大中午拿到 1.67 倍。
//
// 掩码位：1 晨 2 昼 4 暮 8 夜。
const (
	dielDawn  int64 = 1
	dielDay   int64 = 2
	dielDusk  int64 = 4
	dielNight int64 = 8
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

// TestNocturnalFishExcludeDaylight：夜行鱼白天必须不活跃（掩码不含昼位）。
func TestNocturnalFishExcludeDaylight(t *testing.T) {
	defs := loadEcologyDefs(t)
	// 梅花鲈/长吻梅花鲈、鳗鱼、江鳕、欧洲六须鲇、西伯利亚杜父鱼、叉尾鮰、铲鮰、
	// 董氏须鳅：均为夜间或晨昏摄食，白天应当被昼夜层压到 inactive_penalty。
	nocturnal := []string{
		"ruffe", "ruffe_n", "eel", "burbot", "w_catfish", "w_catfish_a",
		"bullhead_syb", "ch_catfish", "fhcatfish", "barb_toni",
	}
	for _, key := range nocturnal {
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
	}
}

// TestDiurnalFishBiteInDaylight：白天摄食的鱼（欧鲌、红鳍鱼、鲈鱼）必须含昼位。
// 不要求排除夜位：鲈鱼档案是「晨/昼/夜」，夜里也吃，只是不如白天。
func TestDiurnalFishBiteInDaylight(t *testing.T) {
	defs := loadEcologyDefs(t)
	for _, key := range []string{"c_bleak", "rudd", "perch"} {
		p, ok := defs[key]
		if !ok {
			t.Errorf("%s：生态档案缺失", key)
			continue
		}
		if p.DielMask&dielDay == 0 {
			t.Errorf("%s：昼行鱼掩码 %d 不含昼位", key, p.DielMask)
		}
	}
	// 明确不在夜里摄食的两种：欧鲌、红鳍鱼。
	for _, key := range []string{"c_bleak", "rudd"} {
		if p := defs[key]; p.DielMask&dielNight != 0 {
			t.Errorf("%s：掩码 %d 含夜位，夜里不会被压下去", key, p.DielMask)
		}
	}
}

// TestDielWeightSeparatesNightAndDay：同一套昼夜层参数下，夜行鱼与昼行鱼在
// 正午/深夜的方向必须相反，且幅度来自掩码活跃段数：三段活跃折 (peak-1)/3，
// 即 1+(2.0-1)/3 ≈ 1.33（见 worldclock.ActiveBoost）。
func TestDielWeightSeparatesNightAndDay(t *testing.T) {
	defs := loadEcologyDefs(t)
	const peak, penalty = 2.0, 0.3
	noon, midnight := int64(12), int64(23)
	threeSeg := 1 + (peak-1)/3 // 3 段活跃的加成

	ruffe := defs["ruffe"] // 晨/暮/夜，3 段
	if got := fishDielWeight(ruffe, noon, peak, penalty); got != penalty {
		t.Fatalf("梅花鲈正午系数应为 %v，实得 %v", penalty, got)
	}
	if got := fishDielWeight(ruffe, midnight, peak, penalty); got != threeSeg {
		t.Fatalf("梅花鲈深夜系数应为 %v，实得 %v", threeSeg, got)
	}
	rudd := defs["rudd"] // 晨/昼/暮，3 段
	if got := fishDielWeight(rudd, midnight, peak, penalty); got != penalty {
		t.Fatalf("红鳍鱼深夜系数应为 %v，实得 %v", penalty, got)
	}
	if got := fishDielWeight(rudd, noon, peak, penalty); got != threeSeg {
		t.Fatalf("红鳍鱼正午系数应为 %v，实得 %v", threeSeg, got)
	}
	// 纯夜行鱼（掩码 8，1 段）才是满额 peak。
	eel := fishEcologyProfile{FishKey: "pure-night", DielMask: dielNight}
	if got := fishDielWeight(eel, midnight, peak, penalty); got != peak {
		t.Fatalf("纯夜行鱼深夜系数应为 %v，实得 %v", peak, got)
	}
	if got := fishDielWeight(eel, noon, peak, penalty); got != penalty {
		t.Fatalf("纯夜行鱼正午系数应为 %v，实得 %v", penalty, got)
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
