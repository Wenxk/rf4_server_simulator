package gameserver

import (
	"fmt"
	"math"
	"sort"

	"rf4server/internal/worldclock"
)

// 天气预览页的「当前活跃鱼种」。
//
// 这里回答一个问题：**这张图此刻哪些鱼能钓**。口径必须与判鱼一致，不能另写一套，
// 否则后台养鱼的人看到的活跃名单和实际咬钩对不上：
//   - 昼夜：`fishDielWeight`（四段曲线 `[夜,晨,昼,昏]`，峰值 1.0、最低 0.08）。
//     曲线值 < `dielActiveThreshold`(0.5) 且开了「非活跃时段禁口」时这条鱼整段不开口。
//   - 水温：与判鱼同一高斯响应 `0.35+0.65exp(-0.5z²)`，`tolerance=max(2.2,(max-min)/2)`。
//     水温本身由 `waterTempsAtLayer` 算，与页面上那条温度曲线同一个数。
//
// 两者相乘得到「综合系数」，它在含义上等于判鱼权重里昼夜与水温两项的乘积，
// 但**不含**饵料、水层、钓法、技能——后台没有这些信息，也不该假装知道。

// WeatherActiveFish 是一条鱼在当前时刻的活跃度快照。
type WeatherActiveFish struct {
	FishKey   string  `json:"fish_key"`
	NameZh    string  `json:"name_zh"`
	Rarity    int     `json:"rarity"`      // 0 普通 / 1 稀有 / 2 超稀有
	RarityTag string  `json:"rarity_tag"`  // 中文档位名
	DielMask  int64   `json:"diel_mask"`   // 昼夜掩码（1 夜 2 晨 4 昼 8 昏，位序同 worldclock.SegmentOf）
	DielLabel string  `json:"diel_label"`  // 「夜/晨」这类可读标签
	TempMinC  float64 `json:"temp_min_c"`  // 适温下限
	TempPrefC float64 `json:"temp_pref_c"` // 最适温度
	TempMaxC  float64 `json:"temp_max_c"`  // 适温上限

	// 两个分项系数，页面据此解释「为什么这条鱼算活跃/不活跃」。
	DielFactor  float64 `json:"diel_factor"`  // 昼夜系数（0.08~1）
	ThermalFact float64 `json:"thermal_fact"` // 水温响应系数（0.35~1）
	// Score 是综合系数 = DielFactor × ThermalFact。排序、筛选都用它。
	Score float64 `json:"score"`
	// Active 是「此刻算不算开口」：昼夜禁口与水温极不适都排除掉。
	Active bool `json:"active"`
	// Reason 说明不活跃的原因，活跃时为空串。
	Reason string `json:"reason"`
}

// WeatherActiveFishView 是「当前活跃鱼种」整块面板数据。
type WeatherActiveFishView struct {
	Map       string              `json:"map"`
	GameHour  int64               `json:"game_hour"`
	Segment   string              `json:"segment"`    // 当前时段名
	WaterC    float64             `json:"water_c"`    // 当前中层水温（与温度曲线同源）
	Season    string              `json:"season"`     // 当前季节
	Total     int                 `json:"total"`      // 该图名册总鱼种数
	ActiveNum int                 `json:"active_num"` // 其中此刻活跃的条数
	Fish      []WeatherActiveFish `json:"fish"`       // 已按 Score 降序，仅含 Active
	Idle      []WeatherActiveFish `json:"idle"`       // 不活跃的（含原因），供解释用
}

// 稀有度档位名，与 species_rarity 的 0/1/2 对应。
var weatherRarityTags = [3]string{"普通", "稀有", "超稀有"}

// WeatherActiveFishFor 计算某张图此刻的活跃鱼种。
//
// roster 由调用方从 LocRoster 取（后台侧负责 DB 读取），这里只做纯计算，
// 便于单测直接构造数据、也避免 gameserver 反向依赖后台的查询口径。
func WeatherActiveFishFor(mapKey string, nowUnix int64, roster []WeatherEcologyInput) *WeatherActiveFishView {
	hour := (nowUnix - worldProtocolEpochUnix) / realSecPerGameHour % 24
	seg := worldclock.SegmentOf(hour)

	// 当前水温取中层（.45）——这是页面上「中层水温」那条线的数值，
	// 也是默认钓组所在水层，玩家看活跃鱼种时脑子里对应的就是这个温度。
	air := currentAirTemp(mapKey, nowUnix)
	_, waterC := waterTempsAtLayer(air, hour, nowUnix, .45)

	out := &WeatherActiveFishView{
		Map:      mapKey,
		GameHour: hour,
		Segment:  worldclock.SegmentName(seg),
		WaterC:   r1(waterC),
		Season:   []string{"春", "夏", "秋", "冬"}[fishSeasonAt(nowUnix)],
		Total:    len(roster),
		Fish:     make([]WeatherActiveFish, 0, len(roster)),
		Idle:     make([]WeatherActiveFish, 0, len(roster)),
	}

	for _, in := range roster {
		f := WeatherActiveFish{
			FishKey:   in.FishKey,
			NameZh:    in.NameZh,
			Rarity:    in.Rarity,
			RarityTag: weatherRarityTagOf(in.Rarity),
			DielMask:  in.DielMask,
			DielLabel: worldclock.MaskLabels(in.DielMask),
			TempMinC:  r1(in.TempMinC),
			TempPrefC: r1(in.TempPrefC),
			TempMaxC:  r1(in.TempMaxC),
		}

		// 昼夜系数：直接调判鱼侧的同一个函数，连阈值都共用。
		p := fishEcologyProfile{DielMask: in.DielMask, DielCurve: in.DielCurve}
		f.DielFactor = r2(fishDielWeight(p, hour))

		// 水温系数：与 fishBiteEnvironmentWeight 里那段高斯响应逐字一致。
		// tolerance 的下限 2.2 不能省——超稀有的适温窗口只有 ±2.5℃，
		// 若按 (max-min)/2 直接算会小于 2.2，响应曲线陡到几乎全是 0。
		tolerance := math.Max(2.2, (in.TempMaxC-in.TempMinC)/2)
		z := (waterC - in.TempPrefC) / tolerance
		f.ThermalFact = r2(.35 + .65*math.Exp(-.5*z*z))

		f.Score = r2(f.DielFactor * f.ThermalFact)

		// 判定「此刻开口」的两个硬条件，与判鱼侧一一对应：
		//   1. 昼夜禁口（fish_diel_inactive_block）：曲线 < 0.5 就整段不开口。
		//   2. 水温：判鱼里水温项是乘子不是开关，理论上再冷也能出鱼。
		//      但页面要给人「现在该钓什么」的结论，所以这里额外标出「水温偏离最适
		//      超过 2 个 tolerance」的极端情况——那种鱼实际概率低到可以忽略。
		switch {
		case f.DielFactor < dielActiveThreshold:
			f.Active = false
			f.Reason = "非活跃时段（" + f.DielLabel + "）"
		case math.Abs(z) > 2:
			f.Active = false
			f.Reason = "水温偏离最适（" + formatTempSpan(in.TempPrefC) + "）"
		default:
			f.Active = true
		}

		if f.Active {
			out.Fish = append(out.Fish, f)
		} else {
			out.Idle = append(out.Idle, f)
		}
	}

	// 综合系数降序；系数相同按名字排，避免每次刷新顺序抖动。
	sort.SliceStable(out.Fish, func(i, j int) bool {
		if out.Fish[i].Score != out.Fish[j].Score {
			return out.Fish[i].Score > out.Fish[j].Score
		}
		return out.Fish[i].NameZh < out.Fish[j].NameZh
	})
	sort.SliceStable(out.Idle, func(i, j int) bool {
		if out.Idle[i].Score != out.Idle[j].Score {
			return out.Idle[i].Score > out.Idle[j].Score
		}
		return out.Idle[i].NameZh < out.Idle[j].NameZh
	})
	out.ActiveNum = len(out.Fish)
	return out
}

// WeatherEcologyInput 是活跃鱼种计算需要的鱼种生态字段。
//
// 由后台从 fish_ecology（已合并 fish_ecology_loc 覆盖）读取后传入，
// gameserver 不直接查库，保持这一层是纯函数。
type WeatherEcologyInput struct {
	FishKey   string
	NameZh    string
	Rarity    int
	DielMask  int64
	DielCurve string
	TempMinC  float64
	TempPrefC float64
	TempMaxC  float64
}

func weatherRarityTagOf(tier int) string {
	if tier < 0 || tier >= len(weatherRarityTags) {
		return weatherRarityTags[0]
	}
	return weatherRarityTags[tier]
}

func formatTempSpan(pref float64) string {
	return fmt.Sprintf("最适 %.1f℃", pref)
}

// currentAirTemp 取该图当前小时的计划气温。
//
// 与页面温度曲线同源（mapWeatherProfile.hour），所以「活跃鱼种」面板上的水温
// 和上方温度曲线在同一时刻必然是同一个数，不会出现两处对不上。
func currentAirTemp(mapKey string, nowUnix int64) float64 {
	p := mapWeatherProfiles()[mapKey]
	if p == nil {
		// 没模板的地图不该走到这里（后台下拉只列有模板的图）。
		// 返回季节均值让水温退化成合理值，而不是 0℃。
		return []float64{10, 23, 13, 1}[fishSeasonAt(nowUnix)]
	}
	day := weatherPlanDay(nowUnix)
	hour := (nowUnix - worldProtocolEpochUnix) / realSecPerGameHour % 24
	n := p.hour(day, hour)
	air, ok := fishingWeatherFloat(&n, 2)
	if !ok {
		return []float64{10, 23, 13, 1}[fishSeasonAt(nowUnix)]
	}
	return air
}
