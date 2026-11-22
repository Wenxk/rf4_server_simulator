package gameserver

import (
	"math"
	"testing"
)

// 路亚复合档位：饵分 × (0.4 + 0.6 × 动作分)。饵不对口是硬伤，动作再好也
// 到不了好档；动作没上报（刚落水）按中性 0.45，先把好饵压到中档，等玩家
// 做出对口动作再由 paceLureActionRearm 重排进短窗。

func TestPaceLureActionQualityNeutral(t *testing.T) {
	if got := paceLureActionQuality("jerkbait", ""); math.Abs(got-0.45) > 1e-9 {
		t.Fatalf("无动作上报应为中性 0.45，实得 %g", got)
	}
	// 顶格动作 jerkbait + jerk = 1.34 → 动作分 1.0
	if got := paceLureActionQuality("jerkbait", "jerk"); math.Abs(got-1) > 1e-9 {
		t.Fatalf("jerkbait+jerk 应为 1.0，实得 %g", got)
	}
	// 中性权重 1.0 → (1-0.8)/0.54 ≈ 0.37
	if got := paceLureActionQuality("jerkbait", "no_such_action"); math.Abs(got-0.37) > 0.005 {
		t.Fatalf("中性动作为约 0.37，实得 %g", got)
	}
}

func TestPaceLureCombinedScore(t *testing.T) {
	cases := []struct {
		name      string
		baitScore float64
		lureType  string
		action    string
		want      float64
	}{
		{"对口饵+对口动作", 1.0, "jerkbait", "jerk", 1.0},
		{"对口饵+无动作", 1.0, "jerkbait", "", 1.0 * (0.4 + 0.6*0.45)},
		{"半对口饵+对口动作", 0.5, "jerkbait", "jerk", 0.5},
		{"差饵+顶格动作也到不了好档", 0.3, "jerkbait", "jerk", 0.3},
		{"中饵+对口动作最高只有中档", 0.6, "pilker", "marine_jig", 0.6},
	}
	for _, c := range cases {
		if got := paceLureCombinedScore(c.baitScore, c.lureType, c.action); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("%s：期望 %g，实得 %g", c.name, c.want, got)
		}
	}
}

func TestPaceLureWindowByTier(t *testing.T) {
	s, _ := newDetachTestServer(t)
	cases := []struct {
		name             string
		tier             int
		lureMax          float64
		wantMin, wantMax float64
	}{
		{"好档默认窗", 2, 6, 1.5, 6},
		{"好档受 lureMax 收窄", 2, 3, 1.5, 3},
		// bite_delay_lure_max 再大也不放大好档窗口（收线短窗是硬约束）。
		{"好档不被放大", 2, 60, 1.5, 6},
		{"中档对齐底钓中饵", 1, 6, 10, 60},
		{"差档对齐底钓差饵", 0, 6, 20, 60},
	}
	for _, c := range cases {
		lmin, lmax := s.paceLureWindowFor(c.tier, c.lureMax)
		if lmin != c.wantMin || lmax != c.wantMax {
			t.Fatalf("%s：期望 %g~%g，实得 %g~%g", c.name, c.wantMin, c.wantMax, lmin, lmax)
		}
	}
}

func TestRodPaceProbFollowsLureAction(t *testing.T) {
	s, _ := newDetachTestServer(t)
	setWeatherTestTunable(t, s, "bite_pace_enabled", "1")

	// 对口饵、还没收线：复合 0.67 → 中档 0.5。
	mid := &FishState{RigType: 5, PaceKnown: true, PaceScore: 1, PaceTier: 0}
	if got := s.rodPaceProb(mid); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("无动作时应落到中档 0.5，实得 %g", got)
	}
	// 同一支杆做出 jerk：复合 1.0 → 好档 1.0，并刷新 PaceTier。
	mid.LureType, mid.LureAction = "jerkbait", "jerk"
	if got := s.rodPaceProb(mid); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("对口动作应抬到好档 1.0，实得 %g", got)
	}
	if mid.PaceTier != 2 {
		t.Fatalf("好档应把 PaceTier 刷到 2，实得 %d", mid.PaceTier)
	}
	// 差饵：动作再好也只是差档 0.12。
	bad := &FishState{RigType: 5, PaceKnown: true, PaceScore: 0.3,
		LureType: "jerkbait", LureAction: "jerk"}
	if got := s.rodPaceProb(bad); math.Abs(got-0.12) > 1e-9 {
		t.Fatalf("差饵应为差档 0.12，实得 %g", got)
	}
	// 非路亚族仍走静态快照，不受动作影响。
	standard := &FishState{RigType: 12, PaceKnown: true, PaceProb: 0.5, PaceLureProb: 0.12}
	if got := s.rodPaceProb(standard); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("底层杆应取 PaceProb 0.5，实得 %g", got)
	}
	// 热关闭立即回中性 1，残留状态不生效。
	setWeatherTestTunable(t, s, "bite_pace_enabled", "0")
	if got := s.rodPaceProb(mid); got != 1 {
		t.Fatalf("热关闭后应回 1，实得 %g", got)
	}
}
