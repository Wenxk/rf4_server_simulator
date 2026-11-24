package store

import (
	"context"
	"database/sql"
	"time"
)

// FishEcologySpecies 一条「鱼种级」的生态档案覆盖。
//
// 层级：资产基线 fish_ecology → 本表 → 水域覆盖 fish_ecology_loc。
// 所有字段都是指针，nil = 继承资产基线（部分覆盖语义，与 fish_ecology_loc 同口径）。
//
// 为什么不直接改 fish_ecology：那张表由 ensureFishEcology 按 profile_version
// 从资产 JSON 重灌，手工值会被版本升级冲掉（见 00082 迁移注释）。
type FishEcologySpecies struct {
	FishKey       string   `json:"fish_key"`
	LayerMin      *float64 `json:"layer_min"`
	LayerPref     *float64 `json:"layer_pref"`
	LayerMax      *float64 `json:"layer_max"`
	TempMinC      *float64 `json:"temp_min_c"`
	TempPrefC     *float64 `json:"temp_pref_c"`
	TempMaxC      *float64 `json:"temp_max_c"`
	DielMask      *int64   `json:"diel_mask"`
	DielCurve     *string  `json:"diel_curve"`
	SeasonMask    *int64   `json:"season_mask"`
	DietJSON      *string  `json:"diet_json"`
	WeatherJSON   *string  `json:"weather_json"`
	ActionJSON    *string  `json:"action_json"`
	SubstrateJSON *string  `json:"substrate_json"`
	ColorJSON     *string  `json:"color_json"`
	CurrentPref   *float64 `json:"current_pref"`
	Activity      *float64 `json:"activity"`
	LightPref     *float64 `json:"light_pref"`
	ClarityPref   *float64 `json:"clarity_pref"`
	Aggression    *float64 `json:"aggression"`
	Enabled       *int64   `json:"enabled"`
	// Origin 数据来源：manual = 后台手填，curated = 批量导入的文献值。
	Origin    string  `json:"origin"`
	UpdatedBy string  `json:"updated_by"`
	UpdatedTS float64 `json:"updated_ts"`
}

// speciesEcologyColumns 是读写共用的列顺序，避免 SELECT 与 Scan 顺序漂移。
const speciesEcologyColumns = `
	fish_key, layer_min, layer_pref, layer_max,
	temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve, season_mask,
	diet_json, weather_json, action_json, substrate_json, color_json,
	current_pref, activity, light_pref, clarity_pref, aggression, enabled,
	origin, updated_by, updated_ts`

// AllFishEcologySpecies 全量读取，供判鱼侧一次性载入缓存、后台一次性渲染列表。
// 表体量是「鱼种数」级别（最多几百行），全量比按需查询更简单也更快。
func (db *DB) AllFishEcologySpecies(ctx context.Context) ([]FishEcologySpecies, error) {
	if db == nil || db.SQL == nil {
		return nil, nil
	}
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT `+speciesEcologyColumns+` FROM fish_ecology_species ORDER BY fish_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FishEcologySpecies{}
	for rows.Next() {
		o, err := scanFishEcologySpecies(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanFishEcologySpecies(sc rowScanner) (FishEcologySpecies, error) {
	var o FishEcologySpecies
	var layerMin, layerPref, layerMax, tempMin, tempPref, tempMax sql.NullFloat64
	var currentPref, activity, lightPref, clarityPref, aggression sql.NullFloat64
	var dielMask, seasonMask, enabled sql.NullInt64
	var dielCurve, dietJSON, weatherJSON, actionJSON, substrateJSON, colorJSON sql.NullString
	if err := sc.Scan(&o.FishKey, &layerMin, &layerPref, &layerMax,
		&tempMin, &tempPref, &tempMax, &dielMask, &dielCurve, &seasonMask,
		&dietJSON, &weatherJSON, &actionJSON, &substrateJSON, &colorJSON,
		&currentPref, &activity, &lightPref, &clarityPref, &aggression, &enabled,
		&o.Origin, &o.UpdatedBy, &o.UpdatedTS); err != nil {
		return o, err
	}
	o.LayerMin, o.LayerPref, o.LayerMax = nullF(layerMin), nullF(layerPref), nullF(layerMax)
	o.TempMinC, o.TempPrefC, o.TempMaxC = nullF(tempMin), nullF(tempPref), nullF(tempMax)
	o.DielMask, o.SeasonMask, o.Enabled = nullI(dielMask), nullI(seasonMask), nullI(enabled)
	o.DielCurve = nullS(dielCurve)
	o.DietJSON, o.WeatherJSON = nullS(dietJSON), nullS(weatherJSON)
	o.ActionJSON, o.SubstrateJSON, o.ColorJSON = nullS(actionJSON), nullS(substrateJSON), nullS(colorJSON)
	o.CurrentPref, o.Activity = nullF(currentPref), nullF(activity)
	o.LightPref, o.ClarityPref = nullF(lightPref), nullF(clarityPref)
	o.Aggression = nullF(aggression)
	return o, nil
}

// UpsertFishEcologySpecies 整行保存（同键覆盖）。updatedBy 只用于审计展示。
// 所有字段为 nil 时调用方应先走 Delete，避免留下一条全空、语义为「无覆盖」的脏行。
func (db *DB) UpsertFishEcologySpecies(ctx context.Context, o FishEcologySpecies, updatedBy string) error {
	if db == nil || db.SQL == nil {
		return nil
	}
	if o.UpdatedTS <= 0 {
		o.UpdatedTS = float64(time.Now().UnixNano()) / 1e9
	}
	if o.Origin == "" {
		o.Origin = "manual"
	}
	_, err := db.SQL.ExecContext(ctx, `
		INSERT INTO fish_ecology_species(`+speciesEcologyColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(fish_key) DO UPDATE SET
			layer_min=excluded.layer_min, layer_pref=excluded.layer_pref,
			layer_max=excluded.layer_max, temp_min_c=excluded.temp_min_c,
			temp_pref_c=excluded.temp_pref_c, temp_max_c=excluded.temp_max_c,
			diel_mask=excluded.diel_mask, diel_curve=excluded.diel_curve,
			season_mask=excluded.season_mask,
			diet_json=excluded.diet_json, weather_json=excluded.weather_json,
			action_json=excluded.action_json, substrate_json=excluded.substrate_json,
			color_json=excluded.color_json, current_pref=excluded.current_pref,
			activity=excluded.activity, light_pref=excluded.light_pref,
			clarity_pref=excluded.clarity_pref, aggression=excluded.aggression,
			enabled=excluded.enabled, origin=excluded.origin,
			updated_by=excluded.updated_by, updated_ts=excluded.updated_ts`,
		o.FishKey, o.LayerMin, o.LayerPref, o.LayerMax,
		o.TempMinC, o.TempPrefC, o.TempMaxC, o.DielMask, o.DielCurve, o.SeasonMask,
		o.DietJSON, o.WeatherJSON, o.ActionJSON, o.SubstrateJSON, o.ColorJSON,
		o.CurrentPref, o.Activity, o.LightPref, o.ClarityPref, o.Aggression, o.Enabled,
		o.Origin, updatedBy, o.UpdatedTS)
	return err
}

// DeleteFishEcologySpecies 删除一条鱼种覆盖，删除后回到资产基线。
func (db *DB) DeleteFishEcologySpecies(ctx context.Context, fishKey string) error {
	if db == nil || db.SQL == nil {
		return nil
	}
	_, err := db.SQL.ExecContext(ctx,
		`DELETE FROM fish_ecology_species WHERE fish_key=?`, fishKey)
	return err
}

// ReplaceFishEcologySpeciesCurated 用一批 curated 数据整表替换「非手填」的鱼种覆盖。
//
// 语义：只删 origin='curated' 的行再插入新的一批，后台手填（origin='manual'）
// 的行原样保留。这样重跑导入脚本不会冲掉运营手工校对过的值——这正是
// 单开一张表、而不是直接改 fish_ecology 的同一个理由。
func (db *DB) ReplaceFishEcologySpeciesCurated(ctx context.Context, rows []FishEcologySpecies, updatedBy string) (int, error) {
	if db == nil || db.SQL == nil {
		return 0, nil
	}
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM fish_ecology_species WHERE origin<>'manual'`); err != nil {
		return 0, err
	}
	now := float64(time.Now().UnixNano()) / 1e9
	count := 0
	for _, o := range rows {
		if o.FishKey == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fish_ecology_species(`+speciesEcologyColumns+`)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(fish_key) DO UPDATE SET
				layer_min=excluded.layer_min, layer_pref=excluded.layer_pref,
				layer_max=excluded.layer_max, temp_min_c=excluded.temp_min_c,
				temp_pref_c=excluded.temp_pref_c, temp_max_c=excluded.temp_max_c,
				diel_mask=excluded.diel_mask, diel_curve=excluded.diel_curve,
				season_mask=excluded.season_mask,
				diet_json=excluded.diet_json, weather_json=excluded.weather_json,
				action_json=excluded.action_json, substrate_json=excluded.substrate_json,
				color_json=excluded.color_json, current_pref=excluded.current_pref,
				activity=excluded.activity, light_pref=excluded.light_pref,
				clarity_pref=excluded.clarity_pref, aggression=excluded.aggression,
				enabled=excluded.enabled, origin=excluded.origin,
				updated_by=excluded.updated_by, updated_ts=excluded.updated_ts
			WHERE fish_ecology_species.origin<>'manual'`,
			o.FishKey, o.LayerMin, o.LayerPref, o.LayerMax,
			o.TempMinC, o.TempPrefC, o.TempMaxC, o.DielMask, o.DielCurve, o.SeasonMask,
			o.DietJSON, o.WeatherJSON, o.ActionJSON, o.SubstrateJSON, o.ColorJSON,
			o.CurrentPref, o.Activity, o.LightPref, o.ClarityPref, o.Aggression, o.Enabled,
			"curated", updatedBy, now); err != nil {
			return count, err
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
