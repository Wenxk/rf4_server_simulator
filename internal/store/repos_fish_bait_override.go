package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// errFishKeyRequired 中文名规范化后为空，无法反查 fish_key。
var errFishKeyRequired = errors.New("必须指定鱼种")

// FishBaitOverride 是管理后台对某个鱼种饵料规则的覆盖。
// 存在且 enabled=1 时整体覆盖编译期内置规则；删除记录即恢复默认。
//
// 主键是 FishKey 而不是中文名（00083 迁移）：中文名在库里既有 BOM 尾字符、也有
// 「大西洋鲑 / 大西洋鲑鱼」这类变体，还出现过同一条鱼两条覆盖互不相同的分叉。
// FishZh 现在只作展示与规则文本匹配，查表一律走 FishKey。
type FishBaitOverride struct {
	FishKey    string   `json:"fish_key"`
	FishZh     string   `json:"fish_zh"`
	Allow      []string `json:"allow"`
	Deny       []string `json:"deny"`
	BottomOnly []string `json:"bottom_only"`
	Condition  string   `json:"condition"`
	Enabled    bool     `json:"enabled"`
	UpdatedBy  string   `json:"updated_by"`
	UpdatedTs  float64  `json:"updated_ts"`
}

// ListFishBaitOverrides 读取全部覆盖记录，供后台与判鱼缓存使用。
func (d *DB) ListFishBaitOverrides(ctx context.Context) ([]FishBaitOverride, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT fish_key,fish_zh,allow_json,deny_json,bottom_json,condition,
		       enabled,updated_by,updated_ts
		FROM fish_bait_override ORDER BY fish_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FishBaitOverride, 0, 64)
	for rows.Next() {
		var o FishBaitOverride
		var allow, deny, bottom string
		var enabled int
		if err := rows.Scan(&o.FishKey, &o.FishZh, &allow, &deny, &bottom, &o.Condition,
			&enabled, &o.UpdatedBy, &o.UpdatedTs); err != nil {
			return nil, err
		}
		o.Allow = decodeStringList(allow)
		o.Deny = decodeStringList(deny)
		o.BottomOnly = decodeStringList(bottom)
		o.Enabled = enabled != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

// SaveFishBaitOverride 写入或替换一个鱼种的覆盖。
// FishKey 为空时按 FishZh 反查补齐，兼容只传中文名的旧调用方。
func (d *DB) SaveFishBaitOverride(ctx context.Context, o FishBaitOverride, by string) error {
	if strings.TrimSpace(o.FishKey) == "" {
		key, name, err := d.resolveFishKeyByName(ctx, o.FishZh)
		if err != nil {
			return err
		}
		o.FishKey, o.FishZh = key, name
	}
	enabled := int64(0)
	if o.Enabled {
		enabled = 1
	}
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO fish_bait_override(fish_key,fish_zh,allow_json,deny_json,bottom_json,condition,
			enabled,updated_by,updated_ts)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(fish_key) DO UPDATE SET
			fish_zh=excluded.fish_zh,
			allow_json=excluded.allow_json, deny_json=excluded.deny_json,
			bottom_json=excluded.bottom_json, condition=excluded.condition,
			enabled=excluded.enabled,
			updated_by=excluded.updated_by, updated_ts=excluded.updated_ts`,
		o.FishKey, strings.TrimSpace(o.FishZh),
		encodeStringList(o.Allow), encodeStringList(o.Deny), encodeStringList(o.BottomOnly),
		strings.TrimSpace(o.Condition), enabled, by, float64(time.Now().Unix()))
	return err
}

// DeleteFishBaitOverride 删除一个鱼种的覆盖（恢复默认）。
func (d *DB) DeleteFishBaitOverride(ctx context.Context, fishKey string) (int64, error) {
	res, err := d.SQL.ExecContext(ctx, `DELETE FROM fish_bait_override WHERE fish_key=?`, fishKey)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteAllFishBaitOverrides 删除全部覆盖（整表恢复默认）。
func (d *DB) DeleteAllFishBaitOverrides(ctx context.Context) (int64, error) {
	res, err := d.SQL.ExecContext(ctx, `DELETE FROM fish_bait_override`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// LookupFishKeyByName 按中文名反查 fish_key，供只传名字的旧接口兼容。
func (d *DB) LookupFishKeyByName(ctx context.Context, fishZh string) (string, string, error) {
	return d.resolveFishKeyByName(ctx, fishZh)
}

// resolveFishKeyByName 按中文名反查 fish_key，兼容尚未切到主键的调用方。
//
// 两段式：先按原名精确命中（绝大多数调用方传的就是库里的名字），未命中再
// 去掉 BOM / NBSP / 全角空格与括号后逐行比较——与 00083 迁移同一口径。
//
// 注意不能把规范化写进 SQL 再用参数比较：`? AS norm ... WHERE norm = ?` 比的是
// 同一个占位符，恒真，会静默返回 loc_wiki_fish 的第一行、把覆盖写到别的鱼种上。
func (d *DB) resolveFishKeyByName(ctx context.Context, fishZh string) (string, string, error) {
	raw := strings.TrimSpace(fishZh)
	norm := normalizeFishName(fishZh)
	if norm == "" {
		return "", "", errFishKeyRequired
	}
	var key, name string
	err := d.SQL.QueryRowContext(ctx, `
		SELECT fish_key, name FROM (
			SELECT fish_key, fish_zh AS name FROM loc_wiki_fish WHERE fish_zh = ?
			UNION ALL
			SELECT fish_key, name_zh AS name FROM loc_fish WHERE name_zh = ?
		) LIMIT 1`, raw, raw).Scan(&key, &name)
	if err == nil {
		return key, name, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT fish_key, name FROM (
			SELECT fish_key, fish_zh AS name FROM loc_wiki_fish
			UNION ALL
			SELECT fish_key, name_zh AS name FROM loc_fish
			WHERE name_zh IS NOT NULL AND name_zh <> ''
		)`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var k, n string
		if rows.Scan(&k, &n) == nil && normalizeFishName(n) == norm {
			return k, n, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	return "", "", sql.ErrNoRows
}

// normalizeFishName 与 00083 迁移同一口径：去 BOM、NBSP、全角空格与括号。
func normalizeFishName(s string) string {
	r := strings.NewReplacer(
		"\ufeff", "", "\u00a0", "", "\u3000", "",
		"(", "", ")", "", "（", "", "）", "",
	)
	return strings.TrimSpace(r.Replace(s))
}

func encodeStringList(v []string) string {
	clean := make([]string, 0, len(v))
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			clean = append(clean, s)
		}
	}
	b, _ := json.Marshal(clean)
	return string(b)
}

func decodeStringList(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}
