package gameserver

import "strings"

// 饵料 → diet_json 食性键映射。仅用于节奏打分（bite_pace）与后台展示，
// 不再参与「吃不吃」的咬口资格；咬口资格统一由规则 allow/deny 决定。
//
// 未知类型返回 nil：这类饵"判不出吃什么"，交给调用方保持中性，不硬判不吃。
func baitDietKeys(b ruleBait) []string {
	switch b.baitType {
	case 2, 6, 7: // 幼虫/蚯蚓水蛭/昆虫成虫
		return []string{"worm"}
	case 3, 4, 14, 15: // 蛙/活饵鱼/鱼片/死饵鱼
		return []string{"fish"}
	case 8, 9, 17: // 煮饵/颗粒/风味饵/Zig 球
		return []string{"boilie"}
	case 10, 11, 13: // 玉米/老虎坚果/面团与粥
		return []string{"plant"}
	case 12, 16: // 钩虾/海虫贝类
		return []string{"crustacean"}
	case 1:
		return miscBaitDietKeys(b)
	}
	return nil
}

// miscBaitDietKeys 类别 1「杂类」其实是混装货架：藻类/面包/玉米/豌豆/马铃薯是植物饵，
// 香煮成饵是煮饵，贻贝/珠蚌/虾仁/蟹肉是甲壳类，鱼块/鱼子/鱼肉类。
// 必须按 id 与名称精确归类——旧版一律返回「植物/煮饵/甲壳/鱼 任一命中」，
// 结果是河斑马纹贻贝肉在拉多加湖能钓到 38/38 个鱼种，等于没有食性约束。
func miscBaitDietKeys(b ruleBait) []string {
	if strings.HasPrefix(b.id, "boil_") {
		return []string{"boilie"}
	}
	switch b.id {
	case "bait_dreissena", "unio_crassus", "crawfish_meat", "crab_meat":
		return []string{"crustacean"}
	case "fish_pieces", "caviar":
		return []string{"fish"}
	case "watergrass", "cheese_cube", "wheat_grain", "sunflower_cake", "bread", "corn", "cabbage", "peas", "potato":
		return []string{"plant"}
	}
	n := ruleName(b.name)
	switch {
	case strings.Contains(n, "贻贝") || strings.Contains(n, "珠蚌") || strings.Contains(n, "虾") || strings.Contains(n, "蟹"):
		return []string{"crustacean"}
	case strings.Contains(n, "鱼"):
		return []string{"fish"}
	case strings.Contains(n, "煮"):
		return []string{"boilie"}
	case strings.Contains(n, "玉米") || strings.Contains(n, "面包") || strings.Contains(n, "藻") ||
		strings.Contains(n, "豌豆") || strings.Contains(n, "马铃薯") || strings.Contains(n, "菜叶"):
		return []string{"plant"}
	}
	return nil
}
