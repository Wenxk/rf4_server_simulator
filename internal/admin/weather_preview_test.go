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
