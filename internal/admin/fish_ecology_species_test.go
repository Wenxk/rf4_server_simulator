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
	"rf4server/internal/store"
)

// 鱼种档案后台：列表 → 保存覆盖 → 校验拦截 → 恢复基线，全链路走 HTTP。
func TestFishEcologySpeciesAdminAPI(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO species_weight(fish_key,min_g,max_g,trophy_g,trophy_line_g,super_trophy_line_g,length_k,length_p)
		 VALUES('burbot',100,5000,2000,6000,9000,0.033,0.33)`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES('loc_test','burbot','江鳕',10)`,
		// 水域归属（列表里的「水域」列）读的是 loc_wiki_fish.loc，不是 loc_fish.loc：
		// 前者是静态库的「这张图上有没有这种鱼」，后者是运行时渔获归属。
		// 造 7 条，顺手验证后端确实按 6 条截断。
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh,in_map_roster) VALUES
		    ('旧奥斯特罗格湖','burbot','江鳕',1),('琥珀湖','burbot','江鳕',1),
		    ('铜湖','burbot','江鳕',1),('苏拉河','burbot','江鳕',1),
		    ('梅德韦杰湖','burbot','江鳕',1),('克马羚诺也湖','burbot','江鳕',1),
		    ('埃尔克湖','burbot','江鳕',1)`,
		// 资产基线故意给成合成值（18.9℃），正是要纠正的那类数据。
		`INSERT INTO fish_ecology(fish_key, layer_min, layer_pref, layer_max,
		    temp_min_c, temp_pref_c, temp_max_c, activity, diel_mask, diel_curve, season_mask)
		 VALUES('burbot', 0.30, 0.55, 0.80, 10.2, 18.9, 27.1, 0.87, 15, '', 15)`,
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
	client := &http.Client{Timeout: 5 * time.Second}
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

	// 1) 列表：返回全字段 base、无 override，overlaid=false。
	code, body = call("GET", "/api/fish/ecology-species?q=burbot", login.Token, "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var list struct {
		Items []struct {
			FishKey  string                    `json:"fish_key"`
			NameZh   string                    `json:"name_zh"`
			Overlaid bool                      `json:"overlaid"`
			Locs     []string                  `json:"locs"`
			Base     store.FishEcologySpecies  `json:"base"`
			Override *store.FishEcologySpecies `json:"override"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, body)
	}
	if len(list.Items) != 1 || list.Items[0].FishKey != "burbot" {
		t.Fatalf("列表=%+v，期望只有 burbot", list.Items)
	}
	row := list.Items[0]
	if row.NameZh != "江鳕" {
		t.Fatalf("中文名=%q，期望 江鳕（应来自 loc_fish 联表）", row.NameZh)
	}
	if row.Overlaid || row.Override != nil {
		t.Fatal("还没保存过，不该有 override")
	}
	// 水域归属（2026-11-25 从「饵料规则」搬来）：必须非空，且不能混进别的鱼。
	// 这条防的是 SQL 漏 JOIN 或把整张表的 loc 都塞给第一条鱼。
	if len(row.Locs) == 0 {
		t.Fatalf("burbot 的水域列表为空，期望至少一张图（实际 %+v）", row.Locs)
	}
	if len(row.Locs) > 6 {
		t.Fatalf("水域最多截断 6 个，实际 %d 个：%+v", len(row.Locs), row.Locs)
	}
	// base 必须是全字段（不只是温度），否则弹窗没法显示「继承 XX」。
	if row.Base.TempPrefC == nil || *row.Base.TempPrefC != 18.9 {
		t.Fatalf("基线偏好温度=%v，期望 18.9", row.Base.TempPrefC)
	}
	if row.Base.Activity == nil || *row.Base.Activity != 0.87 {
		t.Fatalf("基线活跃度=%v，期望 0.87", row.Base.Activity)
	}
	if row.Base.SeasonMask == nil || *row.Base.SeasonMask != 15 {
		t.Fatalf("基线季节掩码=%v，期望 15", row.Base.SeasonMask)
	}

	// 2) 校验：温度上下限颠倒必须 400。
	code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"burbot","temp_min_c":20,"temp_max_c":5}`)
	if code != 400 {
		t.Fatalf("颠倒温度窗口应被拒，实得 %d %s", code, body)
	}
	// 掩码越界同样 400。
	code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"burbot","season_mask":99}`)
	if code != 400 {
		t.Fatalf("越界掩码应被拒，实得 %d %s", code, body)
	}
	// 非对象 JSON 400。
	code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"burbot","diet_json":"[1,2]"}`)
	if code != 400 {
		t.Fatalf("数组型 diet_json 应被拒，实得 %d %s", code, body)
	}
	// 未知鱼种 400。
	code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"no_such_fish","temp_pref_c":10}`)
	if code != 400 {
		t.Fatalf("未知鱼种应被拒，实得 %d %s", code, body)
	}

	// 3) 保存现实值 4/10/14，只给温度三项。
	code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"burbot","temp_min_c":4,"temp_pref_c":10,"temp_max_c":14}`)
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}

	code, body = call("GET", "/api/fish/ecology-species?q=burbot", login.Token, "")
	if code != 200 {
		t.Fatalf("list2: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	row = list.Items[0]
	if !row.Overlaid || row.Override == nil {
		t.Fatal("保存后应标记 overlaid 并回传 override")
	}
	if row.Override.TempPrefC == nil || *row.Override.TempPrefC != 10 {
		t.Fatalf("覆盖偏好温度=%v，期望 10", row.Override.TempPrefC)
	}
	// 未给的字段必须仍是 nil（继承），不能补成 0——0 会被当成合法覆盖值。
	if row.Override.LayerMin != nil || row.Override.Activity != nil || row.Override.SeasonMask != nil {
		t.Fatalf("未给字段被填了值: layer=%v activity=%v season=%v",
			row.Override.LayerMin, row.Override.Activity, row.Override.SeasonMask)
	}
	// base 不受覆盖影响。
	if row.Base.TempPrefC == nil || *row.Base.TempPrefC != 18.9 {
		t.Fatalf("覆盖后基线被改写: %v", row.Base.TempPrefC)
	}
	if row.Override.Origin != "manual" {
		t.Fatalf("默认来源=%q，期望 manual", row.Override.Origin)
	}

	// 4) 只看已覆盖。
	code, body = call("GET", "/api/fish/ecology-species?only=overlaid", login.Token, "")
	if code != 200 {
		t.Fatalf("list overlaid: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 {
		t.Fatalf("total=%d，期望 1", list.Total)
	}

	// 5) 全字段为空 = 删除，不留脏行。
	code, body = call("POST", "/api/fish/ecology-species", login.Token, `{"fish_key":"burbot"}`)
	if code != 200 {
		t.Fatalf("clear: %d %s", code, body)
	}
	rows, err := db.AllFishEcologySpecies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("全空保存后仍有 %d 行，应等价于删除", len(rows))
	}

	// 6) 保存后再恢复基线。
	if code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"burbot","activity":1.4}`); code != 200 {
		t.Fatalf("save2: %d %s", code, body)
	}
	code, body = call("POST", "/api/fish/ecology-species/reset", login.Token, `{"fish_key":"burbot"}`)
	if code != 200 {
		t.Fatalf("reset: %d %s", code, body)
	}
	rows, err = db.AllFishEcologySpecies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("恢复基线后仍有 %d 行", len(rows))
	}
}

// 保存与恢复都必须投递 reload 指令，否则后台改完要等进程重启才生效。
func TestFishEcologySpeciesWritesQueue(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(
		`INSERT INTO species_weight(fish_key,min_g,max_g,trophy_g,trophy_line_g,super_trophy_line_g,length_k,length_p)
		 VALUES('pike',100,5000,2000,6000,9000,0.033,0.33)`); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 5 * time.Second}
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

	countQueue := func() int {
		t.Helper()
		var n int
		if err := db.SQL.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM admin_queue WHERE payload LIKE '%reload_fish_ecology%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if code, body = call("POST", "/api/fish/ecology-species", login.Token,
		`{"fish_key":"pike","temp_pref_c":12}`); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if n := countQueue(); n != 1 {
		t.Fatalf("保存后队列里有 %d 条 reload_fish_ecology，期望 1", n)
	}
	if code, body = call("POST", "/api/fish/ecology-species/reset", login.Token,
		`{"fish_key":"pike"}`); code != 200 {
		t.Fatalf("reset: %d %s", code, body)
	}
	if n := countQueue(); n != 2 {
		t.Fatalf("恢复基线后队列里有 %d 条，期望 2", n)
	}
}
