package store

import (
	"context"
	"time"
)

// FishRollTrace 一条判鱼决策快照。Payload 是紧凑 JSON：
// {"stages":["roster",...],"candidates":[{"key":"n.pike","w":[12.5,8.1,...]}],"empty":"..."}。
// 只做诊断展示，不参与判鱼。
type FishRollTrace struct {
	ID          int64   `json:"id"`
	TS          float64 `json:"ts"`
	UID         int64   `json:"uid"`
	Loc         string  `json:"loc"`
	Spot        int64   `json:"spot"`
	X           float64 `json:"x"`
	Z           float64 `json:"z"`
	RigType     int64   `json:"rig_type"`
	Bait        string  `json:"bait"`
	Hit         bool    `json:"hit"`
	Chosen      string  `json:"chosen"`
	WeightG     int64   `json:"weight_g"`
	EmptyReason string  `json:"empty_reason"`
	TotalWeight float64 `json:"total_weight"`
	Payload     string  `json:"payload"`
}

// InsertFishRollTrace 写入一条追踪并环形裁剪到 keep 条。
// keep<=0 时调用方不应调用；这里再兜一层，避免误清空。
func (db *DB) InsertFishRollTrace(ctx context.Context, t FishRollTrace, keep int) error {
	if db == nil || db.SQL == nil {
		return nil
	}
	if t.TS <= 0 {
		t.TS = float64(time.Now().UnixNano()) / 1e9
	}
	if t.Payload == "" {
		t.Payload = "{}"
	}
	hit := 0
	if t.Hit {
		hit = 1
	}
	res, err := db.SQL.ExecContext(ctx, `
		INSERT INTO fish_roll_trace(ts,uid,loc,spot,x,z,rig_type,bait,hit,chosen,weight_g,empty_reason,total_weight,payload)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.TS, t.UID, t.Loc, t.Spot, t.X, t.Z, t.RigType, t.Bait, hit, t.Chosen, t.WeightG,
		t.EmptyReason, t.TotalWeight, t.Payload)
	if err != nil {
		return err
	}
	if id, ierr := res.LastInsertId(); ierr == nil {
		t.ID = id
	}
	if keep <= 0 {
		return nil
	}
	// 只留最新 keep 条。表规模由 keep 决定（默认 300），全表 NOT IN 扫描是微秒级。
	_, err = db.SQL.ExecContext(ctx,
		`DELETE FROM fish_roll_trace WHERE id NOT IN (SELECT id FROM fish_roll_trace ORDER BY id DESC LIMIT ?)`,
		keep)
	return err
}

// ListFishRollTrace 按时间倒序返回最近 limit 条；loc 非空时只返回该水域。
func (db *DB) ListFishRollTrace(ctx context.Context, loc string, limit int) ([]FishRollTrace, error) {
	if db == nil || db.SQL == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id,ts,uid,loc,spot,x,z,rig_type,bait,hit,chosen,weight_g,empty_reason,total_weight,payload
		FROM fish_roll_trace`
	args := []any{}
	if loc != "" {
		query += ` WHERE loc=?`
		args = append(args, loc)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FishRollTrace, 0, limit)
	for rows.Next() {
		var t FishRollTrace
		var hit int
		if err := rows.Scan(&t.ID, &t.TS, &t.UID, &t.Loc, &t.Spot, &t.X, &t.Z, &t.RigType,
			&t.Bait, &hit, &t.Chosen, &t.WeightG, &t.EmptyReason, &t.TotalWeight, &t.Payload); err != nil {
			return nil, err
		}
		t.Hit = hit != 0
		out = append(out, t)
	}
	return out, rows.Err()
}
