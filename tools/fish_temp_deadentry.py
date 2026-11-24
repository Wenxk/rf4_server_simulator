"""死条目检测：按候选适温窗口，找出「名册里有、但该图水温够不到」的鱼。

对每条鱼 × 每张出没图，统计四季逐时水温里落在窗口内的比例。
比例接近 0 = 该图钓不到（死条目）；跨图差异过大 = 需要覆盖而非改窗口。

用法：
  python tools/fish_temp_deadentry.py                    # 用当前库内基线
  python tools/fish_temp_deadentry.py --csv propose.csv  # 用候选表
"""

import argparse
import csv
import json
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, load_map, water_at  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")
SEASON_NAMES = ["春", "夏", "秋", "冬"]


def load_water():
    d = json.load(open(MAP_DEFS, encoding="utf-8"))
    out = {}
    for name, node in d["maps"].items():
        m = load_map(node, name)
        out[name] = {s: [water_at(m, day, h, s, 1.0, 0.45)[1]
                         for day in range(8) for h in range(24)]
                     for s in (0, 1, 2, 3)}
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--csv", help="候选适温表 fish_key,temp_min_c,temp_pref_c,temp_max_c")
    ap.add_argument("--threshold", type=float, default=0.02,
                    help="该图命中率低于此值即视为死条目（默认 0.02）")
    args = ap.parse_args()

    d = json.load(open(MULT, encoding="utf-8"))
    id2zh, zh2id = {}, {}
    for k, v in d["by_map_key"].items():
        if isinstance(v, dict) and v.get("map"):
            id2zh[k] = v["map"]
            zh2id[v["map"]] = k

    water = load_water()

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    fm = {}
    for r in cur.fetchall():
        fm.setdefault(r["fish_key"], set()).add(r["loc"])

    cur.execute("SELECT fish_key, temp_min_c, temp_pref_c, temp_max_c FROM fish_ecology")
    win = {r["fish_key"]: (r["temp_min_c"], r["temp_max_c"]) for r in cur.fetchall()}

    if args.csv:
        n = 0
        for row in csv.DictReader(open(args.csv, encoding="utf-8-sig")):
            k = row["fish_key"].strip()
            if k in win:
                if "temp_min_c" in row:
                    win[k] = (float(row["temp_min_c"]), float(row["temp_max_c"]))
                else:
                    win[k] = (float(row["sug_min"]), float(row["sug_max"]))
                n += 1
        print(f"已用候选表覆盖 {n} 条窗口\n")

    cur.execute("SELECT fish_key, fish_zh FROM loc_wiki_fish")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    dead, near_dead = [], []
    total_pairs = 0
    for key, locs in fm.items():
        if key not in win:
            continue
        lo, hi = win[key]
        for loc in sorted(locs):
            mid = zh2id.get(loc)
            if not mid:
                continue
            total_pairs += 1
            hits, tot = 0, 0
            per_season = {}
            for s in (0, 1, 2, 3):
                vals = water[mid][s]
                h = sum(1 for v in vals if lo <= v <= hi)
                per_season[s] = h / len(vals)
                hits += h
                tot += len(vals)
            rate = hits / tot if tot else 0
            if rate < args.threshold:
                dead.append((key, zh.get(key, "-"), loc, lo, hi, rate, per_season))
            elif rate < 0.08:
                near_dead.append((key, zh.get(key, "-"), loc, lo, hi, rate, per_season))

    print(f"鱼×图组合共 {total_pairs} 对")
    print(f"死条目（命中率 <{args.threshold:.0%}）：{len(dead)} 对")
    print(f"临界（命中率 <8%）：{len(near_dead)} 对\n")

    if dead:
        print("== 死条目明细 ==")
        print(f"{'fish':<18}{'zh':<12}{'map':<14}{'窗口':>13}  四季命中")
        for key, name, loc, lo, hi, rate, ps in dead[:60]:
            seasons = " ".join(f"{SEASON_NAMES[s]}{ps[s]*100:3.0f}%" for s in (0, 1, 2, 3))
            print(f"{key:<18}{name:<12}{loc:<14}{lo:5.1f}~{hi:5.1f}%  {seasons}")
        if len(dead) > 60:
            print(f"  ... 另 {len(dead)-60} 对")

    if near_dead:
        print("\n== 临界（<8%，随时可能变死条目）==")
        seen = set()
        for key, name, loc, lo, hi, rate, ps in near_dead[:40]:
            seasons = " ".join(f"{SEASON_NAMES[s]}{ps[s]*100:3.0f}%" for s in (0, 1, 2, 3))
            print(f"{key:<18}{name:<12}{loc:<14}{lo:5.1f}~{hi:5.1f}%  {seasons}")


if __name__ == "__main__":
    main()