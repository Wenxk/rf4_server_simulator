package gameserver

import (
	"context"
	"strings"
)

// 硬性鱼口资格门（tunable bite_gate_enabled，默认关）。
//
// 生态层（fishBiteEnvironmentWeight）是全软乘子：水层底线 ×0.005、总 clamp ≥0.08，
// 荒谬组合只降权、永不剔除，还会被 weightedChoice 的全零退化捞回来。门层补上
// 「这口饵根本钓不到这条鱼」的资格裁定，插在水域鱼种覆盖之后、饵料规则/分布/
// 生态权重之前：
//
//   - G1 钓法×食性（硬剔除）：钩上实际饵是拟饵（item_def 类别 17，含路亚族与
//     海钓铁板/飞领挂拟饵）时，鱼的生态食性 diet_json.lure 低于
//     bite_gate_lure_diet_min 直接出候选。默认 0.30：泥鳅/鲫鱼 0.20~0.25、
//     鲤族 0 不攻拟饵；狗鱼 2.07、江鳕 0.63、鲟族 0.30+（RF4 里 jig 钓鲟是
//     真实玩法）照常。
//   - G2 水层（软地板）：目标水层超出 [LayerMin−0.15, LayerMax+0.15] 时权重乘
//     bite_gate_layer_floor（默认 0.01），不剔除——深水鱼偶尔在浅水被钓起是
//     有意保留的极小杂口。0.15 容差用于减少底层杆在相邻水层间的误伤；当前
//     生态表仍有上层/中层鱼 LayerMax < 0.78，底钓（目标 0.93）时它们会按设计
//     落到地板。104 种底层鱼（鳗/七鳃鳗/鲟/庸鲽）会被水面拟饵（0.06+0.15）地板。
//   - G-diet 天然饵食性资格（默认随门开启）：生态食性表 diet_json 参与咬口，
//     只看"这条鱼的食物谱里有没有这类东西"（鱼种级事实，与钓场/纪录无关）。
//     食谱里有 → 正常；食谱里没有但攻略/companion/本图纪录说吃过 → ×0.6 兜底；
//     两头都没有 → 剔除。旧版的"双票打折"（只有食性认就压到 0.05）等于拿记录
//     当门槛，已废弃——那会让"本图攻略没写过的饵"永远钓不到。
//
// 门层不动延迟链、概率掷骰、档位采样、嘬饵/保持率；GM 指定鱼（st.Armed）
// 在更早的分支已绕过全部判鱼。候选被剔空时返回空切片 + gateBlocked 置位，
// 由调用方走「本轮无符合条件的鱼」重排（与空轮掷骰同一语义），且不落
// 内置鱼池兜底。
type biteGateConfig struct {
	enabled     bool
	lureDietMin float64
	layerFloor  float64
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
		enabled:     true,
		lureDietMin: s.tunableF("bite_gate_lure_diet_min", 0.30),
		layerFloor:  s.tunableF("bite_gate_layer_floor", 0.01),
	}
	if cfg.lureDietMin < 0 {
		cfg.lureDietMin = 0
	}
	if cfg.layerFloor < 0 {
		cfg.layerFloor = 0
	}
	if cfg.layerFloor > 1 {
		cfg.layerFloor = 1
	}
	return cfg
}

// biteGateBaitSet 一次判鱼里钩上饵的归类结果。
// defsOK=false（饵定义读不到或没挂饵）时 G1/G-diet 保持中性，只剩 G2。
type biteGateBaitSet struct {
	defsOK  bool
	lure    bool
	natural []ruleBait
}

func (s *Server) biteGateBaitSet(ctx context.Context, c fishRollContext) biteGateBaitSet {
	if s.DB == nil || len(c.baits) == 0 {
		return biteGateBaitSet{}
	}
	out := biteGateBaitSet{defsOK: true}
	for _, id := range c.baits {
		if id == "" {
			continue
		}
		b, ok := s.cachedBaitDefinition(ctx, c, id)
		if !ok {
			// 与 filterFishBaitSet 同口径：饵定义读不到时整体放行，不因数据缺失封鱼。
			return biteGateBaitSet{}
		}
		switch b.category {
		case 17:
			out.lure = true
		case 27:
			out.natural = append(out.natural, b)
		}
	}
	return out
}

// biteGateDietKeys 把天然饵映射到 diet_json 的食性键。
// 未知类型返回 nil：这类饵"判不出吃什么"，交给调用方保持中性，不硬判不吃。
func biteGateDietKeys(b ruleBait) []string {
	switch b.baitType {
	case 2, 6, 7: // 幼虫/蚯蚓水蛭/昆虫成虫
		return []string{"worm"}
	case 3, 4, 14, 15: // 蛙/活饵鱼/鱼片/死饵鱼
		return []string{"fish"}
	case 8, 9, 17: // 煮饵/颗粒/风味饵/Zig 球
		return []string{"boilie"}
	case 10, 11, 13: // 玉米/老虎坚果/面团与粥
		return []string{"plant"}
	case 12, 16: // 钩虾/海虫贝类
		return []string{"crustacean"}
	case 1:
		return miscDietKeys(b)
	}
	return nil
}

// miscDietKeys 类别 1「杂类」其实是混装货架：藻类/面包/玉米/豌豆/马铃薯是植物饵，
// 香煮成饵是煮饵，贻贝/珠蚌/虾仁/蟹肉是甲壳类，鱼块/鱼子/鱼肉类。
// 必须按 id 与名称精确归类——旧版一律返回「植物/煮饵/甲壳/鱼 任一命中」，
// 结果是河斑马纹贻贝肉在拉多加湖能钓到 38/38 个鱼种，等于没有食性约束。
func miscDietKeys(b ruleBait) []string {
	if strings.HasPrefix(b.id, "boil_") {
		return []string{"boilie"}
	}
	switch b.id {
	case "bait_dreissena", "unio_crassus", "crawfish_meat", "crab_meat":
		return []string{"crustacean"}
	case "fish_pieces", "caviar":
		return []string{"fish"}
	case "watergrass", "cheese_cube", "wheat_grain", "sunflower_cake", "bread", "corn", "cabbage", "peas", "potato":
		return []string{"plant"}
	}
	n := ruleName(b.name)
	switch {
	case strings.Contains(n, "贻贝") || strings.Contains(n, "珠蚌") || strings.Contains(n, "虾") || strings.Contains(n, "蟹"):
		return []string{"crustacean"}
	case strings.Contains(n, "鱼"):
		return []string{"fish"}
	case strings.Contains(n, "煮"):
		return []string{"boilie"}
	case strings.Contains(n, "玉米") || strings.Contains(n, "面包") || strings.Contains(n, "藻") ||
		strings.Contains(n, "豌豆") || strings.Contains(n, "马铃薯") || strings.Contains(n, "菜叶"):
		return []string{"plant"}
	}
	return nil
}

// applyBiteGates 过滤候选鱼种：G1 剔除食性不符的，G-diet 按双票打折，
// G2 给水层超界的乘地板。权重 ≤0 的候选直接跳过（与 filterFishBaitSet 同口径）。
// statLoc 是统计名册 loc（本地饵证据 loc_bait_fish 按它查），兜底鱼池路径没有时传空。
func (s *Server) applyBiteGates(ctx context.Context, keys []string, wts []float64, c fishRollContext, cfg biteGateConfig) ([]string, []float64) {
	if len(keys) == 0 {
		return keys, wts
	}
	baits := s.biteGateBaitSet(ctx, c)
	target := s.rigWaterLayer(c)

	// G-diet 只看鱼种食性：饵定义读不到时整层跳过（保持中性），G1/G2 照常。
	dietReady := baits.defsOK && len(baits.natural) > 0

	outKeys := make([]string, 0, len(keys))
	outWts := make([]float64, 0, len(wts))
	for i, key := range keys {
		if wts[i] <= 0 {
			continue
		}
		p := s.cachedFishEcology(ctx, c, key)
		diet := fishEcologyJSONMap(p.DietJSON)
		if baits.lure && diet["lure"] < cfg.lureDietMin {
			continue
		}
		if dietReady {
			if biteGateDietScore(diet, baits.natural) <= 0 {
				continue
			}
		}
		w := wts[i]
		if target < p.LayerMin-biteGateLayerTol || target > p.LayerMax+biteGateLayerTol {
			// 保底杂口（c.pityEnv）：地板抬到至少 0.05，让连续空杆的线能动；
			// 食性类硬门（G1/G-diet）不受 pityEnv 影响。
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

// biteGateDietScore 天然饵食性资格：只看这条鱼的食物谱里有没有这类东西。
// 任一饵在食谱里就放行（多钩可以挂别的饵）；一个都不在才是这条鱼不吃。
// 攻略、官服榜、本图渔获都不参与——它们记录的是"谁钓到过"，不是"鱼吃不吃"。
// 食谱为空表示没有档案数据，按中性放行，不因缺数据判不吃。
func biteGateDietScore(diet map[string]float64, baits []ruleBait) float64 {
	if len(diet) == 0 {
		return 1
	}
	for _, b := range baits {
		if _, edible := baitDietStrength(diet, b); edible {
			return 1
		}
	}
	return 0
}

// baitDietStrength 返回该饵在鱼种食谱里的食性强度（>1 = 主食，<1 = 偶尔吃）。
// 第二个返回值 false 表示食谱里没有这类食物，这条鱼不吃这个饵。
func baitDietStrength(diet map[string]float64, b ruleBait) (float64, bool) {
	keys := biteGateDietKeys(b)
	if b.category == 17 {
		keys = []string{"lure"}
	}
	if len(keys) == 0 {
		return 0, false
	}
	best, found := 0.0, false
	for _, k := range keys {
		v, has := diet[k]
		// 煮饵/颗粒在多数鲤鱼科档案里只记 plant，没有 boilie 键时按植物饵折算。
		if !has && k == "boilie" {
			if p, hasPlant := diet["plant"]; hasPlant {
				v, has = p*0.9, true
			}
		}
		if !has {
			continue
		}
		found = true
		if v > best {
			best = v
		}
	}
	if found {
		return best, true
	}
	// 拟饵键缺失（老档案/兜底档案）时按掠食性折算，避免一挂路亚整图空竿。
	if b.category == 17 {
		if v, has := diet["fish"]; has {
			return v, true
		}
	}
	return 0, false
}
