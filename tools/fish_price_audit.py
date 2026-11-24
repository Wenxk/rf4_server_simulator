#!/usr/bin/env python3
"""鱼价基准审计：全量核对 253 种鱼的单价结构。

用法：
    python tools/fish_price_audit.py            # 默认读 data/rf4server.db
    python tools/fish_price_audit.py --db X     # 指定实机库

两个判据（对应 specs/changes/fish-price-outlier-audit 与 rarity-price-alignment）：

1. 单条蓝冠价值不得超同体型档 p90 的 5 倍（防个别鱼种定价离谱）。
2. 稀有（rarity=1）/ 超稀有（rarity=2）鱼，单价不得低于「体型同量级
   （蓝冠线 0.5~2 倍）普通鱼」的 p90（防稀有鱼比普通鱼还便宜）。

同量级样本 < 2 种时跳过——分位数在 n<2 时不可信。

饵料鱼（作为鱼饵原料出现在 loc_bait_fish 的饵名里）默认不参与普通鱼基线：
它们的价格是钓鱼循环的设计调控杆，不是市场价值。用 --include-bait 可纳入。

退出码：0 全部通过，1 有违规，2 数据源不可用。
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
PRICE_JSON = REPO / "internal/gameserver/assets/fish_market_prices.json"
DEFAULT_DB = REPO / "data/rf4server.db"

# 体型档边界（克）与档内 p90 单条价值（银），由本脚本 --report 输出维护。
SIZE_CLASSES = [
    (0, 200, "小于200g"),
    (200, 1000, "0.2-1kg"),
    (1000, 5000, "1-5kg"),
    (5000, 20000, "5-20kg"),
    (20000, 100000, "20-100kg"),
    (100000, float("inf"), "大于100kg"),
]
OUTLIER_MULTIPLE = 5.0
SAME_MAGNITUDE = (0.5, 2.0)
# 稀有鱼相对同量级普通鱼 p90 的最小溢价：齐平 p90 等于零溢价，历史上出现过
# n_carp / sh_barbel / syb_sterlet 单价与某条普通鱼数值完全相同的情形。
MIN_PREMIUM = 0.01


def load_prices() -> dict[str, float]:
    data = json.loads(PRICE_JSON.read_text(encoding="utf-8"))
    prices: dict[str, float] = {}
    for _map, species in data["by_map"].items():
        for key, value in species.items():
            prices[key] = float(value["1"] if isinstance(value, dict) else value)
    return prices


def load_db(db_path: Path):
    if not db_path.exists():
        print(f"数据源不可用：{db_path} 不存在", file=sys.stderr)
        sys.exit(2)
    db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    rarity = {k: int(v) for k, v in db.execute("select fish_key, rarity from species_rarity")}
    blue = {k: float(v) for k, v in db.execute("select fish_key, blue_trophy_g from fish_grade_lines")}
    names = {k: v for k, v in db.execute("select item_id, name_zh from item_catalog")}
    # 饵料词条：loc_bait_fish.bait 里出现的饵名 + 所有 bait*/lure* 类道具名。
    tokens: set[str] = set()
    for (bait,) in db.execute("select distinct bait from loc_bait_fish"):
        if not bait:
            continue
        for token in re.split(r"[;,+、/()（）\s]+", bait):
            if len(token) >= 2:
                tokens.add(token.strip())
    tokens |= {
        name
        for name, kind in db.execute("select name_zh, kind from item_catalog")
        if kind and (kind.startswith("bait") or kind.startswith("lure"))
    }
    db.close()
    return rarity, blue, names, tokens


def bait_fish(prices, names, tokens) -> set[str]:
    """鱼名出现在饵名里（双向包含）即视为饵料鱼。"""
    out = set()
    for key in prices:
        name = names.get(key)
        if not name:
            continue
        if any(name in t or (len(t) >= 3 and t in name) for t in tokens):
            out.add(key)
    return out


def size_class(blue_g: float) -> str:
    for lo, hi, label in SIZE_CLASSES:
        if lo <= blue_g < hi:
            return label
    return SIZE_CLASSES[-1][2]


def p90(values: list[float]) -> float:
    values = sorted(values)
    return values[int(0.9 * (len(values) - 1))]


def main() -> int:
    ap = argparse.ArgumentParser(description="全量鱼价结构审计")
    ap.add_argument("--db", type=Path, default=DEFAULT_DB, help="实机库路径（提供稀有度与蓝冠线）")
    ap.add_argument("--include-bait", action="store_true", help="把饵料鱼纳入普通鱼基线")
    ap.add_argument("--report", action="store_true", help="打印各体型档 p90 与饵料鱼清单")
    args = ap.parse_args()

    prices = load_prices()
    rarity, blue, names, tokens = load_db(args.db)
    baits = bait_fish(prices, names, tokens)

    missing = [k for k in prices if k not in rarity or k not in blue]
    if missing:
        print(f"缺失稀有度或蓝冠线：{len(missing)} 种 {missing[:5]}", file=sys.stderr)
        return 2

    use_bait = args.include_bait
    def is_baseline(key: str) -> bool:
        return rarity[key] == 0 and (use_bait or key not in baits)

    commons = [(k, prices[k], blue[k]) for k in prices if is_baseline(k)]
    failures = 0

    # 判据 1：单条蓝冠价值离群。
    by_class: dict[str, list[float]] = {}
    for k in prices:
        by_class.setdefault(size_class(blue[k]), []).append(prices[k] * blue[k] / 1000)
    class_p90 = {c: p90(v) for c, v in by_class.items()}
    for k in sorted(prices):
        value = prices[k] * blue[k] / 1000
        limit = class_p90[size_class(blue[k])] * OUTLIER_MULTIPLE
        if value > limit:
            failures += 1
            print(f"[离群] {k} 单条蓝冠价值 {value:.0f} 银，超 {size_class(blue[k])} 档 p90({class_p90[size_class(blue[k])]:.0f}) 的 {OUTLIER_MULTIPLE:.0f} 倍（单价 {prices[k]:.2f}/kg）")

    # 判据 2：稀有鱼单价低于同量级普通鱼 p90。
    # 关键：基线必须只看「普通鱼」，且不含被判定鱼自己——否则提价会把 p90 一起抬高，
    # 形成永远追不上的自指循环（实测：eel 提到 26.13 后 p90 变成 26.13）。
    lo, hi = SAME_MAGNITUDE
    skipped = 0
    for k in sorted(prices):
        if rarity[k] == 0 or (not use_bait and k in baits):
            continue
        peers = sorted(p for (ck, p, cb) in commons if ck != k and lo <= blue[k] / cb <= hi)
        if len(peers) < 2:
            skipped += 1
            continue
        limit = p90(peers)
        threshold = limit * (1 + MIN_PREMIUM)
        if prices[k] < threshold:
            failures += 1
            print(f"[稀有倒挂] {k}(r={rarity[k]}) 单价 {prices[k]:.2f} 未高于同克重普通鱼 p90 {limit:.2f} 至少 {MIN_PREMIUM*100:.0f}%")

    if args.report:
        print("\n各体型档单条蓝冠价值 p90：")
        for _lo, _hi, label in SIZE_CLASSES:
            print(f"  {label:<10} {class_p90.get(label, 0):8.0f} 银  （{len(by_class.get(label, []))} 种）")
        print(f"\n饵料鱼 {len(baits)} 种：{', '.join(sorted(baits))}")
        print(f"基线普通鱼 {len(commons)} 种（include_bait={use_bait}）")

    if skipped:
        print(f"注：{skipped} 种稀有鱼同量级样本不足 2 种，已跳过分位判定。")
    print(f"鱼价审计：{len(prices)} 种，违规 {failures} 项。")
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    sys.exit(main())