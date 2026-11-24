package gameserver

import (
	"math"
	"sort"
)

// 供后台「鱼种经济」页使用的只读查询与试算出口。
// 这些函数不参与结算，只保证后台看到的默认值与主服实际生效的口径一致。

// marketBaseMap 是内置价表的基准水域（倍率 1.0）。
// 后台覆盖的 price_per_kg 也以该水域的银/公斤表达。
const marketBaseMap = "level_009_ahtuba"

// MarketMapInfo 是后台试算下拉的一项。
type MarketMapInfo struct {
	Key  string  `json:"key"`
	Name string  `json:"name"`
	Pct  float64 `json:"pct"`
}

// MarketMapList 返回全部已登记鱼市倍率的水域，按倍率降序、同倍率按 key 排序。
func MarketMapList() []MarketMapInfo {
	overlayOnce.Do(loadMarketOverlay)
	out := make([]MarketMapInfo, 0, len(marketPctTable))
	for k, pct := range marketPctTable {
		name := marketNameTable[k]
		if name == "" {
			name = k
		}
		out = append(out, MarketMapInfo{Key: k, Name: name, Pct: pct})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pct != out[j].Pct {
			return out[i].Pct > out[j].Pct
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// SpeciesBaseXPRate 返回内置表里的 XP/克（不含后台覆盖）。
func SpeciesBaseXPRate(species string, grade int64) float64 {
	return fishXPRateFor(species, grade)
}

// MarketBaseMapKey 返回内置价表的基准水域键（后台覆盖价的表达口径）。
func MarketBaseMapKey() string { return marketBaseMap }

// MarketMapPct 返回某水域的鱼市倍率；场景键（*_fishmarket）会先归一。
func MarketMapPct(mapKey string) float64 {
	return marketMapPct(normalizeMarketMapKey(mapKey))
}

// SpeciesBasePricePerKg 返回基准水域的内置鱼价（银/公斤，不含后台覆盖）。
// 内置价表缺该鱼种时返回 0，调用方应提示“走兜底速率”。
func SpeciesBasePricePerKg(species string, grade int64) float64 {
	return marketSilverPerKgOverlay(marketBaseMap, species, grade)
}

// PreviewSpeciesEcon 按后台输入试算一尾鱼的经验与卖价。
// xpOverride / priceOverride 传 0 表示用内置默认（与主服一致：价格覆盖以基准水域单价表达）。
func PreviewSpeciesEcon(species string, grade, weightG int64, xpOverride, priceOverride float64, mapKey string, caughtAt, now float64) (xp, cents int64) {
	if grade < 1 {
		grade = 1
	} else if grade > 4 {
		grade = 4
	}
	w := weightG
	if w < 1 {
		w = 1
	}
	rate := fishXPRateFor(species, grade)
	if xpOverride > 0 {
		rate = xpOverride
	}
	xp = int64(math.Max(1, math.Trunc(rate*float64(w))))
	factor := dailyMarketFactor(species, now)
	fresh := freshnessFactor(caughtAt, now)
	if priceOverride > 0 {
		pct := marketMapPct(normalizeMarketMapKey(mapKey))
		if pct <= 0 {
			pct = 1
		}
		cents = int64(math.Max(1, math.Round(priceOverride*pct*factor*(float64(w)/1000.0)*100*fresh)))
	} else if perKg := marketSilverPerKgOverlay(mapKey, species, grade); perKg > 0 {
		cents = int64(math.Max(1, math.Round(perKg*factor*(float64(w)/1000.0)*100*fresh)))
	} else {
		cents = int64(math.Max(1, math.Round(marketRatePerG(species, grade)*factor*float64(w)*100*fresh)))
	}
	return
}

// SpeciesDailyMarketFactor 返回某鱼种在给定时刻的每日行情系数（0.95–1.05）。
// 主服卖鱼结算会在基准价之上乘这个系数，同一天同一鱼种固定，跨天重新取值。
// 后台试算页用它解释「1kg 为什么不是正好等于每公斤基准价」。
func SpeciesDailyMarketFactor(species string, now float64) float64 {
	return dailyMarketFactor(species, now)
}
