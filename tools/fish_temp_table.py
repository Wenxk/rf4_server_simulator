"""从文献值生成建议入库值，并输出可审阅的对照表。

建议入库口径（实测甜点，见 docs/analysis-fish-temp-calibration-2026-11-23.md）：
  以 lit_pref 为中心取 ±HALF_WIDTH，再用文献耐受区间裁边。
  HALF_WIDTH=5.5 → 总宽 11℃，季节区分力 88~90%、跨图区分力 14%、
  最低图覆盖 14~18%，不产生钓不到的死条目。

判定侧公式（fishing_weather_fishing.go:15）：
  tolerance = max(2.2, (temp_max_c - temp_min_c)/2)
  thermal   = 0.35 + 0.65*exp(-0.5*z^2)，z=(water_temp - temp_pref_c)/tolerance
所以 temp_min_c/temp_max_c 只影响容差，temp_pref_c 才是鱼口中心。

用法：
  python tools/fish_temp_table.py                 # 打印 markdown 表
  python tools/fish_temp_table.py --csv out.csv   # 导出可导入的 csv
  python tools/fish_temp_table.py --diff          # 只列与库内基线差异大的
"""

import argparse
import csv
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_literature import PARENT, SPECIES  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")

HALF_WIDTH = 5.5
CONF_ORDER = {"明确": 0, "近缘": 1, "类群": 2, "变种": 3, "虚构": 4}


def resolve(key):
    """返回 (zh, lit_min, lit_max, lit_pref, conf, note)。变种跟随亲本。"""
    if key in SPECIES:
        return SPECIES[key]
    parent = PARENT.get(key)
    if not parent or parent not in SPECIES:
        return None
    zh, lo, hi, pref, conf, note = SPECIES[parent]
    return (zh + "(变种)", lo, hi, pref, "变种", "跟随亲本 " + parent)


def suggest(lit_min, lit_max, lit_pref):
    """以偏好值为中心取 ±HALF_WIDTH，用文献区间裁边。"""
    if lit_pref <= 0:
        return None  # 虚构鱼种：保留资产基线不动
    lo = max(lit_min, lit_pref - HALF_WIDTH)
    hi = min(lit_max, lit_pref + HALF_WIDTH)
    # 保证 min < pref < max 且宽度至少 4.4（否则容差被 2.2 下限吃掉）
    if hi - lo < 4.4:
        mid = (lo + hi) / 2
        lo, hi = mid - 2.2, mid + 2.2
    pref = max(lo + 0.1, min(hi - 0.1, lit_pref))
    return round(lo, 1), round(pref, 1), round(hi, 1)


def load_base():
    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("""SELECT e.fish_key,
        (SELECT w.fish_zh FROM loc_wiki_fish w WHERE w.fish_key=e.fish_key LIMIT 1) AS zh,
        e.temp_min_c, e.temp_pref_c, e.temp_max_c
        FROM fish_ecology e""")
    out = {}
    for r in cur.fetchall():
        out[r["fish_key"]] = (r["temp_min_c"], r["temp_pref_c"], r["temp_max_c"])
    return out


def build_rows():
    base = load_base()
    rows = []
    for key in sorted(base):
        info = resolve(key)
        if not info:
            continue
        zh, lit_min, lit_max, lit_pref, conf, note = info
        bl, bp, bh = base[key]
        sug = suggest(lit_min, lit_max, lit_pref)
        if sug is None:
            sl, sp, sh = bl, bp, bh  # 虚构鱼种保留基线
            note = note + "（保留基线）"
        else:
            sl, sp, sh = sug
        rows.append({
            "fish_key": key, "zh": zh,
            "lit_min": lit_min, "lit_max": lit_max, "lit_pref": lit_pref,
            "conf": conf, "note": note,
            "sug_min": sl, "sug_pref": sp, "sug_max": sh,
            "base_min": bl, "base_pref": bp, "base_max": bh,
            "d_pref": round(sp - bp, 1),
            "d_width": round((sh - sl) - (bh - bl), 1),
        })
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--csv", help="导出 csv 路径")
    ap.add_argument("--diff", action="store_true", help="只列偏好改动 >=3℃ 的")
    ap.add_argument("--conf", help="只看某类置信度")
    args = ap.parse_args()

    rows = build_rows()
    if args.conf:
        rows = [r for r in rows if r["conf"] == args.conf]
    if args.diff:
        rows = [r for r in rows if abs(r["d_pref"]) >= 3]
        rows.sort(key=lambda r: -abs(r["d_pref"]))

    if args.csv:
        with open(args.csv, "w", newline="", encoding="utf-8-sig") as f:
            w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
            w.writeheader()
            w.writerows(rows)
        print(f"已导出 {len(rows)} 行 → {args.csv}")
        return

    print(f"共 {len(rows)} 条；建议窗口：偏好 ±{HALF_WIDTH}℃（总宽 {HALF_WIDTH*2}℃）\n")
    hdr = ("| fish_key | 中文名 | 文献区间 | 文献偏好 | 置信 | "
           "建议 min | 建议 pref | 建议 max | 原 pref | Δpref | 依据 |")
    sep = "|---|---|---|---|---|---|---|---|---|---|---|"
    print(hdr)
    print(sep)
    for r in rows:
        print(f"| `{r['fish_key']}` | {r['zh']} | {r['lit_min']}~{r['lit_max']} | "
              f"{r['lit_pref']} | {r['conf']} | {r['sug_min']} | **{r['sug_pref']}** | "
              f"{r['sug_max']} | {r['base_pref']} | {r['d_pref']:+} | {r['note']} |")


if __name__ == "__main__":
    main()