package gameserver

// 作钓体验测试台（playtest）——只在显式设置 RF4_PLAYTEST=1 时运行。
//
// 目的：在「不改生产代码、不重启线上服务」的前提下，用真实判鱼主链与真实
// 玩家人设（快照库里的装备、等级、地图）产出可审计的体感证据。
//
// 为什么放同包：判鱼、咬钩延迟、档位、重量分布都是未导出方法，只有同包
// 才能原样调用；重新实现一份就等于测了个影子系统。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytest -v -timeout 40m
//
// 输出：data/playtest/*.json（可直接喂给分析脚本）。

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"rf4server/internal/config"
	"rf4server/internal/logging"
	"rf4server/internal/proto"
	"rf4server/internal/store"
	"rf4server/internal/tunnel"
)

// playSnapPath 是新玩家基线库：结构与参数来自服务端，玩家记录只保留
// 本次新建的 playtest01（uid 1000003，等级 1/经验 0/33 件 starter 装备）。
// 库内 catch_log / fish_roll_trace / catch_seen / trophy 等历史记录已全部清空，
// 因此所有结论都不依赖任何既有渔获数据。
const playSnapPath = "../../data/playtest/fresh.db"

// playUID 是本次作钓测试唯一的主体：全新账号 playtest01。
// 全部离线测试与线上测试都用它，避免「离线测老号、线上测新号」两套口径。
const playUID int64 = 1000003

// playRodGUID 取该玩家某件装备的实例 GUID（真实竿，不是构造出来的假 GUID）。
func playRodGUID(t *testing.T, db *store.DB, uid int64, itemID string) string {
	t.Helper()
	var guid string
	err := db.SQL.QueryRow(
		`SELECT guid FROM item WHERE uid=? AND item_id=? LIMIT 1`, uid, itemID).Scan(&guid)
	if err != nil {
		t.Fatalf("取 %s 的实例 GUID: %v", itemID, err)
	}
	return guid
}

func playtestEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("RF4_PLAYTEST") != "1" {
		t.Skip("未设置 RF4_PLAYTEST=1")
	}
}

// playDB 打开快照库（可写：需要写玩家表做真实结算），静态库只读挂载。
func playDB(t *testing.T) *store.DB {
	t.Helper()
	if _, err := os.Stat(playSnapPath); err != nil {
		t.Skipf("缺少快照库 %s: %v", playSnapPath, err)
	}
	db, err := store.OpenWithStatic(playSnapPath, staticTestDBPath())
	if err != nil {
		t.Fatalf("打开快照库: %v", err)
	}
	if err := db.EnableStaticReadOnly(t.Context()); err != nil {
		_ = db.Close()
		t.Fatalf("启用静态只读: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func playServer(t *testing.T, db *store.DB) *Server {
	t.Helper()
	srv := NewServer(config.Game{Tunnel: config.Tunnel{PSK: "playtest"}}, logging.Discard(), nil)
	srv.SetDB(db)
	return srv
}

// playClearBiteOverrides 清掉地图级咬钩覆盖。
//
// ⚠️ 这是本项目一个真实的行为陷阱，测试必须显式处理：
// effectiveBiteDelaySettings 一旦发现 fish_bite_setting 有该地图的行
// （HasOverride），就用该行**整体替换**全局 tunable，而不是逐字段合并。
// 所以后台只要点过一次某地图的「咬钩」卡片，全局 bite_delay_fixed /
// bite_delay_max / bite_delay_scale 对那张图就永久失效——哪怕地图行里
// fixed 填的是 0（= 不固定），全局值也进不来。
//
// 快照库里「克马羚诺也湖」正好有一行 admin 写入的覆盖，会让所有浮钓/底钓
// 测试按真实 20s 上限采样，跟想验证的参数完全不是一回事。
func playClearBiteOverrides(t *testing.T, db *store.DB) {
	t.Helper()
	if _, err := db.SQL.Exec(`DELETE FROM fish_bite_setting`); err != nil {
		t.Fatalf("清除地图咬钩覆盖: %v", err)
	}
}

// playSess 构造会话并**注册进服务端会话表**。
//
// 这一步不是形式主义：咬钩定时器回调 onRodBiteTimerTicket 第一句就是
// `sess := s.liveSessionByUID(uid)`，取不到会话会当作「玩家已断线」直接
// return（保留 CastActive/BiteAt 等重连恢复），根本不判鱼。早期版本这里
// 只 new 了一个 Session 没注册，表现为每杆都空等——计时器在跑、但永远
// 不派鱼，空等时长恰好等于请求方自己的轮询超时，极易被误判成
// 「落水保护 6s」。
//
// 同时必须挂一条真隧道并**持续读帧**：pushPendingBite 会写 14/14，
// net.Pipe 是同步写，没有读端会把推送协程卡死。
func playSess(t *testing.T, srv *Server, uid int64) *Session {
	t.Helper()
	connection, peer := cleanupTestTunnel(t, "playtest")
	s := newSession(srv, 9001, "playtest|local|"+strconv.FormatInt(uid, 10), uid, connection)
	srv.mu.Lock()
	srv.sessions[s.ID], srv.byToken[s.Token] = s, s
	srv.mu.Unlock()
	gsSessionsActive.Add(1)

	// 后台持续读：net.Pipe 同步写，无读端会阻塞推送。
	pushLogMu.Lock()
	playPushes = nil
	pushLogMu.Unlock()
	go func() {
		for {
			kind, _, payload, err := peer.ReadRecord()
			if err != nil {
				return
			}
			if kind != tunnel.TypeData {
				continue
			}
			if env, ok := proto.ParseEnvelope(payload); ok {
				pushLogMu.Lock()
				playPushes = append(playPushes, playPush{Main: env.Main, Sub: env.Sub, Len: len(payload)})
				pushLogMu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		delete(srv.sessions, s.ID)
		s.Close("playtest_finished")
		gsSessionsActive.Add(-1)
	})
	return s
}

// playPush 记录服务端真实推送的帧（14/14 咬钩、4/21 饵量等）。
type playPush struct {
	Main uint8
	Sub  uint8
	Len  int
}

var (
	pushLogMu  sync.Mutex
	playPushes []playPush
)

// takePushes 取走并清空已记录的推送帧。
func takePushes() []playPush {
	pushLogMu.Lock()
	defer pushLogMu.Unlock()
	out := playPushes
	playPushes = nil
	return out
}

func playWrite(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("写出 %s (%d 字节)", p, len(b))
}

func stat(xs []float64) map[string]float64 {
	out := map[string]float64{"n": float64(len(xs))}
	if len(xs) == 0 {
		return out
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	out["mean"] = sum / float64(len(s))
	out["min"] = s[0]
	out["max"] = s[len(s)-1]
	for _, q := range []struct {
		k string
		p float64
	}{{"p10", .10}, {"p25", .25}, {"p50", .50}, {"p75", .75}, {"p90", .90}, {"p95", .95}, {"p99", .99}} {
		out[q.k] = quantile(s, q.p)
	}
	return out
}

func quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := p * float64(len(sorted)-1)
	lo, hi := int(math.Floor(idx)), int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	f := idx - float64(lo)
	return sorted[lo]*(1-f) + sorted[hi]*f
}

// ---------------------------------------------------------------------------
// 1. 判鱼与档位分布：按「地图 × 钓组类型 × 饵」抽样
// ---------------------------------------------------------------------------

type rollCase struct {
	Loc     string
	RigType int64
	Bait    string
	Label   string
}

type rollOutput struct {
	Attempts    int                `json:"attempts"`
	Hits        int                `json:"hits"`
	HitRate     float64            `json:"hit_rate"`
	EmptyReason map[string]int     `json:"empty_reason"`
	Species     map[string]int     `json:"species"`
	Grade       map[string]int     `json:"grade"`
	WeightG     map[string]float64 `json:"weight_stats"`
	XP          float64            `json:"xp_mean"`
	Silver      float64            `json:"silver_mean"`
	Case        rollCase           `json:"case"`
	Note        string             `json:"note,omitempty"`
}

func TestPlaytestRollDistribution(t *testing.T) {
	playtestEnabled(t)
	db := playDB(t)
	srv := playServer(t, db)
	sess := playSess(t, srv, playUID)

	cases := buildRollCases(t, db)
	t.Logf("抽样 %d 组条件", len(cases))

	const perCase = 900
	out := make([]rollOutput, 0, len(cases))
	for _, c := range cases {
		rng := rand.New(rand.NewSource(int64(len(c.Label))*7919 + 13))
		o := rollOutput{Case: c, EmptyReason: map[string]int{}, Species: map[string]int{},
			Grade: map[string]int{}, WeightG: map[string]float64{}, XP: 0, Silver: 0}
		var ws, xps, svs []float64
		cache := newFishingRollCache()
		for i := 0; i < perCase; i++ {
			now := time.Now()
			ctx := fishRollContext{
				cache:   cache,
				now:     now,
				hasPos:  true,
				x:       float64(rng.Intn(1200)),
				z:       float64(rng.Intn(1200)),
				rigType: c.RigType,
				forRig:  true,
				biteRoll: func() float64 {
					// 用命中率纯判鱼能力：概率门固定 1.0，
					// 这样空因只反映「权重链把候选剔空」。
					return 0
				},
			}
			ctx.loc = c.Loc
			ctx.skill = neutralFishingSkillBonus()
			f := srv.rollFishWithRules(c.Loc, c.Bait, 0, neutralFishingSkillBonus(), ctx)
			o.Attempts++
			if f == nil {
				o.EmptyReason["(nil)"]++
				continue
			}
			if f.Species == "" {
				o.EmptyReason["(空鱼种)"]++
				continue
			}
			o.Hits++
			o.Species[f.Species]++
			o.Grade[fmt.Sprintf("%d", f.Grade)]++
			ws = append(ws, float64(f.WeightG))
			xps = append(xps, float64(f.XP))
			svs = append(svs, float64(f.Silver))
		}
		_ = sess
		o.HitRate = float64(o.Hits) / float64(o.Attempts)
		o.WeightG = stat(ws)
		o.XP = meanOf(xps)
		o.Silver = meanOf(svs)
		out = append(out, o)
		t.Logf("%-28s 命中 %5.1f%%  鱼种 %2d  档位 %v  均重 %.0fg",
			c.Label, o.HitRate*100, len(o.Species), o.Grade, o.WeightG["mean"])
	}
	playWrite(t, "../../data/playtest", "roll_distribution.json", out)
}

func meanOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// buildRollCases 构造代表性组合：覆盖主流地图、四种钓组族、常用饵。
//
// 刻意不读 fish_roll_trace / catch_log：那是玩家历史，用户明确要求「不参考
// 任何历史数据」。这里只依赖静态事实表：
//   - loc_fish     每个地图有哪些鱼（名册是判鱼的输入）
//   - loc_alias    地图中文名 ↔ level_* 场景 ID
//   - item_def     哪些饵真实存在
//
// ⚠️ loc 必须传 level_* 场景 ID，不能传中文地名：ResolveStatLoc
// （store/repos_market.go:53）只认 level_ 前缀，中文地名会返回空串，
// 判鱼随即走空池 → 全 0 命中（上一轮就是这个坑）。
func buildRollCases(t *testing.T, db *store.DB) []rollCase {
	t.Helper()
	// 饵与钓组族的固定搭配：浮钓用天然饵，路亚用亮片。
	rigBaits := []struct {
		RigType int64
		Bait    string
		Tag     string
	}{
		{1, "bread", "浮钓/面包"},
		{1, "bloodworm", "浮钓/红虫"},
		{1, "worm_01", "浮钓/蚯蚓"},
		{5, "spinner_2713", "路亚/旋转亮片"},
		{5, "spinnerbaits_1244", "路亚/复合亮片"},
	}
	// 取「中文名 → level_* 场景 ID」，并按名册鱼种数排序。
	sqlRows, err := db.SQL.Query(`
		SELECT a.alias, a.loc, COUNT(f.fish_key) n
		FROM loc_alias a
		JOIN loc_fish f ON f.loc = a.loc
		WHERE a.alias LIKE 'level_%'
		GROUP BY a.alias, a.loc
		HAVING n >= 7
		ORDER BY n DESC`)
	if err != nil {
		t.Fatalf("读地图名册: %v", err)
	}
	defer sqlRows.Close()
	type locRow struct {
		Alias string
		Zh    string
	}
	var locs []locRow
	for sqlRows.Next() {
		var r locRow
		var n int
		if err := sqlRows.Scan(&r.Alias, &r.Zh, &n); err != nil {
			t.Fatal(err)
		}
		locs = append(locs, r)
	}
	if len(locs) == 0 {
		t.Skip("loc_alias/loc_fish 无可用组合")
	}
	var out []rollCase
	for _, lr := range locs {
		for _, rb := range rigBaits {
			out = append(out, rollCase{
				Loc: lr.Alias, RigType: rb.RigType, Bait: rb.Bait,
				Label: fmt.Sprintf("%s(%s)/%s", lr.Zh, lr.Alias, rb.Tag),
			})
		}
		if len(out) >= 40 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 2. 咬钩节奏：延迟分布（含/不含 pace 模式）
// ---------------------------------------------------------------------------

type latencyOutput struct {
	Mode       string             `json:"mode"`
	RigType    int64              `json:"rig_type"`
	Loc        string             `json:"loc"`
	Stats      map[string]float64 `json:"stats"`
	Histogram  []int              `json:"histogram"`
	HistEdges  []float64          `json:"hist_edges"`
	AtCapRatio float64            `json:"at_cap_ratio"`
	CapSeconds float64            `json:"cap_seconds"`
	// pace 生效证据：PaceKnown=false 说明这组根本没进节奏分支。
	PaceKnown  bool    `json:"pace_known"`
	PaceScore  float64 `json:"pace_score"`
	PaceFactor float64 `json:"pace_factor"`
}

func TestPlaytestBiteLatency(t *testing.T) {
	playtestEnabled(t)
	db := playDB(t)
	srv := playServer(t, db)
	sess := playSess(t, srv, playUID)
	_ = sess

	const n = 20000
	edges := []float64{3, 6, 9, 12, 15, 18, 20, 24, 30, 40, 60}
	var out []latencyOutput

	type rigCase struct {
		loc  string
		rig  int64
		name string
	}
	cases := []rigCase{
		{"loc1_02", 1, "浮钓/普通竿"},
		{"loc1_02", 12, "底钓 feeder"},
		{"loc19_03", 26, "底钓/鲤竿"},
		{"loc19_03", 5, "路亚/纺车"},
		{"loc19_03", 6, "路亚/枪柄"},
		{"loc12_01", 50, "海钓/铁板"},
	}
	for _, pace := range []bool{false, true} {
		if err := db.TunableSet(t.Context(), "bite_pace_enabled", boolTunable(pace), "playtest"); err != nil {
			t.Fatal(err)
		}
		srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
		mode := "legacy(无节奏控制)"
		if pace {
			mode = "pace(饵效控速)"
		}
		for _, c := range cases {
			st := &FishState{Loc: c.loc, RigType: c.rig}
			// 关键：pace 分支的门槛是 st.PaceKnown && s.paceEnabled()
			// （fishing.go:486 biteDelayInWeather）。不先算一次节奏状态，
			// PaceKnown 恒为 false，pace=true 这组会静默走同一条老路径，
			// 得到「开关无效」的假结论。
			//
			// computePaceState 只「返回」状态，不回写 st——生产代码在
			// fishing.go:2980 用 st.storePaceState(pace) 写回且必须持 st.mu。
			// 这里照抄同样的两步，否则 PaceKnown 永远是 false。
			if st.GearGuid == "" {
				st.GearGuid = playRodGUID(t, db, playUID, "tele_10012_tl16")
			}
			pace := srv.computePaceState(sess, st, true)
			st.mu.Lock()
			st.storePaceState(pace)
			st.mu.Unlock()
			cfg := srv.effectiveBiteDelaySettings(st)
			// 采样后「先乘 hook 系数、后 clamp」的顺序决定了可见峰值不是
			// bite_delay_max 本身：标准路径是 standardBiteDelay(样本,...) 先
			// clamp 到 maxDelay，再在 scheduleBiteState 里乘 hookBiteDelayFactor。
			// 所以玩家实际看到的天花板 = maxDelay × hookFactor（默认 0.9325×20
			// ≈ 18.65s），拿 d≈maxDelay 当判据会永远得 0%。
			hookF := hookBiteDelayFactor(gearPhysicsSpec{HookPresent: true, HookSize: 4, HookLengthMM: 8})
			visibleCap := cfg.maxDelay * hookF
			var ds []float64
			atCap := 0
			for i := 0; i < n; i++ {
				d := srv.biteDelay(st, neutralFishingSkillBonus())
				d *= hookF
				ds = append(ds, d)
				if math.Abs(d-visibleCap) < 0.02 && !rigTypeLureFamily[c.rig] {
					atCap++
				}
			}
			s := stat(ds)
			out = append(out, latencyOutput{
				Mode: mode, RigType: c.rig, Loc: c.loc, Stats: s,
				Histogram:  histogramF(ds, edges),
				HistEdges:  edges,
				AtCapRatio: float64(atCap) / float64(n),
				CapSeconds: visibleCap,
				PaceKnown:  st.PaceKnown,
				PaceScore:  st.PaceScore,
				PaceFactor: st.PaceFactor,
			})
			t.Logf("%-14s %-10s rig%-3d 均值 %5.2fs  p50 %5.2fs  p90 %5.2fs  顶上限 %.1f%%  pace已知=%v 因子=%.2f",
				mode, c.name, c.rig, s["mean"], s["p50"], s["p90"], float64(atCap)/float64(n)*100,
				st.PaceKnown, st.PaceFactor)
		}
	}
	playWrite(t, "../../data/playtest", "bite_latency.json", out)
}

func boolTunable(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func histogramF(xs []float64, edges []float64) []int {
	out := make([]int, len(edges)+1)
	for _, x := range xs {
		i := 0
		for i < len(edges) && x >= edges[i] {
			i++
		}
		out[i]++
	}
	return out
}

// ---------------------------------------------------------------------------
// 3. 档位/重量/经济阶梯：按真实鱼种验证四档分布与单条价值
// ---------------------------------------------------------------------------

type gradeOutput struct {
	Species    string             `json:"species"`
	Loc        string             `json:"loc"`
	LocAlias   string             `json:"loc_alias"`
	Lines      []int64            `json:"lines"` // 达标/奖杯/蓝冠（克）
	Counts     map[string]int     `json:"counts"`
	WeightStat map[string]float64 `json:"weight_stats"`
	XPMean     map[string]float64 `json:"xp_by_grade"`
	SilverMean map[string]float64 `json:"silver_by_grade"`
	SilverPerG map[string]float64 `json:"silver_per_g_by_grade"`
}


// gradeLadderBait 档位阶梯测试用的饵。
// 必须给真实饵：fish_bait_rules_enabled=1 时 filterFishBaitSet 会按饵料规则
// 过滤鱼种，空饵把候选剔空 → 全 0。bread 对多数淡水鱼都有规则。
const gradeLadderBait = "bread"

// locForSpecies 找一张该鱼种在名册里的地图，并返回 level_* 场景 ID。
//
// 必须返回场景 ID：ResolveStatLoc 只认 level_ 前缀（store/repos_market.go:58），
// 传中文地名会解析成空串 → 判鱼走空池 → 全 0（踩过两次）。
func locForSpecies(t *testing.T, db *store.DB, key string) (alias, zh string) {
	t.Helper()
	rows, err := db.SQL.Query(`
		SELECT a.alias, a.loc, COUNT(f.fish_key) n
		FROM loc_fish f
		JOIN loc_alias a ON a.loc = f.loc
		WHERE f.fish_key = ? AND a.alias LIKE 'level_%'
		GROUP BY a.alias, a.loc
		ORDER BY n DESC, a.alias ASC LIMIT 1`, key)
	if err != nil {
		t.Fatalf("查名册: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var n int
		if err := rows.Scan(&alias, &zh, &n); err == nil {
			return alias, zh
		}
	}
	return "", ""
}

func TestPlaytestGradeLadder(t *testing.T) {
	playtestEnabled(t)
	db := playDB(t)
	srv := playServer(t, db)

	species := []string{"c.roach", "bream", "c.carp", "n.pike", "perch", "zander", "catfish", "beluga"}
	var out []gradeOutput
	for _, key := range species {
		alias, zh := locForSpecies(t, db, key)
		if alias == "" {
			t.Logf("%s 在 loc_fish 名册里找不到，跳过", key)
			continue
		}
		ctx := fishRollContext{cache: newFishingRollCache(), now: time.Now(), forRig: true, rigType: 12}
		lines := srv.cachedSettleStatics(context.Background(), ctx, key)
		g := gradeOutput{Species: key, Loc: zh, LocAlias: alias,
			Lines:      []int64{lines[0], lines[1], lines[2]},
			Counts:     map[string]int{}, WeightStat: map[string]float64{},
			XPMean:     map[string]float64{}, SilverMean: map[string]float64{}, SilverPerG: map[string]float64{}}
		byGrade := map[int64][]*RolledFish{}
		for i := 0; i < 800; i++ {
			c := ctx
			c.hasPos = true
			c.x, c.z = float64(i%50)*7, float64(i%37)*9
			c.loc = alias // 场景 ID，不是中文地名
			c.skill = neutralFishingSkillBonus()
			f := srv.rollFishWithRules(alias, gradeLadderBait, 0, neutralFishingSkillBonus(), c)
			if f == nil || f.Species != key {
				continue
			}
			byGrade[f.Grade] = append(byGrade[f.Grade], f)
			g.Counts[fmt.Sprintf("%d", f.Grade)]++
		}
		var wsum, wcount float64
		for grade, list := range byGrade {
			var ws, xps, svs, spg []float64
			for _, f := range list {
				ws = append(ws, float64(f.WeightG))
				xps = append(xps, float64(f.XP))
				svs = append(svs, float64(f.Silver))
				if f.WeightG > 0 {
					spg = append(spg, float64(f.Silver)/float64(f.WeightG))
				}
				wsum += float64(f.WeightG)
				wcount++
			}
			k := fmt.Sprintf("%d", grade)
			g.XPMean[k] = meanOf(xps)
			g.SilverMean[k] = meanOf(svs)
			g.SilverPerG[k] = meanOf(spg)
		}
		if wcount > 0 {
			g.WeightStat["mean"] = wsum / wcount
		}
		out = append(out, g)
		t.Logf("%-10s(%s) 线=%v 档位分布=%v 均重=%.0fg", key, zh, g.Lines, g.Counts, g.WeightStat["mean"])
	}
	playWrite(t, "../../data/playtest", "grade_ladder.json", out)
}

// ---------------------------------------------------------------------------
// 4. 完整作钓闭环：用真实 handler 走抛竿→入水→咬钩→中钩→收竿结算→入护
// ---------------------------------------------------------------------------

type flowStep struct {
	Step   string  `json:"step"`
	Detail string  `json:"detail"`
	Ms     float64 `json:"ms"`
}

type flowOutput struct {
	Label     string             `json:"label"`
	Loc       string             `json:"loc"`
	RodID     string             `json:"rod_item"`
	RigType   int64              `json:"rig_type"`
	Bait      string             `json:"bait"`
	Steps     []flowStep         `json:"steps"`
	BiteWait  float64            `json:"bite_wait_s"`
	Outcome   string             `json:"outcome"`
	Species   string             `json:"species"`
	Grade     int64              `json:"grade"`
	WeightG   int64              `json:"weight_g"`
	XP        int64              `json:"xp"`
	Silver    int64              `json:"silver"`
	KeepnetOK bool               `json:"keepnet_ok"`
	FishGUID  string             `json:"fish_guid"`
	Err       string             `json:"err,omitempty"`
	Probs     map[string]float64 `json:"probs,omitempty"`
}

// TestPlaytestFishingFlow 用真实 handler 做闭环。为了不等待真实 20 秒延迟，
// 把 bite_delay_fixed 设成 0.05s；判鱼链、档位、结算、入护全部走生产代码。
func TestPlaytestFishingFlow(t *testing.T) {
	playtestEnabled(t)
	db := playDB(t)
	srv := playServer(t, db)
	const uid = playUID
	sess := playSess(t, srv, uid)

	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0.05", "playtest"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	rods := pickPlayRods(t, db, uid)
	if len(rods) == 0 {
		t.Skip("快照库里没有装配完好的竿")
	}
	var out []flowOutput
	for _, rod := range rods {
		for _, loc := range []string{"loc1_02", "loc19_03"} {
			o := runOneFlow(t, srv, sess, uid, rod, loc)
			out = append(out, o)
			t.Logf("%-40s %-9s → %s %s %.0fg xp=%d 银=%d 等待%.2fs",
				o.Label, loc, o.Outcome, o.Species, float64(o.WeightG), o.XP, o.Silver, o.BiteWait)
		}
	}
	playWrite(t, "../../data/playtest", "fishing_flow.json", out)
}

type playRod struct {
	GUID    string
	ItemID  string
	RigType int64
	Bait    string
}

// pickPlayRods 挑出装配完整的竿：有钩、有饵、能定位到地图名册。
func pickPlayRods(t *testing.T, db *store.DB, uid int64) []playRod {
	t.Helper()
	ctx := context.Background()
	rows, err := db.SQL.Query(`
		SELECT r.guid, r.rig_type, i.item_id
		FROM rig r JOIN item i ON i.guid = r.guid
		WHERE r.uid = ? AND i.durability > 0
		ORDER BY r.rig_type, r.created_ts DESC`, uid)
	if err != nil {
		t.Fatalf("列出竿: %v", err)
	}
	defer rows.Close()
	var out []playRod
	seenRig := map[int64]bool{}
	for rows.Next() {
		var r playRod
		if err := rows.Scan(&r.GUID, &r.RigType, &r.ItemID); err != nil {
			t.Fatal(err)
		}
		if seenRig[r.RigType] {
			continue
		}
		seenRig[r.RigType] = true
		out = append(out, r)
	}
	// 只保留有饵的
	keep := out[:0]
	for _, r := range out {
		bait := srvBaitOf(ctx, db, r.GUID)
		if bait == "" {
			continue
		}
		r.Bait = bait
		keep = append(keep, r)
	}
	return keep
}

func srvBaitOf(ctx context.Context, db *store.DB, rodGUID string) string {
	rows, err := db.SQL.Query(`
		SELECT i.item_id FROM rig_component rc
		JOIN item i ON i.guid = rc.guid
		JOIN item_def d ON d.item_id = i.item_id
		WHERE rc.rig_guid = ? AND d.category IN (17, 27) LIMIT 1`, rodGUID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	if rows.Next() {
		var s string
		_ = rows.Scan(&s)
		return s
	}
	return ""
}

// runOneFlow 真实 handler 闭环。
func runOneFlow(t *testing.T, srv *Server, sess *Session, uid int64, rod playRod, loc string) flowOutput {
	t.Helper()
	now := func() float64 { return float64(time.Now().UnixNano()) / 1e6 }
	o := flowOutput{Label: fmt.Sprintf("rig%d/%s", rod.RigType, rod.ItemID), Loc: loc,
		RodID: rod.ItemID, RigType: rod.RigType, Bait: rod.Bait}

	st := srv.stateFor(uid)
	st.mu.Lock()
	st.GearGuid = rod.GUID
	st.RigType = rod.RigType
	st.Loc = loc
	st.DepthCM = 200
	st.CastPosX, st.CastPosZ, st.HasCastPos = 120, 260, true
	st.PosX, st.PosZ, st.HasPos = 120, 260, true
	st.mu.Unlock()

	// 14/1
	t0 := now()
	body1, _ := hCastPrepare(miscCtx(sess, 14, 1, playArgs(rod.GUID)))
	o.Steps = append(o.Steps, flowStep{"14/1 cast_prepare", fmt.Sprintf("%d bytes", len(body1)), now() - t0})

	// 14/2
	t0 = now()
	body2, _ := hCast(miscCtx(sess, 14, 2, playArgs(rod.GUID)))
	o.Steps = append(o.Steps, flowStep{"14/2 cast", fmt.Sprintf("%d bytes", len(body2)), now() - t0})

	// 14/7 入水（0x800 底部静止，覆盖底钓与 feeder）
	waterTree := playObjTree("lobemkloehp", map[int]string{6: loc}, 5, 0x800)
	t0 = now()
	_, _ = hFishingStep(miscCtx(sess, 14, 7, waterTree))
	o.Steps = append(o.Steps, flowStep{"14/7 in_water", "anim=0x800", now() - t0})

	// 把抛竿时刻回拨，跳过落水保护与 12 秒就位门
	st = srv.stateFor(uid)
	st.mu.Lock()
	st.CastTs = time.Now().Add(-30 * time.Second)
	st.FallTs = st.CastTs
	st.FallSeen = true
	st.RigSettled = true
	st.TickSeen = true
	st.mu.Unlock()

	// 等咬钩：直接驱动定时器对应的判鱼路径（不等真实 timer）
	waitStart := time.Now()
	var fish *RolledFish
	for i := 0; i < 400; i++ {
		if st := srv.stateFor(uid); st != nil {
			st.mu.Lock()
			p := st.Pending
			st.mu.Unlock()
			if p != nil {
				fish = p
				break
			}
		}
		// 主动触发一次到点判定（与 timer 到点同一条路径）
		st2 := srv.stateFor(uid)
		st2.mu.Lock()
		ticket := fishingTimerTicket{st2.castGeneration, st2.biteTicket}
		st2.mu.Unlock()
		srv.onRodBiteTimerTicket(uid, st2, ticket)
		time.Sleep(20 * time.Millisecond)
	}
	o.BiteWait = time.Since(waitStart).Seconds()

	if fish == nil {
		o.Outcome = "空竿（未派到鱼）"
		return o
	}
	o.FishGUID = fish.FishGUID
	o.Species = fish.Species
	o.Grade = fish.Grade
	o.WeightG = fish.WeightG
	o.Probs = map[string]float64{"bite_quality": 0, "hook_hold": fish.HookHoldP}

	// 14/11 中钩确认
	t0 = now()
	_, _ = hFishingHooked(miscCtx(sess, 14, 11, playArgs(rod.GUID, fish.FishGUID)))
	o.Steps = append(o.Steps, flowStep{"14/11 hooked", fish.BiteQuality, now() - t0})

	st = srv.stateFor(uid)
	st.mu.Lock()
	hooked := st.Hooked
	st.mu.Unlock()
	if !hooked {
		o.Outcome = "咬钩未刺中（脱钩）"
		return o
	}

	// 14/4 收竿结算
	t0 = now()
	body4, _ := hFishingEnd(miscCtx(sess, 14, 4, playArgs(rod.GUID, fish.FishGUID)))
	o.Steps = append(o.Steps, flowStep{"14/4 settle", fmt.Sprintf("%d bytes", len(body4)), now() - t0})

	st = srv.stateFor(uid)
	st.mu.Lock()
	pend := st.Pending
	if pend != nil {
		o.XP, o.Silver = pend.XP, pend.Silver
	}
	st.mu.Unlock()

	// 14/5 入护
	t0 = now()
	_, _ = hFishingKeep(miscCtx(sess, 14, 5, playArgs(rod.GUID, fish.FishGUID)))
	o.Steps = append(o.Steps, flowStep{"14/5 keep", "", now() - t0})

	// 验证入护
	var cnt int
	_ = srv.DB.SQL.QueryRow(`SELECT COUNT(*) FROM keepnet_items WHERE uid=? AND fish_guid=?`, uid, fish.FishGUID).Scan(&cnt)
	o.KeepnetOK = cnt > 0
	if o.KeepnetOK {
		o.Outcome = "成功入护"
	} else {
		o.Outcome = "结算后未入护"
	}
	return o
}

func playArgs(guids ...string) *proto.Value {
	return proto.Args524(proto.GuidArr(guids), proto.U32(0))
}

func playObjTree(class string, strs map[int]string, u32idx int, u32val uint64) *proto.Value {
	fields := make([]proto.Field, 8)
	for i, v := range strs {
		fields[i] = proto.Field{Op: "STR", Str: &v}
	}
	fields[u32idx] = proto.Field{Op: "U32", Num: &u32val}
	return proto.Args524(proto.Obj(class, 0, fields...))
}

var _ = strings.TrimSpace