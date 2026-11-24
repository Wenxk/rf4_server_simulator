package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/gameserver"
	"rf4server/internal/logging"
)

// 鱼种经济后台：列表 → 覆盖保存 → 试算 → 恢复默认，全链路走 HTTP。
func TestSpeciesEconAdminAPI(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO species_weight(fish_key,min_g,max_g,trophy_g,trophy_line_g,super_trophy_line_g,length_k,length_p)
		 VALUES('bream',100,1500,700,4400,7000,0.033,0.33)`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES('loc_test','bream','欧鳊',10)`,
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

	// 1) 列表：四档齐全，内置值与内置表一致，尚未覆盖。
	code, body = call("GET", "/api/econ/species?q=bream", login.Token, "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var list struct {
		Items []econSpecies `json:"items"`
		Maps  []struct {
			Key string `json:"key"`
		} `json:"maps"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("list json: %v %s", err, body)
	}
	if len(list.Items) != 1 || list.Items[0].FishKey != "bream" || list.Items[0].NameZh != "欧鳊" {
		t.Fatalf("列表内容异常: %+v", list.Items)
	}
	if got := len(list.Items[0].Grades); got != 4 {
		t.Fatalf("档位数=%d，期望 4", got)
	}
	// 内置值必须与 gameserver 导出的算法一致，且四档走统一阶梯 1:1.5:3:6。
	grades := list.Items[0].Grades
	if g := grades[2]; g.XPC != nil || g.Price != nil {
		t.Fatalf("未覆盖时不应带覆盖值: %+v", g)
	}
	for i, g := range grades {
		grade := int64(i + 1)
		if want := round6(gameserver.SpeciesBaseXPRate("bream", grade)); g.BaseXP != want {
			t.Fatalf("g%d 内置经验=%v，期望 %v", grade, g.BaseXP, want)
		}
		if want := round6(gameserver.SpeciesBasePricePerKg("bream", grade)); g.BasePrice != want {
			t.Fatalf("g%d 内置鱼价=%v，期望 %v", grade, g.BasePrice, want)
		}
	}
	for i, mul := range []float64{1.5, 3, 6} {
		b1, bn := grades[0].BasePrice, grades[i+1].BasePrice
		if math.Abs(bn-b1*mul) > 6e-6 {
			t.Fatalf("鱼价 g%d/g1=%v，期望 %v", i+2, bn/b1, mul)
		}
	}
	if list.Items[0].Overridden {
		t.Fatal("未保存前不应标记为已覆盖")
	}
	if len(list.Maps) == 0 {
		t.Fatal("缺少水域下拉数据")
	}

	// 2) 保存：只改 g3 的经验与鱼价，其余档保持默认。
	payload := `{"fish_key":"bream","grades":[
		{"grade":1},{"grade":2},
		{"grade":3,"xp_c":2,"price_per_kg":50},
		{"grade":4}]}`
	if code, body = call("POST", "/api/econ/species/save", login.Token, payload); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	rec, err := db.SpeciesEconGet(ctx, "bream", 3)
	if err != nil || rec == nil || rec.XPC == nil || *rec.XPC != 2 || rec.PricePerKg == nil || *rec.PricePerKg != 50 {
		t.Fatalf("写入覆盖异常: rec=%+v err=%v", rec, err)
	}
	if rec, _ := db.SpeciesEconGet(ctx, "bream", 1); rec != nil {
		t.Fatalf("g1 不应写入覆盖: %+v", rec)
	}
	var audits int
	if err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action='species_econ' AND target='bream'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits == 0 {
		t.Fatal("保存后缺少审计记录")
	}
	assertQueueContains(t, db, "reload_tunables")

	// 3) 再次列表：g3 带覆盖值，整条标记为已覆盖。
	_, body = call("GET", "/api/econ/species?q=bream", login.Token, "")
	_ = json.Unmarshal([]byte(body), &list)
	if !list.Items[0].Overridden || list.Items[0].Grades[2].XPC == nil || *list.Items[0].Grades[2].XPC != 2 {
		t.Fatalf("列表未反映覆盖: %+v", list.Items[0])
	}

	// 4) 试算：2 XP/克 × 1000g = 2000；鱼价按 50 银/公斤折分。
	code, body = call("GET", "/api/econ/preview?species=bream&grade=3&weight_g=1000&map=level_009_ahtuba", login.Token, "")
	if code != 200 {
		t.Fatalf("preview: %d %s", code, body)
	}
	var pv struct {
		XP       int64   `json:"xp"`
		Cents    int64   `json:"price_cents"`
		PricePer float64 `json:"price_per_kg"`
	}
	_ = json.Unmarshal([]byte(body), &pv)
	if pv.XP != 2000 {
		t.Fatalf("试算经验=%d，期望 2000", pv.XP)
	}
	if pv.Cents <= 0 || pv.PricePer != 50 {
		t.Fatalf("试算鱼价异常: %+v", pv)
	}

	// 5) 未保存的输入也能试算：传 xp_c=0.5 时应按 0.5 计算，且不落库。
	_, body = call("GET", "/api/econ/preview?species=bream&grade=3&weight_g=1000&xp_c=0.5", login.Token, "")
	_ = json.Unmarshal([]byte(body), &pv)
	if pv.XP != 500 {
		t.Fatalf("临时覆盖试算=%d，期望 500", pv.XP)
	}
	if rec, _ := db.SpeciesEconGet(ctx, "bream", 3); rec == nil || rec.XPC == nil || *rec.XPC != 2 {
		t.Fatalf("试算不应改写已保存覆盖: %+v", rec)
	}

	// 6) 非法值必须拒绝。
	if code, _ = call("POST", "/api/econ/species/save", login.Token,
		`{"fish_key":"bream","grades":[{"grade":3,"xp_c":-1}]}`); code != 400 {
		t.Fatalf("负数经验应返回 400，实得 %d", code)
	}

	// 7) 恢复默认：行被删除，列表回到未覆盖。
	code, body = call("POST", "/api/econ/species/reset", login.Token, `{"fish_key":"bream"}`)
	if code != 200 {
		t.Fatalf("reset: %d %s", code, body)
	}
	if rec, _ := db.SpeciesEconGet(ctx, "bream", 3); rec != nil {
		t.Fatalf("恢复默认后仍有覆盖: %+v", rec)
	}
	_, body = call("GET", "/api/econ/species?q=bream", login.Token, "")
	_ = json.Unmarshal([]byte(body), &list)
	if list.Items[0].Overridden || list.Items[0].Grades[2].XPC != nil {
		t.Fatalf("恢复默认后列表仍显示覆盖: %+v", list.Items[0])
	}
	if !strings.Contains(body, "欧鳊") {
		t.Fatalf("列表缺少中文名: %s", body)
	}
}
