package gameserver

import (
	"context"
	"math"
	"time"

	"rf4server/internal/store"
)

// 鱼种经济覆盖：后台「鱼种经济」页按 (鱼种, 档位) 覆盖经验速率与基准鱼价。
//
// 覆盖只影响两个出口：
//   - 经验：catchValue 的速率（XP = trunc(rate × 克重)）；
//   - 鱼价：marketPriceCentsForMap 的 per-kg 单价，语义是基准水域（阿赫图巴）银/公斤，
//     其他水域仍按 map_market_multipliers.json 的倍率折算。
//
// 读库失败或没有覆盖行时一律回到内置表，结算链路不因后台数据缺失而中断。

const econOverrideTTL = 30 * time.Second

// econOverrides 返回整表覆盖快照（缓存 30 秒）。返回的 map 只读，调用方不得修改。
func (s *Server) econOverrides() (map[vgKey]float64, map[vgKey]float64) {
	s.econOvMu.RLock()
	if s.econOvLoaded && time.Since(s.econOvAt) < econOverrideTTL {
		xp, price := s.econOvXP, s.econOvPrice
		s.econOvMu.RUnlock()
		return xp, price
	}
	s.econOvMu.RUnlock()

	s.econOvMu.Lock()
	defer s.econOvMu.Unlock()
	if s.econOvLoaded && time.Since(s.econOvAt) < econOverrideTTL {
		return s.econOvXP, s.econOvPrice
	}
	xp := map[vgKey]float64{}
	price := map[vgKey]float64{}
	if s.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		rows, err := s.DB.SpeciesEconList(ctx)
		cancel()
		if err == nil {
			for _, r := range rows {
				key := vgKey{species: r.FishKey, grade: r.Grade}
				if r.XPC != nil && *r.XPC > 0 {
					xp[key] = *r.XPC
				}
				if r.PricePerKg != nil && *r.PricePerKg > 0 {
					price[key] = *r.PricePerKg
				}
			}
		}
	}
	s.econOvXP, s.econOvPrice = xp, price
	s.econOvAt = time.Now()
	s.econOvLoaded = true
	return xp, price
}

// invalidateEconOverrides 丢掉缓存，下次读取重新查库（后台保存后由 reload_tunables 触发）。
func (s *Server) invalidateEconOverrides() {
	s.econOvMu.Lock()
	s.econOvLoaded = false
	s.econOvXP, s.econOvPrice = nil, nil
	s.econOvMu.Unlock()
}

// catchValue 按后台覆盖（若有）计算渔获经验与展示银价。
func (s *Server) catchValue(species string, grade, weightG int64) (xp, silver int64) {
	rate := fishXPRateFor(species, grade)
	xpOv, _ := s.econOverrides()
	if ov, ok := xpOv[vgKey{species: species, grade: grade}]; ok {
		rate = ov
	}
	return catchValueAtRate(species, grade, weightG, rate)
}

// marketPriceCentsForMap 按后台覆盖（若有）计算鱼市报价；没有覆盖时走内置价表。
func (s *Server) marketPriceCentsForMap(e *store.KeepnetEntry, now float64, mapKey string) int64 {
	if e == nil {
		return 0
	}
	grade := e.Cat
	if grade < 1 {
		grade = 1
	}
	_, priceOv := s.econOverrides()
	perKg, ok := priceOv[vgKey{species: e.Species, grade: grade}]
	if !ok {
		return marketPriceCentsForMap(e, now, mapKey)
	}
	pct := marketMapPct(normalizeMarketMapKey(mapKey))
	if pct <= 0 {
		// 未登记倍率的水域仍按基准价（1.0）结算，避免覆盖值把报价打成 0。
		pct = 1
	}
	w := e.WeightG
	if w < 1 {
		w = 1
	}
	factor := dailyMarketFactor(e.Species, now)
	fresh := freshnessFactor(e.TS, now)
	return int64(math.Max(1, math.Round(perKg*pct*factor*(float64(w)/1000.0)*100*fresh)))
}
