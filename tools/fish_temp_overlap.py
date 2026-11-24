"""验证水温的跨图区分力瓶颈：同一季节下各图水温分布的重叠程度。

如果四季基准是全局硬编码，冬季会把所有图压进同一区间，
导致「温度」无法表达「这鱼只在这张图出」——跨图差异只能靠名册本身。

只读，不写库。
"""

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, SEASONAL_AIR, SEASONAL_DEEP, load_map, water_at  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")
SN = ["春", "夏", "秋", "冬"]


def main():
    d = json.load(open(MULT, encoding="utf-8"))
    id2zh = {k: v["map"] for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    data = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        data[id2zh.get(name, name)] = {
            s: [water_at(m, day, h, s, 1.0, 0.45)[1]
                for day in range(8) for h in range(24)]
            for s in (0, 1, 2, 3)
        }

    print("同一季节下，18 张图水温区间的分布与重叠\n")
    for s in (0, 1, 2, 3):
        lo_all, hi_all = [], []
        for loc, seasons in data.items():
            v = seasons[s]
            lo_all.append((min(v), loc))
            hi_all.append((max(v), loc))
        gmin = min(x[0] for x in lo_all)
        gmax = max(x[0] for x in hi_all)
        # 图均值的离散度 = 真正的跨图信号
        means = sorted((sum(seasons[s]) / len(seasons[s]), loc) for loc, seasons in data.items())
        spread = means[-1][0] - means[0][0]
        print(f"== {SN[s]}  季节性基准 air={SEASONAL_AIR[s]:.0f}℃ deep={SEASONAL_DEEP[s]:.0f}℃")
        print(f"   全图水温 {gmin:.1f}~{gmax:.1f}℃（总跨度 {gmax-gmin:.1f}℃）")
        print(f"   图间均值离散 {means[0][0]:.1f} ~ {means[-1][0]:.1f}℃"
              f"（跨度 {spread:.1f}℃，仅为总跨度的 {spread/(gmax-gmin)*100:.0f}%）")
        print(f"   最冷图 {means[0][1]}  最暖图 {means[-1][1]}")
        # 各图区间的重叠率：多少比例的图区间覆盖了全局中位温度
        mid = (gmin + gmax) / 2
        cover = sum(1 for loc, seasons in data.items()
                    if min(seasons[s]) <= mid <= max(seasons[s]))
        print(f"   有 {cover}/{len(data)} 张图的区间包含全图中位温度 {mid:.1f}℃")
        print()

    print("== 结论 ==")
    print("四季基准 seasonalAir/seasonalDeep 是全局硬编码，与地图无关；")
    print("地图差异只通过 air 与 seasonalAir 的差值、乘 thermalResponse(0.44~0.60) 传导，")
    print("因此同季节下 18 张图的均值被压到很窄的区间里。")
    print()
    print("对比：如果 seasonalAir 改成按各图模板均值推导，图间均值离散会接近各图日高温离散。")


if __name__ == "__main__":
    main()