"""列出跨图鱼种及其温度包络，用于定位真实适温与地图水温的结构冲突。

只看出没 >=2 张图的鱼（单图鱼不存在跨图一致性矛盾）。
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
    id2zh, zh2id = {}, {}
    for k, v in d["by_map_key"].items():
        if isinstance(v, dict) and v.get("map"):
            id2zh[k] = v["map"]
            zh2id[v["map"]] = k

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    # 每图全年水温区间（四季 + 逐时，activity=1.0, layer=0.45）
    span = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        vals = [water_at(m, day, h, s, 1.0, 0.45)[1]
                for day in range(8) for h in range(24) for s in (0, 1, 2, 3)]
        span[id2zh.get(name, name)] = (min(vals), max(vals))

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    fm = {}
    for r in cur.fetchall():
        fm.setdefault(r["fish_key"], set()).add(r["loc"])

    cur.execute("SELECT fish_key, temp_min_c, temp_pref_c, temp_max_c, rarity_tier FROM fish_ecology")
    base = {r["fish_key"]: (r["temp_min_c"], r["temp_pref_c"], r["temp_max_c"], r["rarity_tier"])
            for r in cur.fetchall()}

    cur.execute("SELECT fish_key, fish_zh FROM loc_wiki_fish")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    rows = []
    for key, locs in fm.items():
        if len(locs) < 2 or key not in base:
            continue
        env_lo = min(span[l][0] for l in locs if l in span)
        env_hi = max(span[l][1] for l in locs if l in span)
        bmin, bpref, bmax, rar = base[key]
        rows.append((env_hi - env_lo, key, zh.get(key, "-"), len(locs), env_lo, env_hi, bmin, bmax, rar, locs))

    rows.sort(reverse=True)
    print(f"跨图鱼 {len(rows)} 条（出没 >=2 张图）\n")
    print(f"{'fish':<18}{'zh':<12}{'n':>3}{'env_lo':>8}{'env_hi':>8}{'env_span':>9}"
          f"{'base_lo':>9}{'base_hi':>9}{'base_span':>10}  地图")
    for sp, key, name, n, lo, hi, bmin, bmax, rar, locs in rows:
        print(f"{key:<18}{name:<12}{n:>3}{lo:>8.1f}{hi:>8.1f}{sp:>9.1f}"
              f"{bmin:>9.1f}{bmax:>9.1f}{bmax-bmin:>10.1f}  {'/'.join(sorted(locs))}")

    print()
    worst = rows[0]
    print(f"环境跨度最大：{worst[1]}（{worst[2]}）出没 {worst[3]} 图，"
          f"水温 {worst[4]:.1f}~{worst[5]:.1f}℃ 跨度 {worst[0]:.1f}℃")


if __name__ == "__main__":
    main()