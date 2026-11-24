// fish_temp_import —— 把 ref/fish_temp_final.csv 的鱼种适温写入 fish_ecology_species。
//
// 为什么单开这个命令：fish_ecology_species 是运行时表（RuntimeTables），
// seed 不会覆盖它，也没有现成 CLI 入口。批量导入走
// ReplaceFishEcologySpeciesCurated（只删 origin<>'manual'，保住后台手填值）。
//
// 注意 ON CONFLICT 的语义：ReplaceFishEcologySpeciesCurated 的 INSERT 是整行覆盖，
// 指针为 nil 的列会写成 NULL。所以这里从 fish_ecology 把其余全部习性列复制过来，
// 否则一次导入会把昼伏曲线、饵料权重、动作权重等全部清空。
//
// 用法：
//
//	go run ./cmd/fishtemp -db data/rf4server.db -csv ref/fish_temp_final.csv
//	go run ./cmd/fishtemp -db data/rf4server.db -csv ref/fish_temp_final.csv -apply
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"rf4server/internal/store"
)

type row struct {
	key  string
	zh   string
	min  float64
	pref float64
	max  float64
}

func main() {
	dbPath := flag.String("db", "data/rf4server.db", "SQLite 库路径")
	csvPath := flag.String("csv", "ref/fish_temp_final.csv", "适温 CSV")
	apply := flag.Bool("apply", false, "真正写库（默认只校验）")
	updatedBy := flag.String("by", "fishtemp-tool", "updated_by 记录")
	flag.Parse()

	rows, err := readCSV(*csvPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读 CSV:", err)
		os.Exit(2)
	}
	fmt.Printf("CSV %d 条 ← %s\n", len(rows), *csvPath)

	ctx := context.Background()
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开库:", err)
		os.Exit(2)
	}
	defer func() { _ = db.Close() }()

	// 1) 校验 key 全集一致
	have, err := keysOf(ctx, db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读 fish_ecology:", err)
		os.Exit(2)
	}
	var missing, extra []string
	for _, r := range rows {
		if !have[r.key] {
			missing = append(missing, r.key)
		}
	}
	inCSV := map[string]bool{}
	for _, r := range rows {
		inCSV[r.key] = true
	}
	for k := range have {
		if !inCSV[k] {
			extra = append(extra, k)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "key 不一致 缺=%v 多=%v\n", missing, extra)
		os.Exit(2)
	}

	// 2) 校验区间合法
	bad := 0
	for _, r := range rows {
		if !(r.min <= r.pref && r.pref <= r.max) {
			fmt.Fprintf(os.Stderr, "区间非法 %s: %.1f/%.1f/%.1f\n", r.key, r.min, r.pref, r.max)
			bad++
		}
	}
	if bad > 0 {
		os.Exit(2)
	}
	fmt.Printf("✓ key 全集一致 %d 条，区间全部合法\n", len(rows))

	if !*apply {
		fmt.Println("（校验模式，未写库。加 -apply 执行导入）")
		return
	}

	// 3) 组装：温度取 CSV，其余习性从 fish_ecology 原样带过来
	items, err := buildItems(ctx, db, rows)
	if err != nil {
		fmt.Fprintln(os.Stderr, "组装:", err)
		os.Exit(1)
	}

	stdb := &store.DB{SQL: db}
	n, err := stdb.ReplaceFishEcologySpeciesCurated(ctx, items, *updatedBy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "写库:", err)
		os.Exit(1)
	}
	fmt.Printf("✓ 已写入 fish_ecology_species %d 行\n", n)
}

func readCSV(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	hdr, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range hdr {
		idx[strings.TrimPrefix(h, "\ufeff")] = i
	}
	for _, need := range []string{"fish_key", "temp_min_c", "temp_pref_c", "temp_max_c"} {
		if _, ok := idx[need]; !ok {
			return nil, fmt.Errorf("缺列 %s（实际列 %v）", need, hdr)
		}
	}
	var out []row
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		get := func(k string) string {
			if i, ok := idx[k]; ok && i < len(rec) {
				return rec[i]
			}
			return ""
		}
		mn, e1 := strconv.ParseFloat(get("temp_min_c"), 64)
		pf, e2 := strconv.ParseFloat(get("temp_pref_c"), 64)
		mx, e3 := strconv.ParseFloat(get("temp_max_c"), 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return nil, fmt.Errorf("数值解析失败 %s", get("fish_key"))
		}
		out = append(out, row{key: get("fish_key"), zh: get("zh"), min: mn, pref: pf, max: mx})
	}
	return out, nil
}

func keysOf(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rs, err := db.QueryContext(ctx, `SELECT fish_key FROM fish_ecology`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := map[string]bool{}
	for rs.Next() {
		var k string
		if err := rs.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rs.Err()
}

func buildItems(ctx context.Context, db *sql.DB, rows []row) ([]store.FishEcologySpecies, error) {
	rs, err := db.QueryContext(ctx, `
		SELECT fish_key, layer_min, layer_pref, layer_max,
		       diel_mask, diel_curve, season_mask,
		       diet_json, weather_json, action_json, substrate_json, color_json,
		       current_pref, activity, light_pref, clarity_pref, aggression, enabled
		FROM fish_ecology`)
	if err != nil {
		return nil, err
	}
	type habit struct {
		layerMin, layerPref, layerMax                       *float64
		dielMask, seasonMask, enabled                       *int64
		dielCurve, diet, weather, action, substrate, color  *string
		currentPref, activity, lightPref, clarityPref, aggr *float64
	}
	base := map[string]habit{}
	for rs.Next() {
		var k string
		var h habit
		var lm, lp, lx, cp, ac, lpt, cl, ag sql.NullFloat64
		var dm, sm, en sql.NullInt64
		var dc, di, we, act, su, co sql.NullString
		if err := rs.Scan(&k, &lm, &lp, &lx, &dm, &dc, &sm,
			&di, &we, &act, &su, &co, &cp, &ac, &lpt, &cl, &ag, &en); err != nil {
			return nil, err
		}
		h.layerMin = nf(lm)
		h.layerPref = nf(lp)
		h.layerMax = nf(lx)
		h.currentPref = nf(cp)
		h.activity = nf(ac)
		h.lightPref = nf(lpt)
		h.clarityPref = nf(cl)
		h.aggr = nf(ag)
		h.dielMask = ni(dm)
		h.seasonMask = ni(sm)
		h.enabled = ni(en)
		h.dielCurve = ns(dc)
		h.diet = ns(di)
		h.weather = ns(we)
		h.action = ns(act)
		h.substrate = ns(su)
		h.color = ns(co)
		base[k] = h
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	_ = rs.Close()

	out := make([]store.FishEcologySpecies, 0, len(rows))
	for _, r := range rows {
		h, ok := base[r.key]
		if !ok {
			return nil, fmt.Errorf("fish_ecology 缺 %s", r.key)
		}
		mn, pf, mx := r.min, r.pref, r.max
		out = append(out, store.FishEcologySpecies{
			FishKey: r.key,
			// 温度取 CSV
			TempMinC: &mn, TempPrefC: &pf, TempMaxC: &mx,
			// 其余习性原样继承，避免整行覆盖时被写成 NULL
			LayerMin: h.layerMin, LayerPref: h.layerPref, LayerMax: h.layerMax,
			DielMask: h.dielMask, DielCurve: h.dielCurve, SeasonMask: h.seasonMask,
			DietJSON: h.diet, WeatherJSON: h.weather, ActionJSON: h.action,
			SubstrateJSON: h.substrate, ColorJSON: h.color,
			CurrentPref: h.currentPref, Activity: h.activity,
			LightPref: h.lightPref, ClarityPref: h.clarityPref,
			Aggression: h.aggr, Enabled: h.enabled,
		})
	}
	return out, nil
}

func nf(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func ni(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func ns(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	x := v.String
	return &x
}
