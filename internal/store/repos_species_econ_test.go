package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openSpeciesEconDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "species-econ.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func fptr(v float64) *float64 { return &v }

// 只覆盖经验、只覆盖价格、两者都覆盖、清空 —— 四种行状态都要能正确往返。
func TestSpeciesEconSaveAndReset(t *testing.T) {
	db := openSpeciesEconDB(t)
	ctx := context.Background()
	rows := []SpeciesEcon{
		{Grade: 1, XPC: fptr(0.08)},                        // 只改经验
		{Grade: 2, PricePerKg: fptr(4.5)},                  // 只改价格
		{Grade: 3, XPC: fptr(2.0), PricePerKg: fptr(12.5)}, // 两者都改
		{Grade: 4}, // 空行 → 不落库
	}
	if err := db.SpeciesEconSave(ctx, "bream", rows, "tester"); err != nil {
		t.Fatal(err)
	}
	list, err := db.SpeciesEconList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("覆盖行=%d，期望 3（空行不落库）", len(list))
	}
	byGrade := map[int64]SpeciesEcon{}
	for _, r := range list {
		byGrade[r.Grade] = r
	}
	if g := byGrade[1]; g.XPC == nil || *g.XPC != 0.08 || g.PricePerKg != nil {
		t.Fatalf("g1 往返异常: %+v", g)
	}
	if g := byGrade[2]; g.PricePerKg == nil || *g.PricePerKg != 4.5 || g.XPC != nil {
		t.Fatalf("g2 往返异常: %+v", g)
	}
	if g := byGrade[3]; g.XPC == nil || *g.XPC != 2.0 || g.PricePerKg == nil || *g.PricePerKg != 12.5 {
		t.Fatalf("g3 往返异常: %+v", g)
	}
	if g := byGrade[3]; g.UpdatedBy != "tester" || g.UpdatedTS <= 0 {
		t.Fatalf("审计字段异常: %+v", g)
	}

	// 清空某一档：只把该档两个值置空提交，其余档保持不变。
	if err := db.SpeciesEconSave(ctx, "bream", []SpeciesEcon{{Grade: 3}}, "tester"); err != nil {
		t.Fatal(err)
	}
	if got, err := db.SpeciesEconGet(ctx, "bream", 3); err != nil || got != nil {
		t.Fatalf("g3 清空失败: got=%+v err=%v", got, err)
	}
	if got, err := db.SpeciesEconGet(ctx, "bream", 1); err != nil || got == nil {
		t.Fatalf("g1 不应被连带清空: got=%+v err=%v", got, err)
	}

	n, err := db.SpeciesEconReset(ctx, "bream")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("删除行数=%d，期望 2", n)
	}
	list, _ = db.SpeciesEconList(ctx)
	if len(list) != 0 {
		t.Fatalf("恢复默认后仍有 %d 行", len(list))
	}
}

// 非法输入必须整笔拒绝：档位越界、零/负数、天价 typo。
func TestSpeciesEconRejectsInvalid(t *testing.T) {
	db := openSpeciesEconDB(t)
	ctx := context.Background()
	cases := []struct {
		name string
		key  string
		rows []SpeciesEcon
	}{
		{"空鱼种", "  ", []SpeciesEcon{{Grade: 1, XPC: fptr(1)}}},
		{"档位0", "bream", []SpeciesEcon{{Grade: 0, XPC: fptr(1)}}},
		{"档位5", "bream", []SpeciesEcon{{Grade: 5, XPC: fptr(1)}}},
		{"经验为0", "bream", []SpeciesEcon{{Grade: 1, XPC: fptr(0)}}},
		{"经验为负", "bream", []SpeciesEcon{{Grade: 1, XPC: fptr(-1)}}},
		{"鱼价为0", "bream", []SpeciesEcon{{Grade: 1, PricePerKg: fptr(0)}}},
	}
	for _, c := range cases {
		if err := db.SpeciesEconSave(ctx, c.key, c.rows, "tester"); err == nil {
			t.Fatalf("%s: 期望拒绝，实际保存成功", c.name)
		}
	}
	list, _ := db.SpeciesEconList(ctx)
	if len(list) != 0 {
		t.Fatalf("非法保存后不应有残留行，实际 %d", len(list))
	}
}
