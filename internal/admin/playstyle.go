package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// 玩法参数导出/导入（跨机同步手感）。
//
// 与 tools/config_sync.py 是同一套口径，两边必须一起改：
//   - 导出的表清单见 playstyleTables / playstylePlayerTables；
//   - 排除理由见 playstyleExcluded，包里的 excluded_tables 会原样带出去，
//     让拿到包的人知道「为什么这些表不在里面」；
//   - 派生表绝对不能导：fish_ecology 由 ensureFishEcology 按 profile_version
//     重灌、hook_bite_profile 由 syncHookBiteProfiles 从 item_def 派生、
//     fish_grade_lines 由迁移灌入。导过去看似覆盖成功，服务下次启动又被
//     重新派生，或行数对不上而静默半应用。
//
// 导入按业务主键 UPSERT（不清表重灌），单事务提交，导入后核对新增行数。

const (
	playstyleFormatName    = "rf4-playstyle"
	playstyleFormatVersion = 1
)

// playstyleTables 是全局玩法设定表：这些值对所有玩家生效。
var playstyleTables = []string{
	"tunables",
	"fish_ecology_species",
	"fish_ecology_loc",
	"fish_bait_override",
	"fish_bait_auto_rotation",
	"fish_weight_preset",
	"fish_weight_override",
	"species_econ",
	"fish_bite_setting",
	"fish_zone",
	"fish_zone_fish",
	"shop_item_override",
}

// playstylePlayerTables 是逐玩家范围、但只影响使用体验的表。
// 想连配方模板一起搬时用；默认也带上（与 Python 工具的默认一致）。
var playstylePlayerTables = []string{"craft_template"}

// playstyleExcluded 说明哪些用户表按设计不进包。
var playstyleExcluded = map[string]string{
	"fish_ecology":       "由 ensureFishEcology 从资产 JSON 按 profile_version 灌入",
	"hook_bite_profile":  "由 syncHookBiteProfiles 从静态库 item_def 派生",
	"fish_grade_lines":   "由迁移 00027/00062 灌入，等同于静态数据",
	"profile":            "玩家账号",
	"item":               "玩家物品",
	"container_slot":     "玩家容器",
	"owned_items":        "玩家持有物",
	"rig":                "玩家钓组",
	"rig_component":      "玩家钓组部件",
	"rod_setting":        "玩家竿设置",
	"rod_pod_setting":    "玩家竿架设置",
	"rod_pod_alarm":      "玩家竿架报警",
	"txn":                "玩家交易流水",
	"mail":               "玩家邮件",
	"keepnet_items":      "玩家鱼护",
	"skill":              "玩家技能",
	"skill_node":         "玩家技能节点",
	"player_counters":    "玩家计数",
	"player_achievements": "玩家成就",
	"player_qualifications": "玩家资格",
	"player_condition":   "玩家状态",
	"device":             "玩家设备",
	"catch_log":          "玩家渔获记录",
	"catch_seen":         "玩家图鉴",
	"named_fish":         "玩家命名鱼",
	"place_access":       "玩家地图买断",
	"fishing_state":      "玩家垂钓态",
	"item_damage_log":    "玩家物品磨损",
	"membership_order":   "玩家会员订单",
	"reel_performance":   "玩家渔轮磨合",
	"cafe_order":         "玩家咖啡厅订单",
	"cafe_order_completion": "玩家咖啡厅完成记录",
	"campfire_state":     "玩家篝火态",
	"happy_hour_state":   "玩家欢乐时光态",
	"drink_bonus_clock":  "玩家饮品时钟",
	"drink_bonus_effect": "玩家饮品效果",
	"trip_state":         "玩家出钓态",
	"trip_grant":         "玩家出行发放",
	"groundbait_deposit": "玩家窝料账本",
	"groundbait_clock":   "玩家窝料时钟",
	"groundbait_recipe":  "玩家窝料配方实例",
	"fish_hunt_state":    "玩家鱼猎状态",
	"competition":        "比赛",
	"competition_member": "比赛成员",
	"competition_result": "比赛结果",
	"gw_token":           "网关登录令牌（搬过去等于共享登录态）",
	"gw_handoff":         "网关交接态",
	"request_receipts":   "幂等回执（宿主机的请求去重表）",
	"fish_roll_trace":    "判鱼追踪诊断（宿主机的排查记录）",
	"audit_log":          "后台审计日志",
	"admin_queue":        "后台任务队列",
	"admin_user":         "后台账号与密码哈希（含本机凭据）",
	"kv":                 "迁移标记与本地计数",
	"schema_migrations":  "迁移记录（由代码自己跑）",
	"static_dataset_meta": "静态库指纹",
	"gift_pack":          "礼包发放记录",
	"market_offer":       "市场挂单",
	"achievement_def":    "成就定义（代码内置）",
	"skill_link":         "技能连线（代码内置）",
	"skill_def":          "技能定义（静态库）",
	"skill_cat_def":      "技能分类（静态库）",
	"skill_fish_effect_def": "技能鱼效（静态库投影）",
	"skill_cast_effect_def": "技能施放效果（静态库投影）",
	"fish_bait_override_orphan": "00083 迁移留档，仅供回溯",
	"rf4records_catch":   "官方周纪录外部证据（几十万行，与手感无关）",
}

type playstylePackage struct {
	Format          string                     `json:"format"`
	FormatVersion   int                        `json:"format_version"`
	ExportedAt      string                     `json:"exported_at"`
	Source          playstyleSource            `json:"source"`
	RowCounts       map[string]int             `json:"row_counts"`
	Tables          map[string][]map[string]any `json:"tables"`
	SkippedTables   map[string]string          `json:"skipped_tables,omitempty"`
	ExcludedTables  map[string]string          `json:"excluded_tables"`
	Digest          string                     `json:"digest"`
}

type playstyleSource struct {
	DB             string `json:"db"`
	MigrationVer   any    `json:"migration_version"`
	StaticDataVer  any    `json:"static_data_version"`
	StaticFingerprint any `json:"static_fingerprint"`
	IncludePlayerScope bool `json:"include_player_scope"`
}

// playstylePlan 是导入的一步：把 rows 写进 table，只写 writeCols。
type playstylePlan struct {
	table     string
	writeCols []string
	pk        []string
	rows      []map[string]any
	updated   int
	inserted  int
}

// handlePlaystyleExport 生成参数包并直接作为附件下发。
func (s *Server) handlePlaystyleExport(w http.ResponseWriter, r *http.Request) {
	pkg, err := s.buildPlaystylePackage(r)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(r.Context(), adminOf(r), "playstyle_export", "package",
		fmt.Sprintf("digest=%s tables=%d", pkg.Digest, len(pkg.Tables)))

	name := "playstyle-" + time.Now().Format("20060102-1504") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	_ = enc.Encode(pkg)
}

// handlePlaystyleImport 接收包体，dryRun 时只预演。
func (s *Server) handlePlaystyleImport(w http.ResponseWriter, r *http.Request) {
	// 包是几百 KB 的小 JSON，限 8 MB 足够，也挡住误传整库。
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	dryRun := r.URL.Query().Get("dry_run") == "1"
	only := strings.TrimSpace(r.URL.Query().Get("only"))

	var pkg playstylePackage
	if err := json.NewDecoder(r.Body).Decode(&pkg); err != nil {
		httpError(w, 400, "包体不是合法 JSON："+err.Error())
		return
	}
	if pkg.Format != playstyleFormatName {
		httpError(w, 400, fmt.Sprintf("不是 %s 包：format=%q", playstyleFormatName, pkg.Format))
		return
	}
	if pkg.FormatVersion != playstyleFormatVersion {
		httpError(w, 400, fmt.Sprintf("包格式版本 %d 与本服务 %d 不匹配",
			pkg.FormatVersion, playstyleFormatVersion))
		return
	}

	tables := pkg.Tables
	if only != "" {
		want := map[string]struct{}{}
		for _, t := range strings.Split(only, ",") {
			if t = strings.TrimSpace(t); t != "" {
				want[t] = struct{}{}
			}
		}
		var unknown []string
		for t := range want {
			if _, ok := tables[t]; !ok {
				unknown = append(unknown, t)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			httpError(w, 400, "指定了包里没有的表："+strings.Join(unknown, ", "))
			return
		}
		filtered := map[string][]map[string]any{}
		for t := range want {
			filtered[t] = tables[t]
		}
		tables = filtered
	}

	plan, notes, err := s.planPlaystyleImport(r, tables)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}

	resp := map[string]any{
		"ok":       true,
		"dry_run":  dryRun,
		"digest":   pkg.Digest,
		"notes":    notes,
		"tables":   planSummary(plan),
		"warnings": s.playstyleWarnings(r, pkg),
	}
	if dryRun {
		writeJSON(w, resp)
		return
	}

	if err := s.applyPlaystyleImport(r, plan); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(r.Context(), adminOf(r), "playstyle_import", "package",
		fmt.Sprintf("digest=%s %s", pkg.Digest, planSummary(plan)))

	// 生态覆盖与权重档位的缓存只有收到 reload_tunables 才重建
	// （gameserver/mailbox.go）。不推这一条，后台显示已导入、实际判鱼
	// 还在用旧值，直到重启主服。
	_ = s.db.QueuePush(r.Context(), mustJSON(map[string]any{"op": "reload_tunables"}))
	resp["reloaded"] = true
	writeJSON(w, resp)
}

func planSummary(plan []playstylePlan) []map[string]any {
	out := make([]map[string]any, 0, len(plan))
	for _, p := range plan {
		out = append(out, map[string]any{
			"table": p.table, "rows": len(p.rows),
			"updated": p.updated, "inserted": p.inserted,
		})
	}
	return out
}

// playstyleWarnings 在目标库迁移版本更高时提示：目标端代码更新，
// 可能多出包里没有的字段，这些字段会保持默认值。
func (s *Server) playstyleWarnings(r *http.Request, pkg playstylePackage) []string {
	var notes []string
	src, ok := toInt(pkg.Source.MigrationVer)
	if !ok {
		return notes
	}
	var dst int
	if err := s.db.SQL.QueryRowContext(r.Context(),
		`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&dst); err == nil {
		if dst > src {
			notes = append(notes, fmt.Sprintf(
				"目标库迁移版本 %d 高于包内记录的 %d：目标端代码更新，包里没有的字段会保持默认值", dst, src))
		}
	}
	return notes
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// buildPlaystylePackage 只读地打包全部配置表。
func (s *Server) buildPlaystylePackage(r *http.Request) (*playstylePackage, error) {
	ctx := r.Context()
	tables := append([]string{}, playstyleTables...)
	tables = append(tables, playstylePlayerTables...)

	pkg := &playstylePackage{
		Format:         playstyleFormatName,
		FormatVersion:  playstyleFormatVersion,
		ExportedAt:     time.Now().UTC().Format(time.RFC3339),
		RowCounts:      map[string]int{},
		Tables:         map[string][]map[string]any{},
		SkippedTables:  map[string]string{},
		ExcludedTables: playstyleExcluded,
		Source:         playstyleSource{DB: "rf4server.db", IncludePlayerScope: true},
	}

	var mig any
	if err := s.db.SQL.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&mig); err == nil {
		pkg.Source.MigrationVer = mig
	}
	var dv, fp any
	if err := s.db.SQL.QueryRowContext(ctx,
		`SELECT data_version, content_sha256 FROM static_dataset_meta LIMIT 1`).Scan(&dv, &fp); err == nil {
		pkg.Source.StaticDataVer = dv
		pkg.Source.StaticFingerprint = fp
	}

	for _, table := range tables {
		if !s.tableExists(ctx, table) {
			pkg.SkippedTables[table] = "目标库没有这张表（迁移版本较旧？）"
			continue
		}
		cols, err := s.tableColumns(ctx, table)
		if err != nil {
			return nil, err
		}
		order := s.primaryKey(ctx, table)
		if len(order) == 0 {
			order = cols[:1]
		}
		quoted := make([]string, len(order))
		for i, c := range order {
			quoted[i] = quoteIdent(c)
		}
		rows, err := s.db.SQL.QueryContext(ctx,
			`SELECT * FROM `+quoteIdent(table)+` ORDER BY `+strings.Join(quoted, ","))
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", table, err)
		}
		records, err := scanRowsToMaps(rows, cols)
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", table, err)
		}
		pkg.Tables[table] = records
		pkg.RowCounts[table] = len(records)
	}

	pkg.Digest = playstyleDigest(pkg.RowCounts)
	return pkg, nil
}

// playstyleDigest 只对「表名→行数」求哈希：用于快速判断两个包是否等价，
// 不比全文（体量小，但也够用来在审计里对齐同一次导出）。
func playstyleDigest(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%d;", k, counts[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

func (s *Server) tableExists(ctx context.Context, table string) bool {
	var n int
	err := s.db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name=?`, table).Scan(&n)
	return err == nil && n > 0
}

func (s *Server) tableColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.SQL.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func (s *Server) primaryKey(ctx context.Context, table string) []string {
	rows, err := s.db.SQL.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type pkCol struct {
		idx  int
		name string
	}
	var pks []pkCol
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil
		}
		if pk > 0 {
			pks = append(pks, pkCol{pk, name})
		}
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].idx < pks[j].idx })
	out := make([]string, len(pks))
	for i, p := range pks {
		out[i] = p.name
	}
	return out
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func scanRowsToMaps(rows *sql.Rows, cols []string) ([]map[string]any, error) {
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rec := make(map[string]any, len(cols))
		for i, c := range cols {
			v := vals[i]
			// []byte 直接 JSON 化会变 base64；配置表里没有二进制列，
			// 一律按文本处理更符合预期。
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			rec[c] = v
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// planPlaystyleImport 核对列结构并算出每表会改多少行，不写库。
func (s *Server) planPlaystyleImport(r *http.Request, tables map[string][]map[string]any) ([]playstylePlan, []string, error) {
	ctx := r.Context()
	var problems, notes []string
	var plan []playstylePlan

	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)

	for _, table := range names {
		rows := tables[table]
		if !s.tableExists(ctx, table) {
			problems = append(problems, table+"：目标库没有这张表（迁移未跑到？）")
			continue
		}
		local, err := s.tableColumns(ctx, table)
		if err != nil {
			return nil, nil, err
		}
		var pkgCols []string
		if len(rows) > 0 {
			for c := range rows[0] {
				pkgCols = append(pkgCols, c)
			}
			sort.Strings(pkgCols)
		} else {
			pkgCols = local
		}
		localSet := map[string]struct{}{}
		for _, c := range local {
			localSet[c] = struct{}{}
		}
		var missing, extra []string
		for _, c := range pkgCols {
			if _, ok := localSet[c]; !ok {
				missing = append(missing, c)
			}
		}
		pkgSet := map[string]struct{}{}
		for _, c := range pkgCols {
			pkgSet[c] = struct{}{}
		}
		for _, c := range local {
			if _, ok := pkgSet[c]; !ok {
				extra = append(extra, c)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			problems = append(problems, fmt.Sprintf("%s：目标库缺少列 %v", table, missing))
			continue
		}
		// 包里缺主键列必须拒绝。否则「目标库有 fish_key、包里只带 fish_zh」这种
		// 旧包会被当成「本地多出列」放过，然后拿 fish_zh 之外的列全留空去 UPSERT——
		// 看起来导入成功，实际把整行的 allow/deny 洗成 NULL。
		pkCols := s.primaryKey(ctx, table)
		var pkMissing []string
		for _, c := range pkCols {
			if _, ok := pkgSet[c]; !ok {
				pkMissing = append(pkMissing, c)
			}
		}
		if len(pkMissing) > 0 && len(rows) > 0 {
			problems = append(problems, fmt.Sprintf(
				"%s：包内缺少主键列 %v（包来自更旧的版本？请用当前版本重新导出）", table, pkMissing))
			continue
		}
		if len(extra) > 0 {
			notes = append(notes, fmt.Sprintf("%s：目标库多出列 %v，保持现值不覆盖", table, extra))
		}
		writeCols := make([]string, 0, len(pkgCols))
		for _, c := range pkgCols {
			if _, ok := pkgSet[c]; ok {
				writeCols = append(writeCols, c)
			}
		}
		pk := s.primaryKey(ctx, table)
		if len(rows) > 0 && len(pk) == 0 {
			return nil, nil, fmt.Errorf("%s 没有主键，无法安全 UPSERT", table)
		}

		step := playstylePlan{table: table, writeCols: writeCols, pk: pk, rows: rows}
		if len(rows) > 0 {
			where := make([]string, len(pk))
			for i, c := range pk {
				where[i] = quoteIdent(c) + "=?"
			}
			q := `SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE ` + strings.Join(where, " AND ")
			for _, row := range rows {
				args := make([]any, len(pk))
				for i, c := range pk {
					args[i] = row[c]
				}
				var n int
				if err := s.db.SQL.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
					return nil, nil, err
				}
				if n > 0 {
					step.updated++
				} else {
					step.inserted++
				}
			}
		}
		plan = append(plan, step)
	}

	if len(problems) > 0 {
		return nil, nil, fmt.Errorf(
			"列结构不兼容，未做任何写入。请先让目标端跑到同一迁移版本。\n%s",
			strings.Join(problems, "\n"))
	}
	return plan, notes, nil
}

// applyPlaystyleImport 单事务写入；任意一表行数异常就整体回滚。
func (s *Server) applyPlaystyleImport(r *http.Request, plan []playstylePlan) error {
	ctx := r.Context()
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	before := map[string]int{}
	for _, step := range plan {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+quoteIdent(step.table)).Scan(&n); err != nil {
			return fmt.Errorf("读取 %s 行数: %w", step.table, err)
		}
		before[step.table] = n
		if len(step.rows) == 0 {
			continue
		}
		colSQL := make([]string, len(step.writeCols))
		for i, c := range step.writeCols {
			colSQL[i] = quoteIdent(c)
		}
		ph := make([]string, len(step.writeCols))
		for i := range ph {
			ph[i] = "?"
		}
		var conflict string
		pkSQL := make([]string, len(step.pk))
		for i, c := range step.pk {
			pkSQL[i] = quoteIdent(c)
		}
		upd := make([]string, 0, len(step.writeCols))
		for _, c := range step.extraUpdatable() {
			upd = append(upd, quoteIdent(c)+`=excluded.`+quoteIdent(c))
		}
		if len(upd) > 0 {
			conflict = `ON CONFLICT(` + strings.Join(pkSQL, ",") + `) DO UPDATE SET ` + strings.Join(upd, ",")
		} else {
			conflict = `ON CONFLICT(` + strings.Join(pkSQL, ",") + `) DO NOTHING`
		}
		stmt := `INSERT INTO ` + quoteIdent(step.table) + `(` + strings.Join(colSQL, ",") + `) VALUES(` +
			strings.Join(ph, ",") + `) ` + conflict
		args := make([][]any, len(step.rows))
		for i, row := range step.rows {
			vals := make([]any, len(step.writeCols))
			for j, c := range step.writeCols {
				vals[j] = row[c]
			}
			args[i] = vals
		}
		for _, v := range args {
			if _, err := tx.ExecContext(ctx, stmt, v...); err != nil {
				return fmt.Errorf("写入 %s: %w", step.table, err)
			}
		}
	}

	// 行数校验：增量必须等于计划里的「将新增」。
	for _, step := range plan {
		var after int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+quoteIdent(step.table)).Scan(&after); err != nil {
			return err
		}
		if delta := after - before[step.table]; delta != step.inserted {
			return fmt.Errorf("%s 行数异常：预期新增 %d，实际 %d → %d",
				step.table, step.inserted, before[step.table], after)
		}
	}
	return tx.Commit()
}

// extraUpdatable 返回除主键外的写入列（主键不参与 DO UPDATE）。
func (p playstylePlan) extraUpdatable() []string {
	pkSet := map[string]struct{}{}
	for _, c := range p.pk {
		pkSet[c] = struct{}{}
	}
	out := make([]string, 0, len(p.writeCols))
	for _, c := range p.writeCols {
		if _, ok := pkSet[c]; !ok {
			out = append(out, c)
		}
	}
	return out
}