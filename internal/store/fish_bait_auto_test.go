package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openBaitAutoTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "bait-auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// 自动轮换开关必须与规则覆盖解耦：打开开关不能给鱼创建覆盖行，
// 否则内置规则会被固化，再也跟不上规则表更新。
func TestFishBaitAutoDoesNotTouchOverride(t *testing.T) {
	db := openBaitAutoTestDB(t)
	ctx := context.Background()

	if err := db.SetFishBaitAuto(ctx, "c.roach", true, "test"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListFishBaitOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("打开自动轮换不应创建覆盖行，实得 %d 行", len(rows))
	}

	auto, err := db.ListFishBaitAuto(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(auto) != 1 || auto[0].FishKey != "c.roach" || !auto[0].Enabled {
		t.Fatalf("开关记录不符：%+v", auto)
	}

	// 关掉即删行：表里只留开启项，运行时读到的每个 key 都代表要轮换。
	if err := db.SetFishBaitAuto(ctx, "c.roach", false, "test"); err != nil {
		t.Fatal(err)
	}
	if auto, err = db.ListFishBaitAuto(ctx); err != nil {
		t.Fatal(err)
	}
	if len(auto) != 0 {
		t.Fatalf("关闭后应删除记录，实得 %+v", auto)
	}
}

// 覆盖行里的历史 rotate_mode='auto' 一律归一成 soft：自动抽取改由
// 独立开关表达，避免两套真相并存。
func TestFishBaitOverrideDropsAutoMode(t *testing.T) {
	db := openBaitAutoTestDB(t)
	ctx := context.Background()
	if err := db.SaveFishBaitOverride(ctx, FishBaitOverride{
		FishZh: "常见拟鲤", Allow: []string{"红蚯蚓"}, RotateMode: "auto", Enabled: true,
	}, "test"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListFishBaitOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RotateMode != "soft" {
		t.Fatalf("rotate_mode 应归一成 soft，实得 %+v", rows)
	}
}
