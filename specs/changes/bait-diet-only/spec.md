# 判鱼资格只认鱼种食性，渔获记录退出鱼口

Status: done

> 本文已被后续变更取代：2026-11-19 起「爱吃饵」`allow` 列表恢复为硬资格，
> 见 `bait-allow-restored/spec.md`。
> 本页关于「官服榜 / 本图渔获 / rf4 占比不参与判鱼」的结论仍成立；
> 关于「攻略 allow 不参与」的结论已撤销。保留本页作为 11-18 口径的记录。

## Problem Statement

判鱼把「谁钓到过」当成了「鱼吃不吃」：攻略的 `allow` 列表、官服榜
（`companion_bait_fish`）、本图渔获（`loc_bait_fish`）、rf4records 实测占比
四路数据一起参与权重，甚至参与放行资格。结果是同一条鱼换个钓场、或者攻略没覆盖
某个饵，权重就从满分掉到 0.05~0.10 甚至直接剔除——玩家看到的是
「这张图攻略里没写过这个饵，就一直空竿」。

鱼开不开口只取决于两件事：**鱼种自身的食性**和**当前钓法**。谁在哪个湖用什么饵
钓到过，是人的记录，不是鱼的行为。

## Solution

判鱼资格与权重只看鱼种食性 `fish_ecology.diet_json`：

- 食谱里有这类食物 → 参与抽签，权重直接取食谱强度（0.2 ≈ 偶尔吃一口，
  1.5 及以上按主食计）。
- 食谱里压根没有这类食物 → 整鱼剔除。这是唯一允许判「不吃」的依据。
- 没有生态档案的鱼 → 不套食性判定，按中性放行，不因缺数据封鱼。

攻略 `allow`、companion 榜、本图渔获、rf4 占比一律不参与判鱼：既不剔除，也不加权，
增删这些记录不会改变任何一条鱼对任何一款饵的权重。

钓法侧的约束保留且独立：明确禁饵（`deny`）、仅底钓（`bottomOnly`）、海图区号与
jig/顺流/水动前置条件、饵料轮换，都是「钓法对不对」，不属于渔获记录。

## User Stories

1. 作为玩家，我希望同一个鱼种在同一个饵上的咬口表现不随钓场变化，
   以便「鱼吃这个东西」成立，而不是「这张图的攻略写过这个东西」。
2. 作为玩家，我希望没人钓过的冷门饵也能上鱼——只要鱼的食谱里有这类食物。
3. 作为玩家，我希望食谱里没有的东西怎么都钓不到（玉米钓不到大西洋鲑鱼），
   这种空竿是设计，不是 bug。
4. 边界：钩饵与鱼食性不符、钓法不对 → 空竿重排，不从内置鱼池捞外图鱼。
5. 边界：没有生态档案的鱼不因为缺数据被判「不吃」。

## Implementation Decisions

- `internal/gameserver/fishing_bait_rules.go`：`filterFishBaitSet` 的评分段只保留
  `baitDietStrength`（食谱键 → 强度）。删除 rf4 占比分级、companion/local 加权、
  证据四档与 `legalAlternative` 等以记录为依据的分支以及 `rf4BaitShare` 占比缓存。
  单饵偏好上限 `baitPreferenceCap=1.5`，避免食谱高值把饵兴趣顶到 100%。
- `internal/gameserver/fish_bite_gates.go`：G-diet 改为纯食性资格
  （`biteGateDietScore(diet, baits)`：有键放行、无键剔除、空食谱中性），
  删除 companion/local 双票与 `statLoc` 参数。
- 不新增任何可调项：删掉上一版临时加的 `bait_diet_scoring_enabled` 与
  `bait_offdiet_cap`，后台参数表同步移除。`bait_share_*` 三阈值随分级机制一起失效。
- 兼容：无迁移、无表结构变更；生态档案 `diet_json` 不回填、不重灌。
- 回滚：恢复本改动的 `filterFishBaitSet` 与 `biteGateDietScore` 即可，
  无数据侧副作用。

## 验收

- `TestBaitBiteIgnoresHumanRecords`：两个无任何渔获记录的钓场，同一鱼种同一饵权重
  完全相同；补满本图渔获 + companion tier3 之后权重一分不变。
- `TestBaitDietLowValueStillBites`：食谱 0.2 能吃但明显低于 2.0 的主食，
  低值饵权重仍按 4 游戏小时桶抖动。
- `TestBiteGateDietQualification`：玉米粥对银鲫（plant 2.02）是正口、对泥鳅
  （plant 0.2）权重明显更低但仍可咬；银鲫的禁饵条款仍然硬剔。
- `TestBaitPreferenceFollowsDietOnly`：同一食性族的两款饵等权，多饵同时正口给加成，
  去重与槽位顺序不改变结果。
- `go test ./...` 全绿、`go vet ./...` 干净、`go run ./tools/doccheck` issues=0。

## Out of Scope

- 不动上层的 companion 饵效层（`bait_pref_scale` 的 tier ×8/×4/×2 与未上榜惩罚）。
  那是上游项目自带的玩法（饵效），与本改动同源问题但改动面大，单独评估。
- 不动鱼群分布、天气、昼夜、窝料、水层地板等其它权重层。
- 不给「食谱里没有」的饵加兜底：钓法/饵不对就是钓不到，用户 2026-11-18 明确要求。

## 风险与开放问题

1. `diet_json` 的区分度不均：甲壳类在 98% 鱼种上写 0.85~0.95，贝类饵因此对多数鱼
   都「能吃」。这是数据本身的问题，靠调参掩盖不了，需要重生成生态档案时收敛。
2. 食谱高值（≥1.5）全部按主食处理，1.5~2.0 之间的差异被抹平；如需更细的层次，
   应调整 `baitPreferenceCap` 与外层 `maxBaitPreference` 的归一化口径。
