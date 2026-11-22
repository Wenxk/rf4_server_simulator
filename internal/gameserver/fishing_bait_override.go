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
func (s *Server) resolveBaitRule(ctx context.Context, fishZh string) fishBaitRule {
	key := ruleName(fishZh)
	if ov, ok := s.baitOverrides(ctx)[key]; ok {
		return ov.rule
	}
	// 库里的鱼名并不统一（泥鳅鱼/泥鳅、大西洋鲑/大西洋鲑鱼），必须走归一查找，
	// 否则同一条鱼会因为名字取到哪一版而时有时无地丢掉整条规则。
	if r, ok := lookupFishBaitRule(fishZh); ok {
		return r
	}
	return fishBaitRule{}
}

// baitOverrides 读取后台覆盖并缓存；写操作会主动清缓存，这里再兜一层 TTL。
func (s *Server) baitOverrides(ctx context.Context) map[string]baitOverrideEntry {
	s.baitOvMu.RLock()
	if s.baitOvCache != nil && time.Since(s.baitOvAt) < baitOverrideTTL {
		m := s.baitOvCache
		s.baitOvMu.RUnlock()
		return m
	}
	s.baitOvMu.RUnlock()

	s.baitOvMu.Lock()
	defer s.baitOvMu.Unlock()
	if s.baitOvCache != nil && time.Since(s.baitOvAt) < baitOverrideTTL {
		return s.baitOvCache
	}
	m := map[string]baitOverrideEntry{}
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
			m[ruleName(o.FishZh)] = baitOverrideEntry{rule: rule}
		}
	}
	s.baitOvCache = m
	s.baitOvAt = time.Now()
	return m
}

// invalidateBaitOverrides 在后台保存/重置后清缓存，让改动立即生效。
func (s *Server) invalidateBaitOverrides() {
	s.baitOvMu.Lock()
	s.baitOvCache = nil
	s.baitOvAt = time.Time{}
	s.baitOvMu.Unlock()
	// 规则变了，按规则展开的自动轮换池子也必须作废，否则会继续用旧名单抽签。
	s.invalidateBaitPools()
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
