#!/usr/bin/env python3
"""把主库里的「调参」数据导出一份 JSON 留底。

只导设置，不导玩家数据与派生缓存：
  - 含：tunables、权重预设/覆盖、咬口设置、饵料覆盖、自动轮换开关、生态/鱼区水域覆盖
  - 不含：profile / admin_user / catch_log / request_receipts / item* 等业务与账号数据

用法：
    python tools/snapshot_tuning.py                       # 默认读 data/rf4server.db
    python tools/snapshot_tuning.py --db other.db --out snapshots/tuning-2026-11-20.json

输出结构固定（键顺序不变），方便两份快照直接 diff。
"""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import subprocess
import sys
from datetime import datetime, timezone

# 表名 → 说明。顺序即输出顺序，便于人工比对与 diff。
SECTIONS = [
    ("tunables", "后台「游戏参数」全部键值（唯一真源，缺省值在代码里）"),
    ("fish_weight_preset", "稀有度档位权重预设（普通/稀有/超稀有）"),
    ("fish_weight_override", "按水域 × 鱼种的人工覆盖权重"),
    ("fish_bite_setting", "按水域的咬口参数覆盖"),
    ("fish_bait_override", "按鱼种的饵料规则覆盖（轮换组/自动抽取）"),
    ("fish_bait_auto_rotation", "逐鱼「自动抽取」开关"),
    ("fish_ecology_loc", "按水域的生态覆盖（水层/温度/昼夜曲线等）"),
    ("fish_zone", "区域固定鱼种配置"),
    ("fish_zone_fish", "区域 × 鱼种明细"),
]


def read_table(conn: sqlite3.Connection, table: str) -> tuple[list[str], list[dict]]:
    cur = conn.execute(f'SELECT * FROM "{table}"')
    cols = [d[0] for d in cur.description]
    rows = [dict(zip(cols, r)) for r in cur.fetchall()]
    return cols, rows


def git_commit() -> str:
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True, text=True, cwd=os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
        return out.stdout.strip() if out.returncode == 0 else ""
    except Exception:
        return ""


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=os.path.join("data", "rf4server.db"))
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        print(f"找不到数据库: {args.db}", file=sys.stderr)
        return 1

    conn = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row

    schema_max = conn.execute("SELECT COALESCE(MAX(version),0) FROM schema_migrations").fetchone()[0]

    sections: dict[str, object] = {}
    counts: dict[str, int] = {}
    for table, desc in SECTIONS:
        try:
            cols, rows = read_table(conn, table)
        except sqlite3.OperationalError as e:
            # 老库可能还没这张表（迁移未跑），记录为空而不是整份导出失败。
            print(f"跳过 {table}: {e}", file=sys.stderr)
            sections[table] = {"desc": desc, "columns": [], "rows": []}
            counts[table] = 0
            continue
        rows.sort(key=lambda r: tuple(str(r.get(c, "")) for c in cols[:2]))
        sections[table] = {"desc": desc, "columns": cols, "rows": rows}
        counts[table] = len(rows)

    # tunables 额外摊平成 key→value，方便只看"改过什么"。
    flat_tunables = {}
    for r in sections["tunables"]["rows"]:  # type: ignore[index]
        flat_tunables[r["key"]] = r.get("value")

    payload = {
        "generated_at": datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds"),
        "git_commit": git_commit(),
        "db": os.path.basename(args.db),
        "schema_version": schema_max,
        "counts": counts,
        "tunables": flat_tunables,
        "sections": sections,
    }

    out = args.out or os.path.join("snapshots", f"tuning-{datetime.now().strftime('%Y-%m-%d')}.json")
    os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
    with open(out, "w", encoding="utf-8", newline="\n") as f:
        json.dump(payload, f, ensure_ascii=False, indent=2, sort_keys=False)
        f.write("\n")

    print(f"已导出 {out}（{os.path.getsize(out)} 字节）")
    for table, _ in SECTIONS:
        print(f"  {table:24s} {counts[table]:>6d} 行")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
