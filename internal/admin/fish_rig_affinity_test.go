package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/logging"
	"rf4server/internal/store"
)

// 钓法倾向后台：读矩阵 → 改一格 → 越界被拒 → 恢复出厂。
//
// 这张表只有 20 格，判鱼侧按 "钓法族/水层档" 拼键取值，所以接口必须保证
// 「读到的行能原样写回去」，且越界/未知键当场 400——写进库却取不到等于没改。
func TestRigAffinityAPI(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_ = db.EnsureAdmin(context.Background(), "admin", "admin123")
	srv := NewServer(config.Admin{HTTPAddr: "127.0.0.1:0", JWTSecret: "test"}, db, logging.Discard(), WebFS())
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	base := "http://" + srv.Addr()
	client := &http.Client{Timeout: 5 * time.Second}

	r, _ := http.NewRequest("POST", base+"/api/login", strings.NewReader(`{"username":"admin","password":"admin123"}`))
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	do := func(method, path, body string) (int, map[string]any) {
		var rdr *strings.Reader
		if body == "" {
			rdr = strings.NewReader("")
		} else {
			rdr = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, base+path, rdr)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+lr.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, body := do("GET", "/api/fish/rig_affinity", "")
	if code != 200 {
		t.Fatalf("GET 应 200，实得 %d", code)
	}
	rows, _ := body["rows"].([]any)
	if len(rows) != len(store.RigFamilyOrder)*len(store.LayerBandOrder) {
		t.Fatalf("矩阵应有 20 行，实得 %d", len(rows))
	}
	if fams, _ := body["families"].([]any); len(fams) != len(store.RigFamilyOrder) {
		t.Fatalf("钓法族应有 %d 个", len(store.RigFamilyOrder))
	}
	if bands, _ := body["bands"].([]any); len(bands) != len(store.LayerBandOrder) {
		t.Fatalf("水层档应有 %d 个", len(store.LayerBandOrder))
	}

	// 改一格
	code, _ = do("POST", "/api/fish/rig_affinity",
		`{"rows":[{"rig_family":"bottom","layer_band":"bottom","weight":1.8}]}`)
	if code != 200 {
		t.Fatalf("保存应 200，实得 %d", code)
	}
	loaded, _, err := db.LoadRigAffinity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded[store.RigAffinityKey(store.RigFamilyBottom, store.LayerBandBottom)]; got != 1.8 {
		t.Fatalf("底钓×底层应为 1.8，实得 %g", got)
	}

	// 越界与未知键：必须 400，不能静默写库
	if code, _ = do("POST", "/api/fish/rig_affinity",
		`{"rows":[{"rig_family":"bottom","layer_band":"bottom","weight":9}]}`); code != 400 {
		t.Fatalf("超上限应 400，实得 %d", code)
	}
	if code, _ = do("POST", "/api/fish/rig_affinity",
		`{"rows":[{"rig_family":"submarine","layer_band":"bottom","weight":1}]}`); code != 400 {
		t.Fatalf("未知钓法族应 400，实得 %d", code)
	}
	if code, _ = do("POST", "/api/fish/rig_affinity", `{"rows":[]}`); code != 400 {
		t.Fatalf("空请求应 400，实得 %d", code)
	}

	// 恢复出厂
	if code, _ = do("POST", "/api/fish/rig_affinity/restore", "{}"); code != 200 {
		t.Fatalf("恢复应 200，实得 %d", code)
	}
	loaded, _, err = db.LoadRigAffinity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	def := store.RigAffinityDefaultMap()
	for _, d := range store.RigAffinityDefaults() {
		if got := loaded[d.Key()]; got != d.Weight {
			t.Fatalf("%s 恢复后应为 %g，实得 %g", d.Key(), d.Weight, got)
		}
	}
	if len(def) != len(loaded) {
		t.Fatalf("恢复后应 20 行，实得 %d", len(loaded))
	}
}
