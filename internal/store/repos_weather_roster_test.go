package store

import (
	"context"
	"testing"
)

// 名册查询必须经 loc_alias 把 map_key 换成中文地名。
//
// loc_wiki_fish / loc_fish 的 loc 列存的是「克马羚诺也湖」这种中文名，
// 拿 level_001_lake 去关联是 0 行。曾经就是漏了这一步：单测手插 level_xxx
// 假通过，线上每张图都返回 0 条鱼、活跃鱼种面板一片空白。
func TestWeatherRosterForResolvesAlias(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	seed := []string{
		`INSERT INTO loc_alias(alias, loc, note) VALUES('level_900_test','测试湖','roster test')`,
		`INSERT INTO fish_ecology(fish_key, temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve, activity)
		 VALUES('r.perch', 8, 15, 22, 4, '[0.1,0.3,1,0.2]', 1.0)`,
		`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh) VALUES('测试湖','r.perch','测试鲈')`,
	}
	for _, q := range seed {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// 用 map_key 查：必须能通过 alias 找到。
	rows, err := db.WeatherRosterFor(ctx, "level_900_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("经 alias 应查到 1 条，实得 %d 条", len(rows))
	}
	r := rows[0]
	if r.FishKey != "r.perch" || r.NameZh != "测试鲈" {
		t.Fatalf("鱼种不对: %+v", r)
	}
	if r.DielCurve == "" || r.TempPrefC != 15 {
		t.Fatalf("生态字段没带出来: %+v", r)
	}

	// 直接用中文名查也该通（后台某些入口传的就是中文名）。
	byName, err := db.WeatherRosterFor(ctx, "测试湖")
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) != 1 {
		t.Fatalf("用中文名应查到 1 条，实得 %d 条", len(byName))
	}

	// 没有 alias 也没有同名名册的地图必须返回 0 条，而不是报错或造数据。
	none, err := db.WeatherRosterFor(ctx, "level_999_none")
	if err != nil {
		t.Fatalf("未知地图不该报错: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("未知地图应 0 条，实得 %d", len(none))
	}
}

// 名册只含 fish_ecology 里有档案的鱼：没有生态档案也算不出昼夜与适温。
func TestWeatherRosterForSkipsFishWithoutEcology(t *testing.T) {
	db := openMigratedDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO loc_alias(alias, loc, note) VALUES('level_901_test','测试湖2','roster test')`,
		`INSERT INTO fish_ecology(fish_key, temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve, activity)
		 VALUES('r.has', 8, 15, 22, 4, '[0.1,0.3,1,0.2]', 1.0)`,
		`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh) VALUES('测试湖2','r.has','有档案')`,
		`INSERT INTO loc_wiki_fish(loc, fish_key, fish_zh) VALUES('测试湖2','r.none','无档案')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rows, err := db.WeatherRosterFor(ctx, "level_901_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].FishKey != "r.has" {
		t.Fatalf("只应保留有生态档案的鱼，实得 %+v", rows)
	}
}
