package gameserver

import (
	"context"
	"strings"
	"time"
)

const (
	// 自动轮换的最小间隔；手工轮换组已删除，这里只约束自动抽取。
	minBaitRotateSec = 60
	baitOverrideTTL  = 15 * time.Second
)

// baitOverrideEntry 是运行时生效的覆盖：只保留规则本体。
type baitOverrideEntry struct {
	rule fishBaitRule
}

// baitOverrideIndex 是覆盖表的两个索引，指向同一批规则。
//
// 主键是 fish_key（00083 迁移），byKey 是唯一的权威查找路径。byName 只为
// 兼容「手上只有中文名」的调用方而留，键是 ruleName 规范化后的名字——
// 00083 已保证同一个规范化名不会对应两条记录，但不同 fish_key 仍可能
// 各自拥有同一个中文名，所以 byName 只做兜底，命中即用、不保证唯一。
type baitOverrideIndex struct {
	byKey  map[string]baitOverrideEntry
	byName map[string]baitOverrideEntry
}

// RotationState 是自动轮换在某一时刻的可读状态，供后台展示与判鱼共用。
type RotationState struct {
	Enabled     bool     `json:"enabled"`
	Mode        string   `json:"mode"`
	Total       int      `json:"total"`
	Active      int      `json:"active"`
	ActiveBaits []string `json:"active_baits"`
	IntervalSec int64    `json:"interval_sec"`
	NextSwitch  int64    `json:"next_switch"`
	// 自动轮换专用：本期配额、展开后的池子大小、未抽中系数。
	Quota    int     `json:"quota,omitempty"`
	PoolSize int     `json:"pool_size,omitempty"`
	OffScale float64 `json:"off_scale,omitempty"`
	// GlobalOff 表示该鱼开关已勾选、抽签也算出来了，但全局总开关是关的，
	// 游戏内并不生效。后台据此提示「总开关未开」，避免误以为已经生效。
	GlobalOff bool `json:"global_off,omitempty"`
}

// resolveBaitRule 取该鱼种当前生效的规则：
// 有后台覆盖用覆盖，否则回落到编译期内置规则表。
func (s *Server) resolveBaitRule(ctx context.Context, fishKey, fishZh string) fishBaitRule {
	idx := s.baitOverrides(ctx)
	if fishKey != "" {
		if ov, ok := idx.byKey[fishKey]; ok {
			return ov.rule
		}
	}
	if fishZh != "" {
		if ov, ok := idx.byName[ruleName(fishZh)]; ok {
			return ov.rule
		}
	}
	// 库里的鱼名并不统一（泥鳅鱼/泥鳅、大西洋鲑/大西洋鲑鱼），必须走归一查找，
	// 否则同一条鱼会因为名字取到哪一版而时有时无地丢掉整条规则。
	if r, ok := lookupFishBaitRule(fishZh); ok {
		return r
	}
	return fishBaitRule{}
}

// baitOverrides 读取后台覆盖并缓存；写操作会主动清缓存，这里再兜一层 TTL。
func (s *Server) baitOverrides(ctx context.Context) baitOverrideIndex {
	s.baitOvMu.RLock()
	if s.baitOvCache.byKey != nil && time.Since(s.baitOvAt) < baitOverrideTTL {
		idx := s.baitOvCache
		s.baitOvMu.RUnlock()
		return idx
	}
	s.baitOvMu.RUnlock()

	s.baitOvMu.Lock()
	defer s.baitOvMu.Unlock()
	if s.baitOvCache.byKey != nil && time.Since(s.baitOvAt) < baitOverrideTTL {
		return s.baitOvCache
	}
	idx := baitOverrideIndex{
		byKey:  map[string]baitOverrideEntry{},
		byName: map[string]baitOverrideEntry{},
	}
	if s.DB != nil {
		rows, err := s.DB.ListFishBaitOverrides(ctx)
		if err != nil {
			s.log.Warn("读取鱼饵规则覆盖失败", "err", err)
		}
		for _, o := range rows {
			if !o.Enabled {
				continue
			}
			rule := fishBaitRule{
				allow:      cleanRuleTerms(o.Allow),
				deny:       cleanRuleTerms(o.Deny),
				bottomOnly: cleanRuleTerms(o.BottomOnly),
				condition:  o.Condition,
			}
			rule.deriveConditionFlags()
			entry := baitOverrideEntry{rule: rule}
			if o.FishKey != "" {
				idx.byKey[o.FishKey] = entry
			}
			if name := ruleName(o.FishZh); name != "" {
				// 主键是 fish_key，同名不同 key 时只保留第一条，不再互相覆盖。
				if _, exists := idx.byName[name]; !exists {
					idx.byName[name] = entry
				}
			}
		}
	}
	s.baitOvCache = idx
	s.baitOvAt = time.Now()
	return idx
}

// invalidateBaitOverrides 在后台保存/重置后清缓存，让改动立即生效。
func (s *Server) invalidateBaitOverrides() {
	s.baitOvMu.Lock()
	s.baitOvCache = baitOverrideIndex{}
	s.baitOvAt = time.Time{}
	s.baitOvMu.Unlock()
	// 规则变了，按规则展开的自动轮换池子也必须作废，否则会继续用旧名单抽签。
	s.invalidateBaitPools()
}

// lookupBaitOverrideName 按规范化中文名取覆盖规则，供后台只读展示。
func (s *Server) lookupBaitOverrideName(ctx context.Context, fishZh string) (fishBaitRule, bool) {
	ov, ok := s.baitOverrides(ctx).byName[ruleName(fishZh)]
	if !ok {
		return fishBaitRule{}, false
	}
	return ov.rule, true
}

// cleanRuleTerms 去掉空白项，避免后台保存的空串影响匹配。
func cleanRuleTerms(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// normalizeBaitZh 去掉 BOM / NBSP / 括号后比较，与 00083 迁移同口径。
func normalizeBaitZh(s string) string {
	r := strings.NewReplacer(
		"\ufeff", "", "\u00a0", "", "\u3000", "",
		"(", "", ")", "", "（", "", "）", "",
	)
	return strings.TrimSpace(r.Replace(s))
}
