"""每图鱼种适温分布直方图 + 温度换鱼能力的真实瓶颈诊断。

关键发现（2026-11-23）：
  把地图温度区间拉宽，季节重叠度反而上升（全季通吃）。
  真正决定「温度能否换鱼」的是：
      鱼种适温分布的离散度  vs  温度判定窗口宽度
  如果窗口(±5.5=11℃) >= 该图鱼适温分布跨度，那不管温度怎么变，
  每个季节都会覆盖同一批鱼 —— 这是数学必然，与地图温度设定无关。

所以本脚本回答：
  1) 每图的鱼适温分布长什么样（直方图）
  2) 该图的「适温分布跨度」是多少
  3) 窗口宽度要收到多少，温度才能换鱼（给出每图建议窗口）
  4) 该图应设什么水温区间（覆盖 P10~P90）

只读，不写库。
"""

import json
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_literature import PARENT, SPECIES  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")

HALF = 5.5
BINS = [(0, 4), (4, 8), (8, 12), (12, 16), (16, 20), (20, 24), (24, 28), (28, 32)]


def lit(key):
    if key in SPECIES:
        return SPECIES[key][3]
    par = PARENT.get(key)
    return SPECIES[par][3] if par in SPECIES else None


def pct(v, f):
    v = sorted(v)
    return v[min(len(v) - 1, max(0, int(len(v) * f)))]


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

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

    print("每图鱼种适温分布（pref，℃）\n")
    print("        " + "".join(f"{lo:>2}-{hi:<2}" .rjust(7) for lo, hi in BINS))
    print("        " + "".join(f"{'':>7}" for _ in BINS) + "   n  P10/50/90  跨度")

    rows = []
    for loc, keys in sorted(roster.items(), key=lambda kv: -len(kv[1])):
        prefs = [p for p in (lit(k) for k in keys) if p is not None]
        if not prefs:
            continue
        counts = []
        for lo, hi in BINS:
            counts.append(sum(1 for p in prefs if lo <= p < hi))
        bar = "".join(f"{c if c else '·':>7}" for c in counts)
        p10, p50, p90 = pct(prefs, .10), pct(prefs, .50), pct(prefs, .90)
        span = max(prefs) - min(prefs)
        print(f"{loc:<12}{bar}  {len(prefs):>2}  "
              f"{p10:>3.0f}/{p50:>3.0f}/{p90:>3.0f}  {span:>4.0f}")

        # 窗口要窄到多少，温度才能换鱼？经验：窗口 <= 分布跨度的 40%
        rec_half = max(2.5, min(HALF, span * 0.40 / 2))
        rows.append({
            "loc": loc, "mid": zh2id.get(loc), "n": len(prefs),
            "p10": p10, "p50": p50, "p90": p90,
            "span": span, "rec_half": round(rec_half, 1),
            "crosses_bins": sum(1 for c in counts if c > 0),
        })

    print()
    print("== 温度换鱼能力诊断 ==")
    print(f"{'地图':<12}{'鱼':>3}{'适温跨度':>9}{'占用档位':>9}"
          f"{'当前窗口±5.5':>13}{'建议窗口':>10}{'判定':>14}")
    for r in rows:
        ok = r["span"] > HALF * 2
        verdict = "能换鱼" if ok else "换不动（分布太窄）"
        print(f"{r['loc']:<12}{r['n']:>3}{r['span']:>8.0f}℃{r['crosses_bins']:>8}/8"
              f"{'11℃':>13}{r['rec_half']*2:>8.1f}℃{verdict:>14}")

    print()
    narrow = [r for r in rows if r["span"] <= HALF * 2]
    print(f"适温跨度 <=11℃ 的图：{len(narrow)} 张 —— 这些图靠温度换不动鱼，"
          f"只能靠水层/昼夜/饵料")
    for r in narrow:
        print(f"  {r['loc']:<12} 跨度仅 {r['span']:.0f}℃（{r['n']} 条鱼挤在 "
              f"{r['p10']:.0f}~{r['p90']:.0f}℃）")

    wide = [r for r in rows if r["span"] > HALF * 2]
    print()
    print(f"适温跨度 >11℃ 的图：{len(wide)} 张 —— 温度可以换鱼")
    for r in wide:
        print(f"  {r['loc']:<12} 跨度 {r['span']:.0f}℃  "
              f"建议窗口 ±{r['rec_half']}℃（总宽 {r['rec_half']*2:.0f}℃）")


if __name__ == "__main__":
    main()