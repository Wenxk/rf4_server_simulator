package gameserver

// 档位体系对照实验：验证 grade_ratio_enabled 这个开关到底改变什么。
//
// 背景：后台有 grade_pct_below / qualified / trophy / super 四个比例参数
// （设计意图 3% / 87.5% / 9% / 0.5%），但当前库里 grade_ratio_enabled=0。
// 本测试在同一批条件、同一随机种子下各跑一遍，量化开关前后档位分布。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestGradeRatioAB -v -timeout 30m

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// gradeABResult 一组分布。
type gradeABResult struct {
	Mode     string         `json:"mode"`
	Attempts int            `json:"attempts"`
	Hits     int            `json:"hits"`
	Grade    map[string]int `json:"grade"`
	Pct      map[string]float64 `json:"pct"`
	Weight   map[string]float64 `json:"weight"`
}

func TestPlaytestGradeRatioAB(t *testing.T) {
	playtestEnabled(t)
	db := playDB(t)
	srv := playServer(t, db)
	_ = playSess(t, srv, playUID)

	// 挑一组新玩家真能到的条件：起点图 × 三种真实饵 × 浮钓/路亚
	cases := []rollCase{
		{Loc: "level_001_lake", RigType: 1, Bait: "bread", Label: "湖泊/浮钓/面包"},
		{Loc: "level_001_lake", RigType: 1, Bait: "bloodworm", Label: "湖泊/浮钓/红虫"},
		{Loc: "level_001_lake", RigType: 5, Bait: "spinner_2713", Label: "湖泊/路亚/旋转亮片"},
	}
	const perCase = 3000

	run := func(mode string, ratioOn bool) gradeABResult {
		v := "0"
		if ratioOn {
			v = "1"
		}
		if err := db.TunableSet(t.Context(), "grade_ratio_enabled", v, "playtest"); err != nil {
			t.Fatal(err)
		}
		srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

		res := gradeABResult{Mode: mode, Grade: map[string]int{}, Pct: map[string]float64{},
			Weight: map[string]float64{}}
		var allWeights []float64
		byGradeWeights := map[int64][]float64{}
		for _, c := range cases {
			rng := rand.New(rand.NewSource(20261124)) // 固定种子，两组可比
			cache := newFishingRollCache()
			for i := 0; i < perCase; i++ {
				ctx := fishRollContext{
					cache: cache, now: time.Now(), hasPos: true,
					x: float64(rng.Intn(1200)), z: float64(rng.Intn(1200)),
					rigType: c.RigType, forRig: true,
					biteRoll: func() float64 { return 0 },
				}
				ctx.loc = c.Loc
				ctx.skill = neutralFishingSkillBonus()
				f := srv.rollFishWithRules(c.Loc, c.Bait, 0, neutralFishingSkillBonus(), ctx)
				res.Attempts++
				if f == nil || f.Species == "" {
					continue
				}
				res.Hits++
				res.Grade[fmt.Sprintf("%d", f.Grade)]++
				allWeights = append(allWeights, float64(f.WeightG))
				byGradeWeights[f.Grade] = append(byGradeWeights[f.Grade], float64(f.WeightG))
			}
		}
		for k, n := range res.Grade {
			res.Pct[k] = round2(100 * float64(n) / float64(max1(res.Hits)))
		}
		sort.Float64s(allWeights)
		if len(allWeights) > 0 {
			res.Weight["min"] = allWeights[0]
			res.Weight["p50"] = quantileF(allWeights, 0.5)
			res.Weight["p90"] = quantileF(allWeights, 0.9)
			res.Weight["max"] = allWeights[len(allWeights)-1]
			res.Weight["mean"] = round2(meanF(allWeights))
		}
		for g, ws := range byGradeWeights {
			sort.Float64s(ws)
			res.Weight[fmt.Sprintf("g%d_p50", g)] = quantileF(ws, 0.5)
		}
		return res
	}

	off := run("grade_ratio_enabled=0（当前线上）", false)
	on := run("grade_ratio_enabled=1（设计意图）", true)

	// 收尾还原成当前线上值
	_ = db.TunableSet(t.Context(), "grade_ratio_enabled", "0", "playtest")
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	design := map[string]float64{"1": 3, "2": 87.5, "3": 9, "4": 0.5}
	t.Logf("=== 档位分布对照（每组 %d 采样 × %d 条件）===", perCase, len(cases))
	t.Logf("%-8s %14s %14s %12s", "档位", "关闭(当前)", "开启(设计)", "设计目标")
	keys := []string{"1", "2", "3", "4"}
	for _, k := range keys {
		t.Logf("%-8s %13.2f%% %13.2f%% %11.2f%%",
			"档位"+k, off.Pct[k], on.Pct[k], design[k])
	}
	t.Logf("命中：关闭 %d/%d，开启 %d/%d", off.Hits, off.Attempts, on.Hits, on.Attempts)
	t.Logf("权重：关闭 p50=%.0fg p90=%.0fg max=%.0fg | 开启 p50=%.0fg p90=%.0fg max=%.0fg",
		off.Weight["p50"], off.Weight["p90"], off.Weight["max"],
		on.Weight["p50"], on.Weight["p90"], on.Weight["max"])

	playWrite(t, "../../data/playtest", "grade_ab.json",
		map[string]any{"off": off, "on": on, "design_pct": design})
}