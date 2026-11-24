"""按 Go 判鱼侧同口径重算各图逐时水温，用于鱼种适温区间的标定。

只读：解析 internal/gameserver/assets/map_defs.json，不写库。
温度链路复刻（务必与代码保持一致）：

  weather_schedule.go:93  mapWeatherProfile.hour(day, hour)
      high     = forecast[day].field[2]          日高温
      nextHigh = forecast[day+1].field[2]
      rangeC   = 24h 模板温度 max-min            日温差
      cycle    = (1-cos((hour-4)*pi/12))/2
      air      = high + (nextHigh-high)*hour/24 - rangeC*(1-cycle)

  fish_bite_environment.go:538  fishWaterConditionsFor
      seasonalAir  = [10,23,13,1][season]
      seasonalDeep = [9,14,11,4][season]
      thermal = .36 + .16*activity
      surface = seasonalAir + (air-seasonalAir)*thermal + daylight*1.2
      deep    = seasonalDeep + (surface-seasonalDeep)*.22
      water   = surface*(1-layer) + deep*layer
"""

import json
import math
import os
import struct

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MAP_DEFS = os.path.join(ROOT, "internal", "gameserver", "assets", "map_defs.json")

HOUR_PLAN_CLASS = "odnlikfnhnl"
FORECAST_CLASS = "bleinjfhebp"

SEASONAL_AIR = [10.0, 23.0, 13.0, 1.0]
SEASONAL_DEEP = [9.0, 14.0, 11.0, 4.0]
SEASON_NAMES = {0: "春", 1: "夏", 2: "秋", 3: "冬"}


def f32(v):
    return struct.unpack("<f", struct.pack("<I", v & 0xFFFFFFFF))[0]


def clamp(v, lo, hi):
    return max(lo, min(hi, v))


def season_of_month(month):
    """与 fishSeasonAt 一致：12/1/2 冬，3-4 春，5-7 夏，8-11 秋。"""
    if month in (12, 1, 2):
        return 3
    if month in (3, 4):
        return 0
    if month in (5, 6, 7):
        return 1
    return 2


def ir_float(fields, idx):
    """fishingWeatherFloat：按 float32 位模式还原。"""
    if idx >= len(fields):
        return None
    item = fields[idx]
    raw = item.get("v")
    if not isinstance(raw, (int, float)):
        return None
    return f32(int(raw))


def ir_int(fields, idx):
    if idx >= len(fields):
        return None
    raw = fields[idx].get("v")
    return int(raw) if isinstance(raw, (int, float)) else None


def walk(node, out):
    if isinstance(node, dict):
        if node.get("class") == FORECAST_CLASS:
            out["days"].append(node)
        elif node.get("class") == HOUR_PLAN_CLASS:
            out["hours"].append(node)
        for v in node.values():
            walk(v, out)
    elif isinstance(node, list):
        for v in node:
            walk(v, out)


def fnv64a(s):
    h = 0xCBF29CE484222325
    for b in s.encode("utf-8"):
        h ^= b
        h = (h * 0x100000001B3) & 0xFFFFFFFFFFFFFFFF
    return h


def load_map(node, name):
    out = {"days": [], "hours": []}
    walk(node, out)
    days = sorted(out["days"], key=lambda n: ir_int(n.get("fields") or [], 0) or 0)
    highs = []
    for d in days:
        h = ir_float(d.get("fields") or [], 2)
        highs.append(h if h is not None else 0.0)
    temps = []
    for h in out["hours"]:
        t = ir_float(h.get("fields") or [], 2)
        if t is not None:
            temps.append(t)
    range_c = (max(temps) - min(temps)) if temps else 0.0
    return {"name": name, "highs": highs, "range": range_c, "seed": fnv64a(name)}


def air_at(m, day, hour):
    """复刻 mapWeatherProfile.hour 的气温，含 8 天循环。"""
    n = len(m["highs"])
    if n == 0:
        return 0.0
    i = (day + m["seed"] % n) % n
    j = (day + 1 + m["seed"] % n) % n
    high, next_high = m["highs"][i], m["highs"][j]
    frac = hour / 24.0
    cycle = (1 - math.cos((hour - 4) * math.pi / 12)) / 2
    return high + (next_high - high) * frac - m["range"] * (1 - cycle)


def water_at(m, day, hour, season, activity=1.0, layer=0.45):
    air = air_at(m, day, hour)
    daylight = clamp((1 + math.cos((hour - 12) * math.pi / 12)) / 2, 0, 1)
    thermal = 0.36 + 0.16 * clamp(activity, 0.5, 1.5)
    sa, sd = SEASONAL_AIR[season], SEASONAL_DEEP[season]
    surface = sa + (air - sa) * thermal + daylight * 1.2
    deep = sd + (surface - sd) * 0.22
    return surface, surface * (1 - layer) + deep * layer


def main():
    data = json.load(open(MAP_DEFS, encoding="utf-8"))
    maps = {k: load_map(v, k) for k, v in data["maps"].items()}

    print(f"{'map':<30}{'d':>3}{'range':>7}{'high_min':>10}{'high_max':>10}"
          f"{'air_min':>9}{'air_max':>9}{'w_min':>8}{'w_max':>8}{'w_span':>8}")
    all_w, all_air = [], []
    for name, m in maps.items():
        airs, ws = [], []
        for day in range(8):
            for hour in range(24):
                a = air_at(m, day, hour)
                airs.append(a)
                # 季节只有公式里的基准，气温模板不随季节变
                for s in (0, 1, 2, 3):
                    for act in (0.5, 1.0, 1.5):
                        _, w = water_at(m, day, hour, s, act, 0.45)
                        ws.append(w)
        all_w += ws
        all_air += airs
        print(f"{name:<30}{len(m['highs']):>3}{m['range']:>7.1f}"
              f"{min(m['highs']):>10.1f}{max(m['highs']):>10.1f}"
              f"{min(airs):>9.1f}{max(airs):>9.1f}"
              f"{min(ws):>8.1f}{max(ws):>8.1f}{max(ws)-min(ws):>8.1f}")

    print()
    print("== 全库 ==")
    print(f"气温  min={min(all_air):6.2f}  max={max(all_air):6.2f}  span={max(all_air)-min(all_air):5.2f}")
    print(f"水温  min={min(all_w):6.2f}  max={max(all_w):6.2f}  span={max(all_w)-min(all_w):5.2f}"
          f"   （layer=0.45, activity 0.5~1.5, 四季全覆盖）")

    print()
    print("== 每图分季节水温区间（activity=1.0, layer=0.45）==")
    print(f"{'map':<30}{'春':>14}{'夏':>14}{'秋':>14}{'冬':>14}")
    for name, m in maps.items():
        cells = []
        for s in (0, 1, 2, 3):
            v = [water_at(m, d, h, s, 1.0, 0.45)[1] for d in range(8) for h in range(24)]
            cells.append(f"{min(v):5.1f}~{max(v):5.1f}")
        print(f"{name:<30}" + "".join(f"{c:>14}" for c in cells))


if __name__ == "__main__":
    main()