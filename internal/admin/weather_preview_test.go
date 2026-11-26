package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

// 天气预览后台：地图列表 → 概览 → 单图明细，全链路走 HTTP。
//
// 断言重点不是具体温度数字（那由数据资产决定），而是：
//   - 每张有天气模板的图都能出数据、能配中文名；
//   - 逐时序列、日汇总、温度分档、四季区间四块都非空且自洽；
//   - 当前小时有且只有一条 is_now，方便前端定位「现在」。
func TestWeatherPreviewAdminAPI(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	// 中文图名走 loc_alias 反查，塞两条足够验证回填逻辑。
	for _, q := range []string{
		`INSERT INTO loc_alias(alias, loc, note) VALUES('level_001_lake','克马羚诺也湖','weather test')`,
		`INSERT INTO loc_alias(alias, loc, note) VALUES('level_009_ahtuba','阿赫图巴河','weather test')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed loc_alias: %v", err)
		}
	}

	srv := NewServer(config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 10 * time.Second}
	call := func(method, path, token, body string) (int, string) {
		t.Helper()
		r, _ := http.NewRequest(method, base+path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out bytes.Buffer
		_, _ = out.ReadFrom(resp.Body)
		return resp.StatusCode, out.String()
	}

	code, body := call("POST", "/api/login", "", `{"username":"admin","password":"admin123"}`)
	if code != 200 {
		t.Fatalf("login: %d %s", code, body)
	}
	var login struct{ Token string }
	_ = json.Unmarshal([]byte(body), &login)

	// 1) 地图列表：18 张图都有天气模板，图名能回填。
	code, body = call("GET", "/api/weather/maps", login.Token, "")
	if code != 200 {
		t.Fatalf("maps: %d %s", code, body)
	}
	var mapsResp struct {
		Maps []weatherPreviewMap `json:"maps"`
	}
	if err := json.Unmarshal([]byte(body), &mapsResp); err != nil {
		t.Fatalf("maps json: %v %s", err, body)
	}
	if len(mapsResp.Maps) != 18 {
		t.Fatalf("地图数=%d，期望 18", len(mapsResp.Maps))
	}
	names := map[string]string{}
	for _, m := range mapsResp.Maps {
		names[m.Key] = m.Name
		if !m.HasData {
			t.Fatalf("%s 应标记为有天气模板", m.Key)
		}
		if m.Name == "" {
			t.Fatalf("%s 图名为空", m.Key)
		}
	}
	if names["level_001_lake"] != "克马羚诺也湖" {
		t.Fatalf("图名未按 loc_alias 回填: %q", names["level_001_lake"])
	}

	// 2) 概览：不传 map 时每图一行，必须有温度区间，但不该带逐时明细。
	code, body = call("GET", "/api/weather/preview?days=7", login.Token, "")
	if code != 200 {
		t.Fatalf("overview: %d %s", code, body)
	}
	var overview struct {
		NowDay int64 `json:"now_day"`
		Days   int   `json:"days"`
		Maps   []struct {
			Map       string  `json:"map"`
			MapName   string  `json:"map_name"`
			WaterMinC float64 `json:"water_min_c"`
			WaterMaxC float64 `json:"water_max_c"`
			Hours     []any   `json:"hours"`
		} `json:"maps"`
	}
	if err := json.Unmarshal([]byte(body), &overview); err != nil {
		t.Fatalf("overview json: %v %s", err, body)
	}
	if overview.Days != 7 {
		t.Fatalf("days=%d，期望 7", overview.Days)
	}
	if len(overview.Maps) != 18 {
		t.Fatalf("概览图数=%d，期望 18", len(overview.Maps))
	}
	for _, m := range overview.Maps {
		if m.WaterMaxC <= m.WaterMinC {
			t.Fatalf("%s 水温区间异常: %v..%v", m.Map, m.WaterMinC, m.WaterMaxC)
		}
		if len(m.Hours) != 0 {
			t.Fatalf("%s 概览不该带逐时明细（%d 条）", m.Map, len(m.Hours))
		}
	}

	// 3) 单图明细：四块数据都要有，且当前小时唯一。
	code, body = call("GET", "/api/weather/preview?map=level_009_ahtuba&days=7", login.Token, "")
	if code != 200 {
		t.Fatalf("preview: %d %s", code, body)
	}
	var detail struct {
		Preview struct {
			Map        string  `json:"map"`
			MapName    string  `json:"map_name"`
			GameClock  string  `json:"game_clock"`
			WaterMinC  float64 `json:"water_min_c"`
			WaterMaxC  float64 `json:"water_max_c"`
			Season     int     `json:"season"`
			SeasonName string  `json:"season_name"`
			Days       []struct {
				Day        int64   `json:"day"`
				KindName   string  `json:"kind_name"`
				HighC      float64 `json:"high_c"`
				LowC       float64 `json:"low_c"`
				WaterHighC float64 `json:"water_high_c"`
				WaterLowC  float64 `json:"water_low_c"`
				RainHour   int     `json:"rain_hours"`
			} `json:"days"`
			Hours []struct {
				Day         int64   `json:"day"`
				Hour        int64   `json:"hour"`
				AirTempC    float64 `json:"air_temp_c"`
				WaterC      float64 `json:"water_c"`
				DeepC       float64 `json:"deep_c"`
				KindName    string  `json:"kind_name"`
				SegmentName string  `json:"segment_name"`
				IsNow       bool    `json:"is_now"`
			} `json:"hours"`
			Bands []struct {
				FromC float64 `json:"from_c"`
				ToC   float64 `json:"to_c"`
				Count int     `json:"count"`
			} `json:"bands"`
			Seasons []struct {
				SeasonName string  `json:"season_name"`
				WaterMinC  float64 `json:"water_min_c"`
				WaterMaxC  float64 `json:"water_max_c"`
				IsNow      bool    `json:"is_now"`
			} `json:"seasons"`
		} `json:"preview"`
		NowDay  int64 `json:"now_day"`
		NowHour int64 `json:"now_hour"`
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatalf("preview json: %v %s", err, body)
	}
	p := detail.Preview
	if p.Map != "level_009_ahtuba" || p.MapName != "阿赫图巴河" {
		t.Fatalf("地图/图名异常: %q / %q", p.Map, p.MapName)
	}
	if len(p.Days) != 7 {
		t.Fatalf("日汇总=%d，期望 7", len(p.Days))
	}
	if len(p.Hours) != 7*24 {
		t.Fatalf("逐时=%d，期望 168", len(p.Hours))
	}
	if len(p.Bands) == 0 {
		t.Fatal("缺少温度分档统计")
	}
	if len(p.Seasons) != 4 {
		t.Fatalf("四季区间=%d，期望 4", len(p.Seasons))
	}
	// 分档覆盖的样本数应等于逐时总数（每小时的 WaterC 必落且仅落一档）。
	var bandTotal int
	for _, b := range p.Bands {
		if b.ToC <= b.FromC {
			t.Fatalf("档位边界异常: %v..%v", b.FromC, b.ToC)
		}
		bandTotal += b.Count
	}
	if bandTotal != len(p.Hours) {
		t.Fatalf("分档合计=%d，逐时=%d，应有且仅有一档命中", bandTotal, len(p.Hours))
	}
	// 四季里恰好一季标 is_now。
	var nowSeasons int
	for _, s := range p.Seasons {
		if s.WaterMaxC <= s.WaterMinC {
			t.Fatalf("%s 水温区间异常: %v..%v", s.SeasonName, s.WaterMinC, s.WaterMaxC)
		}
		if s.IsNow {
			nowSeasons++
		}
	}
	if nowSeasons != 1 {
		t.Fatalf("当前季标记数=%d，期望 1", nowSeasons)
	}
	// 当前小时有且只有一条。
	var nowHours int
	for _, h := range p.Hours {
		if !h.IsNow {
			continue
		}
		nowHours++
		if h.Day != detail.NowDay || h.Hour != detail.NowHour {
			t.Fatalf("is_now 与 now_day/now_hour 不一致: day=%d hour=%d vs %d/%d",
				h.Day, h.Hour, detail.NowDay, detail.NowHour)
		}
	}
	if nowHours != 1 {
		t.Fatalf("当前小时标记数=%d，期望 1", nowHours)
	}
	// 日汇总的高低温包住该日逐时气温。
	for _, d := range p.Days {
		var lo, hi float64 = 1e9, -1e9
		for _, h := range p.Hours {
			if h.Day != d.Day {
				continue
			}
			if h.AirTempC < lo {
				lo = h.AirTempC
			}
			if h.AirTempC > hi {
				hi = h.AirTempC
			}
		}
		if d.LowC > lo+0.05 || d.HighC < hi-0.05 {
			t.Fatalf("day %d 高低温未包住逐时: 日 %v..%v，逐时 %v..%v", d.Day, d.LowC, d.HighC, lo, hi)
		}
	}

	// 4) 未知地图要 404，不是空 JSON。
	if code, body = call("GET", "/api/weather/preview?map=level_xxx_nope", login.Token, ""); code != 404 {
		t.Fatalf("未知地图应 404，实际 %d %s", code, body)
	}
	// 5) 未登录要 401。
	if code, _ = call("GET", "/api/weather/preview?map=level_001_lake", "", ""); code != 401 {
		t.Fatalf("未登录应 401，实际 %d", code)
	}
}

// 活跃鱼种接口：必须给出真实名册、分项系数可解释、排序正确，
// 且与天气预览共用同一份水温（面板上两处数字不能打架）。
func TestWeatherActiveFishAdminAPI(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	// 造三条鱼：纯夜行、纯昼行、极端热水（永远不活跃），并挂进名册。
	// fish_ecology 是所有图共享的生态基线，loc_wiki_fish 决定名册。
	for _, q := range []string{
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve, activity)
		 VALUES('t.night', 0.5, 8, 15, 22, 1, '[1,0.2,0.1,0.3]', 1.0)`,
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve, activity)
		 VALUES('t.day', 0.5, 8, 15, 22, 4, '[0.1,0.3,1,0.2]', 1.0)`,
		`INSERT INTO fish_ecology(fish_key, layer_pref, temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve, activity)
		 VALUES('t.hot', 0.5, 30, 36, 42, 15, '[1,1,1,1]', 1.0)`,
		// loc_wiki_fish / loc_fish 的 loc 列存的是**中文地名**，不是 level_xxx。
		// 这里必须照真实数据来：先配 loc_alias，再挂中文名下的名册。
		// 早期版本直接把 level_001_lake 塞进 loc_wiki_fish，测试假通过，
		// 而线上每张图都返回 0 条鱼。
		`INSERT INTO loc_alias(alias, loc, note) VALUES('level_001_lake','克马羚诺也湖','weather active fish test')`,
		`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh) VALUES('克马羚诺也湖','t.night','测试夜行鱼')`,
		`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh) VALUES('克马羚诺也湖','t.day','测试昼行鱼')`,
		`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh) VALUES('克马羚诺也湖','t.hot','测试热水鱼')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	srv := NewServer(config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 10 * time.Second}
	call := func(method, path, token string) (int, string) {
		t.Helper()
		r, _ := http.NewRequest(method, base+path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out bytes.Buffer
		_, _ = out.ReadFrom(resp.Body)
		return resp.StatusCode, out.String()
	}

	code, body := call("POST", "/api/login", "")
	_ = code
	// 登录需要 POST body，单独来一次。
	lb, _ := http.NewRequest("POST", base+"/api/login", bytes.NewBufferString(`{"username":"admin","password":"admin123"}`))
	lb.Header.Set("Content-Type", "application/json")
	lresp, err := client.Do(lb)
	if err != nil {
		t.Fatal(err)
	}
	var lout bytes.Buffer
	_, _ = lout.ReadFrom(lresp.Body)
	lresp.Body.Close()
	var login struct{ Token string }
	if err := json.Unmarshal(lout.Bytes(), &login); err != nil || login.Token == "" {
		t.Fatalf("login: %s", lout.String())
	}

	code, body = call("GET", "/api/weather/active-fish?map=level_001_lake", login.Token)
	if code != 200 {
		t.Fatalf("active-fish: %d %s", code, body)
	}
	var resp struct {
		MapName string `json:"map_name"`
		Active  struct {
			Map       string  `json:"map"`
			GameHour  int64   `json:"game_hour"`
			Segment   string  `json:"segment"`
			WaterC    float64 `json:"water_c"`
			Season    string  `json:"season"`
			Total     int     `json:"total"`
			ActiveNum int     `json:"active_num"`
			Fish      []struct {
				FishKey     string  `json:"fish_key"`
				NameZh      string  `json:"name_zh"`
				DielFactor  float64 `json:"diel_factor"`
				ThermalFact float64 `json:"thermal_fact"`
				Score       float64 `json:"score"`
				Active      bool    `json:"active"`
				Reason      string  `json:"reason"`
			} `json:"fish"`
			Idle []struct {
				FishKey string `json:"fish_key"`
				Reason  string `json:"reason"`
			} `json:"idle"`
		} `json:"active"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("json: %v %s", err, body)
	}
	if resp.Active.Total != 3 {
		t.Fatalf("名册总数=%d，期望 3（种了三条鱼）", resp.Active.Total)
	}
	if resp.Active.WaterC == 0 {
		t.Fatal("水温不该是 0")
	}
	if resp.Active.Segment == "" || resp.Active.Season == "" {
		t.Fatalf("时段/季节不能为空: %+v", resp.Active)
	}
	// 热水鱼永远不该在活跃名单里。
	for _, f := range resp.Active.Fish {
		if f.FishKey == "t.hot" {
			t.Fatal("48℃ 的鱼不该算活跃")
		}
		if !f.Active {
			t.Fatalf("%s 在活跃名单里却没标 active", f.FishKey)
		}
		if f.Score <= 0 {
			t.Fatalf("%s 综合系数应 > 0，实得 %v", f.FishKey, f.Score)
		}
	}
	if resp.Active.ActiveNum != len(resp.Active.Fish) {
		t.Fatalf("ActiveNum=%d 与名单 %d 不符", resp.Active.ActiveNum, len(resp.Active.Fish))
	}
	// 热水鱼要落在 idle 且写明原因。
	hotIdle := false
	for _, f := range resp.Active.Idle {
		if f.FishKey == "t.hot" {
			hotIdle = true
			if f.Reason == "" {
				t.Fatal("热水鱼在 idle 里但没有原因")
			}
		}
	}
	if !hotIdle {
		t.Fatalf("热水鱼应落在 idle，实际 idle=%v", resp.Active.Idle)
	}

	// 缺 map / 未知图要拒绝。
	if code, _ = call("GET", "/api/weather/active-fish", login.Token); code != 400 {
		t.Fatalf("缺 map 应 400，实际 %d", code)
	}
	if code, _ = call("GET", "/api/weather/active-fish?map=level_xxx_nope", login.Token); code != 404 {
		t.Fatalf("未知图应 404，实际 %d", code)
	}
	if code, _ = call("GET", "/api/weather/active-fish?map=level_001_lake", ""); code != 401 {
		t.Fatalf("未登录应 401，实际 %d", code)
	}
}
