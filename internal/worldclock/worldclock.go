// Package worldclock 是游戏内时钟的唯一来源。
//
// 规则：1 游戏日 = 1 现实小时（24 倍速），历元 2015-08-01 00:00 UTC。
// 判鱼（gameserver）和后台（admin）都要"现在几点"，两边必须同一套算术，
// 否则后台显示"当前是夜"、判鱼却按白天加权，调参就成了盲调。
// 所以常量与分段函数都放这里，gameserver/spawn.go 只保留同名别名以兼容既有调用点。
package worldclock

// 两个常量都保持**无类型**：gameserver 里既有 int64 也有 float64 的用法，
// 带类型会让 `float64(x) / realSecPerGameHour` 这类表达式编译不过。
const (
	// EpochUnix 游戏历元：2015-08-01 00:00:00 UTC。
	EpochUnix = 1438387200
	// RealSecPerGameHour 150 真实秒 = 1 游戏小时（= 1 游戏日 / 24）。
	RealSecPerGameHour = 150
)

// DayAndHour 返回游戏天数与当前小时（0..23）。
func DayAndHour(nowUnix int64) (day, hour int64) {
	secs := nowUnix - EpochUnix
	return secs / 3600, (secs / RealSecPerGameHour) % 24
}

// 昼夜段位序，与 `1 晨 / 2 昼 / 4 暮 / 8 夜` 的掩码定义一一对应（bit = 0/1/2/3）。
const (
	SegmentDawn  = 0 // 05:00-08:59
	SegmentDay   = 1 // 09:00-15:59
	SegmentDusk  = 2 // 16:00-19:59
	SegmentNight = 3 // 20:00-04:59
)

// segmentNames 供后台展示，顺序即位序。
var segmentNames = [4]string{"晨", "昼", "暮", "夜"}

// SegmentOf 把游戏小时映射到昼夜段位序。这里返回的是**位序**而不是时段编号，
// 调用方直接 `1<<SegmentOf(hour)` 得到掩码位。
func SegmentOf(hour int64) int {
	hour = (hour%24 + 24) % 24
	switch {
	case hour >= 5 && hour < 9:
		return SegmentDawn
	case hour >= 9 && hour < 16:
		return SegmentDay
	case hour >= 16 && hour < 20:
		return SegmentDusk
	default:
		return SegmentNight
	}
}

// SegmentName 返回段名（晨/昼/暮/夜）。
func SegmentName(segment int) string {
	if segment < 0 || segment >= len(segmentNames) {
		return ""
	}
	return segmentNames[segment]
}

// MaskSegments 返回掩码里包含的段数（popcount）。
func MaskSegments(mask int64) int {
	n := 0
	for m := mask; m != 0; m &= m - 1 {
		n++
	}
	return n
}

// MaskLabels 把掩码翻译成「晨/暮/夜」这样的中文串；0 返回「无」。
func MaskLabels(mask int64) string {
	out := ""
	for i := 0; i < len(segmentNames); i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		if out != "" {
			out += "/"
		}
		out += segmentNames[i]
	}
	if out == "" {
		return "无"
	}
	return out
}

// ActiveBoost 把「峰值加成」按活跃段数折算成实际倍率。
//
// 段数越少、越是只在特定时段出没的鱼，那个时段就该越集中：
//
//	1 段（专性，如纯夜行）→ peak
//	2 段                   → 1+2(peak-1)/3
//	3 段                   → 1+(peak-1)/3
//	4 段（全天活跃）       → 1.0，没有时段红利
//
// 放在 worldclock 是因为它和段划分是同一件事的两面：判鱼（gameserver）和后台展示
// （admin）必须算出同一个数，否则后台写着「夜间 ×2」、判鱼实际乘 1.3 就说不清了。
func ActiveBoost(mask int64, peak float64) float64 {
	// peak<=1 一律中性：活跃时段反而压低权重是反直觉的，后台就算填了 0.5 也不采用。
	if peak <= 1 {
		return 1
	}
	seg := MaskSegments(mask)
	if seg <= 0 || seg >= 4 {
		return 1
	}
	return 1 + (peak-1)*float64(4-seg)/3
}
