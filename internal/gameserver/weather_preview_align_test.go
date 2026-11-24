package gameserver

import (
	"encoding/json"
	"testing"
	"time"

	"rf4server/internal/worldclock"
)

// 后台面板的天气必须与客户端看到的是**同一份**。
//
// 这条测试锁住日号轴与天气模板的对齐：面板展示日号（DayAndHour）与天气模板
// 索引日号（weatherPlanDay）必须在同一坐标系里。2026-11-23 拆分纪元时两者
// 差了 weatherPlanDayOffset；2026-11-24 日号回归世界时钟轴后该偏移归 0。
//
// 无论偏移是 0 还是 99180，这里逐项比对的判据不变：面板第 d 天的预报类型/最高温，
// 必须等于 reanchorWeather 在同一天下发给客户端的那一条。偏移写错立刻炸。
func TestWeatherPreviewMatchesClientForecast(t *testing.T) {
	nowUnix := time.Now().Unix()
	// 取面板窗口里的今天。
	nowDay, _ := worldclock.DayAndHour(nowUnix)
	days := int64(7)

	for _, key := range ExportMapWeatherKeys() {
		preview, ok := WeatherPreviewForMap(key, nowUnix, nowDay, nowDay+days-1)
		if !ok {
			t.Fatalf("%s 无天气模板", key)
		}
		if len(preview.Days) == 0 {
			t.Fatalf("%s 面板没有逐日结果", key)
		}

		// 客户端那份：重锚后的预报节点，按协议日号索引。
		maps, err := loadMapDefs()
		if err != nil {
			t.Fatal(err)
		}
		var root irNode
		if err := json.Unmarshal(maps[key], &root); err != nil {
			t.Fatalf("%s 解析: %v", key, err)
		}
		if err := materializeIR(&root); err != nil {
			t.Fatalf("%s 物化: %v", key, err)
		}
		reanchorWeather(&root, nowUnix)
		client := map[int64]irNode{}
		irWalkObj(&root, func(n *irNode) {
			if n.Class != forecastClass {
				return
			}
			if d, ok := irFieldInt(n, 0); ok {
				client[d] = *n
			}
		})

		for _, day := range preview.Days {
			// 面板日号 → 模板日号。偏移现在恒为 0，但换算点保留，
			// 保证「两边日号不是同一个数」这件事在代码里仍然显式存在。
			planDay := day.Day + weatherPlanDayOffset
			sent, ok := client[planDay]
			if !ok {
				t.Fatalf("%s 面板第 %d 天（模板 %d）在客户端预报里不存在：说明两边日号没对齐",
					key, day.Day, planDay)
			}
			kind, _ := irFieldInt(&sent, 1)
			if int64(kind) != int64(day.Kind) {
				t.Fatalf("%s 第 %d 天天气类型不一致：面板 %d(%s)，客户端 %d",
					key, day.Day, day.Kind, day.KindName, kind)
			}
			high, _ := fishingWeatherFloat(&sent, 2)
			if diff := high - day.HighC; diff > 0.05 || diff < -0.05 {
				t.Fatalf("%s 第 %d 天最高温不一致：面板 %.1f，客户端 %.1f",
					key, day.Day, day.HighC, high)
			}
		}
	}
}
