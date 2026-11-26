package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

// 后台规则覆盖：保存 → 列表反映覆盖 → 恢复默认回到内置规则。
func TestBaitRuleSaveAndReset(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh,in_map_roster,in_habitat) VALUES('铜湖','c.roach','常见拟鲤',1,1)`,
		`INSERT INTO tunables(key,value) VALUES('fish_bait_rules_enabled','1')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999901,'rule_test_worm',27,'{"fopbhlmkcpe":6}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('rule_test_worm','测试蚯蚓')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	cfg := config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}
	srv := NewServer(cfg, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 5 * time.Second}

	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, login := do("POST", "/api/login", `{"username":"admin","password":"admin123"}`)
	if code != 200 || login["token"] == nil {
		t.Fatalf("登录失败: %d %v", code, login)
	}
	auth := login["token"].(string)
	doAuth := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+auth)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	findRow := func(out map[string]any) map[string]any {
		rows, _ := out["rows"].([]any)
		for _, it := range rows {
			m := it.(map[string]any)
			if m["name_zh"] == "常见拟鲤" {
				return m
			}
		}
		t.Fatalf("列表里没有目标鱼: %v", out)
		return nil
	}

	// 保存一条覆盖。
	payload := `{"fish_zh":"常见拟鲤","allow":["测试蚯蚓"],"deny":["拟饵"],
		"bottom_only":[],"condition":"","enabled":true}`
	if code, out := doAuth("PUT", "/api/bait/rules", payload); code != 200 {
		t.Fatalf("保存失败: %d %v", code, out)
	}

	code, list := doAuth("GET", "/api/bait/prefs?q=c.roach", "")
	if code != 200 {
		t.Fatalf("列表失败: %d", code)
	}
	if list["rules_enabled"] != true {
		t.Fatalf("rules_enabled 应为 true: %v", list["rules_enabled"])
	}
	row := findRow(list)
	if row["source"] != "override" {
		t.Fatalf("source=%v", row["source"])
	}
	if allow, _ := row["allow"].([]any); len(allow) != 1 || allow[0] != "测试蚯蚓" {
		t.Fatalf("allow=%v", row["allow"])
	}
	if rot, _ := row["rotation"].(map[string]any); rot != nil && rot["enabled"] == true {
		t.Fatalf("未开自动轮换时不应有轮换状态: %v", rot)
	}

	// 恢复默认。
	if code, out := doAuth("POST", "/api/bait/rules/reset", `{"fish_zh":"常见拟鲤"}`); code != 200 {
		t.Fatalf("恢复默认失败: %d %v", code, out)
	}
	_, list = doAuth("GET", "/api/bait/prefs?q=c.roach", "")
	row = findRow(list)
	if row["source"] != "default" {
		t.Fatalf("恢复后 source=%v", row["source"])
	}
	allow, _ := row["allow"].([]any)
	if len(allow) < 2 {
		t.Fatalf("恢复后应回到内置允许列表，实得 %v", row["allow"])
	}
}

// 「立即重抽本期」只做一件事：轮次偏移 +1。偏移必须落库（重启不退回上一轮），
// 并且要推 reload_tunables 让判鱼侧立刻重读（那边有 30 秒缓存）。
func TestBaitAutoRerollBumpsRound(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}
	srv := NewServer(cfg, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Post(base+"/api/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"admin123"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatalf("登录失败: %v", login)
	}

	reroll := func() int64 {
		t.Helper()
		r, _ := http.NewRequest("POST", base+"/api/bait/auto-rotation/reroll", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Round int64 `json:"round"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != 200 {
			t.Fatalf("重抽失败: %d %+v", resp.StatusCode, out)
		}
		return out.Round
	}

	if got := reroll(); got != 1 {
		t.Fatalf("第一次重抽应返回 round=1，实得 %d", got)
	}
	if got := reroll(); got != 2 {
		t.Fatalf("第二次重抽应返回 round=2，实得 %d", got)
	}
	var stored string
	if err := db.SQL.QueryRow(`SELECT value FROM tunables WHERE key='bait_rotation_auto_round'`).Scan(&stored); err != nil {
		t.Fatalf("偏移没有落库: %v", err)
	}
	if stored != "2" {
		t.Fatalf("库里的偏移应为 2，实得 %q", stored)
	}
}

// 分类接口要把「整类规则词」一起返回：后台的「按大类加入」按钮直接用
// 这个 term 写进规则，必须与判鱼匹配表同一份，不能前端另猜。
func TestBaitCategoriesCarryTypeTerms(t *testing.T) {
	db := testDB(t)
	for _, q := range []string{
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(900801,'plt_a',27,'{"fopbhlmkcpe":9}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('plt_a','奇特颗粒 14')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(900802,'pop_a',27,'{"fopbhlmkcpe":8}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('pop_a','甜奶油 PopUp 16')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(900803,'lure_a',17,'{"mbemakijdeh":6}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('lure_a','Bond 125-01')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	srv := NewServer(config.Admin{JWTSecret: "test"}, db, logging.Discard(), WebFS())
	rec := httptest.NewRecorder()
	srv.handleBaitCategories(rec, httptest.NewRequest("GET", "/api/bait/categories", nil))
	if rec.Code != 200 {
		t.Fatalf("状态=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Categories []struct {
			Key      int64  `json:"key"`
			Label    string `json:"label"`
			Term     string `json:"term"`
			Children []struct {
				Sub   int64  `json:"sub"`
				Label string `json:"label"`
				Term  string `json:"term"`
				Count int    `json:"count"`
				// 形态词（只有 baitType=8 有）与品牌/型号层，前端层级选择器要用。
				Shapes []struct {
					Shape string `json:"shape"`
					Label string `json:"label"`
					Term  string `json:"term"`
					Count int    `json:"count"`
				} `json:"shapes"`
				Brands []struct {
					Kind  string `json:"kind"`
					Label string `json:"label"`
					Count int    `json:"count"`
					Lines []struct {
						Base  string `json:"base"`
						Count int    `json:"count"`
						Term  string `json:"term"`
					} `json:"lines"`
				} `json:"brands"`
			} `json:"children"`
		} `json:"categories"`
		Terms []struct {
			Term  string `json:"term"`
			Label string `json:"label"`
			Cat   int64  `json:"cat"`
			Sub   int64  `json:"sub"`
			Count int    `json:"count"`
		} `json:"terms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	byCat := map[int64]string{}
	for _, c := range body.Categories {
		byCat[c.Key] = c.Term
	}
	// 大类级规则词用游戏菜单原文：鱼饵侧是「鱼饵」不是「饵料」。
	// 「饵料」仍是可用别名（旧后台覆盖数据里写过），但不再作为正式名返回。
	if byCat[27] != "鱼饵" || byCat[17] != "拟饵" {
		t.Fatalf("大类规则词=%v，期望 27→鱼饵 / 17→拟饵", byCat)
	}
	// 展示名必须与规则词一致。曾经这里硬编码「饵料」而按钮写「鱼饵」，
	// 同一屏两套叫法，用户会以为范围不同。
	for _, c := range body.Categories {
		if c.Label != c.Term {
			t.Fatalf("大类 cat=%d 的 label=%q 与 term=%q 不一致", c.Key, c.Label, c.Term)
		}
	}
	subTerm := map[int64]string{}
	subLabel := map[int64]string{}
	for _, c := range body.Categories {
		for _, ch := range c.Children {
			subTerm[ch.Sub] = ch.Term
			subLabel[ch.Sub] = ch.Label
		}
	}
	if subTerm[9] != "颗粒" {
		t.Fatalf("baitType 9 的规则词=%q，期望 颗粒", subTerm[9])
	}
	if subLabel[9] != "颗粒" {
		t.Fatalf("baitType 9 的标签=%q，期望 颗粒（旧的「鱼子 / 虾类」是错标）", subLabel[9])
	}
	if subTerm[8] != "香煮成饵" || subLabel[8] != "香煮成饵（下沉 + Pop-up）" {
		t.Fatalf("baitType 8 → term=%q label=%q", subTerm[8], subLabel[8])
	}
	// 形态词必须是独立的两项：游戏把同一口味的「下沉」与「Pop-up」并列成两个菜单项，
	// 库里却只有一个 baitType=8，只能靠 brand 的 _pp_ 标记区分。
	var shapes []string
	for _, c := range body.Categories {
		for _, ch := range c.Children {
			for _, sh := range ch.Shapes {
				shapes = append(shapes, sh.Term)
			}
		}
	}
	if len(shapes) != 2 {
		t.Fatalf("形态词=%v，期望 2 项（下沉 / Pop-up）", shapes)
	}
	if subTerm[6] != "柳叶亮片" {
		t.Fatalf("lureType 6 的规则词=%q，期望 柳叶亮片", subTerm[6])
	}
	termCount := map[string]int{}
	for _, term := range body.Terms {
		termCount[term.Term] = term.Count
		if term.Term == "颗粒" && term.Count != 1 {
			t.Fatalf("颗粒数量=%d，期望 1", term.Count)
		}
	}
	for _, want := range []string{"各类饵", "鱼饵", "拟饵", "颗粒", "香煮成饵", "下沉香煮成饵", "Pop-up 香煮成饵"} {
		if _, ok := termCount[want]; !ok {
			t.Fatalf("大类清单里缺 %q：%v", want, termCount)
		}
	}
}
