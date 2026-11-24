package gameserver

import (
	"context"
	"testing"
	"time"

	"rf4server/internal/store"
)

// 覆盖可以整组替换内置规则；删掉覆盖后回到内置规则。
func TestBaitOverrideReplacesDefaultAndResets(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	setBaitRuleSwitch(t, s, "1")
	ctx := context.Background()
	keys := []string{"c.roach"}
	at := time.Unix(1_800_000_000, 0)
	roll := func(bait string) ([]string, []float64) {
		return s.filterFishBaitSet("rules-test", []string{bait}, keys, []float64{100}, at)
	}
	// 内置规则：常见拟鲤 不吃拟饵。
	if got, _ := roll("rule_lure"); len(got) != 0 {
		t.Fatalf("内置规则应拒绝拟饵，实得 %v", got)
	}
	// 覆盖成允许 Hunter 1-001（拟饵类名）。主键是 fish_key（00083），
	// FishZh 只用来建「按名字兜底」的第二索引。
	ov := store.FishBaitOverride{FishKey: "c.roach", FishZh: "常见拟鲤", Allow: []string{"Hunter 1-001"}, Enabled: true}
	if err := db.SaveFishBaitOverride(ctx, ov, "test"); err != nil {
		t.Fatal(err)
	}
	s.invalidateBaitOverrides()
	if got, _ := roll("rule_lure"); len(got) != 1 {
		t.Fatalf("覆盖后应允许 Hunter 1-001，实得 %v", got)
	}
	// 恢复默认。
	if _, err := db.DeleteFishBaitOverride(ctx, "c.roach"); err != nil {
		t.Fatal(err)
	}
	s.invalidateBaitOverrides()
	if got, _ := roll("rule_lure"); len(got) != 0 {
		t.Fatalf("恢复默认后应再次拒绝拟饵，实得 %v", got)
	}
}
