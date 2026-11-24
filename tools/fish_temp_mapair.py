"""反推每图需要的气温设定（map_defs.json 的日高温）。

水温公式（fish_bite_environment.go:538，layer=0.45）：
    seasonalAir  = [10,23,13,1][season]
    thermal      = .36 + .16*activity        (0.44 ~ 0.60，取 1.0 时为 .52)
    surface      = seasonalAir + (air-seasonalAir)*thermal + daylight*1.2
    deep         = seasonalDeep + (surface-seasonalDeep)*.22
    water        = surface*0.55 + deep*0.45

反过来：给定目标水温，求所需气温
    deep = seasonalDeep + (surface-seasonalDeep)*.22
    water = surface*.55 + deep*.45
   => water = surface*.55 + (.22*surface + .78*seasonalDeep)*.45
            = surface*(.55 + .099) + .351*seasonalDeep
            = surface*.649 + .351*seasonalDeep
   => surface = (water - .351*seasonalDeep) / .649
   surface 里 daylight 项在正午最大 1.2、午夜 0
   取日均 daylight≈0.5 → surface ≈ seasonalAir + (air-seasonalAir)*.52 + .6
   => air = seasonalAir + (surface - seasonalAir - .6)/.52

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

SEASONAL_AIR = [10.0, 23.0, 13.0, 1.0]
SEASONAL_DEEP = [9.0, 14.0, 11.0, 4.0]
THERMAL = 0.52      # activity=1.0 时的传导系数
LAYER = 0.45
DAYLIGHT = 0.6      # 日间平均 daylight 加成
HALF = 5.5
SN = ["春", "夏", "秋", "冬"]


def lit_pref(key):
    if key in SPECIES:
        return SPECIES[key][3]
    p = PARENT.get(key)
    return SPECIES[p][3] if p in SPECIES else None


def pct(vals, f):
    v = sorted(vals)
    return v[min(len(v) - 1, max(0, int(len(v) * f)))]


def water_from_air(air, season):
    sa, sd = SEASONAL_AIR[season], SEASONAL_DEEP[season]
    surface = sa + (air - sa) * THERMAL + DAYLIGHT
    deep = sd + (surface - sd) * 0.22
    return surface * (1 - LAYER) + deep * LAYER


def air_for_water(water, season):
    """water_from_air 的反函数。"""
    sa, sd = SEASONAL_AIR[season], SEASONAL_DEEP[season]
    surface = (water - 0.351 * sd) / 0.649
    return sa + (surface - sa - DAYLIGHT) / THERMAL


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

    md = json.load(open(
        os.path.join(ROOT, "internal/gameserver/assets/map_defs.json"), encoding="utf-8"))
    for_fc = {}
    fc_cls = "bleinjfhebp"
    hour_cls = "odnlikfnhnl"

    def walk(node, out):
        if isinstance(node, dict):
            if node.get("class") == fc_cls:
                out.append(node)
            for v in node.values():
                walk(v, out)
        elif isinstance(node, list):
            for v in node:
                walk(v, out)

    import struct
    for name, node in md["maps"].items():
        out = []
        walk(node, out)
        highs = []
        for n in out:
            f = n.get("fields") or []
            if len(f) > 2:
                raw = f[2].get("v")
                if isinstance(raw, (int, float)):
                    highs.append(struct.unpack("<f", struct.pack("<I", int(raw) & 0xFFFFFFFF))[0])
        for_fc[name] = sorted(highs)

    print("每图所需气温设定（日高温，℃）\n")
    print("目标：80% 名册鱼的适温窗口与该图某季节水温有交集，且冬/夏温差足够换鱼\n")
    print(f"{'地图':<12}{'鱼':>3}{'现日高温':>14}{'需要最低温季':>13}{'需要最高温季':>13}"
          f"{'冬需气':>8}{'夏需气':>8}{'现冬低':>8}{'现夏高':>8}{'判定':>10}")

    plan = []
    for loc, keys in sorted(roster.items(), key=lambda kv: -len(kv[1])):
        mid = zh2id.get(loc)
        if not mid or mid not in for_fc:
            continue
        prefs = [p for p in (lit_pref(k) for k in keys) if p is not None]
        if not prefs:
            continue
        need_lo = pct(prefs, .10) - HALF
        need_hi = pct(prefs, .90) + HALF
        highs = for_fc[mid]
        cur_lo_air, cur_hi_air = min(highs), max(highs)
        # 冬天最冷、夏天最热
        need_winter_air = air_for_water(need_lo, 3)
        need_summer_air = air_for_water(need_hi, 1)
        d_lo = need_winter_air - cur_lo_air     # 正 = 需要更冷（气温更低）
        d_hi = need_summer_air - cur_hi_air     # 正 = 需要更热
        gap = max(d_lo, d_hi)
        verdict = "够用" if gap <= 1 else ("微调" if gap <= 4 else "需大改")
        plan.append((loc, mid, len(prefs), need_lo, need_hi,
                     need_winter_air, need_summer_air,
                     cur_lo_air, cur_hi_air, d_lo, d_hi, gap, verdict))
        print(f"{loc:<12}{len(prefs):>3}"
              f"{cur_lo_air:>6.0f}~{cur_hi_air:<7.0f}"
              f"{need_lo:>13.1f}{need_hi:>13.1f}"
              f"{need_winter_air:>8.0f}{need_summer_air:>8.0f}"
              f"{cur_lo_air:>8.0f}{cur_hi_air:>8.0f}{verdict:>10}")

    print()
    print("== 缺口汇总（℃ 气温）==")
    print(f"{'地图':<12}{'冬端需降温':>11}{'夏端需升温':>11}{'合计动作':>10}")
    for (loc, mid, n, nl, nh, wa, sa_, cl, ch, dl, dh, gap, v) in sorted(
            plan, key=lambda r: -r[11]):
        print(f"{loc:<12}{max(0,-dl):>11.0f}{max(0,dh):>11.0f}{gap:>10.0f}")

    print()
    print("注：")
    print(f"  thermal={THERMAL}（activity=1.0）、layer={LAYER}、daylight 日均 {DAYLIGHT}")
    print("  「冬端需降温」= 现在冬天气温还不够低？正数=需要更低；0=已经够冷")
    print("  「夏端需升温」= 现在夏天气温需要抬高多少才能让暖水鱼开口")


if __name__ == "__main__":
    main()