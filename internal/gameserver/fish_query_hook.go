package gameserver

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"

	"rf4server/internal/store"
)

// 查鱼助手的鱼钩推荐。
//
// 为什么需要它：口径上钩子不决定「鱼来不来」（hook_entry_cap 默认关闭），但它
// 决定两件真事——发鱼阶段的软匹配和大鱼的中钩保持率。钩太小，鱼照来、照样咬，
// 一提竿就脱；钩太大，小鱼个体被 soft filter 压掉。玩家最常问的恰恰是「这鱼用
// 几号钩」，而游戏界面显示的是 S 号，不是库里的内部刻度。
//
// 推荐口径：取「刚好盖住该鱼蓝冠线」的最小钩号。
//   - 覆盖蓝冠线 → 中钩后 hold 恒为 1（r = 体重/入口 <= 1 不再掷保钩骰），
//     不会出现「咬上了又脱」；
//   - 取最小 → 入口尽量贴近鱼体，避免 r < 0.25 触发小鱼 soft filter
//     （0.35+2.6r 那段压权重），尽量少误伤同种小个体。
//
// 入口容量 gape = hookWeightCapG(钩长) × gearOverloadFactor，与判鱼链路同一公式；
// 这里不另设系数，避免回复口径和实战口径漂移。

// FishQueryHook 是回复里那一条「推荐鱼钩」。字段都按可读性组织，中文文案在
// formatFishQueryHook 里拼。
type FishQueryHook struct {
	DisplaySize string  `json:"display_size"`
	Size        int64   `json:"size"`
	LengthMM    float64 `json:"length_mm"`
	EntryG      float64 `json:"entry_g"`
	GapeG       float64 `json:"gape_g"`
	TargetG     float64 `json:"target_g"`
	Basis       string  `json:"basis"`
	ItemID      string  `json:"item_id,omitempty"`
	Brand       string  `json:"brand,omitempty"`
	ModelName   string  `json:"model_name,omitempty"`
	StrengthKg  float64 `json:"strength_kg,omitempty"`
	Options     int     `json:"options"`
	// StrengthShort 表示这个号位没有任何型号的拉力够搏这条鱼：能中钩，但
	// 搏鱼时钩子本身会先断。巨型鱼（欧洲鳇、鳇、姥鲨等）会落到这一档。
	StrengthShort bool `json:"strength_short,omitempty"`
}

// fishQueryHookTarget 给出用于推荐钩号的体重基准及其来源。优先蓝冠线：它是
// 「这条鱼能长到多大」的现实上限，也是玩家会遇到的极限个体。
func (s *Server) fishQueryHookTarget(ctx context.Context, fishKey string) (float64, string) {
	var minG, maxG, trophyG, trophyLine, superLine float64
	err := s.DB.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(min_g,0),COALESCE(max_g,0),COALESCE(trophy_g,0),
		       COALESCE(trophy_line_g,0),COALESCE(super_trophy_line_g,0)
		FROM species_weight WHERE fish_key=?`, fishKey).
		Scan(&minG, &maxG, &trophyG, &trophyLine, &superLine)
	if err != nil {
		return 0, ""
	}
	switch {
	case superLine > 0:
		return superLine, "blue"
	case maxG > 0:
		return maxG, "max"
	case trophyLine > 0:
		return trophyLine * 1.45, "trophy"
	case trophyG > 0:
		return trophyG * 1.5, "trophy"
	}
	return 0, ""
}

// recommendFishQueryHook 按体重基准挑钩号。返回 nil 表示钩子档案不可用（例如
// 静态库没同步），此时回复里不出现钩号，绝不编一个号出来。
func (s *Server) recommendFishQueryHook(ctx context.Context, fishKey string) *FishQueryHook {
	target, basis := s.fishQueryHookTarget(ctx, fishKey)
	if target <= 0 {
		return nil
	}
	rows, err := s.DB.ListHookBiteProfiles(ctx)
	if err != nil || len(rows) == 0 {
		return nil
	}
	// 先按刻度聚合成号位，再按刻度升序（钩越小越靠前）找第一个够用的。
	bySize := map[int64][]store.HookBiteProfile{}
	for _, p := range rows {
		if p.DisplaySize == "" || p.LengthMM <= 0 {
			continue
		}
		bySize[p.Size] = append(bySize[p.Size], p)
	}
	sizes := make([]int64, 0, len(bySize))
	for size := range bySize {
		sizes = append(sizes, size)
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })

	needKg := target / 1000
	chosen := int64(-1)
	for _, size := range sizes {
		for _, p := range bySize[size] {
			if hookGapeForProfile(p) >= target {
				chosen = size
				break
			}
		}
		if chosen >= 0 {
			break
		}
	}
	if chosen < 0 {
		// 理论上不该到这里：最大钩（48mm，入口约 2573kg）盖得住全库最大的蓝冠线
		// （姥鲨 2500kg）。真到了这里说明钩子档案不全，返回 nil 让回复不输出钩号，
		// 好过报一个盖不住的小号。
		return nil
	}
	cands := bySize[chosen]
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].StrengthKg != cands[j].StrengthKg {
			return cands[i].StrengthKg > cands[j].StrengthKg
		}
		if cands[i].Quality != cands[j].Quality {
			return cands[i].Quality > cands[j].Quality
		}
		return cands[i].ItemID < cands[j].ItemID
	})
	best := cands[0]
	gape := hookGapeForProfile(best)
	out := &FishQueryHook{
		DisplaySize: best.DisplaySize,
		Size:        best.Size,
		LengthMM:    best.LengthMM,
		EntryG:      hookWeightCapG(best.LengthMM, best.Size),
		GapeG:       gape,
		TargetG:     target,
		Basis:       basis,
		Options:     len(cands),
	}
	if best.StrengthKg > 0 {
		out.ItemID, out.Brand, out.StrengthKg = best.ItemID, best.Brand, best.StrengthKg
	}
	// 报给玩家的是型号名（如 CHK101 S18），不是内部厂牌键。item_def.brand 是
	// 形如 brsrk_ultra_tr 的内部标识，玩家在客户端根本看不到，写进回复只会让人
	// 困惑；item_catalog.name 才是界面上印的那串字。
	out.ModelName = s.hookModelName(ctx, best.ItemID)
	if best.StrengthKg > 0 && best.StrengthKg < needKg {
		out.StrengthShort = true
	}
	return out
}

// hookModelName 取物品目录里的显示名（如 CHK101 S18）。查不到返回空串，
// 调用方不要把内部 item_id 顶上去。
func (s *Server) hookModelName(ctx context.Context, itemID string) string {
	if s == nil || s.DB == nil || itemID == "" {
		return ""
	}
	var name string
	err := s.DB.SQL.QueryRowContext(ctx,
		`SELECT COALESCE(NULLIF(name_zh,''),NULLIF(name,'')) FROM item_catalog WHERE item_id=?`, itemID).
		Scan(&name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// hookGapeForProfile 用与判鱼同一套公式算入口容量，避免推荐口径与实战口径漂移。
func hookGapeForProfile(p store.HookBiteProfile) float64 {
	length := p.LengthMM
	if length <= 0 && p.Size >= 0 {
		length = 1.5 + 0.35*float64(p.Size)
	}
	return hookWeightCapG(length, p.Size) * gearOverloadFactor
}

// trimNumber 按小数位格式化后去掉无意义的尾零，2.50 → "2.5"，3.00 → "3"。
func trimNumber(v float64, places int) string {
	s := strconv.FormatFloat(v, 'f', places, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// gramsText 让克重读数保持可读：小于 1kg 用整克，1kg 以上用一位小数的公斤。
func gramsText(g float64) string {
	if g >= 1000 {
		return trimNumber(g/1000, 2) + "kg"
	}
	return trimNumber(math.Round(g), 0) + "g"
}

// formatFishQueryHook 把推荐钩号拼成一行。只在有把握的时候说「够用」，
// 盖不住或拉力不足都要明说，不能让玩家以为挂上就稳。
func formatFishQueryHook(h *FishQueryHook) string {
	if h == nil || h.DisplaySize == "" {
		return ""
	}
	line := "推荐鱼钩：" + h.DisplaySize + "（" + trimNumber(h.LengthMM, 2) + "mm，入口约 " +
		gramsText(h.GapeG)
	if h.StrengthKg > 0 {
		line += "，拉力 " + trimNumber(h.StrengthKg, 2) + "kg"
	}
	if h.ModelName != "" {
		line += "，" + h.ModelName
	}
	line += "）"
	if h.StrengthShort {
		line += "；没有这个号位的型号拉力足够，搏大个体时钩子会先断，建议靠泄力拖时间"
	}
	return line
}
