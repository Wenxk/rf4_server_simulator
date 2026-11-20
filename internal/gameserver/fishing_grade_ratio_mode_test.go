package gameserver

import (
	"math"
	"testing"
)

// 饵效倍率不得改变四档比例；面板显示的占比就是实际发鱼的占比。
func TestGradeRatioSharesIgnoreBaitModeMultiplier(t *testing.T) {
	srv, db := newRigTestServer(t)
	enableGradeRatio(t, srv, db)
	setGradeRatioTunables(t, srv, db, map[string]string{
		"grade_pct_below":     "5.06",
		"grade_pct_qualified": "93.65",
		"grade_pct_trophy":    "1.19",
		"grade_pct_super":     "0.1",
	})
	lines := cachedGradeLines{val: ptrI64(1000), trophy: ptrI64(5000), blue: ptrI64(8000)}
	for _, mode := range []float64{1, 1.12, 1.36, 2} {
		weights, handled := srv.gradeRatioGradeWeights(lines, mode, 1, 0)
		if !handled {
			t.Fatalf("mode=%g 应有可用档位", mode)
		}
		total := weights[0] + weights[1] + weights[2] + weights[3]
		if math.Abs(weights[0]/total-.0506) > 1e-9 ||
			math.Abs(weights[1]/total-.9365) > 1e-9 ||
			math.Abs(weights[2]/total-.0119) > 1e-9 ||
			math.Abs(weights[3]/total-.001) > 1e-9 {
			t.Fatalf("mode=%g 改动了四档比例: %v", mode, weights)
		}
	}
}

// 档位比例下 TrophyLuck 必须继续生效：四档占比是「无幸运时的基准」，
// 装备与技能带来的上星概率是玩家升级的直接回报，不能被比例模式吞掉。
func TestGradeRatioKeepsTrophyLuckResample(t *testing.T) {
	up := func() float64 { return 500 }
	// luck=1 必定触发一次「取更高体重」的重采样。
	if got := applyTrophyLuck(300, 1, up); got != 500 {
		t.Fatalf("幸运拉满时应抬到更高体重 500，实得 %g", got)
	}
	// 没有幸运时保持原值，不会凭空上档。
	if got := applyTrophyLuck(300, 0, up); got != 300 {
		t.Fatalf("无幸运时应保持 300，实得 %g", got)
	}
}

// 查询预估也必须按幸运放大，否则玩家看到的「预估上星率」与实际不符。
func TestGradeRatioRetainedMassAppliesTrophyLuck(t *testing.T) {
	law := fishWeightLaw{roundedSurvival: func(int64) float64 { return .4 }}
	// q*(1+luck*(1-q)) = 0.4*(1+0.5*0.6) = 0.52
	if got := law.retainedMass(1000, .5); math.Abs(got-.52) > 1e-9 {
		t.Fatalf("预估保留质量应按幸运放大到 0.52，实得 %g", got)
	}
	// 无幸运时回到基准占比。
	if got := law.retainedMass(1000, 0); math.Abs(got-.4) > 1e-9 {
		t.Fatalf("无幸运时应为基准 0.4，实得 %g", got)
	}
}
