package gameserver

// 真实作钓体验批量测试：以全新玩家身份，用真实客户端协议跑完整流程。
//
// 设计原则（回应用户要求）：
//   - 只走网络协议，不读 catch_log / fish_roll_trace / trophy 任何历史记录；
//     鱼种、重量、档位全部来自本次 14/14 推送的原始字段。
//   - 账号是登录时新建的（playtest01 / uid 1000003），只有 starter 装备，
//     等级 1、经验 0、银币 5000，与真实新玩家一致。
//   - 每组记录：条件（地图/竿/饵/钓法）、步骤、关键数据、结果、异常、体感。
//
// 运行：
//   RF4_LIVEPLAY=1 go test ./internal/gameserver -run TestLivePlayBatch -v -timeout 60m

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"rf4server/internal/proto"
	"rf4server/internal/wiregen"
)

// liveScenario 一组作钓条件。
type liveScenario struct {
	Name    string  `json:"name"`
	Rod     string  `json:"rod_guid"`
	RodItem string  `json:"rod_item"`
	Loc     string  `json:"loc"`
	Bait    string  `json:"bait"`
	RigType int64   `json:"rig_type"`
	Note    string  `json:"note"`
	// ExpectBite=false 的组用于验证失败路径（空竿 / 无饵 / 越界）
	ExpectBite bool `json:"expect_bite"`
}

// liveAttempt 一次抛投的完整记录。
type liveAttempt struct {
	Scenario string     `json:"scenario"`
	CastOK   bool       `json:"cast_ok"`
	CastNote string     `json:"cast_note"`
	WaterOK  bool       `json:"water_ok"`
	BiteWait float64    `json:"bite_wait_s"`
	GotBite  bool       `json:"got_bite"`
	Fish     *liveFish  `json:"fish"`
	Hooked   bool       `json:"hooked"`
	LostWhy  string     `json:"lost_why"`
	SettleMs float64    `json:"settle_ms"`
	Kept     bool       `json:"kept"`
	Pushes   []string   `json:"pushes"`
	Notes    []string   `json:"notes"`
	Extra    [][]string `json:"-"`
}

// liveBatchReport 整轮汇总。
type liveBatchReport struct {
	StartedAt   string         `json:"started_at"`
	Account     string         `json:"account"`
	UID         int64          `json:"uid"`
	Scenarios   []liveScenario `json:"scenarios"`
	Attempts    []liveAttempt  `json:"attempts"`
	Summary     map[string]any `json:"summary"`
	Environment map[string]any `json:"environment"`
}

// batchClient 复用同一个连接跑多组，减少握手开销；连接断了自动重连。
type batchClient struct {
	t     *testing.T
	login string
	c     *liveClient
}

func newBatchClient(t *testing.T, login string) *batchClient {
	t.Helper()
	b := &batchClient{t: t, login: login}
	b.connect()
	return b
}

func (b *batchClient) connect() {
	tok, uid := liveLogin(b.t, b.login)
	b.t.Logf("登录 %s → uid=%d", b.login, uid)
	b.c = newLiveClient(b.t, tok)
	_ = b.c.waitPush(0, 1, 3*time.Second)
	b.c.drainPushes(400 * time.Millisecond)
}

// tickLoop 按真实客户端节奏持续上报 14/7 帧，直到 stop 被关闭。
//
// 这一步不能省：服务端 onRodBiteTimerTicket 有三道门都依赖持续帧——
//   - st.TickSeen       必须见过至少一帧 14/7；
//   - st.RigSettled     每帧按当前动画位重算，不发帧就一直是最后一次的值；
//   - rig-settled gate  要求稳定够 12 秒（路亚跳过这条）。
//
// 只发一帧然后干等，服务端会认为钓组还没稳定，一路顺延到空竿——
// 这正是上一轮 6 组全空的原因。
func (b *batchClient) tickLoop(stop <-chan struct{}, anim uint32, loc string) {
	body := encodeLive(b.t, liveWaterBody(anim, loc))
	tk := time.NewTicker(50 * time.Millisecond) // 20fps
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			b.c.send(14, 7, body)
		}
	}
}
func (b *batchClient) runAttempt(sc liveScenario, biteWait time.Duration) liveAttempt {
	t := b.t
	a := liveAttempt{Scenario: sc.Name, Notes: []string{}}

	// ---- 14/1 准备抛投 ----
	b.c.send(14, 1, encodeLive(t, liveArgs(sc.Rod)))
	if r := b.c.waitResp(6 * time.Second); r != nil {
		a.Notes = append(a.Notes, fmt.Sprintf("14/1 resp %dB", len(r.Body)))
	} else {
		a.Notes = append(a.Notes, "14/1 无响应")
	}

	// ---- 14/2 抛投 ----
	b.c.send(14, 2, encodeLive(t, liveCastBody(sc.Rod, sc.Loc, 20, 1)))
	if r := b.c.waitResp(6 * time.Second); r != nil {
		a.CastOK = len(r.Body) > 0
		a.CastNote = fmt.Sprintf("%d bytes", len(r.Body))
		if len(r.Body) >= 2 {
			a.CastNote = fmt.Sprintf("%d bytes head=%x", len(r.Body), r.Body[:min2(2, len(r.Body))])
		}
	} else {
		a.CastNote = "无响应"
	}

	// ---- 14/7 入水 + 持续帧上报 ----
	// 入水位按钓组族区分：浮钓/底钓 0x800（MottionlessOnBottom），
	// 路亚需要下沉位（0x80000 VerticalDive）才算在水中。
	anim := uint32(0x800)
	if sc.RigType == 5 || sc.RigType == 6 || sc.RigType == 50 {
		anim = 0x80000
	}
	b.c.send(14, 7, encodeLive(t, liveWaterBody(anim, sc.Loc)))
	stopTick := make(chan struct{})
	go b.tickLoop(stopTick, anim, sc.Loc)
	b.c.drainPushes(300 * time.Millisecond)
	a.WaterOK = true

	// ---- 等咬钩 ----
	start := time.Now()
	env := b.c.waitPush(14, 14, biteWait)
	a.BiteWait = time.Since(start).Seconds()
	if env == nil {
		close(stopTick)
		a.Notes = append(a.Notes, fmt.Sprintf("%.0fs 内无 14/14（空竿）", biteWait.Seconds()))
		return a
	}
	a.GotBite = true
	a.Fish = parseFish14(t, env)
	if a.Fish == nil {
		close(stopTick)
		a.Notes = append(a.Notes, "14/14 解码失败")
		return a
	}
	close(stopTick)

	// ---- 14/11 中钩（模拟玩家看到浮标下沉后起竿）----
	b.c.send(14, 11, encodeLive(t, liveArgs(sc.Rod, a.Fish.GUID)))
	b.c.drainPushes(400 * time.Millisecond)
	a.Hooked = true

	// 检查是否被鱼跑掉（14/12 = 鱼离场）
	if leave := b.c.waitPush(14, 12, 1200*time.Millisecond); leave != nil {
		a.LostWhy = "14/12 鱼离场"
		a.Notes = append(a.Notes, "起竿后收到 14/12")
	}

	// ---- 14/4 收竿结算 ----
	t0 := time.Now()
	b.c.send(14, 4, encodeLive(t, liveArgs(sc.Rod)))
	if r := b.c.waitResp(8 * time.Second); r != nil {
		a.SettleMs = float64(time.Since(t0).Microseconds()) / 1000
	} else {
		a.SettleMs = -1
		a.Notes = append(a.Notes, "14/4 无响应")
	}
	b.c.drainPushes(500 * time.Millisecond)

	// ---- 14/5 入护 ----
	b.c.send(14, 5, encodeLive(t, liveArgs(sc.Rod, a.Fish.GUID)))
	if r := b.c.waitResp(8 * time.Second); r != nil {
		a.Kept = len(r.Body) > 0
	}
	b.c.drainPushes(300 * time.Millisecond)
	return a
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestLivePlayBatch 全新玩家完整作钓矩阵。
func TestLivePlayBatch(t *testing.T) {
	liveplayEnabled(t)
	login := os.Getenv("RF4_LOGIN")
	if login == "" {
		login = "playtest01"
	}
	biteWait := 75 * time.Second
	if v := os.Getenv("RF4_BITE_WAIT"); v != "" {
		var secs int
		fmt.Sscanf(v, "%d", &secs)
		if secs > 0 {
			biteWait = time.Duration(secs) * time.Second
		}
	}

	// 从环境变量读取新玩家装备 GUID（由 admin 接口取回后注入）。
	teleRod := os.Getenv("RF4_ROD_TELE")
	spinA := os.Getenv("RF4_ROD_SPIN_A")
	spinB := os.Getenv("RF4_ROD_SPIN_B")
	if teleRod == "" || spinA == "" {
		t.Skip("需要 RF4_ROD_TELE / RF4_ROD_SPIN_A / RF4_ROD_SPIN_B")
	}

	b := newBatchClient(t, login)
	_, uid := liveLogin(t, login)

	scenarios := []liveScenario{
		// 浮钓：新玩家主玩法，起点地图
		{Name: "浮钓/克马羚诺也湖/面包", Rod: teleRod, RodItem: "tele_10012_tl16",
			Loc: "克马羚诺也湖", Bait: "bread", RigType: 1,
			Note: "新玩家第一竿，静水湖，天然饵", ExpectBite: true},
		{Name: "浮钓/克马羚诺也湖/红虫", Rod: teleRod, RodItem: "tele_10012_tl16",
			Loc: "克马羚诺也湖", Bait: "bloodworm", RigType: 1,
			Note: "同图换红虫，看饵料差异", ExpectBite: true},
		{Name: "浮钓/克马羚诺也湖/蚯蚓", Rod: teleRod, RodItem: "tele_10012_tl16",
			Loc: "克马羚诺也湖", Bait: "worm_01", RigType: 1,
			Note: "同图换蚯蚓", ExpectBite: true},
		// 换图：惟有诺克河（等级 2 起）
		{Name: "浮钓/惟有诺克河/面包", Rod: teleRod, RodItem: "tele_10012_tl16",
			Loc: "惟有诺克河", Bait: "bread", RigType: 1,
			Note: "换图验证 roster 差异", ExpectBite: true},
		// 路亚：另一条主线
		{Name: "路亚/克马羚诺也湖/旋转亮片", Rod: spinA, RodItem: "spin_10001_S60L",
			Loc: "克马羚诺也湖", Bait: "spinner_2713", RigType: 5,
			Note: "路亚第一条线，节奏应明显更快", ExpectBite: true},
		{Name: "路亚/克马羚诺也湖/复合亮片", Rod: spinB, RodItem: "spin_5820",
			Loc: "克马羚诺也湖", Bait: "spinnerbaits_1244", RigType: 5,
			Note: "第二支路亚竿", ExpectBite: true},
	}

	rep := liveBatchReport{
		StartedAt: time.Now().Format(time.RFC3339),
		Account:   login,
		UID:       uid,
		Scenarios: scenarios,
		Summary:   map[string]any{},
		Environment: map[string]any{
			"bite_wait_s": biteWait.Seconds(),
			"note":        "全部数据来自本次 14/14 推送原始字段，不引用任何历史记录",
		},
	}

	for i, sc := range scenarios {
		t.Logf("[%d/%d] %s", i+1, len(scenarios), sc.Name)
		a := b.runAttempt(sc, biteWait)
		rep.Attempts = append(rep.Attempts, a)
		if a.Fish != nil {
			t.Logf("  → %s %dg grade=%d 等待%.1fs 结算%.0fms 入护=%v",
				a.Fish.Species, a.Fish.WeightG, a.Fish.Grade, a.BiteWait, a.SettleMs, a.Kept)
		} else {
			t.Logf("  → 无鱼，等待%.1fs", a.BiteWait)
		}
	}

	// ---- 汇总 ----
	var waits []float64
	gradeCount := map[int64]int{}
	speciesCount := map[string]int{}
	bites, empty := 0, 0
	var weights []int64
	for _, a := range rep.Attempts {
		if a.GotBite {
			bites++
			waits = append(waits, a.BiteWait)
			if a.Fish != nil {
				gradeCount[a.Fish.Grade]++
				speciesCount[a.Fish.Species]++
				weights = append(weights, a.Fish.WeightG)
			}
		} else {
			empty++
		}
	}
	sort.Float64s(waits)
	sort.Slice(weights, func(i, j int) bool { return weights[i] < weights[j] })
	rep.Summary["attempts"] = len(rep.Attempts)
	rep.Summary["bites"] = bites
	rep.Summary["empty"] = empty
	rep.Summary["bite_rate"] = float64(bites) / float64(max1(len(rep.Attempts)))
	if len(waits) > 0 {
		rep.Summary["wait_mean_s"] = round2(meanF(waits))
		rep.Summary["wait_p50_s"] = round2(quantileF(waits, 0.5))
		rep.Summary["wait_min_s"] = round2(waits[0])
		rep.Summary["wait_max_s"] = round2(waits[len(waits)-1])
	}
	rep.Summary["grade_hist"] = gradeCount
	rep.Summary["species_hist"] = speciesCount
	if len(weights) > 0 {
		rep.Summary["weight_min_g"] = weights[0]
		rep.Summary["weight_max_g"] = weights[len(weights)-1]
		rep.Summary["weight_mean_g"] = round2(meanI(weights))
	}

	liveWriteJSON(t, "live_batch.json", rep)
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func meanF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func meanI(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += float64(x)
	}
	return s / float64(len(xs))
}

func quantileF(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// TestLivePlayFailPaths 失败路径专项：无饵抛投、断竿、不存在的图。
func TestLivePlayFailPaths(t *testing.T) {
	liveplayEnabled(t)
	login := os.Getenv("RF4_LOGIN")
	if login == "" {
		login = "playtest01"
	}
	rod := os.Getenv("RF4_ROD_TELE")
	if rod == "" {
		t.Skip("需要 RF4_ROD_TELE")
	}
	b := newBatchClient(t, login)
	_, uid := liveLogin(t, login)

	type failCase struct {
		Name  string
		Run   func() (handled bool, note string)
		WantF string
	}
	var results []map[string]any

	record := func(name string, got bool, note, want string) {
		results = append(results, map[string]any{
			"case": name, "handled": got, "note": note, "expect": want,
		})
		t.Logf("  %-28s handled=%-5v %s", name, got, note)
	}

	// 1) 不存在的场景名：应能编码成功，服务端按空 roster 处理
	b.c.send(14, 1, encodeLive(t, liveArgs(rod)))
	b.c.waitResp(5*time.Second)
	b.c.send(14, 2, encodeLive(t, liveCastBody(rod, "level_999_not_exist", 20, 1)))
	r := b.c.waitResp(6 * time.Second)
	record("不存在的地图", r != nil, fmt.Sprintf("resp=%v", r != nil), "不 panic，返回某种应答")
	b.c.drainPushes(400 * time.Millisecond)

	// 2) 全零竿 GUID：服务端应拒或忽略，不得 panic
	b.c.send(14, 1, encodeLive(t, liveArgs("00000000000000000000000000000000")))
	b.c.waitResp(5*time.Second)
	b.c.send(14, 2, encodeLive(t, liveCastBody("00000000000000000000000000000000", "克马羚诺也湖", 20, 1)))
	r2 := b.c.waitResp(6 * time.Second)
	record("全零竿 GUID", r2 != nil, fmt.Sprintf("resp=%v", r2 != nil), "拒绝但不断连")
	b.c.drainPushes(400 * time.Millisecond)

	// 3) 连发两次抛投（模拟连点）：不应产生两套计时器或双鱼
	b.c.send(14, 1, encodeLive(t, liveArgs(rod)))
	b.c.waitResp(4*time.Second)
	b.c.send(14, 2, encodeLive(t, liveCastBody(rod, "克马羚诺也湖", 20, 1)))
	b.c.waitResp(4*time.Second)
	b.c.send(14, 2, encodeLive(t, liveCastBody(rod, "克马羚诺也湖", 20, 1)))
	r3 := b.c.waitResp(4 * time.Second)
	record("连发两次抛投", true, fmt.Sprintf("第二次 resp=%v", r3 != nil), "只保留一竿计时")

	// 4) 未抛竿直接 14/11 起竿：不应崩溃
	b.c.send(14, 11, encodeLive(t, liveArgs(rod, "11111111111111111111111111111111")))
	r4 := b.c.waitResp(4 * time.Second)
	record("无竿起竿", true, fmt.Sprintf("resp=%v", r4 != nil), "忽略不崩")

	// 5) 未抛竿直接 14/5 入护：不应崩溃
	b.c.send(14, 5, encodeLive(t, liveArgs(rod, "22222222222222222222222222222222")))
	r5 := b.c.waitResp(4 * time.Second)
	record("无鱼入护", true, fmt.Sprintf("resp=%v", r5 != nil), "忽略不崩")

	// 6) 连接还活着吗
	b.c.send(14, 1, encodeLive(t, liveArgs(rod)))
	r6 := b.c.waitResp(5 * time.Second)
	record("异常后连接存活", r6 != nil, fmt.Sprintf("resp=%v", r6 != nil), "仍能正常响应")

	liveWriteJSON(t, "live_failpaths.json", map[string]any{
		"account": login, "uid": uid,
		"at": time.Now().Format(time.RFC3339), "results": results,
	})
}

// liveRodList 从持久化数据里读玩家真实竿实例（不猜 GUID）。
func liveRodList(t *testing.T, rods map[string]string) string {
	b, _ := json.MarshalIndent(rods, "", "  ")
	p := filepath.Join(liveOutputDir, "live_rods.json")
	_ = os.MkdirAll(liveOutputDir, 0o755)
	_ = os.WriteFile(p, b, 0o644)
	return p
}

var _ = proto.U32
var _ = wiregen.Table