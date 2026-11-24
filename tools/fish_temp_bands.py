"""把每图水温按 2℃ 分档，统计每档有多少鱼能开口。

设计目标（2026-11-23 用户明确）：
  1) 稀有/超稀有鱼窗口窄 → 更难钓
  2) 其他鱼按地图温度区间动态调整适温
  3) 保证每个温度档都有鱼钓（重叠没关系）

本脚本给出「现状基线」的分档结果，作为改造前的对照。
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
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    wt = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        wt[name] = {s: [water_at(m, day, h, s, 1.0, 0.45)[1]
                        for day in range(8) for h in range(24)]
                    for s in (0, 1, 2, 3)}

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    roster = {}
    for r in cur.fetchall():
        roster.setdefault(r["loc"], set()).add(r["fish_key"])
    cur.execute("SELECT fish_key, temp_min_c, temp_max_c, rarity_tier FROM fish_ecology")
    base = {r["fish_key"]: (r["temp_min_c"], r["temp_max_c"], r["rarity_tier"])
            for r in cur.fetchall()}
    cur.execute("SELECT fish_key, fish_zh FROM loc_wiki_fish")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    print("现状基线：每图每 2℃ 档能开口的鱼数（窗口=库内 temp_min~max）\n")
    for loc, keys in sorted(roster.items(), key=lambda kv: -len(kv[1])):
        mid = zh2id.get(loc)
        if not mid:
            continue
        allw = [v for s in (0, 1, 2, 3) for v in wt[mid][s]]
        lo, hi = int(min(allw)), int(max(allw)) + 1
        # 该图水温在各季的覆盖
        print(f"== {loc}  水温 {min(allw):.1f}~{max(allw):.1f}℃  "
              f"{len(keys)} 种鱼")
        # 逐档
        line = []
        for t in range(lo, hi + 2, 2):
            mid_t = t + 1.0
            # 该档是否被该图某季覆盖
            covered = any(min(wt[mid][s]) <= mid_t <= max(wt[mid][s]) for s in (0, 1, 2, 3))
            n = sum(1 for k in keys
                    if k in base and base[k][0] <= mid_t <= base[k][1])
            mark = "" if covered else "·"
            line.append(f"{t:>3}℃:{n:>2}{mark}")
        print("   " + "  ".join(line))
        # 各季水温区间
        seas = "  ".join(f"{SN[s]}{min(wt[mid][s]):.0f}~{max(wt[mid][s]):.0f}"
                         for s in (0, 1, 2, 3))
        print(f"   {seas}")
        print()


if __name__ == "__main__":
    main()