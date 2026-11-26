package gameserver

import (
	"context"
	"strings"
	"time"

	"rf4server/internal/store"
)

const largeHookSmallFishRatio = 0.25

// hookDisplaySizes 是 item_def 的 gkfbafmbpam 刻度（0..33）到游戏界面钩号的
// 权威映射。下标就是刻度值，空串表示该刻度没有对应的 S 号。
//
// 这两套编号不能互相替代：玩家在客户端和攻略里看到的永远是 S 号（S20、S14、
// S1/0…），而库里存的是内部刻度。刻度与 S 号是严格一对一的——每个刻度只对应
// 一个 S 号，每个 S 号也只对应唯一一个钩长（例如 S16 恒为 3.30mm，与厂牌、型号
// 无关），所以按刻度派生展示号是确定的，不需要解析物品名。
//
// 有序对照（刻度 → 钩长 → S 号）：
//
//	0 → 1.80 → S24     11 → 4.50 → S11      22 → 12.00 → S1/0
//	1 → 1.90 → S22     12 → 5.00 → S10      23 → 14.00 → S2/0
//	2 → 1.95 → S20     13 → 5.50 → S9       24 → 16.00 → S3/0
//	3 → 2.00 → S19     14 → 6.00 → S8       25 → 18.00 → S4/0
//	4 → 2.50 → S18     15 → 6.50 → S7       26 → 23.00 → S5/0
//	5 → 3.00 → S17     16 → 7.00 → S6       27 → 24.00 → S6/0
//	6 → 3.30 → S16     17 → 7.50 → S5       28 → 26.00 → S7/0
//	7 → 3.50 → S15     18 → 8.00 → S4       29 → 28.00 → S8/0
//	8 → 3.70 → S14     19 → 8.50 → S3       30 → 37.00 → S10/0
//	9 → 4.00 → S13     20 → 9.00 → S2       31 → 48.00 → S12/0
//	10 → 4.30 → S12    21 → 10.00 → S1      32 → 62.00 → S14/0
//	                                        33 → 76.00 → S16/0
//
// 校验方式：1141 个钩子里，物品名内能解析出钩号的 807 个全部与本表一致，
// 零冲突；剩余 334 个名字本就不含钩号（如 Stinger Orig、Reactive Original #8）。
var hookDisplaySizes = [...]string{
	"S24", "S22", "S20", "S19", "S18", "S17", "S16", "S15", "S14", "S13", "S12",
	"S11", "S10", "S9", "S8", "S7", "S6", "S5", "S4", "S3", "S2", "S1",
	"S1/0", "S2/0", "S3/0", "S4/0", "S5/0", "S6/0", "S7/0", "S8/0",
	"S10/0", "S12/0", "S14/0", "S16/0",
}

// hookDisplaySize 把内部刻度换成游戏里显示的钩号。超出 0..33 的刻度返回空串，
// 由调用方决定怎么兜底，不臆造号码。
func hookDisplaySize(size int64) string {
	if size < 0 || size >= int64(len(hookDisplaySizes)) {
		return ""
	}
	return hookDisplaySizes[size]
}

// hookSizeForDisplay 是 hookDisplaySize 的逆运算，供按游戏钩号筛选/推荐使用。
// 未收录的写法返回 -1。
func hookSizeForDisplay(display string) int64 {
	display = strings.TrimSpace(display)
	if display == "" {
		return -1
	}
	for i, v := range hookDisplaySizes {
		if v == display {
			return int64(i)
		}
	}
	return -1
}

// hookFamilyFor 把 item_id 前缀归到稳定的钩型族，供管理台筛选。厂牌
// 仍保留在 Brand 字段；钩型族和厂牌不是同一层数据。
func hookFamilyFor(itemID string) string {
	id := strings.ToLower(strings.TrimSpace(itemID))
	switch {
	case strings.HasPrefix(id, "triple_"):
		return "treble"
	case strings.HasPrefix(id, "jhead_"):
		return "jighead"
	case strings.HasPrefix(id, "offset_"), strings.HasPrefix(id, "woffset_"):
		return "offset"
	case strings.HasPrefix(id, "mhook_"), strings.HasPrefix(id, "mdhook_"), strings.HasPrefix(id, "gmhook_"):
		return "marine"
	case strings.HasPrefix(id, "c_hook_"):
		return "carp"
	case strings.HasPrefix(id, "clas_"), strings.HasPrefix(id, "orig_"):
		return "classic"
	default:
		return "other"
	}
}

// SyncHookBiteProfiles 从静态 item_def 同步真实钩子物理数据。调参字段在冲突
// 更新时保留，只有型号、尺寸、拉力等真实数据会被刷新。
func (s *Server) SyncHookBiteProfiles(ctx context.Context) (int, error) {
	return s.syncHookBiteProfiles(ctx)
}

func (s *Server) syncHookBiteProfiles(ctx context.Context) (int, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	defs, err := s.DB.ListItemDefsByCategory(ctx, catHook)
	if err != nil {
		return 0, err
	}
	now := float64(time.Now().Unix())
	n := 0
	for _, def := range defs {
		if def == nil || strings.TrimSpace(def.ItemID) == "" {
			continue
		}
		size := itemDefInt(def, gearAttrHookSize)
		p := store.HookBiteProfile{
			ItemID:      def.ItemID,
			NumericID:   def.NumericID,
			Size:        size,
			DisplaySize: hookDisplaySize(size),
			LengthMM:    itemDefF32(def, gearAttrHookLen),
			StrengthKg:  itemDefF32(def, gearAttrMaxLoad),
			WireWeightG: itemDefF32(def, "ijbjficbhog"),
			Quality:     itemDefInt(def, "obifdihmcfn"),
			Family:      hookFamilyFor(def.ItemID),
			Brand:       def.Brand,
			UpdatedTS:   now,
		}
		if p.LengthMM <= 0 && p.Size >= 0 {
			p.LengthMM = 1.5 + 0.35*float64(p.Size)
		}
		if err := s.DB.UpsertHookBiteProfile(ctx, p); err != nil {
			return n, err
		}
		n++
	}
	if err := s.reloadHookBiteProfiles(ctx); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Server) reloadHookBiteProfiles(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	rows, err := s.DB.ListHookBiteProfiles(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]store.HookBiteProfile, len(rows))
	for _, p := range rows {
		next[p.ItemID] = p
	}
	s.hookProfileMu.Lock()
	s.hookProfiles = next
	s.hookProfileMu.Unlock()
	return nil
}

func (s *Server) hookBiteProfileFor(itemID string) (store.HookBiteProfile, bool) {
	if s == nil || strings.TrimSpace(itemID) == "" {
		return store.HookBiteProfile{}, false
	}
	s.hookProfileMu.RLock()
	p, ok := s.hookProfiles[itemID]
	s.hookProfileMu.RUnlock()
	return p, ok
}

// applyHookBiteProfile 只对基础几何概率做型号级微调；所有倍率默认 1.0，
// 缺档案时完全保持旧行为。small_fish_floor 和 over_capacity_hold_floor
// 是管理台显式配置的兜底值，0 表示不启用。
func applyHookBiteProfile(weightG int64, spec gearPhysicsSpec, p store.HookBiteProfile, solid, hold float64) (float64, float64) {
	if weightG <= 0 || !spec.HookPresent {
		return solid, hold
	}
	// 数据库默认值是 1.0；0 是后台允许的有效调参值，必须真正关闭对应概率。
	// 这里不接受负数，避免脏数据把概率反向放大。
	if p.SolidScale >= 0 {
		solid *= p.SolidScale
	}
	if p.HoldScale >= 0 {
		hold *= p.HoldScale
	}
	gape := hookGapeG(spec)
	if gape > 0 {
		r := float64(weightG) / gape
		if r < largeHookSmallFishRatio && p.SmallFishFloor > 0 && solid < p.SmallFishFloor {
			solid = p.SmallFishFloor
		}
		if r > 1 && p.OverCapacityHoldFloor > 0 && hold < p.OverCapacityHoldFloor {
			hold = p.OverCapacityHoldFloor
		}
	}
	return clampUnit(solid), clampUnit(hold)
}
