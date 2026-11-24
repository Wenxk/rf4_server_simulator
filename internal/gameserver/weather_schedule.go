package gameserver

import (
	"encoding/json"
	"hash/fnv"
	"math"
	"sort"
	"sync"
)

// 地图原有预报提供气候差异；绝对游戏日决定天气，不随玩家进入时间重抽。
type mapWeatherProfile struct {
	days   []irNode
	hours  [24]irNode
	rangeC float64
	seed   uint64
}

var weatherProfilesOnce sync.Once
var weatherProfiles map[string]*mapWeatherProfile

func mapWeatherProfiles() map[string]*mapWeatherProfile {
	weatherProfilesOnce.Do(func() {
		weatherProfiles = make(map[string]*mapWeatherProfile)
		maps, err := loadMapDefs()
		if err != nil {
			return
		}
		for key, raw := range maps {
			var root irNode
			if json.Unmarshal(raw, &root) != nil || materializeIR(&root) != nil {
				continue
			}
			p := &mapWeatherProfile{}
			hash := fnv.New64a()
			_, _ = hash.Write([]byte(key))
			p.seed = hash.Sum64()
			low, high := math.Inf(1), math.Inf(-1)
			irWalkObj(&root, func(n *irNode) {
				switch n.Class {
				case forecastClass:
					p.days = append(p.days, *n)
				case hourPlanClass:
					h, ok := irFieldInt(n, 1)
					temp, valid := fishingWeatherFloat(n, 2)
					if ok && valid && h >= 0 && h < 24 {
						p.hours[h] = *n
						low, high = math.Min(low, temp), math.Max(high, temp)
					}
				}
			})
			sort.Slice(p.days, func(i, j int) bool {
				a, _ := irFieldInt(&p.days[i], 0)
				b, _ := irFieldInt(&p.days[j], 0)
				return a < b
			})
			complete := len(p.days) > 0
			for _, h := range p.hours {
				complete = complete && len(h.Fields) == 10
			}
			if complete {
				p.rangeC = high - low
				weatherProfiles[key] = p
			}
		}
	})
	return weatherProfiles
}

func copyWeatherNode(n irNode) irNode {
	n.Fields = append([]irField(nil), n.Fields...)
	return n
}

func setWeatherInt(n *irNode, idx int, value int64) {
	n.Fields[idx].V, _ = json.Marshal(value)
}

func setWeatherFloat(n *irNode, idx int, value float64) {
	setWeatherInt(n, idx, int64(math.Float32bits(float32(value))))
}

func (p *mapWeatherProfile) day(day int64) irNode {
	idx := (day + int64(p.seed%uint64(len(p.days)))) % int64(len(p.days))
	if idx < 0 {
		idx += int64(len(p.days))
	}
	n := copyWeatherNode(p.days[idx])
	setWeatherInt(&n, 0, day)
	return n
}

func (p *mapWeatherProfile) hour(day, hour int64) irNode {
	daily, next := p.day(day), p.day(day+1)
	kind, _ := irFieldInt(&daily, 1)
	high, _ := fishingWeatherFloat(&daily, 2)
	nextHigh, _ := fishingWeatherFloat(&next, 2)
	wind, _ := fishingWeatherFloat(&daily, 3)
	nextWind, _ := fishingWeatherFloat(&next, 3)
	water, _ := irFieldInt(&daily, 4)
	n := copyWeatherNode(p.hours[hour])
	setWeatherInt(&n, 0, day)
	setWeatherInt(&n, 1, hour)
	// 日出前低温、午后高温；日际温差在一天内缓慢衔接。
	fraction := float64(hour) / 24
	cycle := (1 - math.Cos((float64(hour)-4)*math.Pi/12)) / 2
	temp := high + (nextHigh-high)*fraction - p.rangeC*(1-cycle)
	cloud, rain, thunder := .02, 0.0, 0.0
	switch int(kind) {
	case fishingWeatherFewClouds:
		cloud = .2
	case fishingWeatherCloudy:
		cloud = .6
	case fishingWeatherRain, fishingWeatherHeavyRain, fishingWeatherThunderstorm:
		cloud = .7
		// 每日一次连续天气过程，强度逐小时升降，预报的类型必定达到。
		center := 6 + int((uint64(day)^p.seed)%13)
		distance := math.Abs(float64(hour) - float64(center))
		if distance < 4 {
			envelope := (1 + math.Cos(distance*math.Pi/4)) / 2
			cloud += .25 * envelope
			rain = .4 * envelope
			if int(kind) >= fishingWeatherHeavyRain {
				rain = .9 * envelope
			}
			if int(kind) == fishingWeatherThunderstorm {
				thunder = .8 * envelope
			}
			temp -= 1.5 * envelope
		}
	}
	setWeatherFloat(&n, 2, temp)
	setWeatherFloat(&n, 3, cloud)
	setWeatherFloat(&n, 4, rain)
	setWeatherFloat(&n, 5, thunder)
	// 预报与小时计划都使用 km/h；吃口入口再换算为 m/s。
	setWeatherFloat(&n, 6, math.Max(0, wind+(nextWind-wind)*fraction))
	setWeatherInt(&n, 9, water)
	return n
}

// weatherPlanDay 返回天气计划用的游戏日号，自 worldProtocolEpochUnix 起算。
//
// 这是**查表坐标**，不是给玩家看的日号。fishingWeatherPlanFromIR 把每小时
// 记成 day*24+hour，weatherAt 用 (now-协议锚点)/realSecPerGameHour 换算，
// 两边必须同一个起算点，否则生成与查表会整体错开十万天、一小时也查不到。
//
// 不能用 worldDayAndHour（日号纪元）：那个会随开服重置，而天气是协议内容，
// 客户端按 f0 的日号显示预报，锚点必须和客户端时间轴一致。
func weatherPlanDay(nowUnix int64) int64 {
	return (nowUnix - worldProtocolEpochUnix) / (realSecPerGameHour * 24)
}

// reanchorWeather 沿用地图协议容器，填入昨天至未来六天的统一天气计划。
func reanchorWeather(root *irNode, nowUnix int64) {
	if root == nil || len(root.Fields) == 0 {
		return
	}
	var key string
	if json.Unmarshal(root.Fields[0].V, &key) != nil {
		return
	}
	p := mapWeatherProfiles()[key]
	if p == nil {
		return
	}
	// 天气计划的 day 字段与 weatherAt 的查表坐标必须同一时间轴，否则
	// 「生成时用日号纪元、查询时用协议锚点」会整体错开十万天，查不到任何一小时。
	// 天气是协议内容（客户端按 f0 日号显示预报），所以两边都走协议锚点。
	day := weatherPlanDay(nowUnix)
	var forecasts []*irNode
	var hours *irNode
	irWalkObj(root, func(n *irNode) {
		if n.Class == forecastClass {
			forecasts = append(forecasts, n)
		}
		if len(n.Items) > 0 && n.Items[0].Class == hourPlanClass {
			hours = n
		}
	})
	if hours == nil || len(forecasts) != len(p.days) {
		return
	}
	start, end := day-1, day+int64(len(forecasts))-1
	items := make([]irNode, 0, (end-start)*24+1)
	for d := start; d <= end; d++ {
		forecast := p.day(d)
		maxTemp, maxWind := math.Inf(-1), 0.0
		for h := int64(0); h < 24; h++ {
			n := p.hour(d, h)
			temp, _ := fishingWeatherFloat(&n, 2)
			wind, _ := fishingWeatherFloat(&n, 6)
			maxTemp, maxWind = math.Max(maxTemp, temp), math.Max(maxWind, wind)
			if d < end || h == 0 {
				items = append(items, n)
			}
		}
		if d < end {
			setWeatherFloat(&forecast, 2, maxTemp)
			setWeatherFloat(&forecast, 3, maxWind)
			*forecasts[d-start] = forecast
		}
	}
	hours.Items = items
}
