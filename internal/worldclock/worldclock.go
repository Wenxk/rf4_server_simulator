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

// 昼夜段位序，与 `1 夜 / 2 晨 / 4 昼 / 8 昏` 的掩码定义一一对应（bit = 0/1/2/3）。
//
// 位序与上游 `fishActivityForHour` 一致（夜=0/晨=1/昼=2/昏=3）：生态资产里的
// diel_mask 与 diel_curve（`[夜,晨,昼,昏]` 四段权重）都按这个顺序编码，
// 本地若另立一套（曾经是 晨=0）会让上游资产整份失效、曲线也整体错位一格。
const (
	SegmentNight = 0 // 20:00-04:59
	SegmentDawn  = 1 // 05:00-08:59
	SegmentDay   = 2 // 09:00-15:59
	SegmentDusk  = 3 // 16:00-19:59
)

// segmentNames 供后台展示，顺序即位序。
var segmentNames = [4]string{"夜", "晨", "昼", "昏"}

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

// SegmentName 返回段名（夜/晨/昼/昏）。
func SegmentName(segment int) string {
	if segment < 0 || segment >= len(segmentNames) {
		return ""
	}
	return segmentNames[segment]
}

// MaskLabels 把掩码翻译成「夜/晨/昼/昏」这样的中文串；0 返回「无」。
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
