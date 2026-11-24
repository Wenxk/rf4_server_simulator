package gameserver

import (
	"fmt"
	"math"
	"sort"

	"rf4server/internal/worldclock"
)

// weatherPreviewLayers 是「这张图的水温区间」的采样水层。
//
// 必须与判鱼侧同口径：fishWaterConditionsFor 里 layer 随钓法在 0（表层）
// 到 0.93（底钓）之间变化，只看中层会把区间算窄 2~7℃——2026-11-23 的
// 鱼种适温标定就因为只算 0.45 而误判了 11 张图「温度不够」。
var weatherPreviewLayers = []float64{0.0, 0.2, 0.45, 0.7, 0.93}

// WeatherPreviewHour 是后台「天气预览」用的单小时快照。
//
// 字段全部是「只读展示」语义：气温/水温/风/雾/云/雨/雷取自与判鱼完全同一套
// 天气排程（mapWeatherProfile.hour），因此后台看到的数字就是玩家客户端收到的数字。
type WeatherPreviewHour struct {
	Day         int64   `json:"day"`        // 游戏日
	Hour        int64   `json:"hour"`       // 游戏小时 0..23
	AirTempC    float64 `json:"air_temp_c"` // 气温
	SurfaceC    float64 `json:"surface_c"`  // 表层水温（layer=0）
	WaterC      float64 `json:"water_c"`    // 中层水温（layer=0.45，与默认钓组同口径）
	DeepC       float64 `json:"deep_c"`     // 底层水温（layer=0.93，底钓口径）
	Cloud       float64 `json:"cloud"`      // 云量 0..1
	Rain        float64 `json:"rain"`       // 降雨 0..1
	Thunder     float64 `json:"thunder"`    // 雷暴 0..1
	WindKMH     float64 `json:"wind_kmh"`   // 风速 km/h
	Fog         float64 `json:"fog"`        // 雾 0..1
	Kind        int     `json:"kind"`       // 天气类型，见 WeatherPreviewKindName
	KindName    string  `json:"kind_name"`  // 中文名
	Segment     int     `json:"segment"`    // 昼夜段位序 0夜 1晨 2昼 3昏
	SegmentName string  `json:"segment_name"`
	IsNow       bool    `json:"is_now"` // 是否当前小时
}

// WeatherPreviewDay 是逐日预报汇总。
type WeatherPreviewDay struct {
	Day        int64   `json:"day"`
	Kind       int     `json:"kind"` // 该日预报类型（forecast 节点原值）
	KindName   string  `json:"kind_name"`
	HighC      float64 `json:"high_c"`       // 当日最高气温
	LowC       float64 `json:"low_c"`        // 当日最低气温
	WaterHighC float64 `json:"water_high_c"` // 当日最高中层水温
	WaterLowC  float64 `json:"water_low_c"`  // 当日最低中层水温
	MaxWind    float64 `json:"max_wind_kmh"`
	RainHour   int     `json:"rain_hours"` // 有明显降雨的小时数
}

// WeatherPreviewTempBand 是温度分档统计，用于回答「这张图通常在几度」。
type WeatherPreviewTempBand struct {
	FromC float64 `json:"from_c"`
	ToC   float64 `json:"to_c"`
	Count int     `json:"count"`
	Share float64 `json:"share"` // 占该区间的比例
}

// WeatherPreviewSeason 是单个季节的水温/气温区间。
//
// 与 Days/Hours 的区别：Days/Hours 只反映「当前这段预报窗口」，随季节漂移；
// 这里扫遍该图 8 天预报模板 × 24 小时 × 5 个水层，给出**这张图在一个季节里
// 到底能出多宽的温度**——鱼种适温标定用的就是这个口径。
type WeatherPreviewSeason struct {
	Season     int     `json:"season"`
	SeasonName string  `json:"season_name"`
	WaterMinC  float64 `json:"water_min_c"`
	WaterMaxC  float64 `json:"water_max_c"`
	AirMinC    float64 `json:"air_min_c"`
	AirMaxC    float64 `json:"air_max_c"`
	IsNow      bool    `json:"is_now"` // 当前游戏季节
}

// WeatherPreview 是某张地图一段时间的天气预览。
type WeatherPreview struct {
	Map       string `json:"map"`
	MapName   string `json:"map_name"` // 中文图名，由后台按 loc_alias 补
	DayFrom   int64  `json:"day_from"`
	DayTo     int64  `json:"day_to"`
	NowDay    int64  `json:"now_day"`
	NowHour   int64  `json:"now_hour"`
	GameClock string `json:"game_clock"` // HH:MM，游戏内时钟（真实分钟，非整点）

	// 温度区间（全水层覆盖——这是「这图能出什么温度」的答案）
	WaterMinC float64 `json:"water_min_c"`
	WaterMaxC float64 `json:"water_max_c"`
	AirMinC   float64 `json:"air_min_c"`
	AirMaxC   float64 `json:"air_max_c"`

	Days       []WeatherPreviewDay      `json:"days"`
	Hours      []WeatherPreviewHour     `json:"hours"`
	Bands      []WeatherPreviewTempBand `json:"bands"`
	Seasons    []WeatherPreviewSeason   `json:"seasons"`
	Season     int                      `json:"season"`
	SeasonName string                   `json:"season_name"`
}

var weatherPreviewKindNames = map[int]string{
	fishingWeatherClear:        "晴",
	fishingWeatherFewClouds:    "少云",
	fishingWeatherCloudy:       "多云",
	fishingWeatherRain:         "小雨",
	fishingWeatherHeavyRain:    "大雨",
	fishingWeatherThunderstorm: "雷雨",
}

// WeatherPreviewKindName 返回天气类型的中文名。
func WeatherPreviewKindName(kind int) string {
	if n, ok := weatherPreviewKindNames[kind]; ok {
		return n
	}
	return "未知"
}

// WeatherPreviewMaxDays 是一次天气预览允许的最大天数。
const WeatherPreviewMaxDays = 14

// weatherPlanDayOffset 面板展示日号与天气模板日号的差值：模板日号 = 展示日号 + 本值。
//
// **2026-11-24 起恒为 0**：展示日号已回归世界时钟轴（与客户端「游戏中 N 天」同源），
// 而天气模板的 f0 本来就锚在协议轴上，两者现在是同一个坐标系，不需要偏移。
//
// 保留这个变量而不是直接删掉，是为了让「面板日号」与「模板日号」在代码里依然
// 是两个有名字的东西——万一以后又要在展示层做平移，改动点仍然只有这一行，
// 而不是散落到 WeatherPreviewForMap 的循环里。
// 守卫 TestWeatherPreviewMatchesClientForecast 会逐图逐日比对两边，偏移写错立刻炸。
var weatherPlanDayOffset int64 = 0

// WeatherPreviewForMap 生成指定地图的天气预览。
//
// dayFrom/dayTo 是**游戏日号**（与客户端「游戏中 N 天」同一个数），
// 超过 WeatherPreviewMaxDays 会被截断。日号本身已锚在协议轴上（见
// worldclock.DayAndHour），所以索引天气模板时不需要任何偏移。
// 全部数值来自与客户端下发、判鱼共用同一套 mapWeatherProfile，不是另写一份近似。
func WeatherPreviewForMap(mapKey string, nowUnix int64, dayFrom, dayTo int64) (*WeatherPreview, bool) {
	p := mapWeatherProfiles()[mapKey]
	if p == nil || len(p.days) == 0 {
		return nil, false
	}
	nowDay, nowHour := worldclock.DayAndHour(nowUnix)
	if dayTo < dayFrom {
		dayTo = dayFrom
	}
	if dayTo-dayFrom > WeatherPreviewMaxDays-1 {
		dayTo = dayFrom + WeatherPreviewMaxDays - 1
	}

	out := &WeatherPreview{
		Map:       mapKey,
		DayFrom:   dayFrom,
		DayTo:     dayTo,
		NowDay:    nowDay,
		NowHour:   nowHour,
		GameClock: formatGameClock(nowUnix),
		Season:    fishSeasonAt(nowUnix),
	}
	out.SeasonName = []string{"春", "夏", "秋", "冬"}[out.Season%4]

	airLo, airHi := math.Inf(1), math.Inf(-1)
	waterLo, waterHi := math.Inf(1), math.Inf(-1)

	for d := dayFrom; d <= dayTo; d++ {
		// 展示日号与模板日号现在是同一个坐标系（weatherPlanDayOffset 已归 0）。
		// 留着这行是为了让两套日号的换算点仍然集中在一处。
		planDay := d + weatherPlanDayOffset
		// 日预报类型取 forecast 节点原值——客户端预报卡片显示的就是这个，
		// 「正午那一小时」会因降雨过程窗偏移而在部分日子对不上。
		forecast := p.day(planDay)
		dayKind, ok := irFieldInt(&forecast, 1)
		if !ok || dayKind < 0 || dayKind > fishingWeatherThunderstorm {
			dayKind = fishingWeatherClear
		}

		dayHigh, dayLow := math.Inf(-1), math.Inf(1)
		waterHigh, waterLow := math.Inf(-1), math.Inf(1)
		dayWind := 0.0
		rainHours := 0

		for h := int64(0); h < 24; h++ {
			n := p.hour(planDay, h)
			air, _ := fishingWeatherFloat(&n, 2)
			cloud, _ := fishingWeatherFloat(&n, 3)
			rain, _ := fishingWeatherFloat(&n, 4)
			thunder, _ := fishingWeatherFloat(&n, 5)
			wind, _ := fishingWeatherFloat(&n, 6)
			fog, _ := fishingWeatherFloat(&n, 8)

			hourKind := fishingWeatherHour{
				airTemp: air, wind: wind / 3.6, fog: fog,
				cloud: cloud, rain: rain, thunder: thunder,
			}.kind()

			// 三层水温都算出来：中层给曲线，表层/底层给区间（判鱼按钓法切水层）。
			surf, _ := waterTempsAtLayer(air, h, nowUnix, 0)
			_, midW := waterTempsAtLayer(air, h, nowUnix, 0.45)
			_, deepW := waterTempsAtLayer(air, h, nowUnix, 0.93)

			seg := worldclock.SegmentOf(h)
			out.Hours = append(out.Hours, WeatherPreviewHour{
				Day: d, Hour: h,
				AirTempC: r1(air), SurfaceC: r1(surf), WaterC: r1(midW), DeepC: r1(deepW),
				Cloud: r2(cloud), Rain: r2(rain), Thunder: r2(thunder),
				WindKMH: r1(wind), Fog: r2(fog),
				Kind: hourKind, KindName: WeatherPreviewKindName(hourKind),
				Segment: seg, SegmentName: worldclock.SegmentName(seg),
				IsNow: d == nowDay && h == nowHour,
			})

			dayHigh = math.Max(dayHigh, air)
			dayLow = math.Min(dayLow, air)
			waterHigh = math.Max(waterHigh, midW)
			waterLow = math.Min(waterLow, midW)
			dayWind = math.Max(dayWind, wind)
			if rain > 0.05 {
				rainHours++
			}
			// 区间统计覆盖全水层，否则底钓鱼看到的温度落在区间之外。
			airLo, airHi = math.Min(airLo, air), math.Max(airHi, air)
			waterLo = math.Min(waterLo, math.Min(surf, math.Min(midW, deepW)))
			waterHi = math.Max(waterHi, math.Max(surf, math.Max(midW, deepW)))
		}
		out.Days = append(out.Days, WeatherPreviewDay{
			Day: d, Kind: int(dayKind), KindName: WeatherPreviewKindName(int(dayKind)),
			HighC: r1(dayHigh), LowC: r1(dayLow),
			WaterHighC: r1(waterHigh), WaterLowC: r1(waterLow),
			MaxWind: r1(dayWind), RainHour: rainHours,
		})
	}

	if math.IsInf(airLo, 1) {
		return out, true
	}
	out.AirMinC, out.AirMaxC = r1(airLo), r1(airHi)
	out.WaterMinC, out.WaterMaxC = r1(waterLo), r1(waterHi)
	out.Bands = tempBands(waterLo, waterHi, out.Hours)
	out.Seasons = seasonalTempRanges(mapKey, nowUnix)
	return out, true
}

// seasonalTempRanges 扫遍该图 8 天预报模板 × 24 小时 × 5 个水层，给出四季温度区间。
//
// 这是「这张图能出什么温度」的权威答案：Days/Hours 只看当前 7 天窗口，
// 换个季节数字就变；而鱼种适温标定要的是全年最宽口径。
// 水温公式里的季节项（seasonalAir/seasonalDeep）是全局硬编码、与地图无关，
// 地图差异只经 thermalResponse 衰减后传导，所以这里必须按季节分别采样。
func seasonalTempRanges(mapKey string, nowUnix int64) []WeatherPreviewSeason {
	p := mapWeatherProfiles()[mapKey]
	if p == nil || len(p.days) == 0 {
		return nil
	}
	nowSeason := fishSeasonAt(nowUnix)
	out := make([]WeatherPreviewSeason, 0, 4)
	for s := 0; s < 4; s++ {
		// 各季取年中代表日：春 3/16、夏 6/16、秋 9/16、冬 1/16。
		// 必须用季节锚点换算，否则日号纪元一重置，四季区间会跟着跳。
		seasonUnix := worldclock.SeasonEpochUnix + seasonSampleDay(s)*3600
		airLo, airHi := math.Inf(1), math.Inf(-1)
		waterLo, waterHi := math.Inf(1), math.Inf(-1)
		for d := int64(0); d < int64(len(p.days)); d++ {
			for h := int64(0); h < 24; h++ {
				n := p.hour(d, h)
				air, ok := fishingWeatherFloat(&n, 2)
				if !ok {
					continue
				}
				airLo, airHi = math.Min(airLo, air), math.Max(airHi, air)
				for _, ly := range weatherPreviewLayers {
					_, w := waterTempsAtLayer(air, h, seasonUnix, ly)
					waterLo, waterHi = math.Min(waterLo, w), math.Max(waterHi, w)
				}
			}
		}
		item := WeatherPreviewSeason{
			Season: s, SeasonName: []string{"春", "夏", "秋", "冬"}[s],
			IsNow: s == nowSeason,
		}
		if !math.IsInf(airLo, 1) {
			item.AirMinC, item.AirMaxC = r1(airLo), r1(airHi)
			item.WaterMinC, item.WaterMaxC = r1(waterLo), r1(waterHi)
		}
		out = append(out, item)
	}
	return out
}

// seasonSampleDay 返回某季节代表日的「年内天数」（0 = 1 月 1 日）。
//
// 季节划分与 fishSeasonAt 一致：冬 12/1/2、春 3/4/5、夏 6/7/8、秋 9/10/11。
// 月长按 28 天算 2 月（与 fishSeasonAt 的 monthLengths 同口径）。
func seasonSampleDay(season int) int64 {
	// 各月起始累计天数（1 月起）。
	starts := [12]int64{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	switch season {
	case 0:
		return starts[2] + 15 // 3 月 16 日
	case 1:
		return starts[5] + 15 // 6 月 16 日
	case 2:
		return starts[8] + 15 // 9 月 16 日
	default:
		return starts[0] + 15 // 1 月 16 日
	}
}

// tempBands 把逐时中层水温按 2℃ 分档统计。
func tempBands(lo, hi float64, hours []WeatherPreviewHour) []WeatherPreviewTempBand {
	if len(hours) == 0 || math.IsInf(lo, 1) {
		return nil
	}
	start := math.Floor(lo/2) * 2
	end := math.Ceil(hi/2) * 2
	var out []WeatherPreviewTempBand
	total := len(hours)
	for a := start; a < end; a += 2 {
		cnt := 0
		for _, h := range hours {
			if h.WaterC >= a && h.WaterC < a+2 {
				cnt++
			}
		}
		out = append(out, WeatherPreviewTempBand{
			FromC: a, ToC: a + 2, Count: cnt,
			Share: float64(cnt) / float64(total),
		})
	}
	return out
}

// waterTempsAtLayer 返回该小时的（表层水温, 指定水层水温）。
//
// 与 fishWaterConditionsFor 同公式，只是不依赖 fishRollContext：
// 后台预览要的是「这一小时的物理水温」，不该绑定某条鱼的具体活跃度。
func waterTempsAtLayer(air float64, hour int64, nowUnix int64, layer float64) (surface, water float64) {
	season := fishSeasonAt(nowUnix)
	seasonalAir := []float64{10, 23, 13, 1}[season]
	seasonalDeep := []float64{9, 14, 11, 4}[season]
	daylight := clampFloat((1+math.Cos((float64(hour)-12)*math.Pi/12))/2, 0, 1)
	// 后台预览用 neutral 热响应（activity=1.0），不跟随某条鱼的具体活跃度。
	thermalResponse := .36 + .16*1.0
	sfc := seasonalAir + (air-seasonalAir)*thermalResponse + daylight*1.2
	deep := seasonalDeep + (sfc-seasonalDeep)*.22
	return roundTo(sfc, 2), roundTo(sfc*(1-layer)+deep*layer, 2)
}

// formatGameClock 把 nowUnix 换算成游戏内 HH:MM。
//
// 一个游戏小时 = 150 真实秒，所以小时内还有真实分钟可算——直接用
// worldclock.DayAndHour 的整点小时会让后台始终停在 :00，看着像时钟没走。
//
// 起算点必须与 DayAndHour 同源（ProtocolEpochUnix）。虽然两者相差整游戏日、
// 只取 hour/minute 时结果一样，但混用两个 epoch 会让下一个读这段的人以为
// 这里和日号是两套轴，改一处漏一处。
func formatGameClock(nowUnix int64) string {
	secs := nowUnix - worldclock.ProtocolEpochUnix
	if secs < 0 {
		secs = 0
	}
	hour := (secs / worldclock.RealSecPerGameHour) % 24
	within := secs % worldclock.RealSecPerGameHour
	minute := within * 60 / worldclock.RealSecPerGameHour
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func r1(v float64) float64 { return roundTo(v, 1) }
func r2(v float64) float64 { return roundTo(v, 2) }

// ExportMapWeatherKeys 返回所有有天气模板的地图 map_key（供后台列下拉）。
func ExportMapWeatherKeys() []string {
	profiles := mapWeatherProfiles()
	out := make([]string, 0, len(profiles))
	for k := range profiles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ExportGameDayAndHour 把 nowUnix 换算成（游戏日, 游戏小时），供后台展示。
func ExportGameDayAndHour(nowUnix int64) (day, hour int64) {
	return worldclock.DayAndHour(nowUnix)
}
