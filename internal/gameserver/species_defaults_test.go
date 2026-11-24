package gameserver

import (
	"context"
	"math"
	"testing"
)

// 默认数值一致性：253 个鱼种都必须严格踩同一条阶梯。
//
//	经验 1 : 3.265 : 20.40 : 40.81（相对未达标，fishXPGradeFactor）
//	鱼价 1 : 1.5 : 3 : 6（相对未达标，speciesPriceGradeFactor）
//
// 后台覆盖值只改单点，默认值本身不能自相矛盾。
func TestSpeciesDefaultsConsistent(t *testing.T) {
	db := openStaticTestDB(t)
	ctx := context.Background()
	rows, err := db.SQL.QueryContext(ctx, `SELECT fish_key FROM species_weight ORDER BY fish_key`)
	if err != nil {
		t.Fatalf("读取鱼种名册: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if len(keys) < 250 {
		t.Fatalf("名册只有 %d 个鱼种，期望 253", len(keys))
	}
	relDiff := func(a, b float64) float64 {
		return math.Abs(a-b) / math.Max(1, math.Abs(b))
	}
	for _, key := range keys {
		baseXP := SpeciesBaseXPRate(key, 1)
		basePrice := SpeciesBasePricePerKg(key, 1)
		if baseXP <= 0 {
			t.Errorf("%s 未达标经验速率为 %v", key, baseXP)
		}
		if basePrice <= 0 {
			t.Errorf("%s 未达标基准价为 %v", key, basePrice)
			continue
		}
		for grade := int64(1); grade <= 4; grade++ {
			if want := baseXP * fishXPGradeFactor[grade-1]; relDiff(SpeciesBaseXPRate(key, grade), want) > 1e-9 {
				t.Errorf("%s g%d 经验 %v 偏离阶梯 %v", key, grade, SpeciesBaseXPRate(key, grade), want)
			}
			if want := basePrice * speciesPriceGradeFactor[grade-1]; relDiff(SpeciesBasePricePerKg(key, grade), want) > 1e-9 {
				t.Errorf("%s g%d 鱼价 %v 偏离阶梯 %v", key, grade, SpeciesBasePricePerKg(key, grade), want)
			}
		}
		// 四档必须严格递增：上星>达标>未达标，蓝冠>上星。
		for grade := int64(2); grade <= 4; grade++ {
			if SpeciesBaseXPRate(key, grade) <= SpeciesBaseXPRate(key, grade-1) {
				t.Errorf("%s 经验未严格递增：g%d %v ≤ g%d %v", key, grade,
					SpeciesBaseXPRate(key, grade), grade-1, SpeciesBaseXPRate(key, grade-1))
			}
			if SpeciesBasePricePerKg(key, grade) <= SpeciesBasePricePerKg(key, grade-1) {
				t.Errorf("%s 鱼价未严格递增：g%d %v ≤ g%d %v", key, grade,
					SpeciesBasePricePerKg(key, grade), grade-1, SpeciesBasePricePerKg(key, grade-1))
			}
		}
	}
}

// 统一阶梯本身锁死：改这里等于改全服经济，必须是有意为之。
func TestUnifiedGradeLadder(t *testing.T) {
	wantXP := []float64{1, 3.2648, 20.4048, 40.8097}
	for i, w := range wantXP {
		if got := fishXPGradeFactor[i]; math.Abs(got-w)/w > 1e-4 {
			t.Errorf("经验阶梯 g%d=%v，期望 ≈%v", i+1, got, w)
		}
	}
	wantPrice := []float64{1, 1.5, 3, 6}
	for i, w := range wantPrice {
		if got := speciesPriceGradeFactor[i]; got != w {
			t.Errorf("鱼价阶梯 g%d=%v，期望 %v", i+1, got, w)
		}
	}
}

// 2026-11-23 的基准值锁定：经验只剩「每种的未达标基数」是数据，
// 鱼价只锁未达标基准，三档溢价由 TestUnifiedGradeLadder 管。
func TestSpeciesDefaultsCorrected(t *testing.T) {
	const eps = 1e-9
	priceCases := map[string]float64{
		"os_grayling":     28.04,
		"chir":            4.38,
		"sebastes_m":      18.7,
		"v_bream":         19.28,
		"sig_vouksa":      17.53,
		"crawfish":        84.27,
		"sig_valaam":      40.9,
		"salvel_l":        13.32,
		"riv_brown_trout": 14.38,
		"roach":           4.23,
		"bs_salmon":       5.89,
		// 2026-11-23 溢价异常校正（fish-price-outlier-audit）：
		// 单条蓝冠价值曾超同体型 p90 的 5 倍，收敛到 p90×2。
		"chimaera":    219.41,
		"c_anguineus": 16.38,
		"himant_g":    79.98,
		// 2026-11-23 稀有度对齐补漏（rarity-price-alignment）：全量复验后仍有 5 种
		// 单价齐平或低于同量级普通鱼 p90，提到 p90 之上（留 2% 余量）。
		"eel":         26.65,
		"g_tench":     28.06,
		"n_carp":      11.50,
		"sh_barbel":   18.26,
		"syb_sterlet": 25.56,
	}
	for key, want := range priceCases {
		if got := SpeciesBasePricePerKg(key, 1); math.Abs(got-want) > eps {
			t.Errorf("%s 未达标基准价=%v，期望 %v", key, got, want)
		}
	}

	// 经验：手算 trunc(基数 × 阶梯 × 克重)，覆盖推断种、组种与实测锚。
	xpCases := []struct {
		key    string
		grade  int64
		weight int64
		xp     int64
	}{
		{"roach", 2, 300, 143},          // 0.147 × 3.2648 × 300 = 143.98
		{"roach", 3, 300, 899},          // 0.147 × 20.4048 × 300 = 899.85
		{"roach", 4, 300, 1799},         // 0.147 × 40.8097 × 300 = 1799.7
		{"s_lamprey", 2, 400, 3264},     // 2.5 × 3.2648 × 400 = 3264.8
		{"trid_lamprey", 3, 400, 20404}, // 2.5 × 20.4048 × 400 = 20404.8
		{"frog", 1, 100, 344},           // 3.4471 × 100 = 344.71
		{"kutum", 4, 1000, 10002},       // 0.2451 × 40.8097 × 1000 = 10002.4（roach_run）
		{"eel", 4, 1000, 102024},        // 2.5 × 40.8097 × 1000 = 102024.2（marine_rock）
		{"alb_barbel", 3, 1000, 35930},  // 1.7609 × 20.4048 × 1000 = 35930.9（herb_carp）
		{"bream", 3, 6000, 8999},        // 锚档实测 1.4999 × 6000 = 8999.4
		{"tench", 4, 6782, 18085},       // 唯一 g4 实测 2.6667 × 6782 = 18085.5
	}
	for _, c := range xpCases {
		xp := int64(float64(c.weight) * SpeciesBaseXPRate(c.key, c.grade))
		if xp != c.xp {
			t.Errorf("%s g%d %dg XP=%d，期望 %d（rate=%v）", c.key, c.grade, c.weight, xp, c.xp,
				SpeciesBaseXPRate(c.key, c.grade))
		}
	}
}

// 鱼价离群守卫：单条蓝冠尺寸鱼的卖价不得超过同体型档 p90 的 5 倍。
// 2026-11-23 的 fish-price-outlier-audit 揪出 chimaera（10.2×）、c_anguineus（9.4×）、
// himant_g（5.2×）三种，已收敛到 p90×2。这条测试防止后续改价再退回去。
func TestNoPriceOutlierBySizeClass(t *testing.T) {
	overlayOnce.Do(loadMarketOverlay)
	blueLine := map[string]float64{
		"chimaera": 1875, "c_anguineus": 70000, "himant_g": 9000,
		"molva": 35000, "conger": 75000, "lophius_p": 45000,
		"eel": 7500, "sebastes_n": 11000, "cetorhinus_m": 2500000,
	}
	// 体型档边界（克）与本次审计得到的档内 p90 单条价值（银）。
	classes := []struct {
		lo, hi float64
		p90    float64
		name   string
	}{
		{0, 200, 252, "<200g"},
		{200, 1000, 91, "0.2-1kg"},
		{1000, 5000, 206, "1-5kg"},
		{5000, 20000, 360, "5-20kg"},
		{20000, 100000, 573, "20-100kg"},
		{100000, 1e12, 5823, ">100kg"},
	}
	for key, line := range blueLine {
		price := SpeciesBasePricePerKg(key, 1)
		if price <= 0 {
			t.Fatalf("%s 基准价缺失", key)
		}
		value := price * line / 1000
		for _, c := range classes {
			if line >= c.lo && line < c.hi {
				if value > c.p90*5 {
					t.Errorf("%s 单条蓝冠价值 %.0f 银，超过 %s 档 p90(%.0f) 的 5 倍（单价 %.2f/kg）",
						key, value, c.name, c.p90, price)
				}
				break
			}
		}
	}
}

// 稀有度价格对齐守卫：同克重下，稀有（rarity=1）/ 超稀有（rarity=2）鱼的单价
// 不得低于「体型同量级（蓝冠线 0.5~2 倍）普通鱼」的 p90。
// 2026-11-23 的 rarity-price-alignment 修正了 22 种（最差 n_carp 仅为同档普通鱼的 0.94 倍）。
//
// p90 不在此处现场计算：单测里只有十几个手挑鱼种，样本一稀疏分位数就失真
// （曾出现挑 6 个代表把 p90 抬到 32.72，误判 eel 26.13 不合格）。
// 这里直接钉住 `python tools/fish_price_audit.py` 用全量 253 种实测出的 p90，
// 断言基准价不得低于它。全量重算请重跑该脚本并同步本表。
func TestRarityPriceNotBelowCommon(t *testing.T) {
	overlayOnce.Do(loadMarketOverlay)
	// key: 同量级普通鱼单价 p90（银/kg），n 为参与统计的普通鱼种数。
	cases := []struct {
		key    string
		rarity int
		p90    float64
		peersN int
	}{
		{"n_carp", 1, 9.20, 19},
		{"b_carp", 1, 9.20, 25},
		{"s_freaks", 1, 9.20, 25},
		{"din_l_carp", 1, 9.20, 19},
		{"carp_ghost", 1, 9.20, 29},
		{"o_ogon", 1, 9.20, 27},
		{"kohaku", 2, 9.20, 27},
		{"carp_albino", 2, 9.20, 29},
		{"eel", 1, 24.36, 62},
		{"g_tench", 1, 26.20, 53},
		{"sh_barbel", 1, 14.61, 41},
		{"syb_sterlet", 1, 20.45, 49},
	}
	const eps = 1e-9
	// 同时要求严格高于 p90：上一轮把 20 种「齐平到 p90」，结果 n_carp/sh_barbel/syb_sterlet
	// 的单价与同档某条普通鱼数值完全相同（9.20/14.61/20.45），稀有度等于零溢价，等于没改。
	// 现在要求至少高出 1%，否则视为回退。
	const minPremium = 0.01
	for _, c := range cases {
		price := SpeciesBasePricePerKg(c.key, 1)
		if price <= 0 {
			t.Errorf("%s 基准价缺失", c.key)
			continue
		}
		if price+eps < c.p90*(1+minPremium) {
			t.Errorf("%s(r=%d) 单价 %.2f 未高于同克重普通鱼 p90 %.2f 至少 %.0f%%（该 p90 取自 %d 种同量级普通鱼）",
				c.key, c.rarity, price, c.p90, minPremium*100, c.peersN)
		}
	}
}
