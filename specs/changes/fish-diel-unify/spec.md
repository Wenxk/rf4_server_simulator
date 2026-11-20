# 昼夜口径归并：分布层不再自带一份昼夜习性

Status: cancelled

> 已被 `specs/changes/diel-activity-curve` 取代：昼夜改四段乘性曲线，位序翻转为 1 夜 2 晨 4 昼 8 昏。本文保留作历史记录。

## Problem Statement

昼夜习性在代码里存在**两套互不相干的参数**：

1. 生态档案的 `diel_mask`（1 晨 2 昼 4 暮 8 夜）经 `fishDielWeight` 生效，按活跃段数分档
   给加成（详见 `specs/changes/fish-diel-weight/spec.md`）。
2. 鱼群分布层 `internal/gameserver/fish_distribution.go` 里另有一张写死 14 种鱼的
   `activityMultiplier`：夜行组（梅花鲈、长吻梅花鲈、鳗鱼、江鳕、欧洲六须鲇、西伯利亚杜父鱼、
   斑点叉尾鮰、铲鮰）夜里 ×1.8、白天 ×0.35；白天组（常见狗鱼、鲈鱼、欧鲌、红鳍鱼、董氏须鳅）
   白天 ×1.4、夜里 ×0.45。

两套同时生效带来三个问题：

- **幅度互相淹没**：分布乘子本身是 0~4.0，昼夜层只有 1.33~2.0 / 0.30，玩家实际感知被散布
  噪声掩盖；实测关掉分布后昼夜曲线才显现。
- **口径互相打架**：董氏须鳅（`barb_toni`）是底栖夜行鱼，掩码为纯夜（8），写死表却把它
  归进「白天 ×1.4」；梅花鲈掩码写的是「晨/昼/夜」（11，漏暮、误含昼），与写死表的
  「白天 ×0.35」正好相反。
- **调参无处下手**：运维在后台调 `fish_diel_active_boost`，同时被一张代码里看不见的表
  反向修正，行为不可预期。

## Solution

删掉分布层的 `activityMultiplier`，昼夜只由昼夜层一处决定：

- `distributionMultiplier(area, species, now)` 退化成**纯空间散布**：按 40 世界单位格子与
  四游戏小时周期做确定性哈希，返回 0（这一格没这种鱼）或 `0.15 + 3.85v²`（0.15~4.0）。
  不再读游戏小时，也不再按鱼种分支。
- 梅花鲈档案掩码 `11 → 13`（晨/暮/夜），与「梅花鲈晨昏与夜间摄食、正午差」一致；
  其他 13 种鱼的掩码经过核对（夜行组均不含昼位，欧鲌/红鳍鱼/鲈鱼均含昼位），不做改动。
- 档案版本 `fishEcologyProfileVersion` 4 → 5，触发 `ensureFishEcology` 重灌，
  使已有库也拿到修正后的掩码；启动流程里 `RecalcFishRarity` 排在重灌之后，
  档位不会被种子里的旧四分位口径带回。

## User Stories

1. 作为玩家，我希望「夜里夜行鱼更多」这件事只受一个开关影响：调大昼夜峰值就真的更明显，
   而不是被分布层的另一张表抵消掉一半。
2. 作为运维，我希望后台看到的昼夜标签就是判鱼实际用的昼夜——点开某条鱼能查到它的活跃时段，
   不存在第二份隐藏参数。
3. 边界：分布关闭时（`fish_distribution_enabled=0`）昼夜层照常生效，行为与之前一致；
   分布开启时鱼仍会按格子进出，但进出幅度不再被鱼种昼夜二次放大。

## Implementation Decisions

- 删除 `activityMultiplier`；`distributionMultiplier` 去掉 `worldDayAndHour` 依赖，
  函数签名保留 `now`（散布周期仍按游戏时钟切分）。
- `applyFishDistribution` 不变：`pityEnv` 路径下散布为 0 仍保底 ×0.15。
- 找鱼查询的区域粗筛（`fish_query_regions.go`）复用同一函数，因此推荐结果同步去掉鱼种昼夜，
  与判鱼保持一致。
- 掩码修正只动梅花鲈一条：写死表把它当夜行鱼是对的，问题在档案漏了暮、误含昼；
  其余夜行组鱼种掩码本就正确，无需改动。
- 后台「鱼群规则」里分布开关的说明去掉「游戏昼夜」字样，避免继续暗示分布层管昼夜；
  `docs/gameplay.md` 的鱼群随机分布段落同步改写，并指向昼夜权重一节。

## Testing Decisions

- 单元（`TestDistributionIgnoresGameHour`）：同一格、同一散布周期内换游戏小时，
  乘子必须完全相同——把昼夜塞回分布层这条先红。
- 单元（`TestFishDistributionRegionsAndTime`）：乘子必须等于纯散布公式
  `0.15+3.85v²`（`v<0.3` 时为 0），不允许再乘任何鱼种因子；区域/周期的稳定性断言保留。
- 单元（`TestNocturnalFishExcludeDaylight` / `TestDiurnalFishBiteInDaylight`）：
  20 种有明确昼夜习性的鱼，夜行组掩码不含昼位且含夜位，昼行组反之。梅花鲈改回 11 时前者红。
- 单元（`TestDielWeightSeparatesNightAndDay`）：梅花鲈（晨/暮/夜，3 段）正午 0.30、
  深夜 1+(peak-1)/3 ≈ 1.33；红鳍鱼（晨/昼/暮，3 段）相反；另用一条纯夜行档案断言
  1 段拿满额 peak。断言的是昼夜层单独就能造出方向正确的落差，不依赖分布层。
- 既有 `TestPityDistributionFloor` 不受影响：它只依赖散布为 0 的组合是否可复现。
- 期望值来源：掩码位与公式字面量，不用被测函数反算。

## Out of Scope

- 不改昼夜层的倍率与分档规则（`fish_diel_active_boost` / `fish_diel_inactive_penalty`）。
- 不改分布的空间粒度（40 世界单位）、刷新周期（四游戏小时）与 0.15~4.0 区间。
- 不重新生成其余 252 条鱼的昼夜掩码；`fish_ecology_loc` 的按图覆盖机制不变。
- 不改天气层、窝料层对昼夜的引用方式。

## Further Notes

- `[事实]` 重灌安全：改前核对 `fish_ecology` 253 行与 `fish_ecology_defs.json` 的全部
  非 JSON 字段（水层/温度/掩码/活性/光照等），零差异，说明库里没有人工改过的全局生态值；
  版本号提升只会修正掩码，`enabled` 不在 upsert 的 SET 列表里，后台开关不会被冲掉。
- `[事实]` 写死表的错判来自「生成档案时按鱼种大类给的粗口径」：`barb_toni` 因 key 里带
  `barb`（鲃属字样）被归进白天组，实际它是须鳅科的底栖夜行鱼。
- `[推断]` 归并后同一条鱼的昼夜落差从「分布 0~4 × 昼夜 1.33~2.0」变成单一 1.33~2.0，
  总体夜间/白天对比会**变清晰**但**不会再被随机放大**；若实机觉得还不够，优先调
  `fish_diel_active_boost`，而不是恢复分布层的鱼种因子。
