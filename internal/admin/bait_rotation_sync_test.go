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

// readWeb 读内嵌前端里的单个文件。
//
// 2026-11-27 批次 6 切换后，index.html 已是 SPA 空壳（真正的界面在 app/ 下的模块里），
// 所以前端约束不能再靠「读 index.html 找字符串」——那条路在切换当天就永久失效了。
func readWeb(rel string) ([]byte, error) {
	return fs.ReadFile(WebFS(), rel)
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

// stripJSComments 去掉 // 行注释与 /* */ 块注释，保留字符串字面量。
//
// 为什么需要它：waters.js 顶部有一段注释专门解释「`bait_groups` / `bait_rotate_mode`
// 已经删除、不要再读」。不剥注释的话，那句**说明**会把「不得读旧字段」的断言
// 直接判成违规——测试反而阻止了记录历史。断言要针对代码，不针对散文。
//
// 字符串里的 // 不能当注释（`'#rules'`、URL 都含斜杠），所以这里跟一个最小的
// 状态机：单引号 / 双引号 / 反引号模板各算一个状态，模板里还允许 ${} 嵌套。
func stripJSComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	runes := []rune(src)
	const (
		normal = iota
		line
		block
		single
		double
		backtick
	)
	st := normal
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch st {
		case normal:
			switch {
			case c == '/' && next == '/':
				st = line
				i++
			case c == '/' && next == '*':
				st = block
				i++
			case c == '\'':
				st = single
				b.WriteRune(c)
			case c == '"':
				st = double
				b.WriteRune(c)
			case c == '`':
				st = backtick
				b.WriteRune(c)
			default:
				b.WriteRune(c)
			}
		case line:
			if c == '\n' {
				st = normal
				b.WriteRune(c)
			}
		case block:
			if c == '*' && next == '/' {
				st = normal
				i++
			}
		case single:
			b.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				b.WriteRune(runes[i+1])
				i++
			} else if c == '\'' {
				st = normal
			}
		case double:
			b.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				b.WriteRune(runes[i+1])
				i++
			} else if c == '"' {
				st = normal
			}
		case backtick:
			b.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				b.WriteRune(runes[i+1])
				i++
			} else if c == '`' {
				st = normal
			}
		}
	}
	return b.String()
}

// 前端约束：钓场页必须把「本期开口」渲染出来，且保存鱼饵规则后要重新拉列表。
//
// 批次 6 切换后这条不再读 index.html（它已是 SPA 空壳），改读真正实现该功能的两个
// 页面模块。断言的重点也跟着变了：不只是「有没有这个词」，还要保证**没有回去读
// 已被后端删掉的字段**——照 `bait_groups` 读会永远显示「模式未取得」，界面看着像
// 功能坏了，而且不会有任何报错。
func TestFishPageRendersBaitRotation(t *testing.T) {
	waters, err := readWeb("app/pages/waters.js")
	if err != nil {
		t.Fatal(err)
	}
	page := string(waters)
	for _, sub := range []string{
		"row.bait_pick", // 自动抽取本期开口饵走接口快照，不在前端另算
		"本期开口",          // 列上直接显示抽中的饵
	} {
		if !strings.Contains(page, sub) {
			t.Errorf("钓场页缺少轮换同步标记：%q", sub)
		}
	}
	// 手工轮换组已整体删除（store.FishConfigRow 不再返回），前端不能再读。
	// 只查代码：注释里点名这三个字段是在解释「不要再读」，那是有价值的记录。
	code := stripJSComments(page)
	for _, dead := range []string{"bait_groups", "bait_rotate_mode", "bait_rotate_sec"} {
		if strings.Contains(code, dead) {
			t.Errorf("钓场页仍在读已删除的字段 %q：后端不返回，界面会一直显示「未取得」", dead)
		}
	}

	// 鱼饵偏好保存后必须重新拉一次列表：SPA 没有旧后台那层 BAIT_MAP 缓存，
	// 但「保存完还停在旧数据上」是同一类缺陷，靠 reload() 保证。
	bait, err := readWeb("app/pages/bait.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(bait)
	save := strings.Index(src, `put('/api/bait/rules'`)
	if save < 0 {
		t.Fatal("鱼饵页缺少保存规则的 PUT /api/bait/rules")
	}
	// 保存那一段结尾必须重新拉列表（load() 或 reload()），否则刚存的值当场看不见。
	tail := src[save:]
	if end := strings.Index(tail, "catch (e)"); end > 0 {
		tail = tail[:end]
	} else if len(tail) > 600 {
		tail = tail[:600]
	}
	if !strings.Contains(tail, "reload()") && !strings.Contains(tail, "load()") {
		t.Error("鱼饵页保存后没重新拉列表：刚存的值会当场看不见")
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
