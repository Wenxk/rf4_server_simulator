package gameserver

import (
	"context"
	"time"

	"rf4server/internal/store"
)

// 钓法倾向层：钓法族 × 鱼种水层档的显式乘子。
//
// 它和 fishLayerWeightProfile（几何水层匹配）是两层不同的事：
//   - 几何层：把钓组折算成一个水层点，和鱼种区间比，区间内给高斯峰、区间外衰减。
//     它回答"这一杆的饵在不在这种鱼待的那一层"。
//   - 本层：即使在同一层里，不同钓法族也有偏好（底钓更偏底层鱼、飞钓更偏表层鱼）。
//     它回答"同样够得着，这种钓法更该上哪种鱼"，并且后台可以改。
//
// 乘子原样生效，不参与判鱼链里各因素 .35 次幂的压缩——后台填 1.6 就是 1.6 倍，
// 否则调参的人永远算不出自己改了多少。

// rigFamilyOf 钓组类型 → 钓法族。空串表示"判不出来"（未知钓组不做偏向）。
//
// 判定顺序有讲究：海钓族（50~60）里有铁板/软饵这类同时也算路亚的钓组，
// 必须先判海钓；飞钓 33 不在任何族里，单列。
func rigFamilyOf(rigType int64) string {
	switch {
	case rigType == 33:
		return store.RigFamilyFly
	case marineRigType(rigType):
		return store.RigFamilyMarine
	case rigTypeLureFamily[rigType]:
		return store.RigFamilyLure
	case bottomRigTypes[rigType]:
		return store.RigFamilyBottom
	case floatRigTypes[rigType]:
		return store.RigFamilyFloat
	}
	return ""
}

// fishLayerBand 鱼种偏好水层 → 四档。边界按实机 253 条鱼的 layer_pref 分布取
// 四分位近似：<0.30 六种表层鱼、0.30~0.50 六十三种中上层、0.50~0.72 七十八种中下层、
// ≥0.72 一百零六种底层鱼。边界值归入更深的一档（含下界）。
func fishLayerBand(pref float64) string {
	switch {
	case pref < 0.30:
		return store.LayerBandSurface
	case pref < 0.50:
		return store.LayerBandMidUpper
	case pref < 0.72:
		return store.LayerBandMidLower
	}
	return store.LayerBandBottom
}

// rigAffinityTable 返回当前矩阵（"钓法族/水层档" → 乘子），30 秒缓存。
// 读库失败时退回出厂矩阵：宁可偏向按默认走，也不让整层消失。
func (s *Server) rigAffinityTable(ctx context.Context) map[string]float64 {
	if s == nil {
		return store.RigAffinityDefaultMap()
	}
	s.rigAffMu.RLock()
	if s.rigAffCache != nil && time.Since(s.rigAffAt) <= 30*time.Second {
		m := s.rigAffCache
		s.rigAffMu.RUnlock()
		return m
	}
	s.rigAffMu.RUnlock()

	m := store.RigAffinityDefaultMap()
	if s.DB != nil {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		if loaded, _, err := s.DB.LoadRigAffinity(c); err == nil && len(loaded) > 0 {
			m = loaded
		}
		cancel()
	}
	s.rigAffMu.Lock()
	s.rigAffCache, s.rigAffAt = m, time.Now()
	s.rigAffMu.Unlock()
	return m
}

// rigAffinityWeight 单条鱼种在本次钓法下的乘子；判不出钓法族时中性 1。
func (s *Server) rigAffinityWeight(ctx context.Context, key string, c fishRollContext) float64 {
	if s == nil || s.tunableF("rig_affinity_enabled", 1) != 1 {
		return 1
	}
	family := rigFamilyOf(c.rigType)
	if family == "" {
		return 1
	}
	p := s.cachedFishEcology(ctx, c, key)
	w, ok := s.rigAffinityTable(ctx)[store.RigAffinityKey(family, fishLayerBand(p.LayerPref))]
	if !ok || w <= 0 {
		return 1
	}
	return w
}
