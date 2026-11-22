#!/usr/bin/env python3
"""按 A+B 口径给鱼种补「爱吃饵」名单，写入 fish_bait_override。

A：companion_bait_fish tier3（社区饵效实证的最高档）
B：rf4records_catch 官服周纪录（同一鱼种该饵占比 >= --min-share 且样本 >= --min-count）

写入规则：
- 以内置 fishing_bait_rules.txt 的 252 条规则为基线，**deny / bottom / condition 原样带过去**，
  否则整行覆盖会丢掉禁饵、底钓只吃与海图/jig 前置条件。
- 只给「确实有新饵可加」的鱼写覆盖；没有增量的鱼保持跟随内置表，避免无谓脱钩。
- 已被现有类目词（蠕虫类/拟饵/各类饵…）覆盖的饵不重复追加，保持名单可读。

用法：
  python -I tools/merge_bait_allowlist.py --db data/rf4server.db --dry-run
  python -I tools/merge_bait_allowlist.py --db data/rf4server.db --write
"""

import argparse
import json
import re
import sqlite3
import sys
import time

sys.stdout.reconfigure(encoding="utf-8")

RULES_PATH = "internal/gameserver/fishing_bait_rules.txt"
BULK_AUTHOR = "bulk-A+B"   # 本工具写入标记，重跑时按它清理旧行

# 与 fishing_bait_rules.go 的 baitNameAliases 同源：同一款饵的多种叫法。
ALIASES = {
    "muckworm": ["红蚯蚓"],
    "worm_sur": ["粪便蠕虫", "苏尔斯基粪蚯蚓"],
    "worm_volh": ["粪便蠕虫", "伏尔霍夫斯基粪蚯蚓"],
    "worm_asurv": ["粪便蠕虫", "阿斯特拉罕粪蚯蚓"],
    "dough_sweet": ["甜的面团"],
    "caddisfly": ["毛翅目幼虫"],
    "baitfrog": ["青蛙"],
    "fish_flesh": ["鱼肉块"],
    "fish_pieces": ["鱼肉", "鱼肉块", "鱼肉类", "鱼片", "鱼块"],
    "bread": ["面包屑", "湿面包"],
    "perch_lf": ["河鲈饵", "河鲈饵鱼", "河鲈"],
}


def rule_name(s):
    """复刻 Go 的 ruleName：删 BOM/括号、异体字归一、去所有空白、转小写。
    漏掉「去空白 + 转小写」会和运行时算出两个不同的键（`C2超级怪胎` vs
    `c2超级怪胎`、`无鳞鲤 人面鲤` vs `无鳞鲤人面鲤`）。"""
    variant = {"鮟": "𩽾", "鱇": "𩾌"}
    kana = {"鯡": "鲱", "鮭": "鲑", "鰻": "鳗", "鳌": "螯"}
    out = []
    for ch in s:
        if ch in "\ufeff()（）":
            continue
        ch = kana.get(ch, variant.get(ch, ch))
        if ch.isspace():
            continue
        out.append(ch.lower())
    return "".join(out)


def rule_key(s):
    """比 ruleName 更狠的归一：去空白/连字符/NBSP/全角，供类目词与别名比对。"""
    s = s.lower()
    s = re.sub(r"[\s\u00a0\-_]+", "", s)
    s = s.replace("（", "(").replace("）", ")").replace("／", "/")
    return s


def parse_rules(path):
    rules = {}
    for line in open(path, encoding="utf-8"):
        if "  吃 " not in line:
            continue
        name, tail = line.split("  吃 ", 1)
        if "  前置条件 " not in tail:
            continue
        bait, cond = tail.split("  前置条件 ", 1)
        bait = bait.replace("（", "(").replace("）", ")")
        # 注意：Python 的 partition 返回 (前, 分隔符, 后)，Go 的 strings.Cut 返回
        # (前, 后, found)——顺序不同，写成 (allowed, extra, _) 会把 extra 取成分隔符，
        # 导致 deny / bottomOnly 整体丢失。
        allowed, _, extra = bait.partition("(")
        allow = [x for x in allowed.strip().split("/") if x.strip()]
        deny, bottom = [], []
        if "不吃" in extra:
            seg = re.split(r"[）；;)]", extra.split("不吃", 1)[1])[0]
            deny = [x for x in seg.split("/") if x.strip()]
        if "底钓只吃" in extra:
            seg = extra.split("底钓只吃", 1)[1].rstrip(") \r\n\t")
            bottom = [x for x in seg.split("/") if x.strip()]
        rules[rule_name(name)] = {
            "fish_zh": name.strip(),
            "allow": allow,
            "deny": deny,
            "bottom": bottom,
            "cond": cond.strip(),
        }
    return rules


# 与 internal/gameserver/bait_type_terms.go 对齐：整类词 → (品类, 二级类型集合)。
# 品类 0 = 不限；二级类型在饵料里是 baitType、在拟饵里是 lureType。
# 这张表决定「这款饵是否已被整类词覆盖」——漏一个词，工具就会把整类的每一款饵
# 重复补进 allow，名单立刻膨胀几十条。改 Go 表时同步这里。
_CATEGORY_TERMS = {
    "各类饵": (0, set()),
    "饵料": (27, set()),
    "拟饵": (17, set()),
    "沉底饵": (27, {1}),
    "虫类": (27, {2}),
    "蛙类": (27, {3}),
    "活饵类": (27, {4}),
    "蚯蚓类": (27, {6}),
    "昆虫类": (27, {7}),
    "浮水饵": (27, {8}),
    "颗粒": (27, {9}),
    "玉米浮": (27, {10}),
    "坚果": (27, {11}),
    "钩虾类": (27, {12}),
    "面团类": (27, {13}),
    "鱼片类": (27, {14}),
    "死饵鱼类": (27, {15}),
    "海钓饵": (27, {16}),
    "Zig浮水球": (27, {17}),
    "旋转亮片": (17, {1}),
    "莫斯科路亚": (17, {2}),
    "软饵类": (17, {3}),
    "Crank摇摆": (17, {4}),
    "Popper波扒": (17, {5}),
    "Pilker铁板": (17, {6}),
    "Jerk抽停": (17, {7}),
    "Tube软管": (17, {8}),
    "W-Stick软虫": (17, {9}),
    "Mouse鼠型": (17, {10}),
    "Fly飞蝇": (17, {11}),
    "大号软饵": (17, {12}),
    "Gummi软鳗": (17, {13}),
    "Octopus软饵": (17, {14}),
    "虾型": (17, {15}),
    "Flyingworm飞虫": (17, {16}),
    "鱼卵珠": (17, {17}),
    "Jig铅头": (17, {18}),
    "FlipJig铁板": (17, {19}),
    "Vibra震动": (17, {20}),
    # 历史写法（内置规则表在用）
    "昆虫幼虫": (27, {2}),
    "蠕虫": (27, {6}),
    "蠕虫类": (27, {6}),
    "昆虫": (27, {7}),
    "饵鱼": (27, {4, 15}),
    "饵鱼类": (27, {4, 15}),
    "各类饵鱼": (27, {4, 15}),
    "鱼肉类": (27, {14}),
    "亮片": (17, {1, 2}),
    "亮片类": (17, {1, 2}),
    "亮片系列饵": (17, {1, 2}),
    "旋转饵": (17, {1}),
    "旋转饵类": (17, {1}),
    "旋转亮片路亚": (17, {1}),
    "拟真软饵": (17, {3}),
}
CATEGORY_TERMS = {rule_key(k): v for k, v in _CATEGORY_TERMS.items()}


def bait_covers(term, bait, id2zh):
    """复刻 ruleBait.matches：判断整类词 / 别名 / 精确名 / 前缀是否覆盖该饵。
    bait = (item_id, name_zh, category, lure_type, bait_type)
    """
    bid, name_zh, cat, lure, btype = bait
    t = rule_name(term)
    tk = rule_key(term)
    n = rule_name(name_zh)
    nk = rule_key(name_zh)

    for alias in ALIASES.get(bid, []):
        if rule_name(alias) == t:
            return True
    if tk in CATEGORY_TERMS:
        tcat, tsubs = CATEGORY_TERMS[tk]
        if tcat and cat != tcat:
            return False
        if not tsubs:
            return True
        sub = lure if cat == 17 else btype
        if sub in tsubs:
            return True
        # 规则表里的「鱼肉类」历史上还点名了 fish_flesh——它今天已是 baitType 14，
        # 这条兜底只为兼容旧数据。
        return tk == rule_key("鱼肉类") and bid == "fish_flesh"
    for suffix in ("系列饵", "系列", "类"):
        if t.endswith(suffix) and len(t) > len(suffix):
            stem = t[: -len(suffix)]
            if n.startswith(stem):
                tail = n[len(stem):]
                if stem[-1].isdigit() and tail[:1].isdigit():
                    return False
                return True
            return False
    if n == t:
        return True
    if re.search(r"[a-z]", t) and n.startswith(t):
        tail = n[len(t):]
        if tail and (tail[0] in "-#" or (not t[-1].isdigit() and tail[0].isdigit())):
            return True
    # ruleKey 层：去排版后再比，剥掉「类/饵/系列」后缀
    for kt in {tk}:
        for suffix in ("系列饵", "系列", "类", "饵"):
            if kt.endswith(suffix) and len(kt) > len(suffix):
                if nk.startswith(kt[: -len(suffix)]):
                    return True
                break
    return nk == tk


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default="data/rf4server.db")
    ap.add_argument("--rules", default=RULES_PATH)
    ap.add_argument("--min-share", type=float, default=5.0, help="rf4 单饵占比下限（%%）")
    ap.add_argument("--min-count", type=int, default=5, help="rf4 单饵样本量下限")
    ap.add_argument("--max-add", type=int, default=200, help="单鱼最多新增饵数（防爆）")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--write", action="store_true")
    ap.add_argument("--report", default=".tmp_merge_report.json")
    args = ap.parse_args()
    if not args.dry_run and not args.write:
        raise SystemExit("要么 --dry-run，要么 --write")

    db = sqlite3.connect(args.db)
    cur = db.cursor()

    rules = parse_rules(args.rules)
    print(f"内置规则 {len(rules)} 条")

    # 饵定义（中文名 + 类目/子类型）
    cur.execute(
        "SELECT d.item_id, COALESCE(c.name_zh,''), d.category, COALESCE(d.attrs_json,'{}')"
        " FROM item_def d LEFT JOIN item_catalog c ON c.item_id=d.item_id"
        " WHERE d.category IN (17,27)"
    )
    baits = {}
    for iid, zh, cat, raw in cur.fetchall():
        if not zh:
            continue
        try:
            attrs = json.loads(raw)
        except Exception:
            attrs = {}
        baits[iid] = (iid, zh, cat, int(attrs.get("mbemakijdeh") or 0), int(attrs.get("fopbhlmkcpe") or 0))
    print(f"可用饵定义 {len(baits)}")

    # fish_key -> 规则名
    key2names = {}
    for sql in ("SELECT fish_key,name_zh FROM loc_fish WHERE name_zh<>''",
                "SELECT fish_key,fish_zh FROM loc_wiki_fish WHERE fish_zh<>''"):
        for k, n in cur.execute(sql):
            key2names.setdefault(k, set()).add(n)
    by_norm = {rule_name(k): k for k in rules}

    def has_builtin(name):
        k = rule_name(name)
        return k in by_norm or (k.endswith("鱼") and k[:-1] in by_norm) or (k + "鱼") in by_norm

    key2rule = {}
    for k, names in key2names.items():
        hits = {by_norm[rule_name(n)] for n in names if rule_name(n) in by_norm}
        if len(hits) == 1:
            key2rule[k] = hits.pop()
    print(f"鱼种映射 {len(key2rule)}/{len(key2names)}")

    # 运行时实际查找的鱼名：复刻 loadFishNames。loc_wiki_fish 是按水域存名字的
    # （泥鳅在克马羚诺也湖叫「泥鳅鱼」、在阿赫图巴河叫「泥鳅」），UNION 之后
    # 同一 fish_key 会出现多行，循环里后者覆盖前者。覆盖表的键必须用这个结果，
    # 否则写「泥鳅」而运行时查「泥鳅鱼」，整条覆盖被静默忽略。
    runtime_names = {}
    for k, n in cur.execute("SELECT fish_key,fish_zh FROM loc_wiki_fish"
                            " UNION SELECT fish_key,name_zh FROM loc_fish"):
        # 库里有 65 条鱼名带 BOM（如铜湖的「金鲫\ufeff」），排序会把带 BOM 的排在
        # 后面顶掉干净名字。写入前清掉——Go 的 ruleName 本来就会删 BOM，不影响匹配。
        n = n.replace("\ufeff", "").strip() if n else n
        if n and has_builtin(n):
            runtime_names[k] = n
    rule2key = {}
    for k, rn in key2rule.items():
        rule2key.setdefault(rn, k)
    renamed = []
    for rn, k in rule2key.items():
        run = runtime_names.get(k)
        if run and rule_name(run) != rule_name(rules[rn]["fish_zh"]):
            renamed.append((rules[rn]["fish_zh"], run))
    if renamed:
        print(f"写入名与规则名不同（按运行时名写）：{renamed}")

    # A：companion tier3
    cur.execute("SELECT fish_key,bait_id FROM companion_bait_fish WHERE tier=3")
    tier3 = {}
    for fk, bid in cur.fetchall():
        if bid in baits:
            tier3.setdefault(fk, []).append(bid)
    print(f"A companion tier3：{sum(len(v) for v in tier3.values())} 条，覆盖 {len(tier3)} 鱼")

    # B：rf4 占比
    cur.execute("SELECT fish_key,COUNT(*) FROM rf4records_catch WHERE bait_id<>'' GROUP BY 1")
    totals = dict(cur.fetchall())
    cur.execute("SELECT fish_key,bait_id,COUNT(*) c FROM rf4records_catch"
                " WHERE bait_id<>'' GROUP BY 1,2")
    rf4 = {}
    for fk, bid, cnt in cur.fetchall():
        total = totals.get(fk) or 0
        if not total or bid not in baits:
            continue
        share = cnt * 100.0 / total
        if cnt >= args.min_count and share >= args.min_share:
            rf4.setdefault(fk, []).append((bid, share, cnt))
    print(f"B rf4 占比>={args.min_share}% 且样本>={args.min_count}："
          f"{sum(len(v) for v in rf4.values())} 条，覆盖 {len(rf4)} 鱼")

    plan = []
    stats = {"fish_with_add": 0, "fish_no_add": 0, "added_terms": 0,
             "from_tier3": 0, "from_rf4": 0, "skipped_covered": 0, "skipped_noname": 0,
             "skipped_denied": 0,
             "capped": 0}
    for rule_key_name, r in rules.items():
        fk = next((k for k, rn in key2rule.items() if rn == rule_key_name), None)
        adds = []          # (term, source)
        seen = set()
        extra = 0
        if any(rule_name(t) == "各类饵" for t in r["allow"]):
            stats["fish_no_add"] += 1
            continue
        cand = []
        for bid in tier3.get(fk, []):
            cand.append((bid, "companion"))
        for bid, share, cnt in rf4.get(fk, []):
            cand.append((bid, "rf4"))
        for bid, src in cand:
            b = baits.get(bid)
            if not b:
                stats["skipped_noname"] += 1
                continue
            zh = b[1]
            if rule_name(zh) in seen:
                continue
            if any(bait_covers(t, b, baits) for t in r["allow"]):
                stats["skipped_covered"] += 1
                continue
            # 被本鱼「不吃」名单覆盖的饵不能加：deny 在判鱼时优先剔除，
            # 加进去等于白写（例：湖拟鲤明文不吃拟饵，rf4 却有软饵纪录）。
            if any(bait_covers(t, b, baits) for t in r["deny"]):
                stats["skipped_denied"] += 1
                continue
            seen.add(rule_name(zh))
            adds.append((zh, src))
            if len(adds) >= args.max_add:
                stats["capped"] += 1
                break
        if not adds:
            stats["fish_no_add"] += 1
            continue
        stats["fish_with_add"] += 1
        stats["added_terms"] += len(adds)
        stats["from_tier3"] += sum(1 for _, s in adds if s == "companion")
        stats["from_rf4"] += sum(1 for _, s in adds if s == "rf4")
        plan.append({
            "fish_zh": runtime_names.get(rule2key.get(rule_key_name, ""), r["fish_zh"]),
            "rules_name": r["fish_zh"],
            "fish_key": fk,
            "allow": r["allow"] + [zh for zh, _ in adds],
            "deny": r["deny"],
            "bottom": r["bottom"],
            "cond": r["cond"],
            "added": adds,
        })

    print("\n==== 计划 ====")
    print(f"有新饵的鱼 {stats['fish_with_add']}，无增量 {stats['fish_no_add']}")
    print(f"新增词条 {stats['added_terms']}（tier3 {stats['from_tier3']} / rf4 {stats['from_rf4']}）")
    print(f"因已被类目词覆盖而跳过 {stats['skipped_covered']}，"
          f"被「不吃」挡住 {stats['skipped_denied']}，无中文名 {stats['skipped_noname']}")
    print("\n样例（前 8 条）：")
    for p in plan[:8]:
        print(f"  {p['fish_zh']}: +{[z for z,_ in p['added']]}")

    json.dump({"stats": stats, "plan": plan}, open(args.report, "w", encoding="utf-8"),
              ensure_ascii=False, indent=1)
    print(f"\n明细已写入 {args.report}")

    if args.dry_run:
        print("dry-run：未写库")
        return

    now = float(int(time.time()))
    # 只清本工具上次写的行：重跑幂等，且不会碰后台手工保存的覆盖
    # （手工保存会把 updated_by 改成操作者账号）。
    cur.execute("DELETE FROM fish_bait_override WHERE updated_by=?", (BULK_AUTHOR,))
    purged = cur.rowcount
    if purged:
        print(f"清除上一次批量写入的 {purged} 行")
    for p in plan:
        cur.execute(
            "INSERT INTO fish_bait_override(fish_zh,allow_json,deny_json,bottom_json,condition,"
            "groups_json,rotate_sec,rotate_mode,enabled,updated_by,updated_ts)"
            " VALUES(?,?,?,?,?,'[]',0,'soft',1,?,?)"
            " ON CONFLICT(fish_zh) DO UPDATE SET allow_json=excluded.allow_json,"
            " deny_json=excluded.deny_json,bottom_json=excluded.bottom_json,"
            " condition=excluded.condition,enabled=1,updated_by=excluded.updated_by,"
            " updated_ts=excluded.updated_ts",
            (p["fish_zh"], json.dumps(p["allow"], ensure_ascii=False),
             json.dumps(p["deny"], ensure_ascii=False),
             json.dumps(p["bottom"], ensure_ascii=False), p["cond"], BULK_AUTHOR, now))
    db.commit()
    cur.execute("SELECT COUNT(*) FROM fish_bait_override")
    print(f"写库完成：fish_bait_override 现有 {cur.fetchone()[0]} 行")


if __name__ == "__main__":
    main()
