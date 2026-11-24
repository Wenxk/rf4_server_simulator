"""精确缺口：逐图逐鱼算「该鱼窗口下限 - 该图全年最高水温」。

只有结果 > 0 才是真正的死条目（这张图永远到不了这条鱼的适温）。
这是「调地图温度」还是「调鱼种适温」的判定依据：
  - 缺口 <= 3℃  → 抬该图日高温即可，优先调地图
  - 缺口 > 3℃   → 地图调到那个温度不现实，改调鱼种适温

只读，不写库。
"""

import json
import os
import sqlite3
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, load_map, water_at  # noqa: E402
from fish_temp_literature import PARENT, SPECIES  # noqa: E402
from fish_temp_mapair import air_for_water  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")
HALF = 5.5


def lit(key):
    if key in SPECIES:
        z, lo, hi, p, c, n = SPECIES[key]
        return z, lo, hi, p
    par = PARENT.get(key)
    if par in SPECIES:
        z, lo, hi, p, c, n = SPECIES[par]
        return z + "(变种)", lo, hi, p
    return None


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id, id2zh = {}, {}
    for k, v in d["by_map_key"].items():
        if isinstance(v, dict) and v.get("map"):
            zh2id[v["map"]] = k
            id2zh[k] = v["map"]

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    wmax = {}
    wmin = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        vals = [water_at(m, day, h, s, 1.0, 0.45)[1]
                for day in range(8) for h in range(24) for s in (0, 1, 2, 3)]
        wmax[name] = max(vals)
        wmin[name] = min(vals)

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    pairs = [(r["loc"], r["fish_key"]) for r in cur.fetchall()]

    rows = []
    for loc, key in pairs:
        mid = zh2id.get(loc)
        if not mid:
            continue
        info = lit(key)
        if not info:
            continue
        zh, litlo, lithi, pref = info
        lo = max(litlo, pref - HALF)
        hi = min(lithi, pref + HALF)
        need_heat = lo - wmax[mid]      # >0 = 该图太冷，鱼的下限够不到
        need_cold = wmin[mid] - hi      # >0 = 该图太暖，鱼的上限够不到
        if need_heat > 0 or need_cold > 0:
            rows.append((loc, mid, key, zh, lo, hi, wmax[mid], wmin[mid],
                         need_heat, need_cold))

    rows.sort(key=lambda r: -max(r[8], r[9]))

    print("=== 真正的死条目：该图全年水温到不了鱼的适温窗口 ===\n")
    print(f"{'地图':<12}{'鱼':<20}{'中文':<10}{'窗口':>13}{'图水温':>14}"
          f"{'缺热':>8}{'缺冷':>8}{'处置':>10}")
    for loc, mid, key, zh, lo, hi, mx, mn, nh, nc in rows:
        gap = max(nh, nc)
        if gap <= 3:
            act = "抬气温"
        elif gap <= 8:
            act = "权衡"
        else:
            act = "改鱼种"
        print(f"{loc:<12}{key:<20}{zh:<10}{lo:>6.1f}~{hi:<6.1f}"
              f"{mn:>6.1f}~{mx:<7.1f}{nh:>8.1f}{nc:>8.1f}{act:>10}")

    print(f"\n合计 {len(rows)} 对死条目，涉及 {len({r[2] for r in rows})} 种鱼、"
          f"{len({r[0] for r in rows})} 张图")

    if rows:
        print("\n=== 需要的日高温修正（仅列缺口<=3℃ 的，抬气温就能解决）===")
        by_map = {}
        for loc, mid, key, zh, lo, hi, mx, mn, nh, nc in rows:
            if 0 < nh <= 3:
                by_map.setdefault(loc, []).append((key, zh, lo, nh))
        for loc, items in by_map.items():
            mid = zh2id[loc]
            need_w = max(i[2] for i in items)
            need_air = air_for_water(need_w, 1)
            print(f"  {loc:<12} 需夏季水温 ≥{need_w:.1f}℃ → 夏季日高温需 ≥{need_air:.0f}℃"
                  f"（涉及 {', '.join(i[1] for i in items)}）")


if __name__ == "__main__":
    main()