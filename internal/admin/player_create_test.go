package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

// 后台「新建玩家」只预建 profile 行：UID 由网关分配、这里不绑设备。
// 覆盖：显式 UID、自动取号、重复 UID 拒绝、参数校验、审计留痕。
func TestPlayerCreate(t *testing.T) {
	db := testDB(t)
	srv := NewServer(config.Admin{JWTSecret: "test-create"}, db, logging.Discard(), WebFS())
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Username:         "create-tester",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString(srv.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string, token string) (int, string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/players/create", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	if code, _ := call(`{"uid":1000001,"name":"甲"}`, ""); code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401, 实得 %d", code)
	}

	// 空库自动取号落在网关默认起点，等级按同一套等级/经验换算回填。
	code, body := call(`{"name":"  甲板工  ","level":10,"silver":8000,"gold":100}`, tok)
	if code != http.StatusOK {
		t.Fatalf("新建玩家应 200, 实得 %d: %s", code, body)
	}
	var created struct {
		UID    int64  `json:"uid"`
		Name   string `json:"name"`
		Level  int64  `json:"level"`
		XP     int64  `json:"xp"`
		Silver int64  `json:"silver"`
		Gold   int64  `json:"gold"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("解析新建结果: %v", err)
	}
	if created.UID != defaultPlayerUIDBase {
		t.Fatalf("空库首个玩家 UID 应为 %d，实得 %d", defaultPlayerUIDBase, created.UID)
	}
	if created.Name != "甲板工" {
		t.Fatalf("昵称应去掉首尾空格，实得 %q", created.Name)
	}
	p, err := db.GetProfile(t.Context(), created.UID)
	if err != nil {
		t.Fatalf("读取新建档案: %v", err)
	}
	if p.Level != 10 || p.XP <= 0 || p.Silver != 8000 || p.Gold != 100 || p.Name != "甲板工" {
		t.Fatalf("新建档案字段不符: %+v", p)
	}

	// 同一 UID 再建一次必须被拒，且不能覆盖已有档案。
	code, body = call(`{"uid":1000001,"name":"冒名顶替"}`, tok)
	if code != http.StatusConflict {
		t.Fatalf("重复 UID 应 409, 实得 %d: %s", code, body)
	}
	if again, _ := db.GetProfile(t.Context(), 1000001); again.Name != "甲板工" {
		t.Fatalf("重复新建不应覆盖昵称，实得 %q", again.Name)
	}

	// 不填 UID 时按现有最大号 +1 排，不撞已有号；昵称留空沿用默认 player。
	code, body = call(`{}`, tok)
	if code != http.StatusOK {
		t.Fatalf("自动取号应 200, 实得 %d: %s", code, body)
	}
	var auto struct {
		UID  int64  `json:"uid"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(body), &auto)
	if auto.UID != defaultPlayerUIDBase+1 || auto.Name != "player" {
		t.Fatalf("自动取号结果不符: %+v", auto)
	}
	if p, err := db.GetProfile(t.Context(), auto.UID); err != nil || p.Silver != 5000 {
		t.Fatalf("默认初始银币应为 5000: %+v %v", p, err)
	}

	// 参数校验：负资产与超长昵称直接 400，不落库。
	for _, bad := range []string{
		`{"uid":1000010,"silver":-1}`,
		`{"uid":1000011,"gold":-5}`,
		`{"uid":1000012,"name":"` + strings.Repeat("鱼", 33) + `"}`,
		`{"uid":-3}`,
	} {
		if code, body := call(bad, tok); code != http.StatusBadRequest {
			t.Fatalf("非法参数应 400, 实得 %d: %s (%s)", code, body, bad)
		}
	}
	if _, err := db.GetProfile(t.Context(), 1000010); err == nil {
		t.Fatal("非法参数不应落库")
	}

	// 每次成功新建都要留审计，kind=player_create、target=UID。
	entries, err := db.AuditList(t.Context(), 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		if e.Action == "player_create" && e.Target == "1000001" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("应恰好有一条 UID 1000001 的 player_create 审计, 实得 %d", seen)
	}
}
