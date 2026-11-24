package worldclock

import (
	"testing"
)

// 1 游戏日 = 1 现实小时：150 真实秒一个游戏小时，3600 秒一天。
// 日号锚在协议轴（2015-08-01），与客户端「游戏中 N 天」同源，**从 0 起算不 +1**。
func TestDayAndHour(t *testing.T) {
	cases := []struct {
		offset int64 // 相对日号起算点的真实秒
		day    int64
		hour   int64
	}{
		{0, 0, 0}, // 历元当天是第 0 天（与客户端一致）
		{150, 0, 1},
		{150 * 12, 0, 12},
		{150 * 23, 0, 23},
		{3600, 1, 0},           // 满一天
		{3600*3 + 150*7, 3, 7}, // 第 3 天 07 点
	}
	for _, c := range cases {
		d, h := DayAndHour(ProtocolEpochUnix + c.offset)
		if d != c.day || h != c.hour {
			t.Fatalf("纪元 +%d 秒应为第 %d 天 %d 点，实得第 %d 天 %d 点", c.offset, c.day, c.hour, d, h)
		}
	}
}

// DayAndHour 的天数必须与客户端「游戏中 N 天」、天气模板查表坐标三者逐字等价。
//
// 客户端把世界总游戏分钟 W = (now-SeasonEpochUnix)*24/60 当绝对时间轴，
// 日号 = W/1440 = (now-SeasonEpochUnix)/3600，无 +1、无偏移。
// weatherPlanDay 用的是同一个式子（见 weather_schedule.go）。
// 这条测试把三者钉在一起——任何一方加偏移都会让面板「今天」与客户端错开，
// 7 天窗口随即越界（2026-11-24 的 +1 就是这么暴露的）。
func TestDayMatchesClientGameDay(t *testing.T) {
	base := int64(SeasonEpochUnix)
	for _, off := range []int64{
		0,                 // 历元当天
		3600 * 99181,      // 2026-11-23 那个曾经的「日号纪元」时刻
		3600*99184 + 1500, // 大致是现在
		3600 * 100000,     // 未来
	} {
		now := base + off
		// 客户端口径：W/1440 与 (W%1440)/60
		w := (now - int64(SeasonEpochUnix)) * 24 / 60
		wantDay := w / 1440
		wantHour := (w % 1440) / 60

		gotDay, gotHour := DayAndHour(now)
		if gotDay != wantDay || gotHour != wantHour {
			t.Fatalf("偏离历元 %d 秒：客户端会显示第 %d 天 %d 点，DayAndHour 给第 %d 天 %d 点",
				off, wantDay, wantHour, gotDay, gotHour)
		}
		// 天气模板查表坐标必须同一个数，否则面板与客户端算出两段天气。
		if planDay := (now - int64(ProtocolEpochUnix)) / (RealSecPerGameHour * 24); planDay != gotDay {
			t.Fatalf("偏离历元 %d 秒：weatherPlanDay=%d 但 DayAndHour=%d，两者必须同源", off, planDay, gotDay)
		}
	}
}

// 季节锚点与已退役的日号纪元不是同一个值，且季节标定不受日号轴变更影响。
// 这条测试锁住的就是「日号回归协议轴不许动季节」——253 种鱼的适温窗口挂在季节上。
func TestSeasonDayDecoupledFromDayEpoch(t *testing.T) {
	if SeasonEpochUnix == DayEpochUnix {
		t.Fatal("季节锚点与已退役的日号纪元常量不该是同一个值")
	}
	// 日号现在与协议轴同源，季节天数仍从 SeasonEpochUnix 起算。
	now := int64(DayEpochUnix)
	wantSeasonDay := ((now - SeasonEpochUnix) / 3600) % 365
	if got := SeasonDay(now); got != wantSeasonDay {
		t.Fatalf("季节天数应为 %d，实得 %d", wantSeasonDay, got)
	}
	// 季节天数必须恒在 [0,365)。
	for _, off := range []int64{0, 3600, 86400, 365 * 24 * 3600, 900 * 24 * 3600} {
		if d := SeasonDay(SeasonEpochUnix + off); d < 0 || d >= 365 {
			t.Fatalf("偏移 %d 秒时季节天数 %d 越界", off, d)
		}
	}
}

// DayEpochUnix 虽已退役，但它那个值仍要满足「落在整游戏日边界上」——
// 测试拿它当「已知相位」锚点用，相位歪了会让依赖它的用例集体漂移。
//
// 一个游戏日 = RealSecPerGameHour*24 = 3600 真实秒。它与 SeasonEpochUnix
// 的差若是整数倍，则 (t-DayEpoch) 与 (t-SeasonEpoch) 落在同一时刻相位上。
// 2026-11-23 选值时差值 = 357051600 秒 = 99181 整日。
func TestDayEpochOffsetIsWholeGameDays(t *testing.T) {
	secPerGameDay := int64(RealSecPerGameHour * 24)
	delta := int64(DayEpochUnix) - int64(SeasonEpochUnix)
	if delta%secPerGameDay != 0 {
		t.Fatalf("已退役的日号纪元常量与季节锚点相差 %d 秒，不是 %d 秒（整游戏日）的整数倍；"+
			"它有 22 处测试当相位锚点用，不整除会让那些用例漂移", delta, secPerGameDay)
	}
	// 顺带记录这个常量平移了多少天，便于以后对账。
	if days := delta / secPerGameDay; days != 99181 {
		t.Logf("日号纪元常量相对季节锚点偏移 %d 个游戏日（2026-11-23 选值时为 99181）", days)
	}
}

// 协议锚点与已退役的日号纪元必须是两个不同的常量：307 档案 f13、篝火到期、
// 船、退货窗口用的「世界总游戏分钟」是客户端绝对时间轴，归零会让这些时长判定
// 全部失效（退货 48 游戏日，归零后 now-69120 变成大负数）。
//
// 展示日号现在虽然也锚在 ProtocolEpochUnix 上，但那是**只读共享坐标**，
// 绝不意味着可以重置它——这个常量一旦动，全部存量凭据一起错。
func TestProtocolEpochStaysIndependentFromDayEpoch(t *testing.T) {
	if ProtocolEpochUnix == DayEpochUnix {
		t.Fatal("协议锚点不能等于已退役的日号纪元：重置它会让退货/篝火/船的时间轴归零")
	}
	// 协议锚点应仍锚在原游戏历元上，与季节锚点同值（但语义不同，不可互相替代）。
	if ProtocolEpochUnix != SeasonEpochUnix {
		t.Fatalf("协议锚点应保持原游戏历元 %d，实得 %d", SeasonEpochUnix, ProtocolEpochUnix)
	}
}

// 段划分必须与掩码位定义严格对应：1 晨 / 2 昼 / 4 暮 / 8 夜。
// 这里锁住的是 2026-11-17 修过的那次错位（原实现整体偏一格，夜里查的是「晨」位）。
func TestSegmentOfBoundaries(t *testing.T) {
	cases := []struct {
		hour int64
		want int
	}{
		{4, SegmentNight}, {5, SegmentDawn}, {8, SegmentDawn},
		{9, SegmentDay}, {15, SegmentDay},
		{16, SegmentDusk}, {19, SegmentDusk},
		{20, SegmentNight}, {23, SegmentNight}, {0, SegmentNight},
	}
	for _, c := range cases {
		if got := SegmentOf(c.hour); got != c.want {
			t.Fatalf("%d 点应为第 %d 段（%s），实得第 %d 段", c.hour, c.want, SegmentName(c.want), got)
		}
	}
	// 负值与跨日小时都要归一，不能因为传入 -1 就落到数组外。
	if got := SegmentOf(-1); got != SegmentNight {
		t.Fatalf("-1 点应归一到夜段，实得 %d", got)
	}
	if got := SegmentOf(28); got != SegmentNight {
		t.Fatalf("28 点应归一到 4 点（夜段），实得 %d", got)
	}
}

func TestMaskHelpers(t *testing.T) {
	if got := MaskLabels(1); got != "夜" {
		t.Fatalf("mask=1 应为「夜」，实得 %q", got)
	}
	if got := MaskLabels(1 | 2 | 8); got != "夜/晨/昏" {
		t.Fatalf("mask=11 应为「夜/晨/昏」，实得 %q", got)
	}
	if got := MaskLabels(0); got != "无" {
		t.Fatalf("mask=0 应为「无」，实得 %q", got)
	}
	// 段名与位序一一对应（后台表头按这个顺序画四段）。
	for i, want := range []string{"夜", "晨", "昼", "昏"} {
		if got := SegmentName(i); got != want {
			t.Fatalf("第 %d 段应为 %q，实得 %q", i, want, got)
		}
	}
}
