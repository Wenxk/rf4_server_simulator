package gameserver

import (
	"math"
	"strings"
	"testing"
	"time"

	"rf4server/internal/worldclock"
)

// activeFishRoster 造一组覆盖各种情况的鱼种：昼夜曲线只开夜、只开昼、
// 适温窗口宽/窄、温度明显不匹配的。
func activeFishRoster() []WeatherEcologyInput {
	return []WeatherEcologyInput{
		{ // 纯夜行，适温居中
			FishKey: "night", NameZh: "夜行鱼", Rarity: 0, DielMask: 1,
			DielCurve: "[1,0.2,0.1,0.3]", TempMinC: 8, TempPrefC: 15, TempMaxC: 22,
		},
		{ // 纯昼行，适温居中
			FishKey: "day", NameZh: "昼行鱼", Rarity: 1, DielMask: 4,
			DielCurve: "[0.1,0.3,1,0.2]", TempMinC: 8, TempPrefC: 15, TempMaxC: 22,
		},
		{ // 全天活跃
			FishKey: "anytime", NameZh: "全天鱼", Rarity: 0, DielMask: 15,
			DielCurve: "[1,1,1,1]", TempMinC: 4, TempPrefC: 15, TempMaxC: 28,
		},
		{ // 只在极热水里舒服，当前水温够不到
			FishKey: "hot", NameZh: "热水鱼", Rarity: 2, DielMask: 15,
			DielCurve: "[1,1,1,1]", TempMinC: 30, TempPrefC: 36, TempMaxC: 42,
		},
	}
}

// 活跃鱼种的口径必须与判鱼一致：昼夜系数直接来自 fishDielWeight，
// 水温系数来自同一段高斯响应。这里逐条比对分项系数，防止后台另写一套。
func TestActiveFishReusesBiteFormulas(t *testing.T) {
	now := time.Now().Unix()
	view := WeatherActiveFishFor("level_001_lake", now, activeFishRoster())
	if view == nil {
		t.Fatal("应返回结果")
	}
	hour := (now - worldProtocolEpochUnix) / realSecPerGameHour % 24
	air := currentAirTemp("level_001_lake", now)
	_, waterC := waterTempsAtLayer(air, hour, now, .45)

	all := append(append([]WeatherActiveFish{}, view.Fish...), view.Idle...)
	if len(all) != 4 {
		t.Fatalf("应覆盖 4 条鱼，实得 %d", len(all))
	}
	for _, f := range all {
		var in WeatherEcologyInput
		for _, r := range activeFishRoster() {
			if r.FishKey == f.FishKey {
				in = r
			}
		}
		// 昼夜：与判鱼同一个函数。
		wantDiel := fishDielWeight(fishEcologyProfile{DielMask: in.DielMask, DielCurve: in.DielCurve}, hour)
		if math.Abs(f.DielFactor-wantDiel) > 0.011 {
			t.Fatalf("%s 昼夜系数 %v，判鱼口径 %v", f.FishKey, f.DielFactor, wantDiel)
		}
		// 水温：同一段高斯响应。
		tol := math.Max(2.2, (in.TempMaxC-in.TempMinC)/2)
		z := (waterC - in.TempPrefC) / tol
		wantThermal := .35 + .65*math.Exp(-.5*z*z)
		if math.Abs(f.ThermalFact-wantThermal) > 0.011 {
			t.Fatalf("%s 水温系数 %v，判鱼口径 %v", f.FishKey, f.ThermalFact, wantThermal)
		}
		if math.Abs(f.Score-f.DielFactor*f.ThermalFact) > 0.02 {
			t.Fatalf("%s 综合系数应为两项之积：%v != %v×%v",
				f.FishKey, f.Score, f.DielFactor, f.ThermalFact)
		}
	}
}

// 非活跃时段禁口：昼夜系数低于 0.5 的鱼必须落在 Idle 里，且原因写明是昼夜，
// 不能混进活跃名单。判鱼侧同一阈值（fish_diel_inactive_block）。
func TestActiveFishBlocksInactiveDiel(t *testing.T) {
	// 构造一个明确落在夜段的时刻，让「纯昼行」那条被禁口。
	base := time.Now().Unix()
	_, curHour := worldDayAndHour(base)
	// 挪到 23 点（夜段）。
	target := base + int64(((23-curHour+24)%24)*realSecPerGameHour)
	view := WeatherActiveFishFor("level_001_lake", target, activeFishRoster())

	var dayFish *WeatherActiveFish
	for i := range view.Idle {
		if view.Idle[i].FishKey == "day" {
			dayFish = &view.Idle[i]
		}
	}
	if dayFish == nil {
		t.Fatalf("夜里「昼行鱼」应被判为非活跃，实际活跃名单：%v", fishKeysOf(view.Fish))
	}
	if dayFish.Reason == "" {
		t.Fatal("非活跃鱼必须给出原因")
	}
	if !strings.Contains(dayFish.Reason, "非活跃时段") {
		t.Fatalf("原因应说明昼夜，实得 %q", dayFish.Reason)
	}
}

// 适温严重不匹配的鱼不进活跃名单：热水鱼（最适 36℃）在任何地图的水温下
// 都偏离超过 2 个 tolerance，页面不该把它列成「现在能钓」。
func TestActiveFishExcludesThermalMismatch(t *testing.T) {
	view := WeatherActiveFishFor("level_001_lake", time.Now().Unix(), activeFishRoster())
	for _, f := range view.Fish {
		if f.FishKey == "hot" {
			t.Fatalf("热水鱼不该出现在活跃名单（水温 %v℃，最适 %v℃）", view.WaterC, f.TempPrefC)
		}
	}
	var hot *WeatherActiveFish
	for i := range view.Idle {
		if view.Idle[i].FishKey == "hot" {
			hot = &view.Idle[i]
		}
	}
	if hot == nil {
		t.Fatal("热水鱼应落在不活跃名单")
	}
	if !strings.Contains(hot.Reason, "水温") {
		t.Fatalf("原因应说明水温，实得 %q", hot.Reason)
	}
}

// 活跃名单按综合系数降序，且条数与 ActiveNum 一致。
func TestActiveFishSortedAndCounted(t *testing.T) {
	view := WeatherActiveFishFor("level_001_lake", time.Now().Unix(), activeFishRoster())
	if view.ActiveNum != len(view.Fish) {
		t.Fatalf("ActiveNum=%d 与名单长度 %d 不符", view.ActiveNum, len(view.Fish))
	}
	for i := 1; i < len(view.Fish); i++ {
		if view.Fish[i-1].Score < view.Fish[i].Score {
			t.Fatalf("活跃名单未按综合系数降序：第 %d 条 %v < 第 %d 条 %v",
				i-1, view.Fish[i-1].Score, i, view.Fish[i].Score)
		}
	}
	if len(view.Fish)+len(view.Idle) != view.Total {
		t.Fatalf("活跃 %d + 不活跃 %d != 总数 %d",
			len(view.Fish), len(view.Idle), view.Total)
	}
}

// 面板水温必须与温度曲线同源：同一时刻、同一水层（.45）算出来必须一致。
func TestActiveFishWaterMatchesPreviewCurve(t *testing.T) {
	now := time.Now().Unix()
	view := WeatherActiveFishFor("level_001_lake", now, activeFishRoster())
	// 入参是**玩家日号**（面板口径），不是协议日号——两者差 weatherPlanDayOffset。
	nowDay, _ := worldDayAndHour(now)
	preview, ok := WeatherPreviewForMap("level_001_lake", now, nowDay, nowDay)
	if !ok {
		t.Fatal("预览应生成")
	}
	// 在逐时明细里找当前那一小时，比对中层水温。
	found := false
	for _, h := range preview.Hours {
		if h.Day == preview.NowDay && h.Hour == preview.NowHour {
			if math.Abs(h.WaterC-view.WaterC) > 0.11 {
				t.Fatalf("活跃鱼种面板水温 %v 与温度曲线中层 %v 不一致",
					view.WaterC, h.WaterC)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("没找到当前小时的明细")
	}
}

// 空名册不能崩，也不该造出幽灵数据。
func TestActiveFishEmptyRoster(t *testing.T) {
	view := WeatherActiveFishFor("level_001_lake", time.Now().Unix(), nil)
	if view == nil {
		t.Fatal("空名册也应返回结构")
	}
	if view.Total != 0 || view.ActiveNum != 0 || len(view.Fish) != 0 || len(view.Idle) != 0 {
		t.Fatalf("空名册不该有数据：%+v", view)
	}
}

// 时段名与 worldclock 同源，不能各写一份。
func TestActiveFishSegmentMatchesClock(t *testing.T) {
	now := time.Now().Unix()
	view := WeatherActiveFishFor("level_001_lake", now, activeFishRoster())
	_, hour := worldDayAndHour(now)
	want := worldclock.SegmentName(worldclock.SegmentOf(hour))
	if view.Segment != want {
		t.Fatalf("时段名 %q，worldclock 口径 %q", view.Segment, want)
	}
}

func fishKeysOf(list []WeatherActiveFish) []string {
	out := make([]string, 0, len(list))
	for _, f := range list {
		out = append(out, f.FishKey)
	}
	return out
}
