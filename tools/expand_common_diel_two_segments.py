#!/usr/bin/env python3
"""把「普通」稀有度里只有一个昼夜活跃段的鱼扩成两个时段。

规则（2026-11-26 定）：
  - 只有「夜」→ 补「昏」；
  - 只有「昼」→ 补「晨」。

为什么改曲线而不是只改掩码：判鱼侧 fishDielWeight 在曲线非空时**只看曲线**，
diel_mask 仅用于界面展示与「没有曲线」时的回退。253 条鱼全都有曲线，所以只改
掩码等于什么都没改（后台会显示「已覆盖」，实际咬口分布一点没变）。

为什么新增段给 0.65 而不是 0.5：
  - 「活跃段」判定线是曲线值 >= 0.5（dielActiveThreshold），低于它会被
    fish_diel_inactive_block 整段禁口，新增时段等于白加；
  - 0.5 正好卡在线上，后续微调曲线时容易掉回禁口；
  - 0.65 明显高于活跃线、又明显低于峰值 1.0，保留「次要时段」语义：
    该段权重 x0.65，能钓到但这段时间不如主时段密。

掩码由曲线重新推导（>=0.5 置位），保证「鱼种卡上的活跃时段」与判鱼口径一致。

用法：
    python tools/expand_common_diel_two_segments.py          # 只预览，不写盘
    python tools/expand_common_diel_two_segments.py --apply  # 写资产 + 写库
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ASSET = ROOT / "internal/gameserver/assets/fish_ecology_defs.json"
DB = ROOT / "data/rf4server.db"

# 位序与 diel_curve 下标一一对应：[夜, 晨, 昼, 昏]
SEG_NIGHT, SEG_DAWN, SEG_DAY, SEG_DUSK = 0, 1, 2, 3
SEG_NAMES = ["夜", "晨", "昼", "昏"]

# 只有一个时段时补哪个：夜→昏、昼→晨（黄昏接夜、清晨接昼，都是相邻的过渡时段）
FILL_FOR_SINGLE = {SEG_NIGHT: SEG_DUSK, SEG_DAY: SEG_DAWN}
# 补进去的时段权重
FILL_WEIGHT = 0.65
# 活跃段判定线，与 gameserver.dielActiveThreshold 同值
ACTIVE_THRESHOLD = 0.5


def active_segments(mask: int) -> list[int]:
    return [i for i in range(4) if mask & (1 << i)]


def mask_from_curve(curve: list[float]) -> int:
    return sum(1 << i for i, v in enumerate(curve) if v >= ACTIVE_THRESHOLD)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true", help="写回资产与数据库（默认只预览）")
    ap.add_argument("--db", default=str(DB), help="sqlite 路径")
    args = ap.parse_args()

    asset: list[dict] = json.loads(ASSET.read_text(encoding="utf-8"))
    by_key = {x["fish_key"]: x for x in asset}

    conn = sqlite3.connect(args.db)
    conn.row_factory = sqlite3.Row
    rarity = {r["fish_key"]: r["rarity"] for r in conn.execute("SELECT * FROM species_rarity")}

    targets = []
    for x in asset:
        key = x["fish_key"]
        # 只动「普通」：稀有 / 超稀有 的时段少是刻意的稀缺性设计，不该被摊平
        if rarity.get(key, 0) != 0:
            continue
        mask = x["diel_mask"]
        segs = active_segments(mask)
        if len(segs) != 1:
            continue
        src = segs[0]
        if src not in FILL_FOR_SINGLE:
            continue
        targets.append((key, x, src, FILL_FOR_SINGLE[src]))

    if not targets:
        print("没有需要处理的鱼种")
        return 0

    print(f"待处理 {len(targets)} 条「普通」单时段鱼（新增段权重 {FILL_WEIGHT}）：\n")
    print(f"{'fish_key':22} {'中文名':14} {'原时段':6} {'补':4} {'原掩码':6} {'新掩码':6}  曲线变化")
    print("-" * 110)
    planned = []
    for key, x, src, dst in targets:
        curve = json.loads(x["diel_curve"])
        old_curve = list(curve)
        curve[dst] = FILL_WEIGHT
        new_mask = mask_from_curve(curve)
        name = (x.get("source") or {}).get("name", "")
        print(
            f"{key:22} {name:14} {SEG_NAMES[src]:6} {SEG_NAMES[dst]:4} "
            f"{x['diel_mask']:6d} {new_mask:6d}  {x['diel_curve']} -> {json.dumps(curve)}"
        )
        planned.append((key, x, curve, new_mask))

    if not args.apply:
        print("\n预览模式：未写盘。加 --apply 才会写回资产与数据库。")
        return 0

    # 1) 资产 JSON：只改目标行的 diel_curve / diel_mask，其余字段与键序原样保留
    for key, x, curve, new_mask in planned:
        x["diel_curve"] = json.dumps(curve)
        x["diel_mask"] = new_mask
        x["diel_source"] = "local:two-segment"
    ASSET.write_text(
        json.dumps(asset, ensure_ascii=False, indent=2).replace("\n", "\r\n") + "\r\n",
        encoding="utf-8",
        newline="",
    )
    print(f"\n资产已写回：{ASSET.relative_to(ROOT)}")

    # 2) 数据库基线表：资产由启动时的 ensureFishEcology 重灌，但版本号不抬就灌不进去，
    #    所以这里直接同步写库，避免「改了文件但库里还是旧值」的中间态。
    for key, x, curve, new_mask in planned:
        conn.execute(
            "UPDATE fish_ecology SET diel_curve=?, diel_mask=? WHERE fish_key=?",
            (json.dumps(curve), new_mask, key),
        )
    conn.commit()
    print(f"数据库基线已同步：{len(planned)} 行")

    # 3) 清掉「只有掩码、没有曲线」的覆盖：判鱼侧曲线优先，这种行完全不起作用，
    #    留着只会让列表显示「已覆盖」而实际咬口没变，误导后续调参。
    #    只删 diel_curve 为 NULL 的行——单独设了曲线的覆盖是真生效的，不能碰。
    rows = list(conn.execute("SELECT * FROM fish_ecology_species"))
    cleared = []
    for r in rows:
        cols = {c for c in r.keys() if r[c] is not None}
        payload = cols - {"fish_key", "origin", "updated_by", "updated_ts"}
        if payload == {"diel_mask"} and r["diel_curve"] is None:
            conn.execute("DELETE FROM fish_ecology_species WHERE fish_key=?", (r["fish_key"],))
            cleared.append((r["fish_key"], r["diel_mask"]))
    conn.commit()
    if cleared:
        print(f"\n已清除 {len(cleared)} 条失效的昼夜覆盖（判鱼侧曲线优先，掩码不生效）：")
        for key, mask in cleared:
            print(f"  {key:22} mask={mask}")

    # 4) 自检：全库「掩码 == 曲线推导」
    bad = []
    for r in conn.execute("SELECT fish_key, diel_mask, diel_curve FROM fish_ecology"):
        try:
            curve = json.loads(r["diel_curve"])
        except (TypeError, ValueError):
            continue
        if mask_from_curve(curve) != r["diel_mask"]:
            bad.append(r["fish_key"])
    conn.close()
    if bad:
        print(f"\n自检失败：{len(bad)} 条掩码与曲线不一致 -> {bad[:10]}", file=sys.stderr)
        return 1
    print("\n自检通过：全部鱼种 diel_mask == 曲线推导结果")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())