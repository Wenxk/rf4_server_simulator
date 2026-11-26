package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rf4server/internal/gameserver"
)

// 「天气预览」后台：把每张图的温度区间与逐小时天气摊开给管理员看。
//
// 数据源不另写一份：全部调用 gameserver 的导出函数（WeatherPreviewForMap /
// ExportMapWeatherKeys），保证后台看到的数字与客户端预报、判鱼用到的天气完全一致。

// weatherPreviewMaps 是地图下拉的项目。
type weatherPreviewMap struct {
	Key     string `json:"key"`
	Name    string `json:"map_name"` // 中文图名（loc_alias 回填，缺失时用 key）
	HasData bool   `json:"has_data"`
}

// handleWeatherMaps GET：返回有天气模板的地图列表。
func (s *Server) handleWeatherMaps(w http.ResponseWriter, r *http.Request) {
	keys := gameserver.ExportMapWeatherKeys()
	names := s.weatherMapNames(r.Context(), keys)
	out := make([]weatherPreviewMap, 0, len(keys))
	for _, k := range keys {
		out = append(out, weatherPreviewMap{Key: k, Name: names[k], HasData: true})
	}
	writeJSON(w, map[string]any{"maps": out})
}

// weatherMapNames 批量把 map_key 解析成中文图名。
//
// loc_alias 是 alias(level key) → loc(中文名) 的单向映射，这里反查一次
// 就能给全部图配上名字，不用在前端硬编码一张对照表。
func (s *Server) weatherMapNames(ctx context.Context, keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = k // 兜底：解析不到就用 key，至少不是空串
	}
	if len(keys) == 0 {
		return out
	}
	rows, err := s.db.SQL.QueryContext(ctx, `SELECT alias, loc FROM loc_alias`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var alias, loc string
		if rows.Scan(&alias, &loc) != nil {
			continue
		}
		if _, seen := out[alias]; seen {
			out[alias] = loc
		}
	}
	return out
}

// gameNowUnix 是「游戏现在几点」的唯一入口。
//
// 目前游戏时钟直接用真实时间推进（worldclock.EpochUnix + 实时秒 / 150），
// 所以这里就是 time.Now()；抽成函数是为了将来若启用时间加速/偏移，
// 只需改这一处，API 与前端都不用动。
func gameNowUnix() int64 { return time.Now().Unix() }

// handleWeatherPreview GET /api/weather/preview?map=level_xxx&days=7&from=day
//
// 不传 map 时返回全部图的概览（只要温度区间，不含逐时明细），
// 让前端能先画一张「所有图温度区间对比」的表，再下钻单图。
func (s *Server) handleWeatherPreview(w http.ResponseWriter, r *http.Request) {
	now := gameNowUnix()
	q := r.URL.Query()
	mapKey := strings.TrimSpace(q.Get("map"))
	days, _ := strconv.Atoi(q.Get("days"))
	if days <= 0 {
		days = 7
	}
	if days > gameserver.WeatherPreviewMaxDays {
		days = gameserver.WeatherPreviewMaxDays
	}

	keys := gameserver.ExportMapWeatherKeys()
	names := s.weatherMapNames(r.Context(), keys)

	// 不指定地图：返回概览（每图一行温度区间），不返回逐时明细。
	if mapKey == "" {
		list := make([]*gameserver.WeatherPreview, 0, len(keys))
		nowDay, _ := gameNowDayHour(now)
		for _, k := range keys {
			p, ok := gameserver.WeatherPreviewForMap(k, now, nowDay, nowDay+int64(days)-1)
			if !ok {
				continue
			}
			p.MapName = names[k]
			// 概览只回答「这张图几度、这季是什么天」，逐时明细有 168 条/图，
			// 18 张图一起下发会让首屏 payload 涨到 3000+ 条，前端也用不上。
			p.Hours = nil
			list = append(list, p)
		}
		writeJSON(w, map[string]any{
			"now_day":  nowDay,
			"game_now": now,
			"days":     days,
			"maps":     list,
		})
		return
	}

	// 指定地图：主路径。from 缺省 = 今天。
	nowDay, nowHour := gameNowDayHour(now)
	from := nowDay
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = n
		}
	}
	p, ok := gameserver.WeatherPreviewForMap(mapKey, now, from, from+int64(days)-1)
	if !ok {
		httpError(w, 404, "该地图没有天气模板: "+mapKey)
		return
	}
	p.MapName = names[mapKey]
	writeJSON(w, map[string]any{
		"preview":  p,
		"now_day":  nowDay,
		"now_hour": nowHour,
		"game_now": now,
	})
}

func gameNowDayHour(nowUnix int64) (int64, int64) {
	return gameserver.ExportGameDayAndHour(nowUnix)
}

// handleWeatherActiveFish GET /api/weather/active-fish?map=level_xxx
//
// 返回某张图此刻「哪些鱼能钓」：昼夜曲线 × 水温响应两个分项系数、综合排序、
// 以及不活跃鱼的原因。判断口径全部复用判鱼侧（gameserver.WeatherActiveFishFor），
// 后台只负责从库里把鱼种生态读出来。
func (s *Server) handleWeatherActiveFish(w http.ResponseWriter, r *http.Request) {
	mapKey := strings.TrimSpace(r.URL.Query().Get("map"))
	if mapKey == "" {
		httpError(w, 400, "缺少 map 参数")
		return
	}
	// 只允许有天气模板的图：活跃鱼种的水温来自天气计划，
	// 没模板的图算不出水温，给个假活跃名单比不给更糟。
	known := false
	for _, k := range gameserver.ExportMapWeatherKeys() {
		if k == mapKey {
			known = true
			break
		}
	}
	if !known {
		httpError(w, 404, "该地图没有天气模板: "+mapKey)
		return
	}

	now := gameNowUnix()
	roster, err := s.db.WeatherRosterFor(r.Context(), mapKey)
	if err != nil {
		httpError(w, 500, "读取鱼种名册失败: "+err.Error())
		return
	}
	inputs := make([]gameserver.WeatherEcologyInput, 0, len(roster))
	for _, r := range roster {
		inputs = append(inputs, gameserver.WeatherEcologyInput{
			FishKey: r.FishKey, NameZh: r.NameZh, Rarity: r.Rarity,
			DielMask: r.DielMask, DielCurve: r.DielCurve,
			TempMinC: r.TempMinC, TempPrefC: r.TempPrefC, TempMaxC: r.TempMaxC,
		})
	}
	view := gameserver.WeatherActiveFishFor(mapKey, now, inputs)
	writeJSON(w, map[string]any{
		"map_name": s.weatherMapNames(r.Context(), []string{mapKey})[mapKey],
		"active":   view,
		"game_now": now,
	})
}
