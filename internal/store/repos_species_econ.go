package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SpeciesEcon 是某鱼种某档位的经济覆盖行。两个数值列都可空：
// nil 表示该维度跟随内置表（经验走 fishXPRateFor，鱼价走 fish_market_prices.json）。
type SpeciesEcon struct {
	FishKey    string
	Grade      int64
	XPC        *float64
	PricePerKg *float64
	UpdatedBy  string
	UpdatedTS  float64
}

// SpeciesEconList 读取全部覆盖行，供主服 30 秒缓存整表加载。
func (d *DB) SpeciesEconList(ctx context.Context) ([]SpeciesEcon, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT fish_key, grade, xp_c, price_per_kg, updated_by, updated_ts
		FROM species_econ ORDER BY fish_key, grade`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SpeciesEcon, 0)
	for rows.Next() {
		var rec SpeciesEcon
		var xp, price sql.NullFloat64
		if err := rows.Scan(&rec.FishKey, &rec.Grade, &xp, &price, &rec.UpdatedBy, &rec.UpdatedTS); err != nil {
			return nil, err
		}
		if xp.Valid {
			v := xp.Float64
			rec.XPC = &v
		}
		if price.Valid {
			v := price.Float64
			rec.PricePerKg = &v
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SpeciesEconSave 按鱼种整体写入四档覆盖：两个值都为空时删除该行。
// 调用方（后台）每次提交该鱼种全部四档，避免「只改一档」需要额外的三态协议。
func (d *DB) SpeciesEconSave(ctx context.Context, fishKey string, recs []SpeciesEcon, updatedBy string) error {
	fishKey = strings.TrimSpace(fishKey)
	if fishKey == "" {
		return fmt.Errorf("鱼种不能为空")
	}
	now := float64(time.Now().Unix())
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, rec := range recs {
		if rec.Grade < 1 || rec.Grade > 4 {
			return fmt.Errorf("档位越界: %d", rec.Grade)
		}
		if rec.XPC == nil && rec.PricePerKg == nil {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM species_econ WHERE fish_key=? AND grade=?`, fishKey, rec.Grade); err != nil {
				return err
			}
			continue
		}
		if rec.XPC != nil && (*rec.XPC <= 0 || *rec.XPC > 10000) {
			return fmt.Errorf("经验速率超出范围: %v", *rec.XPC)
		}
		if rec.PricePerKg != nil && (*rec.PricePerKg <= 0 || *rec.PricePerKg > 1000000) {
			return fmt.Errorf("鱼价超出范围: %v", *rec.PricePerKg)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO species_econ(fish_key, grade, xp_c, price_per_kg, updated_by, updated_ts)
			VALUES(?,?,?,?,?,?)
			ON CONFLICT (fish_key, grade) DO UPDATE SET
				xp_c=excluded.xp_c, price_per_kg=excluded.price_per_kg,
				updated_by=excluded.updated_by, updated_ts=excluded.updated_ts`,
			fishKey, rec.Grade, rec.XPC, rec.PricePerKg, updatedBy, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SpeciesEconReset 清空某鱼种的全部覆盖，返回删除行数。
func (d *DB) SpeciesEconReset(ctx context.Context, fishKey string) (int64, error) {
	fishKey = strings.TrimSpace(fishKey)
	if fishKey == "" {
		return 0, fmt.Errorf("鱼种不能为空")
	}
	res, err := d.SQL.ExecContext(ctx, `DELETE FROM species_econ WHERE fish_key=?`, fishKey)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SpeciesEconGet 读取某鱼种某档位的覆盖行；没有覆盖时返回 nil。
func (d *DB) SpeciesEconGet(ctx context.Context, fishKey string, grade int64) (*SpeciesEcon, error) {
	row := d.SQL.QueryRowContext(ctx, `
		SELECT fish_key, grade, xp_c, price_per_kg, updated_by, updated_ts
		FROM species_econ WHERE fish_key=? AND grade=?`, fishKey, grade)
	var rec SpeciesEcon
	var xp, price sql.NullFloat64
	if err := row.Scan(&rec.FishKey, &rec.Grade, &xp, &price, &rec.UpdatedBy, &rec.UpdatedTS); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if xp.Valid {
		v := xp.Float64
		rec.XPC = &v
	}
	if price.Valid {
		v := price.Float64
		rec.PricePerKg = &v
	}
	return &rec, nil
}
