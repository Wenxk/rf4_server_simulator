package store

import (
	"context"
	"time"
)

// FishBaitAuto 是一条「该鱼种参与自动轮换」的开关记录。
//
// 与 FishBaitOverride 的区别：覆盖表以鱼名为主键、整行替换内置规则；
// 这里只记开关，规则内容仍取内置表或覆盖表。两者互不影响——给一条没有
// 覆盖的鱼打开自动轮换，不会把内置规则固化成覆盖行。
type FishBaitAuto struct {
	FishKey   string  `json:"fish_key"`
	Enabled   bool    `json:"enabled"`
	UpdatedBy string  `json:"updated_by"`
	UpdatedTs float64 `json:"updated_ts"`
}

// ListFishBaitAuto 读取全部自动轮换开关，供后台与判鱼缓存使用。
func (d *DB) ListFishBaitAuto(ctx context.Context) ([]FishBaitAuto, error) {
	rows, err := d.SQL.QueryContext(ctx,
		`SELECT fish_key,enabled,updated_by,updated_ts FROM fish_bait_auto_rotation ORDER BY fish_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FishBaitAuto, 0, 64)
	for rows.Next() {
		var a FishBaitAuto
		var enabled int
		if err := rows.Scan(&a.FishKey, &enabled, &a.UpdatedBy, &a.UpdatedTs); err != nil {
			return nil, err
		}
		a.Enabled = enabled != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetFishBaitAuto 打开或关闭某鱼种的自动轮换。enabled=false 时直接删行，
// 让表里只保留开启项——运行时读到的每个 key 都代表「要轮换」。
func (d *DB) SetFishBaitAuto(ctx context.Context, fishKey string, enabled bool, by string) error {
	if !enabled {
		_, err := d.SQL.ExecContext(ctx, `DELETE FROM fish_bait_auto_rotation WHERE fish_key=?`, fishKey)
		return err
	}
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO fish_bait_auto_rotation(fish_key,enabled,updated_by,updated_ts)
		VALUES(?,1,?,?)
		ON CONFLICT(fish_key) DO UPDATE SET
			enabled=1, updated_by=excluded.updated_by, updated_ts=excluded.updated_ts`,
		fishKey, by, float64(time.Now().Unix()))
	return err
}
