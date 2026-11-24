package gateway_test

import (
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rf4server/internal/gateway"
	"rf4server/internal/logging"
	"rf4server/internal/store"
	"rf4server/internal/testutil"
)

// 登录名决定账号：后台建好的同名档案会被客户端 login= 绑定；没建过的名字
// 回落到来源 IP 账号（本机 1000001），且不会凭空新建档案。
func TestLoginNameBindsExistingProfile(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	now := float64(time.Now().Unix())
	for _, p := range []struct {
		uid  int64
		name string
	}{{1000001, "Wenya666"}, {1000002, "WenXS"}} {
		if _, err := db.SQL.Exec(`INSERT INTO profile(uid,name,created_ts,updated_ts) VALUES(?,?,?,?)`,
			p.uid, p.name, now, now); err != nil {
			t.Fatalf("预建档案 %s: %v", p.name, err)
		}
	}

	_, stubAddr := testutil.StartStubGameServer(t, testutil.TestPSK)
	gw := testutil.StartGatewayWithStore(t, stubAddr, testutil.TestPSK,
		gateway.NewSQLiteStore(db, logging.Discard()))

	loginAs := func(query string) int64 {
		t.Helper()
		client := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
			Timeout:   5 * time.Second,
		}
		resp, err := client.Get(gw.LoginURL + query)
		if err != nil {
			t.Fatalf("登录请求 %s: %v", query, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var doc struct {
			UserID int64 `xml:"userid"`
		}
		if err := xml.Unmarshal(body, &doc); err != nil || doc.UserID <= 0 {
			t.Fatalf("解析登录响应 %s: err=%v body=%s", query, err, body)
		}
		return doc.UserID
	}
	profileCount := func() int {
		t.Helper()
		var n int
		if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM profile`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	kvValue := func(key string) (string, bool) {
		t.Helper()
		v, ok, err := db.KVGet(t.Context(), key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("读 kv %s: %v", key, err)
		}
		return v, ok
	}
	// 个人中心应跟随该 IP 最近一次登录的账号，而不是永远停在默认号。
	portalUser := func() int64 {
		t.Helper()
		base := strings.TrimSuffix(gw.LoginURL, "/login.php")
		client := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
			Timeout:   5 * time.Second,
		}
		resp, err := client.Post(base+"/portal/api/session", "application/json", nil)
		if err != nil {
			t.Fatalf("个人中心登录: %v", err)
		}
		defer resp.Body.Close()
		var sess struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil || sess.Token == "" {
			t.Fatalf("个人中心会话解析失败: err=%v", err)
		}
		req, _ := http.NewRequest(http.MethodGet, base+"/portal/api/profile", nil)
		req.Header.Set("Authorization", "Bearer "+sess.Token)
		prof, err := client.Do(req)
		if err != nil {
			t.Fatalf("个人中心资料: %v", err)
		}
		defer prof.Body.Close()
		var data struct {
			UserID int64 `json:"userId"`
		}
		if err := json.NewDecoder(prof.Body).Decode(&data); err != nil {
			t.Fatalf("个人中心资料解析失败: %v", err)
		}
		return data.UserID
	}

	// 同名档案直接登录，且大小写不敏感。
	if uid := loginAs("?login=WenXS"); uid != 1000002 {
		t.Fatalf("login=WenXS 应得 1000002，实得 %d", uid)
	}
	if got := portalUser(); got != 1000002 {
		t.Fatalf("登 WenXS 后个人中心应跟随 1000002，实得 %d", got)
	}
	if uid := loginAs("?login=wenxs"); uid != 1000002 {
		t.Fatalf("login=wenxs 应归到同一个号 1000002，实得 %d", uid)
	}
	if uid := loginAs("?login=Wenya666"); uid != 1000001 {
		t.Fatalf("login=Wenya666 应得 1000001，实得 %d", uid)
	}
	if got := portalUser(); got != 1000001 {
		t.Fatalf("登 Wenya666 后个人中心应跟随 1000001，实得 %d", got)
	}
	// 首登即落持久映射，键按小写归一。
	if v, ok := kvValue("login_uid:wenxs"); !ok || v != "1000002" {
		t.Fatalf("登录名映射未落库: ok=%v v=%q", ok, v)
	}

	// 改名后登录名仍然有效：映射是账号，昵称只是显示名。
	if _, err := db.SQL.Exec(`UPDATE profile SET name='WenXS改' WHERE uid=1000002`); err != nil {
		t.Fatal(err)
	}
	if uid := loginAs("?login=WenXS"); uid != 1000002 {
		t.Fatalf("改名后 login=WenXS 仍应得 1000002，实得 %d", uid)
	}

	// 未建档的名字：不新建档案，回落到本机固定号 1000001。
	before := profileCount()
	if uid := loginAs("?login=NotCreatedYet"); uid != 1000001 {
		t.Fatalf("未建档名字应回落到 1000001，实得 %d", uid)
	}
	if after := profileCount(); after != before {
		t.Fatalf("未建档名字不得新建档案: %d -> %d", before, after)
	}

	// 不带 login 的旧路径保持原样：本机固定号。
	if uid := loginAs(""); uid != 1000001 {
		t.Fatalf("无 login 参数应沿用 IP 账号 1000001，实得 %d", uid)
	}

	// 只带 token 时按 token 自带的 uid 走，避免二次登录把刚切好的号打回去。
	if uid := loginAs("?token=" + "local%7Caa%7C1000002%7Cbb"); uid != 1000002 {
		t.Fatalf("token 登录应得 token 内的 1000002，实得 %d", uid)
	}

	// 超长名字按未填处理，回落 IP 账号，不产生映射。
	longName := strings.Repeat("鱼", 40)
	encoded := ""
	for range longName {
		encoded += "%E9%B1%BC" // 「鱼」
	}
	if uid := loginAs("?login=" + encoded); uid != 1000001 {
		t.Fatalf("超长名字应回落 1000001，实得 %d", uid)
	}
	if _, ok := kvValue("login_uid:" + strings.ToLower(longName)); ok {
		t.Fatal("超长名字不该写入映射")
	}
}
