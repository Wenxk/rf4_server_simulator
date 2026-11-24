"""扫描「适温窗口宽度」对筛选力的影响。

对每条鱼，以 temp_pref 为中心、给定半宽展开窗口，统计它在出没图上的
平均命中率与最低命中率。用来回答：窗口该多宽，温度才既有筛选力、
又不会把鱼判成钓不到。

只读，不写库。
"""

import json
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, load_map, water_at  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {}
    for k, v in d["by_map_key"].items():
        if isinstance(v, dict) and v.get("map"):
            zh2id[v["map"]] = k

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    water = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        water[name] = [v for s in (0, 1, 2, 3)
                       for v in (water_at(m, day, h, s, 1.0, 0.45)[1]
                                 for day in range(8) for h in range(24))]

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    fm = {}
    for r in cur.fetchall():
        fm.setdefault(r["fish_key"], set()).add(r["loc"])
    cur.execute("SELECT fish_key, temp_min_c, temp_pref_c, temp_max_c FROM fish_ecology")
    base = {r["fish_key"]: (r["temp_min_c"], r["temp_pref_c"], r["temp_max_c"])
            for r in cur.fetchall()}

    print("窗口宽度扫描（以 temp_pref_c 为中心，±半宽；仅统计跨图鱼 77 条）")
    print(f"{'宽度℃':>7}{'平均覆盖':>10}{'最低图覆盖':>11}{'<2%的对数':>11}{'<10%的对数':>12}{'完全覆盖对的占比':>16}")

    for half in (3, 4, 5, 6, 7, 8, 9, 10, 12, 14, 16):
        all_rates, dead, near, full = [], 0, 0, 0
        pairs = 0
        for key, locs in fm.items():
            if key not in base or len(locs) < 2:
                continue
            pref = base[key][1]
            lo, hi = pref - half, pref + half
            for loc in locs:
                mid = zh2id.get(loc)
                if not mid:
                    continue
                vals = water[mid]
                rate = sum(1 for v in vals if lo <= v <= hi) / len(vals)
                all_rates.append(rate)
                pairs += 1
                if rate < 0.02:
                    dead += 1
                elif rate < 0.10:
                    near += 1
                if rate > 0.995:
                    full += 1
        avg = sum(all_rates) / len(all_rates)
        print(f"{half*2:>7}{avg*100:>9.1f}%{min(all_rates)*100:>10.1f}%"
              f"{dead:>11}{near:>12}{full/pairs*100:>15.1f}%")

    print()
    print("参考：当前基线窗口宽度按鱼不同（冷水 16.7℃ / 暖水 21℃，覆盖 60~78%）")
    print("参考：全库水温总跨度 24.3℃，单图全年跨度 14~21℃")


if __name__ == "__main__":
    main()