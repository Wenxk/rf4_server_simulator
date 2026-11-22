package admin

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/logging"
	"rf4server/internal/store"
)

func mustOverride(fishZh string) store.FishBaitOverride {
	return store.FishBaitOverride{
		FishZh:     fishZh,
		Allow:      []string{"红蚯蚓"},
		Deny:       []string{},
		BottomOnly: []string{},
		Enabled:    true,
	}
}

func readWebIndex() ([]byte, error) {
	return fs.ReadFile(WebFS(), "index.html")
}

// 鱼饵偏好页配置的覆盖，必须在钓场鱼口页（/api/fish/config）同步可见：
// FishConfigRow 需要携带来源与启停；手工轮换组已删除。
func TestFishConfigCarriesBaitRotation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh,in_map_roster,in_habitat) VALUES('铜湖','c.roach','常见拟鲤',1,1)`,
		`INSERT INTO species_weight(fish_key,min_g,max_g) VALUES('c.roach',60,1200)`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// 直接在库层写一条轮换覆盖（等价于后台保存的结果）。
	if err := db.SaveFishBaitOverride(ctx, mustOverride("常见拟鲤"), "test"); err != nil {
		t.Fatal(err)
	}

	cfg, err := db.GetAdminFishConfig(ctx, "铜湖")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range cfg.Fish {
		if f.FishKey != "c.roach" {
			continue
		}
		found = true
		if f.BaitSource != "override" {
			t.Fatalf("bait_source=%q，期望 override（钓场页据此判断有无轮换）", f.BaitSource)
		}
		if !f.BaitEnabled {
			t.Fatal("bait_enabled 应为 true")
		}
	}
	if !found {
		t.Fatalf("鱼口配置里没有 c.roach，实际 %d 条", len(cfg.Fish))
	}
}

// 没有覆盖时 bait_source 必须是 default，避免前端误显示轮换。
func TestFishConfigWithoutOverrideHasDefaultSource(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.SQL.Exec(
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh,in_map_roster,in_habitat) VALUES('铜湖','c.roach','常见拟鲤',1,1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := db.GetAdminFishConfig(ctx, "铜湖")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range cfg.Fish {
		if f.FishKey != "c.roach" {
			continue
		}
		if f.BaitSource != "default" {
			t.Fatalf("无覆盖时 bait_source=%q，期望 default", f.BaitSource)
		}
		return
	}
	t.Fatal("鱼口配置里没有 c.roach")
}

// 前端约束：钓场页必须把轮换渲染出来，且鱼饵偏好保存后要失效饵料缓存。
func TestFishPageRendersBaitRotation(t *testing.T) {
	b, err := readWebIndex()
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, sub := range []string{
		"baitSummaryHTML(f.fish_key,f)", // 传入整行，才能拿到 bait_* 字段
		"invalidateBaitMap",             // 保存后失效缓存
		"row.bait_pick",                 // 自动抽取本期开口饵走接口快照，不在前端另算
		"本期开口",                          // 列上直接显示抽中的饵
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("钓场页缺少轮换同步标记：%q", sub)
		}
	}
}

// 自动抽取的鱼种，钓场页要直接看到「本期抽到哪几款饵」：
// /api/fish/config 的行必须带 bait_pick（配额 / 池子 / 抽中的型号 / 下次切换）。
// 抽签走判鱼同一套 hash，所以这里只断言结构与口径，不断言具体抽中哪一款。
func TestFishConfigCarriesAutoRotationPick(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.EnsureAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.SQL.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustExec(`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh,in_map_roster,in_habitat) VALUES('铜湖','c.beluga','欧洲鳇',1,1)`)
	mustExec(`INSERT INTO species_weight(fish_key,min_g,max_g) VALUES('c.beluga',5000,120000)`)
	// 超稀有配额 1：至少要两个型号组才值得轮换，所以塞三组。
	mustExec(`INSERT INTO fish_ecology(fish_key,rarity_tier) VALUES('c.beluga',2)`)
	baits := []struct {
		id, numeric, name string
		attrs             string
	}{
		{`boil_halibut_10`, `8801`, `比目鱼煮饵 10`, `{"fopbhlmkcpe":1}`},
		{`boil_halibut_14`, `8802`, `比目鱼煮饵 14`, `{"fopbhlmkcpe":1}`},
		{`popup_cream_16`, `8803`, `甜奶油 PopUp 16`, `{"fopbhlmkcpe":8}`},
		{`corn_8`, `8804`, `玉米粒 8`, `{"fopbhlmkcpe":10}`},
	}
	for _, b := range baits {
		mustExec(`INSERT INTO item_def(numeric_id,item_id,category,kind,brand,attrs_json) VALUES(?,?,27,'bait','',?)`, b.numeric, b.id, b.attrs)
		mustExec(`INSERT INTO item_catalog(item_id,name,name_zh,kind,numeric_id) VALUES(?,?,?,'bait',?)`, b.id, b.name, b.name, b.numeric)
	}
	if err := db.SaveFishBaitOverride(ctx, store.FishBaitOverride{
		FishZh:  "欧洲鳇",
		Allow:   []string{"比目鱼煮饵", "甜奶油", "玉米粒"},
		Enabled: true,
	}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFishBaitAuto(ctx, "c.beluga", true, "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.TunableSet(ctx, "bait_rotation_auto_enabled", "1", "test"); err != nil {
		t.Fatal(err)
	}

	cfg, err := db.GetAdminFishConfig(ctx, "铜湖")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(config.Admin{JWTSecret: "test"}, db, logging.Discard(), WebFS())
	srv.applyFishConfigBaitPick(ctx, cfg)

	var row *store.FishConfigRow
	for i := range cfg.Fish {
		if cfg.Fish[i].FishKey == "c.beluga" {
			row = &cfg.Fish[i]
		}
	}
	if row == nil {
		t.Fatalf("鱼口配置里没有 c.beluga，实际 %d 条", len(cfg.Fish))
	}
	pick := row.BaitPick
	if pick == nil {
		t.Fatal("开了自动抽取却没有 bait_pick，钓场页看不到本期开口饵")
	}
	if !pick.Enabled {
		t.Fatalf("池子 %d 组、配额 %d，应当进入轮换", pick.PoolSize, pick.Quota)
	}
	if pick.Quota != 1 || pick.PoolSize != 3 {
		t.Fatalf("配额/池子=%d/%d，期望 1/3", pick.Quota, pick.PoolSize)
	}
	if len(pick.ActiveBaits) != 1 {
		t.Fatalf("抽中 %d 款，期望 1（超稀有）：%v", len(pick.ActiveBaits), pick.ActiveBaits)
	}
	if pick.GlobalOff {
		t.Fatal("总开关已开，global_off 不应为 true")
	}
	if pick.NextSwitch <= time.Now().Unix() {
		t.Fatalf("下次切换时间 %d 已过期", pick.NextSwitch)
	}

	// 总开关关掉：抽签照算，但要标 global_off，前端据此提示「抽了不生效」。
	if err := db.TunableSet(ctx, "bait_rotation_auto_enabled", "0", "test"); err != nil {
		t.Fatal(err)
	}
	cfg2, err := db.GetAdminFishConfig(ctx, "铜湖")
	if err != nil {
		t.Fatal(err)
	}
	srv.applyFishConfigBaitPick(ctx, cfg2)
	for i := range cfg2.Fish {
		if cfg2.Fish[i].FishKey != "c.beluga" {
			continue
		}
		if cfg2.Fish[i].BaitPick == nil || !cfg2.Fish[i].BaitPick.GlobalOff {
			t.Fatalf("总开关关闭时应标 global_off，实得 %+v", cfg2.Fish[i].BaitPick)
		}
	}
}
