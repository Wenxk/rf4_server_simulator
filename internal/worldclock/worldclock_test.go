package worldclock

import (
	"math"
	"testing"
)

// 1 游戏日 = 1 现实小时：150 真实秒一个游戏小时，3600 秒一天。
func TestDayAndHour(t *testing.T) {
	cases := []struct {
		offset int64 // 相对历元的真实秒
		day    int64
		hour   int64
	}{
		{0, 0, 0},
		{150, 0, 1},
		{150 * 12, 0, 12},
		{150 * 23, 0, 23},
		{3600, 1, 0},           // 满一天
		{3600*3 + 150*7, 3, 7}, // 第 3 天 07 点
	}
	for _, c := range cases {
		d, h := DayAndHour(EpochUnix + c.offset)
		if d != c.day || h != c.hour {
			t.Fatalf("历元 +%d 秒应为第 %d 天 %d 点，实得第 %d 天 %d 点", c.offset, c.day, c.hour, d, h)
		}
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

func TestActiveBoostBySegmentCount(t *testing.T) {
	const peak = 2.0
	cases := []struct {
		mask int64
		want float64
	}{
		{8, 2.0},               // 一段：满额
		{1 | 4, 1 + 2.0/3},     // 两段
		{1 | 2 | 8, 1 + 1.0/3}, // 三段
		{15, 1},                // 四段：无时段红利
		{0, 1},                 // 无档案
	}
	for _, c := range cases {
		if got := ActiveBoost(c.mask, peak); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("mask=%d 期望 %.4f，实得 %.4f", c.mask, c.want, got)
		}
	}
	// peak<=1 时没有任何加成，防止后台把倍率调成 0 后出现「活跃反而更低」。
	if got := ActiveBoost(8, 0); got != 1 {
		t.Fatalf("peak=0 应退化为 1，实得 %v", got)
	}
	if got := ActiveBoost(8, 0.5); got != 1 {
		t.Fatalf("peak<1 应退化为 1，实得 %v", got)
	}
}

func TestMaskHelpers(t *testing.T) {
	if got := MaskLabels(8); got != "夜" {
		t.Fatalf("mask=8 应为「夜」，实得 %q", got)
	}
	if got := MaskLabels(1 | 4 | 8); got != "晨/暮/夜" {
		t.Fatalf("mask=13 应为「晨/暮/夜」，实得 %q", got)
	}
	if got := MaskLabels(0); got != "无" {
		t.Fatalf("mask=0 应为「无」，实得 %q", got)
	}
	for _, c := range []struct {
		mask int64
		want int
	}{{0, 0}, {8, 1}, {1 | 2 | 8, 3}, {15, 4}} {
		if got := MaskSegments(c.mask); got != c.want {
			t.Fatalf("mask=%d 段数应为 %d，实得 %d", c.mask, c.want, got)
		}
	}
}
