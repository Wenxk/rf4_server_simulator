"""按用户方案生成鱼种适温：稀有/超稀有收窄，普通鱼按地图水温动态对齐。

方案规则（2026-11-23 用户明确拍板）：
  1) 稀有/超稀有鱼：窗口收窄，更难钓获
       超稀有 r2 → 半宽 2.5℃（总宽 5℃，tolerance=2.5 刚好大于 2.2 下限）
       稀有   r1 → 半宽 3.5℃（总宽 7℃）
       普通   r0 → 半宽 5.5℃（总宽 11℃）
  2) 普通鱼按该图水温动态分配，保证每个温度档都有鱼钓
  3) 允许重叠，不要求季节换鱼
  4) 同亲本变种（锦鲤、镜鲤）在 ±4℃ 内错位，避免一次全中

求解流程（三阶段，替代「逐图取中位数」——那会把鱼全拉回中段）：
  A 初始化：全部鱼按文献 pref 定窗口（虚构鱼种保留基线）
  B 变种错位：同亲本在 ±4℃ 内铺开
  C 逐图补缺：对每张图的「主温区」按 2℃ 分档，档内不足 min_per_band 条时
      先尝试「拉伸窗口」（半宽临时可到 MAX_HALF_BY_RARITY）
      再尝试「移动 pref」（优先普通鱼、优先其他图依赖少的）
    反复迭代直到无缺口或达到轮次上限。

「主温区」的判定：
  按该图逐时水温统计频率，取累计前 coverage 的档位。
  coverage 自适应：鱼多的图铺得宽，鱼少的图只保证核心区
      n>=40 → 0.90；n>=20 → 0.85；n>=10 → 0.75；否则 0.65

只读，不写库。输出 ref/fish_temp_final.csv
"""

import argparse
import collections
import csv
import json
import os
import sqlite3
import math
import statistics
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from fish_temp_calibrate import MAP_DEFS, load_map, water_at  # noqa: E402
from fish_temp_literature import PARENT, SPECIES  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "rf4server.db")
MULT = os.path.join(ROOT, "internal", "gameserver", "assets", "map_market_multipliers.json")

HALF_BY_RARITY = {0: 5.5, 1: 3.5, 2: 2.5}
# 补缺时可临时拉伸到的半宽上限（普通 17℃ / 稀有 9℃ / 超稀有 7℃）
MAX_HALF_BY_RARITY = {0: 8.5, 1: 4.5, 2: 3.5}
VARIANT_SPREAD = 4.0
BAND_W = 2.0
# 全库实测水温 1.55~32.22℃，≤2℃ 仅占 0.03%。
# 下限给到 -3℃ 是纯浪费：多出的 4.5℃ 会挤占上限、并被判成「偏离文献」。
# 取 1.0℃（略低于实测最低，留一点余量），上限留 34℃ 覆盖阿赫图巴河 32.2℃。
LO_LIMIT, HI_LIMIT = 1.0, 34.0


def _lo(pref, half):
    """下限向下取整到 0.1 —— 宁可多给 0.05℃，也不要在两条鱼之间裂缝。"""
    return round(math.floor((pref - half) * 10) / 10, 1)


def _hi(pref, half):
    """上限向上取整到 0.1。"""
    return round(math.ceil((pref + half) * 10) / 10, 1)


def _bound(pref, half):
    pf = round(pref, 1)
    return [max(LO_LIMIT, _lo(pf, half)), pf, min(HI_LIMIT, _hi(pf, half))]
SN = ["春", "夏", "秋", "冬"]


def lit_pref(key):
    if key in SPECIES:
        return SPECIES[key][3]
    par = PARENT.get(key)
    return SPECIES[par][3] if par in SPECIES else None


def coverage_for(n):
    if n >= 40:
        return 0.90
    if n >= 20:
        return 0.85
    if n >= 10:
        return 0.75
    return 0.65


def band_requirement(n_keys, want):
    """每档最少几条鱼。鱼太少的图只要求 1 条。

    用户原话「只要保证每个区间都有鱼钓就行了」——下限就是 1 条。
    鱼种充足的图再抬高到 want 条（默认 2），避免整段只有一种鱼。
    """
    return max(1, min(want, n_keys // 6))


def main_bands(freq, allv, cov):
    """按 1℃ 频率累计取覆盖 cov 的区间。

    返回 (1℃ 档中心列表, t_lo, t_hi, 2℃ 粗档区间列表)。
    逐档检查用 1℃（保证「每个温度值都有鱼」），
    统计达标率另用 2℃ 粗档区间（按整段取并集，避免单点取样的噪声）。
    """
    acc, kept = 0.0, []
    for t in sorted(freq):
        acc += freq[t] / len(allv)
        kept.append(t)
        if acc >= cov:
            break
    if not kept:
        return [], 0.0, 0.0, []
    t_lo, t_hi = float(min(kept)), float(max(kept) + 1)
    fine = [t + 0.5 for t in sorted(kept)]
    coarse = []
    a = t_lo
    while a < t_hi - 1e-9:
        coarse.append((a, min(a + BAND_W, t_hi)))
        a += BAND_W
    return fine, t_lo, t_hi, coarse


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--min-per-band", type=int, default=2)
    ap.add_argument("--rounds", type=int, default=8)
    ap.add_argument("--max-drift", type=float, default=999.0,
                    help="普通鱼 pref 允许偏离文献 pref 的最大 ℃ 数")
    ap.add_argument("--csv", default="ref/fish_temp_final.csv")
    args = ap.parse_args()

    d = json.load(open(MULT, encoding="utf-8"))
    zh2id = {v["map"]: k for k, v in d["by_map_key"].items()
             if isinstance(v, dict) and v.get("map")}

    md = json.load(open(MAP_DEFS, encoding="utf-8"))
    # 水温必须按「全水层」统计：判鱼侧 layer 随钓法变
    #   bottomRigTypes → .93（底钓）  lure → effectiveLureLayer（拟饵泳层）
    #   其它 → normalizeWaterLayer(depthCM/500)
    # 只按中层 .45 会把范围算窄 2~7.6℃，误判成「地图温度不够」。
    LAYERS = (0.0, 0.2, 0.45, 0.7, 0.93)
    wt = {}
    for name, node in md["maps"].items():
        m = load_map(node, name)
        wt[name] = {s: [water_at(m, day, h, s, 1.0, ly)[1]
                        for day in range(8) for h in range(24) for ly in LAYERS]
                    for s in (0, 1, 2, 3)}

    db = sqlite3.connect(DB)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    cur.execute("SELECT loc, fish_key FROM loc_fish")
    roster = {}
    for r in cur.fetchall():
        roster.setdefault(r["loc"], set()).add(r["fish_key"])
    cur.execute("SELECT fish_key, rarity_tier, temp_min_c, temp_pref_c, temp_max_c "
                "FROM fish_ecology")
    base = {r["fish_key"]: (r["rarity_tier"], r["temp_min_c"],
                            r["temp_pref_c"], r["temp_max_c"])
            for r in cur.fetchall()}
    cur.execute("SELECT fish_key, fish_zh FROM loc_wiki_fish")
    zh = {}
    for r in cur.fetchall():
        if r["fish_zh"]:
            zh.setdefault(r["fish_key"], r["fish_zh"].replace("\ufeff", ""))

    # 只统计有地图 ID 的名册
    maps = []
    for loc, keys in roster.items():
        mid = zh2id.get(loc)
        if not mid or mid not in wt:
            continue
        allv = [v for s in (0, 1, 2, 3) for v in wt[mid][s]]
        freq = collections.Counter(int(v) for v in allv)
        cov = coverage_for(len(keys))
        centers, t_lo, t_hi, coarse = main_bands(freq, allv, cov)
        maps.append({"loc": loc, "keys": [k for k in keys if k in base],
                     "allv": allv, "t_lo": t_lo, "t_hi": t_hi,
                     "cov": cov, "centers": centers, "coarse": coarse,
                     "lo": min(allv), "hi": max(allv),
                     "need": band_requirement(len([k for k in keys if k in base]),
                                              args.min_per_band)})
    maps.sort(key=lambda m: -len(m["keys"]))

    # ── A 初始化：按文献定窗口 ──
    win = {}     # key -> [mn, pref, mx]
    origin = {}  # key -> 依据
    for k, (rar, bmin, bpref, bmax) in base.items():
        half = HALF_BY_RARITY.get(rar, 5.5)
        lp = lit_pref(k)
        if lp is None or lp <= 0:
            win[k] = [bmin, bpref, bmax]
            origin[k] = "保留基线"
            continue
        win[k] = _bound(lp, half)
        origin[k] = "文献"

    # ── B 变种错位 ──
    groups = {}
    for k in win:
        par = PARENT.get(k)
        if par:
            groups.setdefault(par, []).append(k)
    shifted = 0
    for par, members in groups.items():
        if len(members) < 2:
            continue
        members.sort()
        anchor = win[par][1] if par in win else statistics.median(
            [win[k][1] for k in members])
        n = len(members)
        for i, k in enumerate(members):
            off = -VARIANT_SPREAD / 2 + VARIANT_SPREAD * (i / (n - 1))
            half = HALF_BY_RARITY.get(base[k][0], 5.5)
            pref = round(anchor + off, 1)
            win[k] = _bound(pref, half)
            origin[k] = "变种-错位"
            shifted += 1

    # 鱼被多少张图需要（移动代价权重）
    demand = collections.Counter()
    for m in maps:
        for k in m["keys"]:
            demand[k] += 1

    def covers(k, c):
        w = win.get(k)
        return bool(w) and w[0] <= c <= w[2]

    def count(m, c):
        return sum(1 for k in m["keys"] if covers(k, c))

    # ── C 逐图补缺 ──
    stretched = moved = 0
    for rnd in range(args.rounds):
        fixed = 0
        for m in maps:
            for c in m["centers"]:
                deficit = m["need"] - count(m, c)
                if deficit <= 0:
                    continue
                pool = [k for k in m["keys"] if k in win]
                if not pool:
                    continue

                # tier1：拉伸已有鱼的窗口（不动 pref，代价最小）
                # 排序优先级：① 拉伸后仍落在文献区间内（不违背生物学）
                #            ② 拉伸幅度小  ③ 普通鱼优先
                def stretch_cost(k, c):
                    rar = base[k][0]
                    half_now = (win[k][2] - win[k][0]) / 2
                    half = min(max(half_now, abs(c - win[k][1])),
                               MAX_HALF_BY_RARITY.get(rar, half_now))
                    lo = win[k][1] - half
                    hi = win[k][1] + half
                    sp = SPECIES.get(k)
                    if sp and sp[1] <= sp[2]:
                        # 0 = 完全在文献内，1 = 超出文献幅度（按 ℃ 计）
                        over = max(0.0, sp[1] - lo) + max(0.0, hi - sp[2])
                    else:
                        over = 0.0
                    return (round(over, 1) > 0, round(over, 1), half - half_now, rar, k)

                cands = []
                for k in pool:
                    rar = base[k][0]
                    half_now = (win[k][2] - win[k][0]) / 2
                    half_max = MAX_HALF_BY_RARITY.get(rar, half_now)
                    need = abs(c - win[k][1])
                    if need <= half_max + 1e-9 and need > half_now + 1e-9:
                        cands.append(stretch_cost(k, c))
                cands.sort()
                for *_, k in cands[:deficit]:
                    half_max = MAX_HALF_BY_RARITY.get(base[k][0], 5.5)
                    pref = win[k][1]
                    half = max((win[k][2] - win[k][0]) / 2, abs(c - pref))
                    half = min(half, half_max)
                    win[k] = _bound(pref, half)
                    origin[k] = "补缺-拉伸"
                    stretched += 1
                    fixed += 1

                deficit = m["need"] - count(m, c)
                if deficit <= 0:
                    continue

                # tier2：移动 pref
                # 排序优先级：① 移动后偏离文献少（生物学代价低优先）
                #            ② 普通鱼优先  ③ 其他图依赖少  ④ 位移小
                cands = []
                for k in pool:
                    if covers(k, c):
                        continue
                    rar = base[k][0]
                    ref = lit_pref(k)
                    if ref is not None and abs(c - ref) > args.max_drift:
                        continue   # 漂移超限：宁可这一档空着
                    # 移动后该鱼偏离文献的幅度
                    if ref is None:
                        drift = 0.0
                    else:
                        sp = SPECIES.get(k)
                        half = (win[k][2] - win[k][0]) / 2
                        lo, hi = c - half, c + half
                        if sp and sp[1] <= sp[2]:
                            drift = max(0.0, sp[1] - lo) + max(0.0, hi - sp[2])
                        else:
                            drift = abs(c - ref)
                    cost = (round(drift, 1), rar, demand[k], abs(c - win[k][1]))
                    cands.append((cost, k))
                cands.sort()
                for _, k in cands[:deficit]:
                    half = (win[k][2] - win[k][0]) / 2
                    pref = round(min(HI_LIMIT - half, max(LO_LIMIT + half, c)), 1)
                    win[k] = _bound(pref, half)
                    origin[k] = "补缺-移动"
                    moved += 1
                    fixed += 1
        if fixed == 0:
            break

    # ── 校验 ──
    print(f"窗口：普通 ±{HALF_BY_RARITY[0]} / 稀有 ±{HALF_BY_RARITY[1]} / "
          f"超稀有 ±{HALF_BY_RARITY[2]} ℃")
    print(f"变种错位 {shifted} 条（同亲本在 ±{VARIANT_SPREAD}℃ 内铺开）")
    print(f"补缺：拉伸窗口 {stretched} 次 / 移动 pref {moved} 次")
    print(f"分档宽度 {BAND_W}℃，主温区按鱼数自适应："
          f"n>=40→90%  n>=20→85%  n>=10→75%  否则 65%\n")

    print("每图各温度档鱼数（括号=稀有+超稀有，!! = 主温区内不足该图门槛）\n")
    problems = []
    for m in maps:
        freq = collections.Counter(int(v) for v in m["allv"])
        line, bad = [], 0
        for t in sorted(freq):
            c = t + 0.5
            ids = [k for k in m["keys"] if covers(k, c)]
            nr = sum(1 for k in ids if base[k][0] > 0)
            in_main = m["t_lo"] <= c <= m["t_hi"]
            if in_main and len(ids) < m["need"]:
                bad += 1
                line.append(f"{t}℃:{len(ids)}({nr})!!")
            else:
                mark = "" if in_main else "·"
                line.append(f"{t}℃:{len(ids)}({nr}){mark}")
        print(f"== {m['loc']}  {m['lo']:.1f}~{m['hi']:.1f}℃  {len(m['keys'])} 种  "
              f"主温区 {m['t_lo']:.0f}~{m['t_hi']:.0f}℃(覆盖{m['cov']:.0%})")
        print("   " + "  ".join(line))
        if bad:
            problems.append((m["loc"], bad))
        print()

    if problems:
        print(f"== 主温区仍不足（{len(problems)} 张图）==")
        for loc, n in problems:
            print(f"  {loc}: {n} 个 1℃ 档位不足该图门槛")
    else:
        print("✓ 所有图的主温区 1℃ 档位均达标")

    # 2℃ 粗档达标率：粗档由两个 1℃ 档组成，取两档中心判断
    tot = ok = 0
    bad_bands = []
    for m in maps:
        for a, b in m["coarse"]:
            tot += 1
            pts = [a + 0.5] if b - a <= 1.0 else [a + 0.5, b - 0.5]
            worst_t, worst_n = None, 10 ** 9
            for c in pts:
                n = count(m, c)
                if n < worst_n:
                    worst_t, worst_n = c, n
            if worst_n >= m["need"]:
                ok += 1
            else:
                bad_bands.append((m["loc"], a, b, worst_t, worst_n, m["need"]))
    print(f"\n按 {BAND_W}℃ 粗档统计（两档中心取样）："
          f"{ok}/{tot} 档达标（{ok / tot:.1%}）")
    if bad_bands:
        print("  不达标明细（端点处恰好落在窗口缝隙，不代表整段无鱼）：")
        for loc, a, b, t, n, need in bad_bands:
            print(f"    {loc} {a:.0f}~{b:.0f}℃ @{t:.2f}℃ 只有 {n} 条（需 {need}）")

    print("\n== 稀有度难度验证（全库随机取温 2000 次，平均能钓到几条）==")
    import random
    random.seed(42)
    samples = [random.uniform(2, 30) for _ in range(2000)]
    stat = {}
    for rar, name in [(0, "普通"), (1, "稀有"), (2, "超稀有")]:
        cnt = 0
        for t in samples:
            cnt += sum(1 for k, v in win.items()
                       if base[k][0] == rar and v[0] <= t <= v[2])
        stat[name] = cnt / len(samples)
        print(f"  {name:<4} 平均 {stat[name]:5.1f} 条可钓")
    print(f"  难度比（普通:稀有:超稀有）= "
          f"1 : {stat['稀有'] / stat['普通']:.2f} : {stat['超稀有'] / stat['普通']:.2f}")

    out = os.path.join(ROOT, args.csv)
    rows = []
    for k, (mn, pf, mx) in sorted(win.items()):
        rar = base.get(k, (0, 0, 0, 0))[0]
        rows.append({"fish_key": k, "zh": zh.get(k, ""), "rarity": rar,
                     "temp_min_c": mn, "temp_pref_c": pf, "temp_max_c": mx,
                     "width": round(mx - mn, 1), "basis": origin.get(k, "")})
    with open(out, "w", newline="", encoding="utf-8-sig") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)
    print(f"\n已导出 → {out}（{len(rows)} 条）")


if __name__ == "__main__":
    main()