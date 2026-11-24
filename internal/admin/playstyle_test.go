package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

// 参数导出/导入全链路：导出拿到包 → 改库 → 导入还原 → 预演不写库。
//
// 这条链路最怕的是「看起来成功、其实半应用」，所以除了值还原，
// 还要盯两件事：预演绝不能写库、派生表绝不能出现在包里。
func TestPlaystyleExportImportRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	// 造几条可辨认的配置：参数、饵料覆盖、生态覆盖。
	for _, q := range []string{
		`INSERT INTO tunables(key,value,updated_by,updated_ts) VALUES('bite_probability','0.5','seed',0)`,
		`INSERT INTO tunables(key,value,updated_by,updated_ts) VALUES('bite_delay_max','20','seed',0)`,
		`INSERT INTO fish_bait_override(fish_zh,allow_json,enabled,updated_by,updated_ts)
		 VALUES('欧鳊','["玉米"]',1,'seed',0)`,
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

	// 1) 导出
	code, body = call("GET", "/api/playstyle/export", login.Token, "")
	if code != 200 {
		t.Fatalf("export: %d %s", code, body)
	}
	var pkg playstylePackage
	if err := json.Unmarshal([]byte(body), &pkg); err != nil {
		t.Fatalf("export json: %v", err)
	}
	if pkg.Format != playstyleFormatName || pkg.FormatVersion != playstyleFormatVersion {
		t.Fatalf("包标识异常: %s v%d", pkg.Format, pkg.FormatVersion)
	}
	// testDB 自带若干预置参数，所以不假设 tunables 的总行数；
// 只要求导出的包**包含**我们写入的两条。
	got := map[string]bool{}
	for _, row := range pkg.Tables["tunables"] {
		if k, _ := row["key"].(string); k == "bite_probability" || k == "bite_delay_max" {
			got[k] = true
		}
	}
	if len(got) != 2 {
		t.Fatalf("tunables 缺少种子参数，实得 %v（共 %d 行）", got, len(pkg.Tables["tunables"]))
	}

	// 派生表与玩家存档绝不能出现在包里——导过去会被服务重新覆盖，
	// 或者等于把玩家账号和登录令牌一起送出去。
	for _, banned := range []string{
		"fish_ecology", "hook_bite_profile", "fish_grade_lines",
		"profile", "item", "gw_token", "admin_user", "schema_migrations",
	} {
		if _, ok := pkg.Tables[banned]; ok {
			t.Fatalf("包里不应含 %s", banned)
		}
	}
	if _, ok := pkg.ExcludedTables["fish_ecology"]; !ok {
		t.Fatal("excluded_tables 应说明 fish_ecology 为何不导")
	}

	// 2) 改库：值改了、行删了
	if _, err := db.SQL.Exec(`UPDATE tunables SET value='999' WHERE key='bite_probability'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`DELETE FROM fish_bait_override`); err != nil {
		t.Fatal(err)
	}

	// 3) 预演：必须报告将要更新，且绝不写库
	raw, _ := json.Marshal(pkg)
	code, body = call("POST", "/api/playstyle/import?dry_run=1", login.Token, string(raw))
	if code != 200 {
		t.Fatalf("dry-run: %d %s", code, body)
	}
	var dry struct {
		DryRun bool `json:"dry_run"`
		Tables []struct {
			Table    string `json:"table"`
			Rows     int    `json:"rows"`
			Updated  int    `json:"updated"`
			Inserted int    `json:"inserted"`
		} `json:"tables"`
	}
	if err := json.Unmarshal([]byte(body), &dry); err != nil {
		t.Fatalf("dry json: %v", err)
	}
	if !dry.DryRun {
		t.Fatal("dry_run 应为 true")
	}
	var baitInserted, tunUpdated int
	for _, tb := range dry.Tables {
		switch tb.Table {
		case "fish_bait_override":
			baitInserted = tb.Inserted
		case "tunables":
			// 预演会报告包里全部 tunables 行都「将更新」（值相同也算更新），
			// 这里只要求 ≥2（含我们的两条种子），不假设总数。
			tunUpdated = tb.Updated
		}
	}
	if baitInserted != 1 {
		t.Fatalf("预演应报告 fish_bait_override 新增 1 行，实得 %d", baitInserted)
	}
	if tunUpdated < 2 {
		t.Fatalf("预演应报告至少 2 条 tunables 更新，实得 %d", tunUpdated)
	}
	// 预演不能写库
	var v string
	if err := db.SQL.QueryRow(`SELECT value FROM tunables WHERE key='bite_probability'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "999" {
		t.Fatalf("预演写库了：bite_probability=%s，期望仍是 999", v)
	}
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM fish_bait_override`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("预演写库了：fish_bait_override 有 %d 行", n)
	}

	// 4) 真导：值还原、删掉的行补回
	code, body = call("POST", "/api/playstyle/import", login.Token, string(raw))
	if code != 200 {
		t.Fatalf("import: %d %s", code, body)
	}
	if err := db.SQL.QueryRow(`SELECT value FROM tunables WHERE key='bite_probability'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "0.5" {
		t.Fatalf("导入后 bite_probability=%s，期望 0.5", v)
	}
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM fish_bait_override`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("导入后 fish_bait_override 行数=%d，期望 1", n)
	}

	// 5) 导入要推 reload_tunables：生态覆盖与权重档位的缓存只有收到它才重建。
	var payload string
	if err := db.SQL.QueryRow(`SELECT payload FROM admin_queue ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatalf("未推送重载指令: %v", err)
	}
	if !strings.Contains(payload, "reload_tunables") {
		t.Fatalf("重载指令内容异常: %s", payload)
	}

	// 6) 审计留痕
	var cnt int
	if err := db.SQL.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action='playstyle_import'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt == 0 {
		t.Fatal("导入应写一条 audit_log")
	}
}

// 坏包必须被挡住，且不写库。
func TestPlaystyleRejectsBadPackage(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 10 * time.Second}
	post := func(path, token, body string) (int, string) {
		t.Helper()
		r, _ := http.NewRequest("POST", base+path, bytes.NewBufferString(body))
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

	code, body := post("/api/login", "", `{"username":"admin","password":"admin123"}`)
	if code != 200 {
		t.Fatalf("login: %d %s", code, body)
	}
	var login struct{ Token string }
	_ = json.Unmarshal([]byte(body), &login)

	// 非本工具的包
	code, body = post("/api/playstyle/import", login.Token, `{"format":"other","tables":{}}`)
	if code != 400 {
		t.Fatalf("非本工具包应 400，实得 %d %s", code, body)
	}
	// 格式版本不匹配
	code, body = post("/api/playstyle/import", login.Token,
		`{"format":"rf4-playstyle","format_version":999,"tables":{}}`)
	if code != 400 {
		t.Fatalf("版本不匹配应 400，实得 %d %s", code, body)
	}
	// 非法 JSON
	code, body = post("/api/playstyle/import", login.Token, `{not json`)
	if code != 400 {
		t.Fatalf("非法 JSON 应 400，实得 %d %s", code, body)
	}
	// 缺列的包：目标库有 fish_key，包里只有 fish_zh —— 应拒绝且不写库
	code, body = post("/api/playstyle/import", login.Token,
		`{"format":"rf4-playstyle","format_version":1,"digest":"x","tables":{"fish_bait_override":[{"fish_zh":"欧鳊"}]}}`)
	if code != 400 {
		t.Fatalf("缺列包应 400，实得 %d %s", code, body)
	}
	if !strings.Contains(body, "缺少") {
		t.Fatalf("错误信息应指出缺少列或主键，实得 %s", body)
	}
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM fish_bait_override`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("被拒绝的导入写了 %d 行", n)
	}
}