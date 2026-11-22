package gameserver

import "context"

// 水层地板（tunable bite_gate_enabled，默认关）。
//
// 吃不吃只由饵料规则的 allow/deny 决定；这一层不再参与食性裁定，只回答
// 「饵在不在鱼的水层」：
//
//   - 目标水层超出 [LayerMin−0.15, LayerMax+0.15] 时权重乘 bite_gate_layer_floor
//     （默认 0.01），不剔除——深水鱼偶尔在浅水被钓起是有意保留的极小杂口。
//     0.15 容差用于减少底层杆在相邻水层间的误伤；当前生态表仍有上层/中层鱼
//     LayerMax < 0.78，底钓（目标 0.93）时它们会按设计落到地板。
//
// 原先的 G1（拟饵 diet.lure 门槛）和 G-diet（天然饵食谱键资格）已删除：
// 252/252 个鱼种都有 allow 规则，两个硬门实际只在越过后台编辑的 allow，
// 造成「allow 写了也不咬」的黑箱。现在 allow 是唯一准入，见 filterFishBaitSet。
//
// 本层不动延迟链、概率掷骰、档位采样、嘬饵/保持率；GM 指定鱼（st.Armed）
// 在更早的分支已绕过全部判鱼。
type biteGateConfig struct {
	enabled    bool
	layerFloor float64
}

// biteGateLayerTol 水层容差。与生态层 fishLayerWeightProfile 的拟饵向上追击
// 容差（+.15）同口径，这里对两个方向统一用 0.15：浅滩浮钓（100cm→0.2）仍能
// 钓到 LayerMin≈0.31 的鲤科鱼，而水面拟饵钓不到 LayerMin≥0.44 的底栖鱼。
const biteGateLayerTol = 0.15

func (s *Server) biteGateConfig() biteGateConfig {
	if s.tunableF("bite_gate_enabled", 0) != 1 {
		return biteGateConfig{}
	}
	cfg := biteGateConfig{
		enabled:    true,
		layerFloor: s.tunableF("bite_gate_layer_floor", 0.01),
	}
	if cfg.layerFloor < 0 {
		cfg.layerFloor = 0
	}
	if cfg.layerFloor > 1 {
		cfg.layerFloor = 1
	}
	return cfg
}

// applyBiteGates 只给水层超界的候选乘地板。权重 ≤0 的候选直接跳过。
func (s *Server) applyBiteGates(ctx context.Context, keys []string, wts []float64, c fishRollContext, cfg biteGateConfig) ([]string, []float64) {
	if len(keys) == 0 {
		return keys, wts
	}
	target := s.rigWaterLayer(c)
	outKeys := make([]string, 0, len(keys))
	outWts := make([]float64, 0, len(wts))
	for i, key := range keys {
		if wts[i] <= 0 {
			continue
		}
		p := s.cachedFishEcology(ctx, c, key)
		w := wts[i]
		if target < p.LayerMin-biteGateLayerTol || target > p.LayerMax+biteGateLayerTol {
			// 保底杂口（c.pityEnv）：地板抬到至少 0.05，让连续空杆的线能动。
			floor := cfg.layerFloor
			if c.pityEnv && floor < 0.05 {
				floor = 0.05
			}
			w *= floor
		}
		outKeys = append(outKeys, key)
		outWts = append(outWts, w)
	}
	return outKeys, outWts
}
