// Package worldclock 是游戏内时钟的唯一来源。
//
// 规则：1 游戏日 = 1 现实小时（24 倍速）。
// 判鱼（gameserver）和后台（admin）都要"现在几点"，两边必须同一套算术，
// 否则后台显示"当前是夜"、判鱼却按白天加权，调参就成了盲调。
// 所以常量与分段函数都放这里，gameserver/spawn.go 只保留同名别名以兼容既有调用点。
//
// **日号已回归世界时钟轴（2026-11-24 定论）**：
//
//   - 展示日号（DayAndHour 的 day）与客户端「游戏中 N 天」必须是同一个数。
//     客户端那个数字就是 floor((now-SeasonEpochUnix)/3600)，纯粹由 now 决定，
//     服务端没有任何槽位能单独覆盖它（见 docs/analysis-clock-axis-migration-2026-11-24.md）。
//     所以 DayAndHour 只能锚在 ProtocolEpochUnix 上且不 +1，重启日号这种做法已放弃。
//   - SeasonEpochUnix 决定「现在是哪个季节」，必须钉死在固定值。
//     鱼的适温窗口（fish_ecology_species 的 253 行）是按这套季节标定的，
//     动它会让所有地图季节跳变、水温整体偏移、适温窗口全部错位。
//
// DayEpochUnix 保留但**已退役**：它只是「一个落在整游戏日边界上的已知时刻」，
// 供测试当锚点用，不再参与任何日号计算。不要再把它接回 DayAndHour。
package worldclock

// 常量都保持**无类型**：gameserver 里既有 int64 也有 float64 的用法，
// 带类型会让 `float64(x) / realSecPerGameHour` 这类表达式编译不过。
const (
	// DayEpochUnix 曾是「开服日号纪元」：2026-11-23 13:00:00 UTC（本地 21:00），
	// 开服当天算第 1 天。**2026-11-24 已退役，不再参与日号计算。**
	//
	// 它作为常量保留有两个用处：
	//   1. 它恰好落在整游戏日边界上（SeasonEpochUnix + 整 99181 个游戏日，
	//      差值 357051600 秒能被 RealSecPerGameHour*24 整除），是个方便的
	//      「已知相位」测试锚点；
	//   2. 记录这段历史，防止有人再把它接回 DayAndHour。
	//
	// 为什么放弃「日号从头排」：客户端显示的「游戏中 N 天」是 floor(W/1440)+1，
	// 纯 now 的函数，服务端无法覆盖。重置 DayEpochUnix 只会让后台显示「第 4 天」、
	// 客户端仍显示 99184，两者永远对不上。详见
	// docs/analysis-clock-axis-migration-2026-11-24.md。
	DayEpochUnix = 1795438800
	// SeasonEpochUnix 季节锚点：2015-08-01 00:00:00 UTC（原游戏历元）。
	// 鱼的适温窗口按这套季节标定，**改这个等于重做全部 253 种鱼的温度标定**。
	SeasonEpochUnix = 1438387200
	// ProtocolEpochUnix 协议坐标锚点：2015-08-01 00:00:00 UTC，与 SeasonEpochUnix 同值。
	//
	// 客户端把「世界总游戏分钟」当作绝对时间轴（keepalive FILETIME 每 5 秒校准），
	// 这个坐标**不能随开服重置**：归零会让已存续的购买记录、篝火、船、钓鱼游全部
	// 算出负数时长。它与季节锚点同值只是历史巧合，语义完全不同，故分开命名。
	//
	// 2026-11-24 起，**展示日号（DayAndHour）也锚在这里**，因为客户端那个数字
	// 就是这个坐标展开出来的，后台要跟客户端显示同一个数就别无选择。
	ProtocolEpochUnix = 1438387200
	// RealSecPerGameHour 150 真实秒 = 1 游戏小时（= 1 游戏日 / 24）。
	RealSecPerGameHour = 150
)

// DayAndHour 返回游戏天数与当前小时（0..23）。
//
// 天数锚在 ProtocolEpochUnix（2015-08-01），与客户端屏幕上的「游戏中 N 天」
// 完全同源：客户端把世界总游戏分钟 W = (now-SeasonEpochUnix)*24/60 当作绝对
// 时间轴，日号就是 W/1440 = (now-SeasonEpochUnix)/3600，**从 0 起算、不 +1**。
// 本函数逐字复刻这个式子，因此与 weatherPlanDay（天气模板查表坐标）也是同一个数。
//
// 曾经为了「开服第 1 天」好看而 +1，结果面板「今天」比客户端多一天，
// 天气预览 7 天窗口正好越界到客户端没下发的日号上（TestWeatherPreviewMatchesClientForecast
// 抓到）。不要加回去。
//
// **也不要**改成从 DayEpochUnix 起算：那会让后台显示「第 4 天」而客户端仍显示
// 99184，两者永远对不上（2026-11-23 试过一次，一天后即回退）。
func DayAndHour(nowUnix int64) (day, hour int64) {
	secs := nowUnix - ProtocolEpochUnix
	return secs / 3600, (secs / RealSecPerGameHour) % 24
}

// SeasonDay 返回「季节用」的年内天数（0..364），自 SeasonEpochUnix 起算。
//
// 与 DayAndHour 的天数是两个数：前者给玩家看，后者给判鱼算季节。
// 把两者分开是为了让日号可以重置，而季节标定保持不动。
func SeasonDay(nowUnix int64) int64 {
	day := (nowUnix - SeasonEpochUnix) / 3600
	day %= 365
	if day < 0 {
		day += 365
	}
	return day
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
