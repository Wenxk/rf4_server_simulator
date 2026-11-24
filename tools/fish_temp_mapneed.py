"""反推：要让每图的鱼种在温度上「分得开」，该图的水温区间应该设成多少。

设计目标（2026-11-23 用户明确）：
  同一张图里，不同温度区间钓到不同鱼种。所以需要：
    A) 图的温度区间覆盖名册鱼适温的绝大部分（不然有鱼钓不到）
    B) 图的温度区间足够宽，能把鱼的适温档拉开（不然季节换不动鱼）

做法：对每图，取名册鱼 pref 的 P10~P90 分位作为「必须覆盖的核心区」，
      再看该图现有水温区间差在哪，给出建议的目标区间。

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


def pct(vals, f):
    if not vals:
        return None
    v = sorted(vals)
    return v[min(len(v) - 1, max(0, int(len(v) * f)))]


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    wtemp = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        wtemp[name] = {s: [water_at(m, day, h, s, 1.0, 0.45)[1]
                           for day in range(8) for h in range(24)]
                       for s in (0, 1, 2, 3)}

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    roster = {}
    for r in cur.fetchall():
        roster.setdefault(r["loc"], set()).add(r["fish_key"])

    rows = []
    for loc, keys in roster.items():
        mid = zh2id.get(loc)
        if not mid:
            continue
        prefs = [p for p in (lit_pref(k) for k in keys) if p is not None]
        if not prefs:
            continue
        allw = [v for s in (0, 1, 2, 3) for v in wtemp[mid][s]]
        wlo, whi = min(allw), max(allw)
        # 需要覆盖的核心区：P10~P90 的 pref，再各留 HALF 的窗口半宽
        need_lo = pct(prefs, .10) - HALF
        need_hi = pct(prefs, .90) + HALF
        rows.append({
            "loc": loc, "n": len(prefs), "mid": mid,
            "p10": pct(prefs, .10), "p50": pct(prefs, .50), "p90": pct(prefs, .90),
            "wlo": wlo, "whi": whi, "need_lo": need_lo, "need_hi": need_hi,
            "gap_lo": need_lo - wlo, "gap_hi": whi - need_hi,
        })

    rows.sort(key=lambda r: -(r["gap_hi"] - r["gap_lo"]))

    print("每图：现有水温区间 vs 名册鱼需要的区间\n")
    print(f"{'地图':<12}{'鱼':>3}{'鱼pref P10/50/90':>20}"
          f"{'现有水温':>15}{'需要区间':>16}{'冷端缺口':>10}{'热端缺口':>10}{'判定':>12}")
    for r in rows:
        gap = max(r["gap_lo"], r["gap_hi"])
        if gap <= 0:
            verdict = "够用"
        elif gap <= 2:
            verdict = "差一点"
        else:
            verdict = "明显不足"
        print(f"{r['loc']:<12}{r['n']:>3}"
              f"{r['p10']:>7.0f}/{r['p50']:.0f}/{r['p90']:<7.0f}"
              f"{r['wlo']:>7.1f}~{r['whi']:<7.1f}"
              f"{r['need_lo']:>7.1f}~{r['need_hi']:<8.1f}"
              f"{r['gap_lo']:>+10.1f}{r['gap_hi']:>+10.1f}{verdict:>12}")

    print()
    print("说明：")
    print("  鱼pref P10/50/90 = 名册鱼偏好温度的 10%/50%/90% 分位（去掉极端值后的核心分布）")
    print("  需要区间 = [P10-5.5, P90+5.5]，即让 80% 的鱼都能在自己的窗口内有开口机会")
    print("  冷端缺口 = 需要的最低温度 - 现在最低水温（正数表示现在不够冷？负数表示现在够冷）")
    print("  热端缺口 = 现在最高水温 - 需要的最高温度（正数表示够热，负数表示不够热）")

    print()
    print("== 按缺口排序：最需要调温度的地图 ==")
    for r in rows:
        if max(r["gap_lo"], r["gap_hi"]) > 2:
            print(f"  {r['loc']:<12} 需要 {r['need_lo']:.1f}~{r['need_hi']:.1f}℃"
                  f"  现有 {r['wlo']:.1f}~{r['whi']:.1f}℃"
                  f"  缺热 {max(0, -r['gap_hi']):.1f}℃ 缺冷 {max(0, r['gap_lo']):.1f}℃")


if __name__ == "__main__":
    main()