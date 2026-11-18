package store

import (
	"context"
	"database/sql"
	"time"
)

// FishEcologyLoc 一条「水域 × 鱼种」的生态档案覆盖。
// 指针字段为 nil = 该字段继承全局 fish_ecology（部分覆盖语义）。
type FishEcologyLoc struct {
	StatLoc     string   `json:"stat_loc"`
	FishKey     string   `json:"fish_key"`
	LayerMin    *float64 `json:"layer_min"`
	LayerPref   *float64 `json:"layer_pref"`
	LayerMax    *float64 `json:"layer_max"`
	TempMinC    *float64 `json:"temp_min_c"`
	TempPrefC   *float64 `json:"temp_pref_c"`
	TempMaxC    *float64 `json:"temp_max_c"`
	DielMask    *int64   `json:"diel_mask"`
	SeasonMask  *int64   `json:"season_mask"`
	DietJSON    *string  `json:"diet_json"`
	WeatherJSON *string  `json:"weather_json"`
	Activity    *float64 `json:"activity"`
	Enabled     *int64   `json:"enabled"`
	UpdatedBy   string   `json:"updated_by"`
	UpdatedTS   float64  `json:"updated_ts"`
}

// ListFishEcologyLoc 返回某水域的全部覆盖行；statLoc 为空时返回空。
func (db *DB) ListFishEcologyLoc(ctx context.Context, statLoc string) ([]FishEcologyLoc, error) {
	if db == nil || db.SQL == nil || statLoc == "" {
		return nil, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `
		SELECT stat_loc, fish_key, layer_min, layer_pref, layer_max,
		       temp_min_c, temp_pref_c, temp_max_c, diel_mask, season_mask,
		       diet_json, weather_json, activity, enabled, updated_by, updated_ts
		FROM fish_ecology_loc WHERE stat_loc=? ORDER BY fish_key`, statLoc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FishEcologyLoc{}
	for rows.Next() {
		o, err := scanFishEcologyLoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// AllFishEcologyLoc 全量读取，供判鱼侧一次性载入缓存。
func (db *DB) AllFishEcologyLoc(ctx context.Context) ([]FishEcologyLoc, error) {
	if db == nil || db.SQL == nil {
		return nil, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `
		SELECT stat_loc, fish_key, layer_min, layer_pref, layer_max,
		       temp_min_c, temp_pref_c, temp_max_c, diel_mask, season_mask,
		       diet_json, weather_json, activity, enabled, updated_by, updated_ts
		FROM fish_ecology_loc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FishEcologyLoc{}
	for rows.Next() {
		o, err := scanFishEcologyLoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFishEcologyLoc(sc rowScanner) (FishEcologyLoc, error) {
	var o FishEcologyLoc
	var layerMin, layerPref, layerMax sql.NullFloat64
	var tempMin, tempPref, tempMax sql.NullFloat64
	var dielMask, seasonMask, enabled sql.NullInt64
	var dietJSON, weatherJSON sql.NullString
	var activity sql.NullFloat64
	if err := sc.Scan(&o.StatLoc, &o.FishKey, &layerMin, &layerPref, &layerMax,
		&tempMin, &tempPref, &tempMax, &dielMask, &seasonMask,
		&dietJSON, &weatherJSON, &activity, &enabled, &o.UpdatedBy, &o.UpdatedTS); err != nil {
		return o, err
	}
	o.LayerMin, o.LayerPref, o.LayerMax = nullF(layerMin), nullF(layerPref), nullF(layerMax)
	o.TempMinC, o.TempPrefC, o.TempMaxC = nullF(tempMin), nullF(tempPref), nullF(tempMax)
	o.DielMask, o.SeasonMask, o.Enabled = nullI(dielMask), nullI(seasonMask), nullI(enabled)
	o.DietJSON, o.WeatherJSON = nullS(dietJSON), nullS(weatherJSON)
	o.Activity = nullF(activity)
	return o, nil
}

func nullF(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func nullI(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func nullS(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// UpsertFishEcologyLoc 整行保存（同键覆盖）。updatedBy 只用于审计展示。
func (db *DB) UpsertFishEcologyLoc(ctx context.Context, o FishEcologyLoc, updatedBy string) error {
	if db == nil || db.SQL == nil {
		return nil
	}
	if o.UpdatedTS <= 0 {
		o.UpdatedTS = float64(time.Now().UnixNano()) / 1e9
	}
	_, err := db.SQL.ExecContext(ctx, `
		INSERT INTO fish_ecology_loc(
			stat_loc, fish_key, layer_min, layer_pref, layer_max,
			temp_min_c, temp_pref_c, temp_max_c, diel_mask, season_mask,
			diet_json, weather_json, activity, enabled, updated_by, updated_ts)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(stat_loc, fish_key) DO UPDATE SET
			layer_min=excluded.layer_min, layer_pref=excluded.layer_pref,
			layer_max=excluded.layer_max, temp_min_c=excluded.temp_min_c,
			temp_pref_c=excluded.temp_pref_c, temp_max_c=excluded.temp_max_c,
			diel_mask=excluded.diel_mask, season_mask=excluded.season_mask,
			diet_json=excluded.diet_json, weather_json=excluded.weather_json,
			activity=excluded.activity, enabled=excluded.enabled,
			updated_by=excluded.updated_by, updated_ts=excluded.updated_ts`,
		o.StatLoc, o.FishKey, o.LayerMin, o.LayerPref, o.LayerMax,
		o.TempMinC, o.TempPrefC, o.TempMaxC, o.DielMask, o.SeasonMask,
		o.DietJSON, o.WeatherJSON, o.Activity, o.Enabled, updatedBy, o.UpdatedTS)
	return err
}

// DeleteFishEcologyLoc 删除一条覆盖，删除后该水域该鱼种回到继承全局档案。
func (db *DB) DeleteFishEcologyLoc(ctx context.Context, statLoc, fishKey string) error {
	if db == nil || db.SQL == nil {
		return nil
	}
	_, err := db.SQL.ExecContext(ctx,
		`DELETE FROM fish_ecology_loc WHERE stat_loc=? AND fish_key=?`, statLoc, fishKey)
	return err
}
