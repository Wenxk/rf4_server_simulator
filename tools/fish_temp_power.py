"""窗口宽度 × 温度筛选力：判断温度能否在不同地图之间区分鱼口。

两组指标：
 A) 单图内：窗口能否随季节/时段产生明显出鱼差异（覆盖率的季节性落差）
 B) 跨图间：同一鱼在不同图的覆盖率差异（温度能否把「这图能出、那图不能出」表达出来）

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
SN = ["春", "夏", "秋", "冬"]


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
    cur.execute("SELECT fish_key, temp_pref_c FROM fish_ecology")
    pref = {r["fish_key"]: r["temp_pref_c"] for r in cur.fetchall()}

    print("窗口宽度 → 温度的实际区分力（77 条跨图鱼平均）")
    print()
    print(f"{'宽度':>6}{'季节落差均值':>13}{'季节落差中位':>13}"
          f"{'跨图落差均值':>13}{'跨图落差中位':>13}{'夏季均覆盖':>11}{'冬季均覆盖':>11}")

    for half in (3, 4, 5, 6, 7, 8, 10, 12):
        season_gaps, map_gaps, sum_cov, win_cov = [], [], [], []
        for key, locs in fm.items():
            if key not in pref or len(locs) < 2:
                continue
            p = pref[key]
            lo, hi = p - half, p + half
            seasonal_avg = []
            map_avg = []
            for loc in locs:
                mid = zh2id.get(loc)
                if not mid:
                    continue
                per = []
                for s in (0, 1, 2, 3):
                    v = water[mid][s]
                    per.append(sum(1 for x in v if lo <= x <= hi) / len(v))
                map_avg.append(sum(per) / 4)
                season_gaps.append(max(per) - min(per))
                seasonal_avg.append(per)
            if len(map_avg) >= 2:
                map_gaps.append(max(map_avg) - min(map_avg))
            # 夏季/冬季全图平均覆盖
            for i, s in enumerate((1, 3)):
                v = [c[i] for c in seasonal_avg]
                if v:
                    (sum_cov if s == 1 else win_cov).append(sum(v) / len(v))

        def med(x):
            x = sorted(x)
            return x[len(x) // 2] if x else 0

        print(f"{half*2:>6}{sum(season_gaps)/len(season_gaps)*100:>12.0f}%"
              f"{med(season_gaps)*100:>12.0f}%"
              f"{sum(map_gaps)/len(map_gaps)*100:>12.0f}%"
              f"{med(map_gaps)*100:>12.0f}%"
              f"{sum(sum_cov)/len(sum_cov)*100:>10.0f}%"
              f"{sum(win_cov)/len(win_cov)*100:>10.0f}%")


if __name__ == "__main__":
    main()