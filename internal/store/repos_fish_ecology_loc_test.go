package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openMigratedDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// 覆盖行是「部分覆盖」：只写温度时水层必须落 NULL（= 继承全局），
// 二次保存同键不能产生第二行——否则判鱼会读到哪一行取决于查询顺序。
func TestFishEcologyLocUpsertIsPartialAndIdempotent(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()

	temp := 12.0
	layer := 0.9
	if err := db.UpsertFishEcologyLoc(ctx, FishEcologyLoc{
		StatLoc: "琥珀湖", FishKey: "n.carp", TempPrefC: &temp,
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFishEcologyLoc(ctx, FishEcologyLoc{
		StatLoc: "琥珀湖", FishKey: "n.carp", TempPrefC: &temp, LayerPref: &layer,
	}, "second"); err != nil {
		t.Fatal(err)
	}

	rows, err := db.ListFishEcologyLoc(ctx, "琥珀湖")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("同键二次保存产生 %d 行，期望 1", len(rows))
	}
	got := rows[0]
	if got.TempPrefC == nil || *got.TempPrefC != 12 {
		t.Fatalf("温度覆盖=%v，期望 12", got.TempPrefC)
	}
	if got.LayerPref == nil || *got.LayerPref != 0.9 {
		t.Fatalf("水层覆盖=%v，期望 0.9", got.LayerPref)
	}
	if got.LayerMin != nil || got.TempMaxC != nil || got.DietJSON != nil {
		t.Fatalf("未给出的字段必须为 nil（继承全局），实得 min=%v max=%v diet=%v",
			got.LayerMin, got.TempMaxC, got.DietJSON)
	}
	if got.UpdatedBy != "second" {
		t.Fatalf("审计人=%q，期望 second", got.UpdatedBy)
	}

	// 另一张图同鱼种互不影响。
	other := 21.0
	if err := db.UpsertFishEcologyLoc(ctx, FishEcologyLoc{
		StatLoc: "克马羚诺也湖", FishKey: "n.carp", TempPrefC: &other,
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	all, err := db.AllFishEcologyLoc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("全量载入 %d 行，期望 2", len(all))
	}

	if err := db.DeleteFishEcologyLoc(ctx, "琥珀湖", "n.carp"); err != nil {
		t.Fatal(err)
	}
	rows, err = db.ListFishEcologyLoc(ctx, "琥珀湖")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("删除后仍有 %d 行", len(rows))
	}
}

// 追踪表是环形的：插入后只保留最新 keep 条，且按 loc 过滤时不会串水域。
func TestFishRollTraceKeepsNewestAndFiltersByLoc(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		loc := "甲湖"
		if i >= 3 {
			loc = "乙湖"
		}
		if err := db.InsertFishRollTrace(ctx, FishRollTrace{
			UID: 7, Loc: loc, Bait: "bait", Hit: i%2 == 0,
			Chosen: "n.pike", WeightG: int64(100 + i), Payload: "{}",
		}, 3); err != nil {
			t.Fatal(err)
		}
	}
	all, err := db.ListFishRollTrace(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("keep=3 时保留 %d 行，期望 3", len(all))
	}
	// 倒序：最新一条是 i=4（乙湖）。
	if all[0].Loc != "乙湖" || all[0].WeightG != 104 {
		t.Fatalf("最新一行=%+v，期望乙湖 104g", all[0])
	}
	onlyB, err := db.ListFishRollTrace(ctx, "乙湖", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range onlyB {
		if row.Loc != "乙湖" {
			t.Fatalf("按水域过滤串入了 %q", row.Loc)
		}
	}
	if len(onlyB) != 2 {
		t.Fatalf("乙湖 %d 行，期望 2", len(onlyB))
	}
}
