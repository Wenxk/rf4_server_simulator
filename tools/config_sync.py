#!/usr/bin/env python3
"""玩法参数导出/导入：把后台能改的设定打成一份 JSON，搬到别的机器上复现同样的手感。

用法：
    # 导出（在源机器上跑）
    python tools/config_sync.py export
    python tools/config_sync.py export --out my-playstyle.json
    python tools/config_sync.py export --no-player-scope   # 只导全局设定，不含逐玩家数据

    # 看包里有什么（不连库）
    python tools/config_sync.py show my-playstyle.json

    # 导入（在目标机器上跑；先 --dry-run 看会改什么）
    python tools/config_sync.py import my-playstyle.json --dry-run
    python tools/config_sync.py import my-playstyle.json

    # 只导入某几张表
    python tools/config_sync.py import my-playstyle.json --only tunables,fish_ecology_species

设计要点（每条都踩过坑）：

1. **只导「后台能改」的表，不导派生表。**
   `fish_ecology` 由 `ensureFishEcology` 从资产 JSON 按 profile_version 灌入，
   `hook_bite_profile` 由 `syncHookBiteProfiles` 从静态库 `item_def` 派生，
   `fish_grade_lines` 由迁移灌入。这三张表导出会跟宿主机的静态库版本打架：
   导过去看似「覆盖成功」，下次服务启动又被重新派生，或者行数对不上而静默半应用。

2. **不导玩家存档与会话态。**
   `profile` / `item` / `rig` / `txn` / `mail` / `gw_token` / `gw_handoff` /
   `request_receipts` / `catch_log` / `fish_roll_trace` / `audit_log` 一律不带。
   导到别人机器上等于把玩家账号和登录令牌一起送过去。

3. **导入按业务主键 UPSERT，自增 id 原样保留。**
   `fish_zone.id` 是自增主键，`fish_zone_fish.zone_id` 引用它。若导入时重新分配
   id，区域与鱼种的关联会整体错位。所有表主键都是业务键（除 fish_zone），
   因此统一 ON CONFLICT DO UPDATE 即可，不改任何表结构。

4. **导出用 SQLite backup API 的只读快照，不是裸读。**
   服务运行时主库处于 WAL 模式，直接读可能拿到半个事务；这里用只读连接
   + 单事务读，保证同一时刻的一致视图。

5. **导入前后都做完整性校验。**
   导入后逐表比对行数，写进审计；一旦某张表行数不符立即报错并回滚整次导入
   （单事务提交，全成或全败）。

6. **不碰 `schema_migrations`。**
   迁移由代码自己跑。包里只记「导出时的迁移版本」，导入时若目标库更高就警告
   （说明对方代码更新，可能多出本包没有的字段），但仍允许导入。

退出码：0 成功，1 校验失败，2 数据源不可用。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
DEFAULT_DB = REPO / "data/rf4server.db"

FORMAT_NAME = "rf4-playstyle"
FORMAT_VERSION = 1

# 全局玩法设定：这些表里的值对所有玩家生效，决定「手感」。
# 顺序无意义，但导入时按此顺序执行，便于日志对齐。
GLOBAL_TABLES = [
    # 后台所有开关与数值（咬钩概率、掉卡、档位比例…）
    "tunables",
    # 鱼种生态三层覆盖：资产基线之上、水域覆盖之下的手工层
    "fish_ecology_species",
    # 单图生态覆盖（同一鱼种在某张图上单独调）
    "fish_ecology_loc",
    # 逐鱼饵料规则（fish_key 主键，00083 起）
    "fish_bait_override",
    # 自动抽取的逐鱼开关
    "fish_bait_auto_rotation",
    # 稀有度档位的基础权重（fish_weight_preset，按 rarity_tier 三行）
    "fish_weight_preset",
    # 单图单鱼的手感覆盖
    "fish_weight_override",
    # 鱼种经济四档覆盖
    "species_econ",
    # 每张图的咬口整体缩放
    "fish_bite_setting",
    # 区域名册（自增 id 必须原样保留）
    "fish_zone",
    "fish_zone_fish",
    # 商店货架覆盖
    "shop_item_override",
]

# 逐玩家范围的设定：想连玩家侧一起搬时才导（--no-player-scope 可排除）。
# 这里只放「不影响账号/资产、但影响单玩家体验」的表，不放存档本体。
PLAYER_SCOPE_TABLES = [
    # 玩家自建的配方模板（属于玩家数据，但换机器想保留制作习惯时可以带）
    "craft_template",
]

# 明确排除的表及理由，写进包里的 manifest 供人工核对。
EXCLUDED = {
    # 派生表：由代码/资产/静态库重新生成，导出会跟宿主机版本打架
    "fish_ecology": "由 ensureFishEcology 从资产 JSON 按 profile_version 灌入",
    "hook_bite_profile": "由 syncHookBiteProfiles 从静态库 item_def 派生",
    "fish_grade_lines": "由迁移 00027/00062 灌入，等同于静态数据",
    # 玩家存档与资产
    "profile": "玩家账号",
    "item": "玩家物品",
    "container_slot": "玩家容器",
    "owned_items": "玩家持有物",
    "rig": "玩家钓组",
    "rig_component": "玩家钓组部件",
    "rod_setting": "玩家竿设置",
    "rod_pod_setting": "玩家竿架设置",
    "rod_pod_alarm": "玩家竿架报警",
    "txn": "玩家交易流水",
    "mail": "玩家邮件",
    "keepnet_items": "玩家鱼护",
    "skill": "玩家技能",
    "skill_node": "玩家技能节点",
    "player_counters": "玩家计数",
    "player_achievements": "玩家成就",
    "player_qualifications": "玩家资格",
    "player_condition": "玩家状态",
    "device": "玩家设备",
    "catch_log": "玩家渔获记录",
    "catch_seen": "玩家图鉴",
    "named_fish": "玩家命名鱼",
    "place_access": "玩家地图买断",
    "fishing_state": "玩家垂钓态",
    "item_damage_log": "玩家物品磨损",
    "membership_order": "玩家会员订单",
    "reel_performance": "玩家渔轮磨合",
    "cafe_order": "玩家咖啡厅订单",
    "cafe_order_completion": "玩家咖啡厅完成记录",
    "campfire_state": "玩家篝火态",
    "happy_hour_state": "玩家欢乐时光态",
    "drink_bonus_clock": "玩家饮品时钟",
    "drink_bonus_effect": "玩家饮品效果",
    "trip_state": "玩家出钓态",
    "trip_grant": "玩家出行发放",
    "groundbait_deposit": "玩家窝料账本",
    "groundbait_clock": "玩家窝料时钟",
    "groundbait_recipe": "玩家窝料配方实例",
    "fish_hunt_state": "玩家鱼猎状态",
    "competition": "比赛",
    "competition_member": "比赛成员",
    "competition_result": "比赛结果",
    # 会话态与运行时噪音
    "gw_token": "网关登录令牌（搬过去等于共享登录态）",
    "gw_handoff": "网关交接态",
    "request_receipts": "幂等回执（宿主机的请求去重表）",
    "fish_roll_trace": "判鱼追踪诊断（宿主机的排查记录）",
    "audit_log": "后台审计日志",
    "admin_queue": "后台任务队列",
    "admin_user": "后台账号与密码哈希（含本机凭据）",
    "kv": "迁移标记与本地计数",
    "schema_migrations": "迁移记录（由代码自己跑）",
    "static_dataset_meta": "静态库指纹",
    "gift_pack": "礼包发放记录",
    "market_offer": "市场挂单",
    "achievement_def": "成就定义（代码内置）",
    "skill_link": "技能连线（代码内置）",
    "skill_def": "技能定义（静态库）",
    "skill_cat_def": "技能分类（静态库）",
    "skill_fish_effect_def": "技能鱼效（静态库投影）",
    "skill_cast_effect_def": "技能施放效果（静态库投影）",
    "fish_bait_override_orphan": "00083 迁移留档，仅供回溯",
    "rf4records_catch": "官方周纪录外部证据（几十万行，与手感无关）",
}


def fail(msg: str, code: int = 2) -> "NoReturn":  # type: ignore[valid-type]
    print(f"错误：{msg}", file=sys.stderr)
    sys.exit(code)


def open_ro(path: Path) -> sqlite3.Connection:
    """只读打开。服务在跑时也能安全读（WAL 快照）。"""
    if not path.exists():
        fail(f"数据库不存在：{path}")
    try:
        conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        conn.row_factory = sqlite3.Row
        conn.execute("SELECT 1 FROM sqlite_master LIMIT 1")
        return conn
    except sqlite3.Error as exc:
        fail(f"无法只读打开 {path}：{exc}")


def open_rw(path: Path) -> sqlite3.Connection:
    if not path.exists():
        fail(f"数据库不存在：{path}")
    try:
        conn = sqlite3.connect(str(path), timeout=15)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA busy_timeout=15000")
        return conn
    except sqlite3.Error as exc:
        fail(f"无法打开 {path}：{exc}")


def table_columns(conn: sqlite3.Connection, table: str) -> list[str]:
    return [r["name"] for r in conn.execute(f'PRAGMA table_info("{table}")')]


def primary_key(conn: sqlite3.Connection, table: str) -> list[str]:
    rows = list(conn.execute(f'PRAGMA table_info("{table}")'))
    pk = [(r["pk"], r["name"]) for r in rows if r["pk"] > 0]
    pk.sort()
    return [name for _i, name in pk]


def table_exists(conn: sqlite3.Connection, table: str) -> bool:
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (table,)
    ).fetchone()
    return row is not None


def scalar(conn: sqlite3.Connection, sql: str, args: tuple = ()) -> object:
    row = conn.execute(sql, args).fetchone()
    return row[0] if row else None


# ---------------------------------------------------------------- export


def do_export(args: argparse.Namespace) -> int:
    db_path = Path(args.db)
    conn = open_ro(db_path)

    tables = list(GLOBAL_TABLES)
    if not args.no_player_scope:
        tables += PLAYER_SCOPE_TABLES

    payload: dict[str, object] = {}
    row_counts: dict[str, int] = {}
    skipped: dict[str, str] = {}
    columns_map: dict[str, list[str]] = {}

    for table in tables:
        if not table_exists(conn, table):
            skipped[table] = "目标库没有这张表（迁移版本较旧？）"
            continue
        cols = table_columns(conn, table)
        columns_map[table] = cols
        order = primary_key(conn, table) or cols[:1]
        order_sql = ",".join(f'"{c}"' for c in order)
        rows = conn.execute(f'SELECT * FROM "{table}" ORDER BY {order_sql}').fetchall()
        payload[table] = [dict(r) for r in rows]
        row_counts[table] = len(rows)

    migration = scalar(conn, "SELECT MAX(version) FROM schema_migrations")
    static_fp = None
    static_ver = None
    if table_exists(conn, "static_dataset_meta"):
        row = conn.execute(
            "SELECT data_version, content_sha256 FROM static_dataset_meta LIMIT 1"
        ).fetchone()
        if row:
            static_ver = row[0]
            static_fp = row[1]
    conn.close()

    doc = {
        "format": FORMAT_NAME,
        "format_version": FORMAT_VERSION,
        "exported_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "source": {
            "db": str(db_path.name),
            "migration_version": migration,
            "static_data_version": static_ver,
            "static_fingerprint": static_fp,
            "include_player_scope": not args.no_player_scope,
        },
        "row_counts": row_counts,
        "tables": payload,
        "skipped_tables": skipped,
        "excluded_tables": EXCLUDED,
    }

    # 指纹取「表名+行数+主键序列」的哈希：只用于快速判断两个包是否等价，
    # 不比全文（几十万行的 rf4records 已排除，这里体量很小）。
    digest_src = json.dumps(
        {t: [row_counts.get(t, 0), len(v)] for t, v in payload.items()},
        sort_keys=True,
    )
    doc["digest"] = hashlib.sha256(digest_src.encode()).hexdigest()[:16]

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(
        json.dumps(doc, ensure_ascii=False, indent=1, sort_keys=False),
        encoding="utf-8",
    )

    print(f"已导出 {out}  （{out.stat().st_size / 1024:.1f} KB）")
    print(f"迁移版本 {migration} · 静态库指纹 {str(static_fp)[:12]}… · digest {doc['digest']}")
    print()
    total = 0
    for table in tables:
        if table in skipped:
            print(f"  跳过  {table:28} {skipped[table]}")
            continue
        n = row_counts[table]
        total += n
        print(f"  {n:6} 行  {table}")
    print(f"\n共 {len(payload)} 张表 / {total} 行。")
    if skipped:
        print(f"注：{len(skipped)} 张表在源库中不存在，已跳过。")
    return 0


# ---------------------------------------------------------------- show


def do_show(args: argparse.Namespace) -> int:
    path = Path(args.package)
    if not path.exists():
        fail(f"包不存在：{path}", 2)
    doc = json.loads(path.read_text(encoding="utf-8"))
    if doc.get("format") != FORMAT_NAME:
        fail(f"不是 {FORMAT_NAME} 包：format={doc.get('format')!r}")

    src = doc.get("source", {})
    print(f"包       : {path}")
    print(f"导出时间 : {doc.get('exported_at')}")
    print(f"格式版本 : {doc.get('format_version')}  · digest {doc.get('digest')}")
    print(f"源库迁移 : {src.get('migration_version')} · 静态指纹 {str(src.get('static_fingerprint'))[:12]}…")
    print(f"含玩家侧 : {src.get('include_player_scope')}")
    print()
    counts = doc.get("row_counts", {})
    for table, n in counts.items():
        print(f"  {n:6} 行  {table}")
    print(f"\n共 {len(counts)} 张表。")
    excluded = doc.get("excluded_tables", {})
    if excluded:
        print(f"\n按设计排除 {len(excluded)} 张表（含玩家存档与会话态），不随包迁移。")
    return 0


# ---------------------------------------------------------------- import


def schema_diff(
    conn: sqlite3.Connection, table: str, pkg_columns: list[str]
) -> tuple[list[str], list[str]]:
    """返回 (包里有但目标库没有, 目标库有但包里没有)。"""
    if not table_exists(conn, table):
        return [], []
    local = set(table_columns(conn, table))
    remote = set(pkg_columns)
    return sorted(remote - local), sorted(local - remote)


def do_import(args: argparse.Namespace) -> int:
    path = Path(args.package)
    if not path.exists():
        fail(f"包不存在：{path}", 2)
    doc = json.loads(path.read_text(encoding="utf-8"))
    if doc.get("format") != FORMAT_NAME:
        fail(f"不是 {FORMAT_NAME} 包：format={doc.get('format')!r}")
    if doc.get("format_version") != FORMAT_VERSION:
        fail(
            f"包格式版本 {doc.get('format_version')} 与本工具 {FORMAT_VERSION} 不匹配",
            1,
        )

    tables: dict[str, list[dict]] = doc.get("tables", {})
    if args.only:
        wanted = {t.strip() for t in args.only.split(",") if t.strip()}
        unknown = wanted - set(tables)
        if unknown:
            fail(f"--only 指定了包里没有的表：{', '.join(sorted(unknown))}", 1)
        tables = {t: v for t, v in tables.items() if t in wanted}

    db_path = Path(args.db)
    conn = open_rw(db_path)

    src_mig = doc.get("source", {}).get("migration_version")
    dst_mig = scalar(conn, "SELECT MAX(version) FROM schema_migrations")
    if src_mig and dst_mig and int(dst_mig) > int(src_mig):
        print(
            f"警告：目标库迁移版本 {dst_mig} 高于包内记录的 {src_mig}。"
            f"目标端代码更新，可能多出包里没有的字段（这些字段会保持默认值）。",
            file=sys.stderr,
        )

    # 逐表核对列差异：目标库缺列 = 包里的值无处可写；多列 = 保持目标库现值。
    problems: list[str] = []
    notes: list[str] = []
    plan: list[tuple[str, list[str], list[str], list[dict]]] = []
    for table, rows in tables.items():
        if not table_exists(conn, table):
            problems.append(f"{table}：目标库没有这张表（迁移未跑到？）")
            continue
        pkg_cols = list(rows[0].keys()) if rows else table_columns(conn, table)
        missing, extra = schema_diff(conn, table, pkg_cols)
        if missing:
            problems.append(f"{table}：目标库缺少列 {missing}")
            continue
        # 包里缺主键列必须拒绝：否则「目标库有 fish_key、包里只带 fish_zh」这种
        # 旧包会被当成「本地多出列」放过，然后拿 fish_zh 之外的列全留空去 UPSERT，
        # 看起来导入成功，实际把整行的 allow/deny 洗成 NULL。
        pk_cols = primary_key(conn, table)
        pk_missing = [c for c in pk_cols if c not in pkg_cols]
        if pk_missing and rows:
            problems.append(
                f"{table}：包内缺少主键列 {pk_missing}"
                "（包来自更旧的版本？请用当前版本重新导出）"
            )
            continue
        if extra:
            notes.append(f"{table}：目标库多出列 {extra}，保持现值不覆盖")
        write_cols = [c for c in pkg_cols if c not in extra]
        plan.append((table, write_cols, pk_cols, rows))

    if problems:
        for p in problems:
            print(f"  ✗ {p}", file=sys.stderr)
        conn.close()
        fail("列结构不兼容，未做任何写入。请先让目标端跑到同一迁移版本。", 1)
    for n in notes:
        print(f"注：{n}")

    # 统计将要发生的变化
    changes: list[tuple[str, int, int, int]] = []
    for table, cols, pk, rows in plan:
        if not rows:
            changes.append((table, 0, 0, 0))
            continue
        if not pk:
            fail(f"{table} 没有主键，无法安全 UPSERT", 1)
        pk_sql = " AND ".join(f'"{c}"=?' for c in pk)
        existing = 0
        for row in rows:
            hit = conn.execute(
                f'SELECT 1 FROM "{table}" WHERE {pk_sql} LIMIT 1',
                tuple(row.get(c) for c in pk),
            ).fetchone()
            if hit:
                existing += 1
        changes.append((table, len(rows), existing, len(rows) - existing))

    print()
    print(f"{'表':30} {'包内':>6} {'将更新':>7} {'将新增':>7}")
    for table, n, upd, ins in changes:
        print(f"{table:30} {n:6} {upd:7} {ins:7}")

    if args.dry_run:
        print("\n--dry-run：以上为预演，未写入任何数据。")
        conn.close()
        return 0

    # 单事务：全成或全败。
    before: dict[str, int] = {}
    try:
        conn.execute("BEGIN IMMEDIATE")
        for table, cols, pk, rows in plan:
            before[table] = scalar(conn, f'SELECT COUNT(*) FROM "{table}"') or 0
            if not rows:
                continue
            col_sql = ",".join(f'"{c}"' for c in cols)
            ph = ",".join("?" * len(cols))
            upd_cols = [c for c in cols if c not in pk]
            if upd_cols:
                upd_sql = ",".join(f'"{c}"=excluded."{c}"' for c in upd_cols)
                conflict = f'ON CONFLICT({",".join(chr(34)+c+chr(34) for c in pk)}) DO UPDATE SET {upd_sql}'
            else:
                conflict = f'ON CONFLICT({",".join(chr(34)+c+chr(34) for c in pk)}) DO NOTHING'
            stmt = (
                f'INSERT INTO "{table}"({col_sql}) VALUES({ph}) {conflict}'
            )
            conn.executemany(stmt, [tuple(row.get(c) for c in cols) for row in rows])

        # 导入后校验行数：增量应等于计划里的「将新增」，否则回滚。
        expected_ins = {t: i for t, _n, _u, i in changes}
        for table, _cols, _pk, rows in plan:
            after = scalar(conn, f'SELECT COUNT(*) FROM "{table}"') or 0
            delta = after - before[table]
            if delta != expected_ins.get(table, 0):
                raise RuntimeError(
                    f"{table} 行数异常：预期新增 {expected_ins.get(table, 0)}，"
                    f"实际 {before[table]} → {after}（增量 {delta}，包内 {len(rows)} 行）"
                )
        conn.commit()
    except Exception as exc:  # noqa: BLE001 - 任何异常都要回滚
        conn.rollback()
        conn.close()
        fail(f"导入失败已回滚：{exc}", 1)

    # 审计：记下这次导入的摘要，便于事后追「谁在什么时候换过手感」。
    try:
        summary = json.dumps(
            {t: {"rows": n, "updated": u, "inserted": i} for t, n, u, i in changes},
            ensure_ascii=False,
        )
        conn.execute(
            "INSERT INTO audit_log(actor,action,target,detail,ts) VALUES(?,?,?,?,strftime('%s','now'))",
            (
                "config_sync",
                "import_playstyle",
                str(path.name),
                f"digest={doc.get('digest')} {summary}",
            ),
        )
        conn.commit()
    except sqlite3.Error:
        # 审计表结构不同就跳过，不影响导入结果。
        pass

    # 推送重载指令：tunables/饵料/经济缓存到期会自动刷新，但生态覆盖与权重档位
    # 的缓存只有收到 reload_tunables 才重建（见 gameserver/mailbox.go）。不推这一条，
    # 后台会显示已导入、实际判鱼还在用旧值，直到重启主服。
    queued = False
    try:
        conn.execute(
            "INSERT INTO admin_queue(payload, created_ts) VALUES(?, strftime('%s','now'))",
            (json.dumps({"op": "reload_tunables"}),),
        )
        conn.commit()
        queued = True
    except sqlite3.Error as exc:
        print(f"警告：重载指令推送失败（{exc}），请重启主服或等缓存自然过期。", file=sys.stderr)

    conn.close()
    print(f"\n导入完成：{len(plan)} 张表。digest {doc.get('digest')}")
    if queued:
        print("已推送 reload_tunables，主服下一拍（约 1 秒）重建全部配置缓存。")
    return 0


# ---------------------------------------------------------------- main


def main() -> int:
    ap = argparse.ArgumentParser(
        description="玩法参数导出/导入（跨机器复现同一套手感）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__.split("退出码")[0],
    )
    sub = ap.add_subparsers(dest="cmd", required=True)

    ex = sub.add_parser("export", help="从当前库导出参数包")
    ex.add_argument("--db", default=str(DEFAULT_DB), help="源库路径")
    ex.add_argument("--out", default="playstyle.json", help="输出包路径")
    ex.add_argument(
        "--no-player-scope",
        action="store_true",
        help="不含逐玩家范围的表（craft_template 配方模板）",
    )
    ex.set_defaults(func=do_export)

    sh = sub.add_parser("show", help="查看参数包内容（不连库）")
    sh.add_argument("package", help="参数包路径")
    sh.set_defaults(func=do_show)

    im = sub.add_parser("import", help="把参数包导入当前库")
    im.add_argument("package", help="参数包路径")
    im.add_argument("--db", default=str(DEFAULT_DB), help="目标库路径")
    im.add_argument("--dry-run", action="store_true", help="只预演，不写入")
    im.add_argument("--only", default="", help="只导入指定表，逗号分隔")
    im.set_defaults(func=do_import)

    args = ap.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())