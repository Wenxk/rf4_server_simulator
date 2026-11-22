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

func TestBaitDietKeyMapping(t *testing.T) {
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
			if got := baitDietKeys(ruleBait{id: tc.id, baitType: tc.baitType}); !slices.Equal(got, tc.want) {
				t.Fatalf("baitType %d id=%s keys=%v, want %v", tc.baitType, tc.id, got, tc.want)
			}
		})
	}
}

// 严格 allow：有规则时只认 allow，companion tier3 也不能越级。
func TestStrictAllowOnly(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	setBaitRuleSwitch(t, s, "1")
	// c.roach 的 allow 里有蚯蚓；frog 只吃苍蝇。
	for range 64 {
		f := s.rollFish("level_900_rules", "rule_worm")
		if f == nil || f.Species != "c.roach" {
			t.Fatalf("strict allow 应只出 c.roach，实得 %+v", f)
		}
	}
	// rule_other 不在 allow；即使 companion 给了 tier3，也不允许咬。
	if _, err := db.SQL.Exec(`INSERT INTO companion_bait_fish(bait_id,fish_key,tier,evidence) VALUES('rule_other','c.roach',3,999)`); err != nil {
		t.Fatal(err)
	}
	for range 64 {
		if f := s.rollFish("level_900_rules", "rule_other"); f != nil {
			t.Fatalf("allow 之外的饵必须归零，实得 %+v", f)
		}
	}
}

// G2：水层超界乘地板不剔除；栖息带内的正常权重不受影响。
func TestBiteGateLayerFloor(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	// 模板库只带 item_def，名册表为空；补中文名供规则解析。
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
	if w := run(fishRollContext{rigType: 5, lureLayer: 0.06, now: time.Now()}, "burbot"); w != cfg.layerFloor {
		t.Fatalf("burbot at surface must hit floor, got %v", w)
	}
	// 河鲈（v4.1 带 0.245~0.695）攻带内拟饵（0.30）：正常权重；水面拟饵（0.06）
	// 已在带外（容差 0.15），按设计被地板，不再是无差别放行。
	if w := run(fishRollContext{rigType: 5, lureLayer: 0.30, now: time.Now()}, "perch"); w != 1 {
		t.Fatalf("perch in band must keep full weight, got %v", w)
	}
	if w := run(fishRollContext{rigType: 5, lureLayer: 0.06, now: time.Now()}, "perch"); w != cfg.layerFloor {
		t.Fatalf("perch at surface must hit floor, got %v", w)
	}
	// 泥鳅（v4.1 带 0.525~0.975）浮钓近底（400cm→0.8）：带内不受影响。
	if w := run(fishRollContext{rigType: 1, depthCM: 400, now: time.Now()}, "vjun"); w != 1 {
		t.Fatalf("loach near bottom must keep full weight, got %v", w)
	}
	// 底钓（0.93）对鲤科/底栖带鱼零误伤：鲫鱼带 0.54~0.98 在容差内。
	if w := run(fishRollContext{rigType: 12, now: time.Now()}, "crucian"); w != 1 {
		t.Fatalf("crucian on bottom rig must keep full weight, got %v", w)
	}
}

func TestBiteGateConfigDefaultsAndClamps(t *testing.T) {
	s, _ := seedBaitRuleTest(t)
	if cfg := s.biteGateConfig(); cfg.enabled {
		t.Fatal("gate must default off")
	}
	setBiteGateSwitch(t, s, "1")
	if cfg := s.biteGateConfig(); !cfg.enabled || cfg.layerFloor != 0.01 {
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
