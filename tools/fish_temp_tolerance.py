"""容差 → 区分力：给适温表定填写精度。

tolerance = max(2.2, (max-min)/2)，也就是说 max-min 只要 >=4.4，容差就等于半宽。
容差越小，thermal 曲线越尖，温度越有筛选力，但触及 0.15 下限的样本会变多。

本脚本对一批代表鱼扫描容差，输出：
  - 平均权重（越高说明整体活跃不被压）
  - 权重极差（越大说明温度越能区分）
  - 上限占比（到达 1.0 的比例，越高说明「鱼在合适温度时满血」）

只读，不写库。
"""

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

# 代表鱼：冷 / 温 / 暖，含跨图最多的几条
SAMPLE = ["burbot", "a_char", "pike", "n.pike", "perch", "bream",
          "s.orfe", "e.pikeperch", "asp", "c.carp", "w_catfish", "sterlet"]


def thermal(temp, pref, tol):
    z = (temp - pref) / tol
    return 0.35 + 0.65 * math.exp(-0.5 * z * z)


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

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
    cur.execute("SELECT fish_key, temp_pref_c FROM fish_ecology")
    pref = {r["fish_key"]: r["temp_pref_c"] for r in cur.fetchall()}

    avail = [k for k in SAMPLE if k in pref and len(fm.get(k, ())) >= 2]
    print(f"样本鱼 {len(avail)} 条：{', '.join(avail)}\n")
    print("容差 tol（℃）→ 温度权重表现（全部为 鱼×图×季节 逐时样本的平均）")
    print(f"{'tol':>6}{'平均权重':>10}{'平均极差':>10}{'最低权重':>10}"
          f"{'达1.0占比':>10}{'压到0.15占比':>13}")

    for tol in (1.5, 2.0, 2.2, 3.0, 4.0, 5.0, 6.0, 8.0, 10.0):
        means, ranges, los, tops, floors, n = [], [], [], 0, 0, 0
        allw = []
        for key in avail:
            p = pref[key]
            per = []
            for loc in fm[key]:
                mid = zh2id.get(loc)
                if not mid:
                    continue
                for t in water[mid]:
                    w = thermal(t, p, tol)
                    per.append(w)
                    allw.append(w)
                    if w >= 0.9999:
                        tops += 1
                    if w <= 0.1501:
                        floors += 1
                    n += 1
            if per:
                means.append(sum(per) / len(per))
                ranges.append(max(per) - min(per))
                los.append(min(per))
        print(f"{tol:>6.1f}{sum(means)/len(means):>10.3f}{sum(ranges)/len(ranges):>10.3f}"
              f"{min(los):>10.3f}{tops/n*100:>9.1f}%{floors/n*100:>12.1f}%")

    print()
    print("对照：当前库内基线（tol = max(2.2, 半宽)，半宽约 8~10℃ → tol 8~10）")
    print("       即温度几乎不起作用，全库权重均值 0.88、极差 0.26")


if __name__ == "__main__":
    main()