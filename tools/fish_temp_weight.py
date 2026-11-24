"""按判鱼真实公式计算温度权重，评估候选适温表的实际效果。

关键：温度不是硬窗口判定，而是高斯型响应
    tolerance = max(2.2, (temp_max_c - temp_min_c) / 2)
    z         = (water_temp - temp_pref_c) / tolerance
    thermal   = 0.35 + 0.65 * exp(-0.5 * z^2)
最终 activity = clamp(... * thermal * ..., 0.15, 1.25)
下限 0.15 意味着任何鱼在任何水温下都不会真正归零。

只读，不写库。
"""

import argparse
import csv
import json
import math
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, load_map, water_at  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")
SN = ["春", "夏", "秋", "冬"]
FLOOR = 0.15


def thermal_weight(temp, pref, tmin, tmax):
    tol = max(2.2, (tmax - tmin) / 2.0)
    z = (temp - pref) / tol
    return 0.35 + 0.65 * math.exp(-0.5 * z * z)


def read_window(row):
    """兼容两套列名：temp_*_c（直接候选）/ sug_*（文献表导出）。"""
    if "temp_pref_c" in row:
        return (float(row["temp_min_c"]), float(row["temp_pref_c"]),
                float(row["temp_max_c"]))
    return (float(row["sug_min"]), float(row["sug_pref"]), float(row["sug_max"]))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--csv", help="候选适温表")
    ap.add_argument("--top", type=int, default=0)
    args = ap.parse_args()

    d = json.load(open(MULT, encoding="utf-8"))
    id2zh, zh2id = {}, {}
    for k, v in d["by_map_key"].items():
        if isinstance(v, dict) and v.get("map"):
            id2zh[k] = v["map"]
            zh2id[v["map"]] = k

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    water = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        water[name] = {s: [water_at(m, day, h, s, 1.0, 0.45)[1]
                           for day in range(8) for h in range(24)]
                       for s in (0, 1, 2, 3)}

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    fm = {}
    for r in cur.fetchall():
        fm.setdefault(r["fish_key"], set()).add(r["loc"])
    cur.execute("SELECT fish_key, temp_min_c, temp_pref_c, temp_max_c FROM fish_ecology")
    win = {r["fish_key"]: (r["temp_min_c"], r["temp_pref_c"], r["temp_max_c"])
           for r in cur.fetchall()}
    cur.execute("SELECT fish_key, fish_zh FROM loc_wiki_fish")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    used = "库内基线"
    if args.csv:
        n = 0
        for row in csv.DictReader(open(args.csv, encoding="utf-8-sig")):
            k = row["fish_key"].strip()
            if k in win:
                win[k] = read_window(row)
                n += 1
        used = f"候选表（覆盖 {n} 条）"

    print(f"温度数据来源：{used}")
    print("公式：tolerance=max(2.2,(max-min)/2)  thermal=0.35+0.65*exp(-0.5*z^2)  下限钳 0.15\n")

    # 全局：所有鱼×图×季节的平均 thermal / 极差 / 是否触及下限
    all_means, all_ranges, floors = [], [], 0
    pairs = 0
    detail = []
    for key, locs in fm.items():
        if key not in win:
            continue
        tmin, pref, tmax = win[key]
        tol = max(2.2, (tmax - tmin) / 2)
        per_map = {}
        for loc in locs:
            mid = zh2id.get(loc)
            if not mid:
                continue
            per_s = {}
            for s in (0, 1, 2, 3):
                ws = [thermal_weight(t, pref, tmin, tmax) for t in water[mid][s]]
                per_s[s] = sum(ws) / len(ws)
                if min(ws) <= FLOOR + 1e-9:
                    floors += 1
            per_map[loc] = per_s
            pairs += 1
        if not per_map:
            continue
        vals = [v for per_s in per_map.values() for v in per_s.values()]
        all_means.append(sum(vals) / len(vals))
        all_ranges.append(max(vals) - min(vals))
        detail.append((key, zh.get(key, "-"), tmin, pref, tmax, tol,
                       sum(vals) / len(vals), max(vals) - min(vals), per_map))

    print(f"鱼×图 组合 {pairs} 对；鱼 {len(detail)} 条")
    print(f"温度权重均值 {sum(all_means)/len(all_means):.3f}"
          f"   平均极差 {sum(all_ranges)/len(all_ranges):.3f}")
    print(f"触及下限 0.15 的样本数 {floors}（说明该温度下鱼被压到最低档）")
    print()

    # 区分力：同一鱼在不同图/季节的权重落差
    print("== 温度能否区分出鱼口？（按权重极差排序，越大越有区分力）==")
    sf = "h" if args.top == 0 else str(args.top)
    fmt = "{:<18}{:<12}{:>7}{:>7}{:>7}{:>7}{:>8}{:>8}"
    print(fmt.format("fish", "zh", "min", "pref", "max", "tol", "均值", "极差"))
    detail.sort(key=lambda r: r[7], reverse=True)
    rows = detail if args.top == 0 else detail[:args.top]
    for key, name, tmin, pref, tmax, tol, mean, rng, _ in rows:
        print(fmt.format(key, name, f"{tmin:.1f}", f"{pref:.1f}", f"{tmax:.1f}",
                         f"{tol:.1f}", f"{mean:.3f}", f"{rng:.3f}"))

    print()
    print("== 权重极差分布 ==")
    buckets = [(0, .05), (.05, .10), (.10, .20), (.20, .40), (.40, 1)]
    for lo, hi in buckets:
        n = sum(1 for r in detail if lo <= r[7] < hi)
        print(f"  极差 {lo:.2f}~{hi:.2f}: {n} 条")


if __name__ == "__main__":
    main()