"""按「地图温度设计」视角诊断：每张图的鱼种适温构成 vs 该图能提供的温度。

设计目标（2026-11-23 用户明确）：
  温度用来增加同一地图内鱼口的随机性——同一张图，在不同温度区间能钓到不同鱼种。
  因此判定标准不是「文献温度对不对」，而是：
    1) 该图的温度范围能否覆盖名册里绝大多数鱼的适温（不能有钓不到的鱼）
    2) 名册鱼的适温是否分散在多个档位（否则温度再变也还是那几条鱼）
    3) 不同温度段是否有足够的鱼种替换

只读，不写库。
"""

import json
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, load_map, water_at  # noqa: E402
from fish_temp_literature import PARENT, SPECIES  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")
HALF = 5.5
SN = ["春", "夏", "秋", "冬"]


def lit_pref(key):
    if key in SPECIES:
        return SPECIES[key][3]
    p = PARENT.get(key)
    return SPECIES[p][3] if p in SPECIES else None


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    # 每图四季水温区间与逐时样本
    wtemp = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        wtemp[name] = {
            s: [water_at(m, day, h, s, 1.0, 0.45)[1]
                for day in range(8) for h in range(24)]
            for s in (0, 1, 2, 3)
        }

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    roster = {}
    for r in cur.fetchall():
        roster.setdefault(r["loc"], set()).add(r["fish_key"])
    cur.execute("SELECT fish_key, fish_zh FROM loc_wiki_fish")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    print("每图：名册鱼适温分布 vs 该图水温区间\n")
    summary = []
    for loc, keys in sorted(roster.items(), key=lambda kv: -len(kv[1])):
        mid = zh2id.get(loc)
        if not mid:
            continue
        prefs = sorted(p for p in (lit_pref(k) for k in keys) if p is not None)
        if not prefs:
            continue
        n = len(prefs)
        q = lambda f: prefs[min(n - 1, int(n * f))]
        allw = [v for s in (0, 1, 2, 3) for v in wtemp[mid][s]]
        wlo, whi = min(allw), max(allw)
        # 各季节水温区间
        seas = {s: (min(wtemp[mid][s]), max(wtemp[mid][s])) for s in (0, 1, 2, 3)}
        # 覆盖率：鱼在某季节有开口机会（窗口与该季水温有交集）的比例
        cover_by_season = {}
        for s in (0, 1, 2, 3):
            slo, shi = seas[s]
            ok = sum(1 for k in keys
                     if (p := lit_pref(k)) is not None
                     and not (p + HALF < slo or p - HALF > shi))
            cover_by_season[s] = ok / n
        # 温度梯度：名册鱼 pref 的跨度 / 水温跨度
        summary.append({
            "loc": loc, "mid": mid, "n": n,
            "pmin": prefs[0], "p25": q(.25), "p50": q(.50), "p75": q(.75), "pmax": prefs[-1],
            "wlo": wlo, "whi": whi, "seas": seas, "cover": cover_by_season,
            "fish": keys,
        })

    print(f"{'地图':<12}{'鱼':>3}{'pref 分布 min/25/50/75/max':>34}"
          f"{'水温区间':>14}{'春':>6}{'夏':>6}{'秋':>6}{'冬':>6}")
    for r in summary:
        dist = f"{r['pmin']:.0f}/{r['p25']:.0f}/{r['p50']:.0f}/{r['p75']:.0f}/{r['pmax']:.0f}"
        print(f"{r['loc']:<12}{r['n']:>3}{dist:>34}"
              f"{r['wlo']:>6.1f}~{r['whi']:<7.1f}"
              + "".join(f"{r['cover'][s]*100:>5.0f}%" for s in (0, 1, 2, 3)))

    print()
    print("== 问题地图 ==")
    for r in summary:
        issues = []
        if min(r["cover"].values()) < 0.9:
            bad = [s for s in (0, 1, 2, 3) if r["cover"][s] < 0.9]
            issues.append(f"季节覆盖不足 {'/'.join(SN[s] for s in bad)}"
                          f"（最低 {min(r['cover'].values())*100:.0f}%）")
        span_fish = r["pmax"] - r["pmin"]
        span_water = r["whi"] - r["wlo"]
        if span_fish < 8:
            issues.append(f"鱼种适温过挤（跨度仅 {span_fish:.0f}℃）")
        if span_water < span_fish * 0.7:
            issues.append(f"水温跨度 {span_water:.0f}℃ 撑不起鱼种跨度 {span_fish:.0f}℃")
        if issues:
            print(f"  {r['loc']:<12} {'；'.join(issues)}")

    print()
    print("== 温度区分力（该图不同季节出鱼的重叠程度）==")
    print("  指标：某季节开口的鱼，在另一季节仍开口的平均比例。越低越好（说明季节能换鱼）")
    for r in summary:
        sets = {}
        for s in (0, 1, 2, 3):
            slo, shi = r["seas"][s]
            sets[s] = {k for k in r["fish"]
                       if (p := lit_pref(k)) is not None
                       and not (p + HALF < slo or p - HALF > shi)}
        ovs = []
        for a in (0, 1, 2, 3):
            for b in (0, 1, 2, 3):
                if a < b and sets[a]:
                    ovs.append(len(sets[a] & sets[b]) / len(sets[a] | sets[b]))
        ov = sum(ovs) / len(ovs) if ovs else 0
        flag = "" if ov < 0.55 else "  ← 季节换不动鱼"
        print(f"  {r['loc']:<12} 季节鱼群重叠 {ov*100:5.1f}%{flag}")


if __name__ == "__main__":
    main()