"""评估鱼种适温区间在现有水温体系下的可达性。

对每条鱼：取其出没地图（loc_fish 静态名册），按四季度算该图的逐时水温，
统计「鱼种适温窗口 ∩ 图水温范围」的命中比例。
用途：在把文献适温写进 fish_ecology_species 之前，先看清哪些鱼会被判成稀有。

只读，不写库。
"""

import json
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import (  # noqa: E402
    MAP_DEFS, SEASON_NAMES, load_map, water_at,
)

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")


def map_id_to_zh():
    d = json.load(open(MULT, encoding="utf-8"))
    out = {}
    for k, v in d["by_map_key"].items():
        if isinstance(v, dict) and v.get("map"):
            out[k] = v["map"]
    return out


def map_water_table():
    """每张图 × 每季节 × 逐时的水温样本。"""
    d = json.load(open(MAP_DEFS, encoding="utf-8"))
    out = {}
    for name, node in d["maps"].items():
        m = load_map(node, name)
        seasons = {}
        for s in (0, 1, 2, 3):
            vals = [water_at(m, day, h, s, 1.0, 0.45)[1]
                    for day in range(8) for h in range(24)]
            seasons[s] = vals
        out[name] = seasons
    return out


def main():
    id2zh = map_id_to_zh()
    zh2id = {v: k for k, v in id2zh.items()}
    water = map_water_table()

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()

    # 鱼 -> 出没图
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    fish_maps = {}
    for r in cur.fetchall():
        fish_maps.setdefault(r["fish_key"], set()).add(r["loc"])

    # 鱼种基线
    cur.execute("SELECT fish_key, temp_min_c, temp_pref_c, temp_max_c FROM fish_ecology")
    base = {r["fish_key"]: (r["temp_min_c"], r["temp_pref_c"], r["temp_max_c"])
            for r in cur.fetchall()}

    cur.execute("""SELECT fish_key, fish_zh FROM loc_wiki_fish""")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    print(f"{'fish':<20}{'zh':<12}{'maps':>5}{'wmin':>7}{'wmax':>7}"
          f"{'span':>7}{'cover_base':>11}{'cover_true':>11}")
    rows = []
    for key, locs in sorted(fish_maps.items()):
        ids = [zh2id[l] for l in locs if l in zh2id]
        if not ids or key not in base:
            continue
        lo_all, hi_all = 1e9, -1e9
        hits_b, tot = 0, 0
        bmin, bpref, bmax = base[key]
        for mid in ids:
            for s in (0, 1, 2, 3):
                vals = water[mid][s]
                lo_all = min(lo_all, min(vals))
                hi_all = max(hi_all, max(vals))
                for v in vals:
                    tot += 1
                    if bmin <= v <= bmax:
                        hits_b += 1
        rows.append((key, zh.get(key, "-"), len(ids), lo_all, hi_all, bmax - bmin,
                     hits_b / tot if tot else 0))

    rows.sort(key=lambda r: r[6])
    for key, name, nmap, lo, hi, span, cov in rows:
        print(f"{key:<20}{name:<12}{nmap:>5}{lo:>7.1f}{hi:>7.1f}{span:>7.1f}"
              f"{cov*100:>10.1f}%{'':>11}")

    print()
    print(f"共 {len(rows)} 条鱼有出没图映射")
    # 冲突清单：鱼适温窗口整个落在图水温区间之外
    print()
    print("== 适温窗口与出没图水温几乎不重叠（覆盖率 <10%）==")
    bad = [r for r in rows if r[6] < 0.10]
    for key, name, nmap, lo, hi, span, cov in bad:
        bmin, bpref, bmax = base[key]
        print(f"  {key:<18}{name:<12} 窗口 {bmin:.1f}~{bmax:.1f}"
              f"  图水温 {lo:.1f}~{hi:.1f}  覆盖 {cov*100:.1f}%")
    print(f"  合计 {len(bad)} 条")


if __name__ == "__main__":
    main()