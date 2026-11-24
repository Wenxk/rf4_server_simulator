"""按「名册文献适温」抬高指定地图的日高温，让水温真正能覆盖该图鱼种的适温。

背景（2026-11-23 用户拍板）：
  部分地图（埃尔克湖、苏拉河、旧奥斯特罗格湖等）名册几乎是纯暖水鱼
  （鲈鱼、鲃鱼、鲤鱼，文献最适 18~25℃），但地图水温有 30~40% 的时间 ≤10℃。
  此时若强行「每档都有鱼」，只能把暖水鱼的最适温度压到冷端 10~14℃，
  等于改写了鱼种本身——用户否决这个做法，选择抬高地图气温。

做法：
  对每张图取名册鱼种文献 pref 的上四分位（避免被个别极端值拉飞），
  作为「该图水温上限应达到的温度」target_hi。
  二分搜索日高温整体抬升值 delta，使全水层水温最高值 >= target_hi。
  冷端不动（冬季/清晨本就该冷）。

用法：
  python tools/fish_temp_mapheat.py                 # 只报告
  python tools/fish_temp_mapheat.py --apply         # 写回 map_defs.json
  python tools/fish_temp_mapheat.py --apply --only level_019_american_pond
"""

import argparse
import copy
import json
import os
import sqlite3
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, f32, load_map, water_at  # noqa: E402
from fish_temp_literature import PARENT, SPECIES  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")

FORECAST_CLASS = "bleinjfhebp"
LAYERS = (0.0, 0.2, 0.45, 0.7, 0.93)


def lit_pref(key):
    if key in SPECIES:
        return SPECIES[key][3]
    par = PARENT.get(key)
    return SPECIES[par][3] if par in SPECIES else None


def water_range(m):
    v = [water_at(m, d, h, s, 1.0, ly)[1]
         for s in range(4) for d in range(8) for h in range(24) for ly in LAYERS]
    return min(v), max(v)


def quantile(vals, q):
    v = sorted(vals)
    if not v:
        return 0.0
    i = min(len(v) - 1, max(0, int(round(q * (len(v) - 1)))))
    return v[i]


def enc_f32(x):
    return struct.unpack("<I", struct.pack("<f", float(x)))[0]


def bump_highs(node, delta):
    """把该图所有 forecast 节点的 fields[2] 加 delta，返回改动数。"""
    n = 0

    def walk(x):
        nonlocal n
        if isinstance(x, dict):
            if x.get("class") == FORECAST_CLASS:
                fl = x.get("fields") or []
                if len(fl) > 2:
                    cur = f32(int(fl[2]["v"]))
                    fl[2]["v"] = enc_f32(cur + delta)
                    n += 1
            for v in x.values():
                walk(v)
        elif isinstance(x, list):
            for v in x:
                walk(v)

    walk(node)
    return n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--only", default="")
    ap.add_argument("--q", type=float, default=0.75,
                    help="取名册文献 pref 的哪个分位作为目标上限")
    ap.add_argument("--cap", type=float, default=10.0,
                    help="单图最多抬多少 ℃（防止资产过激）")
    args = ap.parse_args()

    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}
    id2zh = {}
    for zh, mid in zh2id.items():
        id2zh.setdefault(mid, zh)

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    roster = {}
    for r in cur.fetchall():
        roster.setdefault(r["loc"], set()).add(r["fish_key"])

    raw = json.load(open(MAP_DEFS, encoding="utf-8"))
    only = {x for x in args.only.split(",") if x} if args.only else None

    print(f"{'地图':<18}{'鱼数':>4}{'target_hi':>11}{'现上限':>8}"
          f"{'抬升':>7}{'抬后上限':>9}")
    changed = []
    for loc, keys in sorted(roster.items()):
        mid = zh2id.get(loc)
        if not mid or mid not in raw["maps"]:
            continue
        if only and mid not in only:
            continue
        ps = [lit_pref(k) for k in keys if lit_pref(k)]
        if len(ps) < 3:
            continue
        target_hi = quantile(ps, args.q) + 2.0   # +2℃ 留给窗口顶端
        m = load_map(raw["maps"][mid], mid)
        _, now_hi = water_range(m)
        delta = 0.0
        if now_hi < target_hi:
            lo, hi = 0.0, 40.0
            for _ in range(45):
                c = (lo + hi) / 2
                mm = copy.deepcopy(m)
                mm["highs"] = [x + c for x in m["highs"]]
                if water_range(mm)[1] >= target_hi:
                    hi = c
                else:
                    lo = c
            delta = min(hi, args.cap)
        mm = copy.deepcopy(m)
        mm["highs"] = [x + delta for x in m["highs"]]
        after_hi = water_range(mm)[1]
        mark = "  ←" if delta > 0.05 else ""
        print(f"{loc:<18}{len(keys):>4}{target_hi:>11.1f}{now_hi:>8.1f}"
              f"{delta:>7.1f}{after_hi:>9.1f}{mark}")
        if delta > 0.05:
            changed.append((mid, loc, delta))

    if not changed:
        print("\n无需改动：所有图水温上限已覆盖名册适温。")
        return

    print(f"\n需改动 {len(changed)} 张图：")
    for mid, loc, delta in changed:
        print(f"  {loc:<18} {mid:<28} 日高温 {delta:+.2f}℃")

    if not args.apply:
        print("\n（只报告模式，未写文件。加 --apply 写回 map_defs.json）")
        return

    for mid, loc, delta in changed:
        n = bump_highs(raw["maps"][mid], delta)
        print(f"  已改 {loc}（{mid}）{n} 天预报 {delta:+.2f}℃")

    json.dump(raw, open(MAP_DEFS, "w", encoding="utf-8"),
              ensure_ascii=False, separators=(",", ":"))
    print(f"\n已写回 {MAP_DEFS}")


if __name__ == "__main__":
    main()