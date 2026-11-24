package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFishBaitOverrideRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "bait-override.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	want := FishBaitOverride{
		FishKey:    "c.roach",
		FishZh:     "常见拟鲤",
		Allow:      []string{"蚯蚓", "红虫"},
		Deny:       []string{"拟饵"},
		BottomOnly: []string{"蚯蚓"},
		Condition:  "出现 jig 阶梯",
		Enabled:    true,
	}
	if err := db.SaveFishBaitOverride(ctx, want, "tester"); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListFishBaitOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("rows=%d", len(list))
	}
	got := list[0]
	if got.FishKey != want.FishKey || got.FishZh != want.FishZh || got.Condition != want.Condition {
		t.Fatalf("scalar mismatch: %+v", got)
	}
	if !got.Enabled || got.UpdatedBy != "tester" {
		t.Fatalf("meta mismatch: %+v", got)
	}
	if len(got.Allow) != 2 || got.Allow[0] != "蚯蚓" || got.Allow[1] != "红虫" {
		t.Fatalf("allow=%v", got.Allow)
	}

	// 覆盖写入：同一个 fish_key 再次保存应更新而不是新增。
	want.Allow = []string{"玉米"}
	if err := db.SaveFishBaitOverride(ctx, want, "tester2"); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListFishBaitOverrides(ctx)
	if len(list) != 1 || len(list[0].Allow) != 1 || list[0].Allow[0] != "玉米" {
		t.Fatalf("upsert mismatch: %+v", list)
	}

	// 删除即恢复默认。
	if n, err := db.DeleteFishBaitOverride(ctx, "c.roach"); err != nil || n != 1 {
		t.Fatalf("delete n=%d err=%v", n, err)
	}
	if list, _ = db.ListFishBaitOverrides(ctx); len(list) != 0 {
		t.Fatalf("rows after delete=%d", len(list))
	}

	// 整表清空。
	for _, key := range []string{"a", "b", "c"} {
		o := FishBaitOverride{FishKey: key, FishZh: key, Allow: []string{"蚯蚓"}, Enabled: true}
		if err := db.SaveFishBaitOverride(ctx, o, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.DeleteAllFishBaitOverrides(ctx); err != nil || n != 3 {
		t.Fatalf("delete all n=%d err=%v", n, err)
	}
	if list, _ = db.ListFishBaitOverrides(ctx); len(list) != 0 {
		t.Fatalf("rows after delete all=%d", len(list))
	}
}

func TestFishBaitOverrideSanitizesInput(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "bait-override-sanitize.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 空串应被清理，避免脏数据进判鱼路径。
	o := FishBaitOverride{
		FishKey: "frog",
		FishZh:  "青蛙",
		Allow:   []string{"", "  ", "青蛙"},
		Enabled: false,
	}
	if err := db.SaveFishBaitOverride(ctx, o, "tester"); err != nil {
		t.Fatal(err)
	}
	list, _ := db.ListFishBaitOverrides(ctx)
	if len(list) != 1 {
		t.Fatalf("rows=%d", len(list))
	}
	got := list[0]
	if len(got.Allow) != 1 || got.Allow[0] != "青蛙" {
		t.Fatalf("allow not cleaned: %v", got.Allow)
	}
	if got.Enabled {
		t.Fatalf("enabled should stay false: %+v", got)
	}
}
