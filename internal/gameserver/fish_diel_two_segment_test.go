package gameserver

import (
	"encoding/json"
	"testing"
)

// 「普通」鱼的昼夜活跃时段从单段补成双段（2026-11-26 定）。
//
// 背景：普通鱼里有一批只有单个活跃段（只有夜或只有昼），表现为「这条鱼一天只有
// 一个时段能钓」，手感上像 bug 而不是设计。规则是补上相邻的过渡时段：
//   只有夜 → 补昏；只有昼 → 补晨。
//
// 为什么不按 rarity_tier 字段筛「普通」：那是 4 档旧编码（0..3），且
// fishEcologyProfile.RarityTier 带 `json:"-"`，从资产反序列化恒为 0——拿它筛
// 会把全部 253 条都当成普通。真正的权威档位在静态表 species_rarity（0/1/2，
// 由 SyncFishRarityFromStatic 投影进库），测试里拿不到，所以这里**按 key 钉住**
// 本次实际调整过的这批鱼，与 fish_diel_mask_test.go 钉夜行鱼的做法一致。
//
// 只钉两件会真正破坏玩法的事：
//  1. 这些鱼不能退回单活跃段；
//  2. 新增时段必须越过活跃线（>=dielActiveThreshold），否则会被
//     fish_diel_inactive_block 整段禁口，补了等于没补。
func TestCommonFishExpandedToTwoDielSegments(t *testing.T) {
	defs := loadEcologyDefs(t)
	expanded := []string{
		// 原「只有夜」，补「昏」
		"a_catfish", "atl_cod", "barsch", "bream", "burbot", "c_squid", "crawfish",
		"dreissena", "fhcatfish", "katran", "menek", "ripus", "ruffe", "s.bream",
		"s.orfe", "s_bass", "sig_ludoga", "stickleback_ninesp", "vjun", "w_catfish",
		"wa_bream", "walleye", "zoarces_v",
		// 原「只有昼」，补「晨」
		"a_salmon", "b_gill", "g_shad", "gorbusha", "white_crappie",
	}

	for _, key := range expanded {
		p, ok := defs[key]
		if !ok {
			t.Errorf("%s：生态档案缺失", key)
			continue
		}
		curve := dielCurveWeights(p.DielCurve)
		if len(curve) != 4 {
			t.Errorf("%s：缺昼夜曲线", key)
			continue
		}
		active := 0
		for i, v := range curve {
			if v < dielActiveThreshold {
				continue
			}
			active++
			// 该段在判鱼口径下也要真的开口，而不是只有曲线数字够大。
			hour := [4]int64{23, 6, 12, 18}[i]
			if got := fishDielWeight(p, hour); got < dielActiveThreshold {
				t.Errorf("%s：%d 点算出权重 %.2f，低于活跃线（曲线 %q）", key, hour, got, p.DielCurve)
			}
		}
		if active < 2 {
			t.Errorf("%s：只有 %d 个活跃段（曲线 %q），应已补成两个", key, active, p.DielCurve)
		}
	}
}

// 掩码必须由曲线推导而来（>=dielActiveThreshold 置位）。
//
// 判鱼侧 fishDielWeight 在曲线非空时只读曲线，掩码仅用于界面展示与无曲线回退。
// 两者一旦脱钩，后台鱼种卡显示的活跃时段与实际咬口分布就会不一致——这正是
// 2026-11-26 那批「改了勾选但咬口没变」的根因。资产必须自洽。
func TestDielMaskMatchesCurveForAllAssets(t *testing.T) {
	var raw []struct {
		FishKey   string `json:"fish_key"`
		DielMask  int64  `json:"diel_mask"`
		DielCurve string `json:"diel_curve"`
	}
	if err := json.Unmarshal(fishEcologyDefsJSON, &raw); err != nil {
		t.Fatalf("加载生态基线失败: %v", err)
	}
	for _, p := range raw {
		curve := dielCurveWeights(p.DielCurve)
		if len(curve) != 4 {
			t.Errorf("%s：曲线无法解析 %q", p.FishKey, p.DielCurve)
			continue
		}
		want := int64(0)
		for i, v := range curve {
			if v >= dielActiveThreshold {
				want |= 1 << uint(i)
			}
		}
		if p.DielMask != want {
			t.Errorf("%s：diel_mask=%d，按曲线应为 %d（曲线 %q）", p.FishKey, p.DielMask, want, p.DielCurve)
		}
	}
}