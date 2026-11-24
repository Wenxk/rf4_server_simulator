"""地图温度设计方案：给定名册鱼适温，算出每图该设多少气温。

设计目标（2026-11-23 用户明确）：
  温度是设计变量，作用是在同一地图内制造鱼口随机性——
  同一张图，不同温度区间能钓到不同鱼种，而不是全图全季通吃。

计算逻辑：
  1) 取名册鱼 pref 的 P10 / P90，目标水温区间 = [P10-5.5, P90+5.5]
     （覆盖 80% 的鱼；剩下 20% 靠鱼种适温修正来兜）
  2) 反推气温：水温 → 气温的解析式见 fish_temp_mapair.py
       最冷季（冬 season=3）对应该图日高温下限
       最热季（夏 season=1）对应该图日高温上限
  3) 用反推出的气温重算四季水温，检查：
       - 死条目（有鱼永远够不到适温）
       - 季节重叠度（四季是不是在钓同一批鱼）

只读，不写库。方案导出 ref/map_temp_plan.csv。
"""

import csv
import json
import os
import sqlite3
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_literature import PARENT, SPECIES  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")
MAP_DEFS = os.path.join(ROOT, "internal", "gameserver", "assets", "map_defs.json")

HALF = 5.5
SN = ["春", "夏", "秋", "冬"]
FC_CLS = "bleinjfhebp"

SEASONAL_AIR = [10.0, 23.0, 13.0, 1.0]
SEASONAL_DEEP = [9.0, 14.0, 11.0, 4.0]
THERMAL = 0.52
LAYER = 0.45
DAYLIGHT = 0.6


def water_from_air(air, season):
    sa, sd = SEASONAL_AIR[season], SEASONAL_DEEP[season]
    surface = sa + (air - sa) * THERMAL + DAYLIGHT
    deep = sd + (surface - sd) * 0.22
    return surface * (1 - LAYER) + deep * LAYER


def air_for_water(water, season):
    sa, sd = SEASONAL_AIR[season], SEASONAL_DEEP[season]
    surface = (water - 0.351 * sd) / 0.649
    return sa + (surface - sa - DAYLIGHT) / THERMAL


def lit_window(key):
    """返回 (pref, 窗口下限, 窗口上限)；变种跟随亲本。"""
    if key in SPECIES:
        z, lo, hi, p, c, n = SPECIES[key]
    else:
        par = PARENT.get(key)
        if par not in SPECIES:
            return None
        z, lo, hi, p, c, n = SPECIES[par]
    if p <= 0:
        return None  # 虚构鱼种
    return p, max(lo, p - HALF), min(hi, p + HALF)


def pct(v, f):
    v = sorted(v)
    return v[min(len(v) - 1, max(0, int(len(v) * f)))]


def hours_highs(node, out):
    if isinstance(node, dict):
        if node.get("class") == FC_CLS:
            out.append(node)
        for v in node.values():
            hours_highs(v, out)
    elif isinstance(node, list):
        for v in node:
            hours_highs(v, out)


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

    md = json.load(open(MAP_DEFS, encoding="utf-8"))

    def cur_highs(mid):
        out = []
        hours_highs(md["maps"][mid], out)
        hs = []
        for n in out:
            f = n.get("fields") or []
            raw = f[2].get("v")
            if isinstance(raw, (int, float)):
                hs.append(struct.unpack("<f", struct.pack("<I", int(raw) & 0xFFFFFFFF))[0])
        return sorted(hs)

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    roster = {}
    for r in cur.fetchall():
        roster.setdefault(r["loc"], set()).add(r["fish_key"])

    plan, skipped = [], []
    for loc, keys in sorted(roster.items(), key=lambda kv: -len(kv[1])):
        mid = zh2id.get(loc)
        if not mid or mid not in md["maps"]:
            skipped.append(loc)
            continue
        wins = [w for w in (lit_window(k) for k in keys) if w]
        if len(wins) < 3:
            skipped.append(loc)
            continue
        prefs = [w[0] for w in wins]
        p10, p50, p90 = pct(prefs, .10), pct(prefs, .50), pct(prefs, .90)

        # 目标水温区间
        tgt_w_lo = max(-2.0, p10 - HALF)
        tgt_w_hi = min(32.0, p90 + HALF)
        # 反推气温
        tgt_air_lo = max(-20.0, air_for_water(tgt_w_lo, 3))
        tgt_air_hi = min(45.0, air_for_water(tgt_w_hi, 1))

        old_h = cur_highs(mid)
        cur_air_lo, cur_air_hi = (min(old_h), max(old_h)) if old_h else (0, 0)

        # 用目标气温重算四季水温（8 天在 lo~hi 线性插值，代表整年）
        new_w = {}
        for s in (0, 1, 2, 3):
            new_w[s] = [water_from_air(tgt_air_lo + (tgt_air_hi - tgt_air_lo) * i / 7, s)
                        for i in range(8)]
        # 现状水温
        old_w = {}
        for s in (0, 1, 2, 3):
            old_w[s] = [water_from_air(cur_air_lo + (cur_air_hi - cur_air_lo) * i / 7, s)
                        for i in range(8)]

        def evaluate(wmap):
            allw = [v for s in (0, 1, 2, 3) for v in wmap[s]]
            dead = [k for k in keys
                    if (w := lit_window(k)) and (w[1] > max(allw) or w[2] < min(allw))]
            sets = {}
            for s in (0, 1, 2, 3):
                slo, shi = min(wmap[s]), max(wmap[s])
                sets[s] = {k for k in keys
                           if (w := lit_window(k))
                           and not (w[1] > shi or w[2] < slo)}
            ovs = []
            for a in range(4):
                for b in range(a + 1, 4):
                    if sets[a] or sets[b]:
                        u = sets[a] | sets[b]
                        ovs.append(len(sets[a] & sets[b]) / len(u) if u else 0)
            ov = sum(ovs) / len(ovs) if ovs else 0
            return min(allw), max(allw), dead, ov

        o_lo, o_hi, o_dead, o_ov = evaluate(old_w)
        n_lo, n_hi, n_dead, n_ov = evaluate(new_w)

        plan.append({
            "loc": loc, "map_id": mid, "n": len(wins),
            "p10": p10, "p50": p50, "p90": p90,
            "old_air_lo": cur_air_lo, "old_air_hi": cur_air_hi,
            "new_air_lo": round(tgt_air_lo, 1), "new_air_hi": round(tgt_air_hi, 1),
            "d_lo": round(tgt_air_lo - cur_air_lo, 1),
            "d_hi": round(tgt_air_hi - cur_air_hi, 1),
            "old_w_lo": round(o_lo, 1), "old_w_hi": round(o_hi, 1),
            "new_w_lo": round(n_lo, 1), "new_w_hi": round(n_hi, 1),
            "old_dead": len(o_dead), "new_dead": len(n_dead),
            "old_ov": round(o_ov, 3), "new_ov": round(n_ov, 3),
            "new_dead_list": ",".join(sorted(n_dead)[:6]),
        })

    print("每图温度设计方案\n")
    print(f"{'地图':<12}{'鱼':>3}{'pref P10/50/90':>16}"
          f"{'现日高温':>13}{'建议日高温':>14}{'现水温':>14}{'新水温':>14}"
          f"{'季节重叠':>10}{'死条':>6}")
    for r in plan:
        print(f"{r['loc']:<12}{r['n']:>3}"
              f"{r['p10']:>6.0f}/{r['p50']:.0f}/{r['p90']:<7.0f}"
              f"{r['old_air_lo']:>5.0f}~{r['old_air_hi']:<7.0f}"
              f"{r['new_air_lo']:>5.0f}~{r['new_air_hi']:<7.0f}"
              f"{r['old_w_lo']:>6.1f}~{r['old_w_hi']:<7.1f}"
              f"{r['new_w_lo']:>6.1f}~{r['new_w_hi']:<7.1f}"
              f"{r['old_ov']*100:>7.0f}→{r['new_ov']*100:<3.0f}%{r['new_dead']:>5}")

    print()
    print("== 季节重叠改善（越低越好）==")
    for r in sorted(plan, key=lambda r: r["new_ov"] - r["old_ov"]):
        delta = (r["new_ov"] - r["old_ov"]) * 100
        print(f"  {r['loc']:<12} {r['old_ov']*100:5.1f}% → {r['new_ov']*100:5.1f}%"
              f"  ({delta:+.1f})")

    print()
    total_old = sum(r["old_dead"] for r in plan)
    total_new = sum(r["new_dead"] for r in plan)
    print(f"死条目：现状 {total_old} 对 → 方案 {total_new} 对")
    if total_new:
        print("仍需处理：")
        for r in plan:
            if r["new_dead"]:
                print(f"  {r['loc']:<12} {r['new_dead']} 条：{r['new_dead_list']}")

    out = os.path.join(ROOT, "ref", "map_temp_plan.csv")
    with open(out, "w", newline="", encoding="utf-8-sig") as f:
        w = csv.DictWriter(f, fieldnames=list(plan[0].keys()))
        w.writeheader()
        w.writerows(plan)
    print(f"\n已导出 → {out}")
    if skipped:
        print(f"跳过（数据不足）：{', '.join(skipped)}")


if __name__ == "__main__":
    main()