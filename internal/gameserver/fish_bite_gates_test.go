package gameserver

import (
	"context"
	"slices"
	"testing"
	"time"
)

func setBiteGateSwitch(t *testing.T, s *Server, value string) {
	t.Helper()
	if err := s.DB.TunableSet(context.Background(), "bite_gate_enabled", value, "test"); err != nil {
		t.Fatal(err)
	}
	s.tunMu.Lock()
	s.tunAt = time.Time{}
	s.tunMu.Unlock()
}

func TestBiteGateDietKeyMapping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		baitType int64
		id       string
		want     []string
	}{
		// 类别 1「杂类」按具体 id 归类，不再一律返回四个键（那等于贻贝肉通杀）。
		{"mixed-mussel", 1, "bait_dreissena", []string{"crustacean"}},
		{"mixed-corn", 1, "corn", []string{"plant"}},
		{"mixed-boilie", 1, "boil_red", []string{"boilie"}},
		{"mixed-unknown", 1, "unknown_misc", nil},
		{"worm", 6, "", []string{"worm"}},
		{"live-fish", 4, "", []string{"fish"}},
		{"popup", 8, "", []string{"boilie"}},
		{"pellet", 9, "", []string{"boilie"}},
		{"corn", 10, "", []string{"plant"}},
		{"shellfish", 16, "", []string{"crustacean"}},
		{"zig", 17, "", []string{"boilie"}},
		{"unknown", 99, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := biteGateDietKeys(ruleBait{id: tc.id, baitType: tc.baitType}); !slices.Equal(got, tc.want) {
				t.Fatalf("baitType %d id=%s keys=%v, want %v", tc.baitType, tc.id, got, tc.want)
			}
		})
	}
}

// G1：钩上是拟饵时，食性 lure 权重低于门槛的鱼直接出候选——
// 青蛙（0.20）不再攻亮片，常见拟鲤（0.48）照常；开关关闭回到遗留手感。
func TestBiteGatesExcludeLureNonPredators(t *testing.T) {
	s, _ := seedBaitRuleTest(t)
	setBiteGateSwitch(t, s, "0")
	seen := map[string]bool{}
	for range 64 {
		if f := s.rollFish("level_900_rules", "rule_lure"); f != nil {
			seen[f.Species] = true
		}
	}
	if !seen["frog"] || !seen["c.roach"] {
		t.Fatalf("gate off must keep legacy behavior, saw %v", seen)
	}
	setBiteGateSwitch(t, s, "1")
	for range 64 {
		f := s.rollFish("level_900_rules", "rule_lure")
		if f == nil {
			continue
		}
		if f.Species == "frog" {
			t.Fatal("frog must never take a lure with gates on")
		}
		if f.Species != "c.roach" {
			t.Fatalf("unexpected species %q", f.Species)
		}
	}
}

// G2：水层超界乘地板不剔除；栖息带内的正常权重不受影响。
func TestBiteGateLayerFloor(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	// 模板库只带 item_def，名册表为空；补中文名供双票解析攻略规则。
	if _, err := db.SQL.Exec(`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES('gates-test','vjun','泥鳅',1),('gates-test','crucian','银鲫',1),('gates-test','burbot','江鳕',1)`); err != nil {
		t.Fatal(err)
	}
	setBiteGateSwitch(t, s, "1")
	cfg := s.biteGateConfig()
	if !cfg.enabled {
		t.Fatal("test expects gate enabled by tunable below")
	}
	run := func(c fishRollContext, key string) float64 {
		keys, wts := s.applyBiteGates(s.dbCtx(), []string{key, "perch"}, []float64{1, 1}, c, cfg)
		for i, k := range keys {
			if k == key {
				return wts[i]
			}
		}
		return 0
	}
	// 江鳕（v4.1 带 0.38~1.0）攻水面拟饵（0.06）：只剩 1% 杂口。
	if w := run(fishRollContext{rigType: 5, lureLayer: 0.06, baits: []string{"rule_lure"}, now: time.Now()}, "burbot"); w != cfg.layerFloor {
		t.Fatalf("burbot at surface must hit floor, got %v", w)
	}
	// 河鲈（v4.1 带 0.245~0.695）攻带内拟饵（0.30）：正常权重；水面拟饵（0.06）
	// 已在带外（容差 0.15），按设计被地板，不再是无差别放行。
	if w := run(fishRollContext{rigType: 5, lureLayer: 0.30, baits: []string{"rule_lure"}, now: time.Now()}, "perch"); w != 1 {
		t.Fatalf("perch in band must keep full weight, got %v", w)
	}
	if w := run(fishRollContext{rigType: 5, lureLayer: 0.06, baits: []string{"rule_lure"}, now: time.Now()}, "perch"); w != cfg.layerFloor {
		t.Fatalf("perch at surface must hit floor, got %v", w)
	}
	// 泥鳅（v4.1 带 0.525~0.975）浮钓近底（400cm→0.8）：带内不受影响。
	if w := run(fishRollContext{rigType: 1, depthCM: 400, baits: []string{"rule_worm"}, now: time.Now()}, "vjun"); w != 1 {
		t.Fatalf("loach near bottom must keep full weight, got %v", w)
	}
	// 底钓（0.93）对鲤科/底栖带鱼零误伤：鲫鱼带 0.54~0.98 在容差内。
	// v4.1 数据里上层鱼（茴鱼/白鲑等 layer_max=0.45）底钓会被地板——上层鱼
	// 不该被底钓钓起，正是 G2 的设计语义。
	if w := run(fishRollContext{rigType: 12, baits: []string{"rule_worm"}, now: time.Now()}, "crucian"); w != 1 {
		t.Fatalf("crucian on bottom rig must keep full weight, got %v", w)
	}
}

// 门把本图名册剔空时必须空竿重排，不能从内置鱼池捞出不属于本图的鱼。
func TestBiteGateEmptyRosterBlocksPoolFallback(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_alias(alias,loc) VALUES('level_901_gates','gates-test')`,
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('gates-test','crucian','银鲫')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	setBiteGateSwitch(t, s, "0")
	if f := s.rollFish("level_901_gates", "rule_lure"); f == nil || f.Species != "crucian" {
		t.Fatalf("gate off legacy roll: %+v", f)
	}
	setBiteGateSwitch(t, s, "1")
	for range 8 {
		if f := s.rollFish("level_901_gates", "rule_lure"); f != nil {
			t.Fatalf("all-lure-incapable roster must yield no fish, got %q from pool fallback", f.Species)
		}
	}
	// 同一名册挂蚯蚓正常出鱼：门只拦「饵钓不到」，不封图。
	if f := s.rollFish("level_901_gates", "rule_worm"); f == nil || f.Species != "crucian" {
		t.Fatalf("worm must still roll crucian: %+v", f)
	}
}

func TestBiteGateConfigDefaultsAndClamps(t *testing.T) {
	s, _ := seedBaitRuleTest(t)
	if cfg := s.biteGateConfig(); cfg.enabled {
		t.Fatal("gate must default off")
	}
	setBiteGateSwitch(t, s, "1")
	if cfg := s.biteGateConfig(); !cfg.enabled || cfg.lureDietMin != 0.30 || cfg.layerFloor != 0.01 {
		t.Fatalf("defaults: %+v", cfg)
	}
	if err := s.DB.TunableSet(context.Background(), "bite_gate_layer_floor", "-1", "test"); err != nil {
		t.Fatal(err)
	}
	s.tunMu.Lock()
	s.tunAt = time.Time{}
	s.tunMu.Unlock()
	if cfg := s.biteGateConfig(); cfg.layerFloor != 0 {
		t.Fatalf("negative floor must clamp to 0: %+v", cfg)
	}
}

// G-diet 天然饵资格：只看鱼种食谱里有没有这类食物。
// 食谱里有就放行（低值只是偶尔吃），食谱里没有才是"不吃"；
// 门层不看攻略 allow / 官服榜 / 本图渔获。爱吃饵 allow 是紧随其后的
// 饵料层资格（filterFishBaitSet），官服榜与本图渔获始终不参与。
func TestBiteGateDietQualification(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_alias(alias,loc) VALUES('level_902_diet','gates-diet')`,
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('gates-diet','vjun','泥鳅鱼'),('gates-diet','crucian','银鲫')`,
		`INSERT INTO loc_fish(loc,fish_key,name_zh,catches) VALUES('gates-diet','vjun','泥鳅',1),('gates-diet','crucian','银鲫',1)`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999804,'rule_corn',27,'{"fopbhlmkcpe":13}'),(999805,'rule_dead',27,'{"fopbhlmkcpe":15}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('rule_corn','玉米粥'),('rule_dead','死鲭鱼')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	setBiteGateSwitch(t, s, "1")
	// 蚯蚓：泥鳅 worm=1.87、银鲫 worm=1.73，两边都在食谱里。
	seen := map[string]bool{}
	for range 64 {
		if f := s.rollFish("level_902_diet", "rule_worm"); f != nil {
			seen[f.Species] = true
		}
	}
	if !seen["vjun"] || !seen["crucian"] {
		t.Fatalf("worm must roll both loach and crucian, saw %v", seen)
	}
	// 玉米粥：银鲫爱吃 → 规则命中给满分档；泥鳅的设定里没有玉米粥、也没有
	// companion 实证 → 饵料层给 0（食谱 plant=0.2 只说明「偶尔吃」，不单独放行）。
	cache := newFishingRollCache()
	now := time.Now()
	_, vw := s.filterFishBaitSet("gates-diet", []string{"rule_corn"}, []string{"vjun"}, []float64{1}, now, fishRollContext{cache: cache})
	_, cw := s.filterFishBaitSet("gates-diet", []string{"rule_corn"}, []string{"crucian"}, []float64{1}, now, fishRollContext{cache: cache})
	if len(vw) != 0 || len(cw) != 1 {
		t.Fatalf("corn porridge: loach should be rejected, crucian kept: loach=%v crucian=%v", vw, cw)
	}
	// 门层 G-diet 本身仍只看"食谱里有没有这个键"：低值不该被门层剔除。
	gk, gw := s.applyBiteGates(s.dbCtx(), []string{"vjun", "crucian"}, []float64{1, 1},
		fishRollContext{baits: []string{"rule_corn"}, cache: cache}, s.biteGateConfig())
	if len(gk) != 2 || gw[0] <= 0 || gw[1] <= 0 {
		t.Fatalf("G-diet should keep both low and high diet values: keys=%v w=%v", gk, gw)
	}
	// 死饵鱼：银鲫明确禁饵类（不吃拟饵/饵鱼类）——deny 直接剔除。
	for range 16 {
		if f := s.rollFish("level_902_diet", "rule_dead"); f != nil && f.Species == "crucian" {
			t.Fatal("crucian deny-lists baitfish and must be excluded")
		}
	}
}
