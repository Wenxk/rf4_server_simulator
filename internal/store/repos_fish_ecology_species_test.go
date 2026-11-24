package store

import (
	"context"
	"testing"
)

// 鱼种档案覆盖必须是「部分覆盖」：只写温度时其余字段落 NULL（= 继承资产基线），
// 二次保存同键不能产生第二行。
func TestFishEcologySpeciesUpsertIsPartialAndIdempotent(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()

	tmin, tpref, tmax := 4.0, 10.0, 14.0
	if err := db.UpsertFishEcologySpecies(ctx, FishEcologySpecies{
		FishKey: "burbot", TempMinC: &tmin, TempPrefC: &tpref, TempMaxC: &tmax,
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	act := 0.87
	if err := db.UpsertFishEcologySpecies(ctx, FishEcologySpecies{
		FishKey: "burbot", TempMinC: &tmin, TempPrefC: &tpref, TempMaxC: &tmax,
		Activity: &act,
	}, "second"); err != nil {
		t.Fatal(err)
	}

	rows, err := db.AllFishEcologySpecies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("同键二次保存产生 %d 行，期望 1", len(rows))
	}
	got := rows[0]
	if got.TempMinC == nil || *got.TempMinC != 4 {
		t.Fatalf("水温下限=%v，期望 4", got.TempMinC)
	}
	if got.TempPrefC == nil || *got.TempPrefC != 10 {
		t.Fatalf("水温偏好=%v，期望 10", got.TempPrefC)
	}
	if got.Activity == nil || *got.Activity != 0.87 {
		t.Fatalf("活跃度=%v，期望 0.87", got.Activity)
	}
	if got.LayerMin != nil || got.SeasonMask != nil || got.DietJSON != nil || got.Enabled != nil {
		t.Fatalf("未给出的字段必须为 nil（继承基线），实得 layer=%v season=%v diet=%v enabled=%v",
			got.LayerMin, got.SeasonMask, got.DietJSON, got.Enabled)
	}
	if got.Origin != "manual" {
		t.Fatalf("默认来源=%q，期望 manual", got.Origin)
	}
	if got.UpdatedBy != "second" {
		t.Fatalf("审计人=%q，期望 second", got.UpdatedBy)
	}

	if err := db.DeleteFishEcologySpecies(ctx, "burbot"); err != nil {
		t.Fatal(err)
	}
	rows, err = db.AllFishEcologySpecies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("删除后仍有 %d 行", len(rows))
	}
}

// ReplaceFishEcologySpeciesCurated 是批量导入文献值的入口：它只能替换
// origin<>'manual' 的行，后台手填的行必须原样保留——否则一次导入就把人的校对成果冲掉。
func TestReplaceCuratedKeepsManualOverrides(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()

	// 一条手填（后台改的），一条旧的文献值（上次导入留下的）。
	manualTemp := 10.0
	if err := db.UpsertFishEcologySpecies(ctx, FishEcologySpecies{
		FishKey: "burbot", TempPrefC: &manualTemp, Origin: "manual",
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	staleTemp := 15.0
	if err := db.UpsertFishEcologySpecies(ctx, FishEcologySpecies{
		FishKey: "pike", TempPrefC: &staleTemp, Origin: "curated",
	}, "importer"); err != nil {
		t.Fatal(err)
	}

	// 再次导入：只带 pike 的新值 + 一条全新鱼种。
	newPike, newZander := 12.0, 18.0
	n, err := db.ReplaceFishEcologySpeciesCurated(ctx, []FishEcologySpecies{
		{FishKey: "pike", TempPrefC: &newPike, Origin: "curated"},
		{FishKey: "zander", TempPrefC: &newZander, Origin: "curated"},
	}, "importer")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("导入返回 %d 行，期望 2", n)
	}

	rows, err := db.AllFishEcologySpecies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]FishEcologySpecies, len(rows))
	for _, r := range rows {
		byKey[r.FishKey] = r
	}
	// 手填的 burbot 必须还在，且值没被冲掉。
	if m, ok := byKey["burbot"]; !ok {
		t.Fatal("手填覆盖被批量导入删掉了")
	} else if m.TempPrefC == nil || *m.TempPrefC != 10 || m.Origin != "manual" {
		t.Fatalf("手填覆盖被改写成 %+v", m)
	}
	// 旧的文献值被新值替换（不是追加）。
	if p, ok := byKey["pike"]; !ok {
		t.Fatal("文献值缺失 pike")
	} else if p.TempPrefC == nil || *p.TempPrefC != 12 {
		t.Fatalf("pike 文献值=%v，期望 12", p.TempPrefC)
	}
	// 新鱼种被插入。
	if _, ok := byKey["zander"]; !ok {
		t.Fatal("文献值缺失 zander")
	}
	if len(rows) != 3 {
		t.Fatalf("共 %d 行，期望 3（手填 1 + 文献 2）", len(rows))
	}
}
