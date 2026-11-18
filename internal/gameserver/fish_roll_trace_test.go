package gameserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// 快照必须按层对齐：被某层剔除的鱼种在后续层记 0（而不是消失），
// 抽签前总和取截断前的全部候选——否则「权重总量极低」和「候选被筛空」
// 在后台看起来一模一样。
func TestFishRollTracePayloadAlignsStagesAndRanksCandidates(t *testing.T) {
	tr := newFishRollTrace()
	tr.step(rollStageRoster, []string{"a", "b", "c"}, []float64{10, 5, 1})
	tr.step(rollStageEcology, []string{"a", "b"}, []float64{2, 5})
	tr.step(rollStageFinal, []string{"a", "b"}, []float64{2, 5})

	raw, total := tr.payload()
	if total != 7 {
		t.Fatalf("抽签前权重总和=%v，期望 7（c 已被 ecology 剔除，记 0）", total)
	}
	var out rollTracePayload
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("payload 不是合法 JSON: %v (%s)", err, raw)
	}
	if len(out.Stages) != 3 || out.Stages[0] != rollStageRoster || out.Stages[2] != rollStageFinal {
		t.Fatalf("层序=%v，期望 roster→ecology→final", out.Stages)
	}
	if len(out.Candidates) != 3 {
		t.Fatalf("候选数=%d，期望 3（被剔除的 c 也要留痕）", len(out.Candidates))
	}
	// 按最终权重降序：b(5) → a(2) → c(0)
	if out.Candidates[0].Key != "b" || out.Candidates[1].Key != "a" || out.Candidates[2].Key != "c" {
		t.Fatalf("候选顺序=%v，期望 b,a,c", []string{out.Candidates[0].Key, out.Candidates[1].Key, out.Candidates[2].Key})
	}
	// b: roster 5 → ecology 5 → final 5；a: 10 → 2 → 2；c: 1 → 0 → 0
	wantB := []float64{5, 5, 5}
	wantA := []float64{10, 2, 2}
	wantC := []float64{1, 0, 0}
	for i, want := range wantB {
		if out.Candidates[0].W[i] != want {
			t.Fatalf("b 第 %d 层权重=%v，期望 %v（整行 %v）", i, out.Candidates[0].W[i], want, out.Candidates[0].W)
		}
	}
	for i, want := range wantA {
		if out.Candidates[1].W[i] != want {
			t.Fatalf("a 第 %d 层权重=%v，期望 %v（整行 %v）", i, out.Candidates[1].W[i], want, out.Candidates[1].W)
		}
	}
	for i, want := range wantC {
		if out.Candidates[2].W[i] != want {
			t.Fatalf("c 第 %d 层权重=%v，期望 %v（整行 %v）", i, out.Candidates[2].W[i], want, out.Candidates[2].W)
		}
	}
}

// 并列权重必须稳定排序：同一份快照多次展示顺序一致，不随 map 遍历抖动。
// 同时验证只保留前 5 个候选。
func TestFishRollTracePayloadStableTieBreakAndTopFive(t *testing.T) {
	tr := newFishRollTrace()
	keys := []string{"k9", "k3", "k7", "k1", "k5", "k2", "k8", "k4"}
	wts := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	tr.step(rollStageFinal, keys, wts)

	raw, total := tr.payload()
	if total != 8 {
		t.Fatalf("总和=%v，期望 8", total)
	}
	var first rollTracePayload
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Candidates) != fishRollTraceTopN {
		t.Fatalf("候选数=%d，期望只留 %d", len(first.Candidates), fishRollTraceTopN)
	}
	want := []string{"k1", "k2", "k3", "k4", "k5"}
	for i, key := range want {
		if first.Candidates[i].Key != key {
			t.Fatalf("并列应按 key 升序取前 5，实得 %v", candidateKeys(first))
		}
	}
	for i := 0; i < 5; i++ {
		raw2, _ := tr.payload()
		var again rollTracePayload
		if err := json.Unmarshal([]byte(raw2), &again); err != nil {
			t.Fatal(err)
		}
		if again.Candidates[0].Key != first.Candidates[0].Key {
			t.Fatalf("同一份快照两次输出顺序不一致: %s vs %s", first.Candidates[0].Key, again.Candidates[0].Key)
		}
	}
}

func candidateKeys(p rollTracePayload) []string {
	out := make([]string, 0, len(p.Candidates))
	for _, c := range p.Candidates {
		out = append(out, c.Key)
	}
	return out
}

// nil 快照（开关关闭）全程安全：判鱼路径上不能因为没采追踪而 panic。
func TestFishRollTraceNilSafe(t *testing.T) {
	var tr *fishRollTrace
	tr.step(rollStageFinal, []string{"a"}, []float64{1})
	if raw, total := tr.payload(); raw != "{}" || total != 0 {
		t.Fatalf("nil 快照应输出空 payload，实得 %q %v", raw, total)
	}
}

// 落库受开关与保留条数控制：关闭时一行不写；keep=3 时环形保留最新 3 条。
func TestRecordFishRollTraceHonorsTunables(t *testing.T) {
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	tr := newFishRollTrace()
	tr.step(rollStageRoster, []string{"n.pike"}, []float64{2})
	tr.step(rollStageFinal, []string{"n.pike"}, []float64{2})

	write := func() {
		srv.recordFishRollTrace(ctx, fishRollTraceInput{
			UID: 1000001, Loc: "克马羚诺也湖", Spot: 3, X: 100, Z: 200, RigType: 12,
			Bait: "bait_1", Trace: tr, Fish: &RolledFish{Species: "n.pike", WeightG: 1200},
		})
	}
	count := func() int {
		var n int
		if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM fish_roll_trace`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	srv.tunCache = map[string]float64{tunableFishRollTraceEnabled: 0}
	srv.tunAt = time.Now()
	write()
	if got := count(); got != 0 {
		t.Fatalf("关闭追踪仍写入 %d 行", got)
	}

	srv.tunCache = map[string]float64{tunableFishRollTraceEnabled: 1, tunableFishRollTraceKeep: 3}
	srv.tunAt = time.Now()
	for i := 0; i < 5; i++ {
		write()
	}
	if got := count(); got != 3 {
		t.Fatalf("keep=3 时保留 %d 行，期望 3", got)
	}

	rows, err := db.ListFishRollTrace(ctx, "克马羚诺也湖", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("列表返回 %d 行，期望 3", len(rows))
	}
	if !rows[0].Hit || rows[0].Chosen != "n.pike" || rows[0].WeightG != 1200 {
		t.Fatalf("最新一行字段不符: %+v", rows[0])
	}
	if rows[0].TotalWeight != 2 {
		t.Fatalf("抽签前总和=%v，期望 2", rows[0].TotalWeight)
	}
}
