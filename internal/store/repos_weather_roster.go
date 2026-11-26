package store

import "context"

// WeatherRosterEcology 是天气预览页「活跃鱼种」需要的单条鱼种生态数据。
//
// 字段全部是**已合并水域覆盖**后的生效值（COALESCE(覆盖, 全局)），
// 与判鱼侧 fishEcologyFor 的合并顺序一致：先取全局 fish_ecology，
// 再用 fish_ecology_loc 里非空的字段覆盖。后台若拿全局值算活跃度，
// 在生态弹窗里改过昼夜/适温的鱼会显示成没生效。
type WeatherRosterEcology struct {
	FishKey   string
	NameZh    string
	Rarity    int
	DielMask  int64
	DielCurve string
	TempMinC  float64
	TempPrefC float64
	TempMaxC  float64
}

// WeatherRosterFor 返回某水域的名册 + 生态数据，供天气预览页算活跃鱼种。
//
// **map_key 必须先经 loc_alias 换成中文地名**：`loc_wiki_fish.loc` 与 `loc_fish.loc`
// 存的是「克马羚诺也湖」这类中文地名，不是 `level_001_lake`。直接拿 map_key 去关联
// 会得到 0 行（曾经就是这么错的，测试里手插了 level_xxx 才假通过）。
//
// 名册来源与判鱼侧一致：LocRoster（loc_wiki_fish 全集，缺失时回退 loc_fish）。
// 只保留在 fish_ecology 里有生态档案的鱼——没有档案就算不出昼夜与适温，
// 强行列出来只能给个假分数。这类鱼（通常是被删档或还没配生态的）在判鱼里
// 也走的是回退路径，后台不该把它们算成「活跃」。
func (d *DB) WeatherRosterFor(ctx context.Context, statLoc string) ([]WeatherRosterEcology, error) {
	if d == nil || d.SQL == nil || statLoc == "" {
		return nil, nil
	}
	// map_key → 中文地名。loc_wiki_fish / loc_fish 的 loc 列存的是中文名，
	// 拿 level_xxx 直接关联会是 0 行。
	var alias string
	_ = d.SQL.QueryRowContext(ctx,
		`SELECT COALESCE(NULLIF(loc,''),'') FROM loc_alias WHERE alias=? LIMIT 1`, statLoc).Scan(&alias)
	if alias == "" {
		alias = statLoc // 没配别名的图（如海图）沿用原名，至少不会静默返回空
	}
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT e.fish_key,
		       COALESCE(NULLIF(w.fish_zh,''), NULLIF(f.name_zh,''), e.fish_key),
		       COALESCE(e.rarity_tier, 0),
		       COALESCE(o.diel_mask, e.diel_mask, 0),
		       COALESCE(o.diel_curve, e.diel_curve, ''),
		       COALESCE(o.temp_min_c, e.temp_min_c, 0),
		       COALESCE(o.temp_pref_c, e.temp_pref_c, 0),
		       COALESCE(o.temp_max_c, e.temp_max_c, 0)
		FROM fish_ecology e
		LEFT JOIN fish_ecology_loc o
		       ON o.stat_loc = ? AND o.fish_key = e.fish_key
		LEFT JOIN loc_wiki_fish w
		       ON w.loc = ? AND w.fish_key = e.fish_key
		LEFT JOIN loc_fish f
		       ON f.loc = ? AND f.fish_key = e.fish_key
		WHERE EXISTS (SELECT 1 FROM loc_wiki_fish w2 WHERE w2.loc = ? AND w2.fish_key = e.fish_key)
		   OR EXISTS (SELECT 1 FROM loc_fish f2 WHERE f2.loc = ? AND f2.fish_key = e.fish_key AND f2.catches > 0)
		ORDER BY e.fish_key`, statLoc, alias, alias, alias, alias)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WeatherRosterEcology
	for rows.Next() {
		var r WeatherRosterEcology
		if err := rows.Scan(&r.FishKey, &r.NameZh, &r.Rarity,
			&r.DielMask, &r.DielCurve, &r.TempMinC, &r.TempPrefC, &r.TempMaxC); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
