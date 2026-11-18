package gameserver

import (
	"context"
	"encoding/json"
	"sort"

	"rf4server/internal/store"
)

// 判鱼决策追踪：把「为什么空军 / 为什么是这条鱼」变成可读的一行。
//
// 判鱼权重在这之前由约九层依次相乘决定，结果只落一行「空因」日志。本文件在每层之后
// 打一个权重快照，中鱼/空轮时由真实咬钩路径（onBiteTimer）落一行 fish_roll_trace。
// 纯诊断：不参与任何判鱼数值，不推客户端，开关关闭时连快照都不建。

// tunables：均为纯诊断开关，不参与判鱼。
const (
	tunableFishRollTraceEnabled = "fish_roll_trace_enabled"
	tunableFishRollTraceKeep    = "fish_roll_trace_keep"

	defFishRollTraceKeep = 300
	// fishRollTraceTopN 落库只保留最终权重最高的前 N 个候选，避免 payload 随名册膨胀。
	fishRollTraceTopN = 5
)

// 层名与判鱼主链顺序一致（rollFishFromDBWithRules）。
// rollStagePool 是静态名册不可用时的内置鱼池兜底路径，单列一层，
// 免得「这杆走了兜底」和「名册里就这些鱼」在追踪里看起来一样。
const (
	rollStageRoster     = "roster"
	rollStagePool       = "pool"
	rollStageGate       = "gate"
	rollStageZone       = "zone"
	rollStageBait       = "bait"
	rollStageDist       = "dist"
	rollStageEcology    = "ecology"
	rollStageSpot       = "spot"
	rollStageGroundbait = "groundbait"
	rollStageBaitTier   = "bait_tier"
	rollStageFinal      = "final"
)

type rollTraceStage struct {
	name string
	w    map[string]float64
}

// fishRollTrace 一次判鱼的权重快照。nil 表示不采。
// 挂在 fishRollContext 上随值拷贝传递，指针保证各层写入同一个对象。
type fishRollTrace struct {
	stages []rollTraceStage
	order  []string
	seen   map[string]bool
}

func newFishRollTrace() *fishRollTrace {
	return &fishRollTrace{seen: map[string]bool{}}
}

// step 记录某一层处理之后的候选与权重。keys/wts 长度不一致时按较短者截断，
// 不 panic——判鱼路径上任何异常都不该把一杆打断。
func (t *fishRollTrace) step(name string, keys []string, wts []float64) {
	if t == nil {
		return
	}
	n := len(keys)
	if len(wts) < n {
		n = len(wts)
	}
	m := make(map[string]float64, n)
	for i := 0; i < n; i++ {
		key := keys[i]
		m[key] = wts[i]
		if !t.seen[key] {
			t.seen[key] = true
			t.order = append(t.order, key)
		}
	}
	t.stages = append(t.stages, rollTraceStage{name: name, w: m})
}

type rollTraceCandidate struct {
	Key string    `json:"key"`
	W   []float64 `json:"w"`
}

type rollTracePayload struct {
	Stages     []string             `json:"stages"`
	Candidates []rollTraceCandidate `json:"candidates"`
}

// payload 输出落库 JSON 与抽签前候选权重总和。
// 总和取截断前的全部候选，用于判断「这杆是不是权重总量本身就极低」。
func (t *fishRollTrace) payload() (string, float64) {
	if t == nil || len(t.stages) == 0 {
		return "{}", 0
	}
	names := make([]string, len(t.stages))
	for i, s := range t.stages {
		names[i] = s.name
	}
	last := t.stages[len(t.stages)-1].w
	type cand struct {
		key   string
		final float64
		w     []float64
	}
	cands := make([]cand, 0, len(t.order))
	total := 0.0
	for _, key := range t.order {
		final := last[key]
		total += final
		w := make([]float64, len(t.stages))
		for i, s := range t.stages {
			w[i] = s.w[key]
		}
		cands = append(cands, cand{key: key, final: final, w: w})
	}
	// 并列时按 key 排序，避免 map 遍历顺序让同一份快照每次展示不同。
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].final != cands[j].final {
			return cands[i].final > cands[j].final
		}
		return cands[i].key < cands[j].key
	})
	if len(cands) > fishRollTraceTopN {
		cands = cands[:fishRollTraceTopN]
	}
	out := rollTracePayload{Stages: names, Candidates: make([]rollTraceCandidate, 0, len(cands))}
	for _, c := range cands {
		out.Candidates = append(out.Candidates, rollTraceCandidate{Key: c.key, W: c.w})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "{}", total
	}
	return string(b), total
}

func (s *Server) fishRollTraceEnabled() bool {
	if s == nil {
		return false
	}
	return s.tunableF(tunableFishRollTraceEnabled, 1) >= 0.5
}

// newRollTrace 只在开关打开时分配快照，关闭时判鱼路径零额外开销。
func (s *Server) newRollTrace() *fishRollTrace {
	if !s.fishRollTraceEnabled() {
		return nil
	}
	return newFishRollTrace()
}

type fishRollTraceInput struct {
	UID         int64
	Loc         string
	Spot        uint64
	X, Z        float64
	RigType     int64
	Bait        string
	EmptyReason string
	Fish        *RolledFish
	Trace       *fishRollTrace
}

// recordFishRollTrace 落一条追踪并按保留条数环切。
// 唯一调用点是真实咬钩（onBiteTimer），查鱼/区域估算不落库。
func (s *Server) recordFishRollTrace(ctx context.Context, in fishRollTraceInput) {
	if s == nil || s.DB == nil || !s.fishRollTraceEnabled() {
		return
	}
	if in.Trace == nil || len(in.Trace.stages) == 0 {
		return
	}
	keep := int(s.tunableF(tunableFishRollTraceKeep, defFishRollTraceKeep))
	if keep <= 0 {
		return
	}
	payload, total := in.Trace.payload()
	row := store.FishRollTrace{
		UID: in.UID, Loc: in.Loc, Spot: int64(in.Spot), X: in.X, Z: in.Z,
		RigType: in.RigType, Bait: in.Bait, EmptyReason: in.EmptyReason,
		TotalWeight: total, Payload: payload,
	}
	if in.Fish != nil {
		row.Hit = true
		row.Chosen = in.Fish.Species
		row.WeightG = in.Fish.WeightG
	}
	if err := s.DB.InsertFishRollTrace(ctx, row, keep); err != nil {
		s.log.Warn("判鱼追踪写入失败", "err", err)
	}
}
