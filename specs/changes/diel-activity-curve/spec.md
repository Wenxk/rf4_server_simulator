# 昼夜活跃度曲线（diel_curve）

Status: done

## Problem Statement

鱼的昼夜活跃度原先只有 `fish_ecology.diel_mask` 一个四位位图：命中时段权重 1.0、未命中 0.35，
外加一层按活跃段数折算的「峰值加成」（`fish_diel_active_boost`，默认 2.0；非活跃段
`fish_diel_inactive_penalty` 默认 0.3）。两套参数叠在一起有三个毛病：

1. 位图只能表达「开 / 关」，表达不了窗口期——峰值时段最容易出、过了峰值逐渐变少但仍会出。
2. 加成按「段数」一刀折算，同段数的鱼拿同一个倍率，鱼种之间的真实差异被抹平。
3. 加成把权重抬到 2.0 以上，与「基础权重」的语义打架：页面写 10，实际出鱼权重 20，
   调参时说不清到底是哪一层在起作用。

## Solution

1. 新增 `fish_ecology.diel_curve`（四段权重字符串，顺序 **`[夜, 晨, 昼, 昏]`**）。
   `fishDielWeight` 优先按段取系数，并钳到 `[0.08, 1.0]`；曲线为空串时回退旧位图
   1.0 / 0.35。**曲线是乘性权重，峰值恒为 1.0，只压不抬。**
2. 曲线数据来源分三级，逐级回退（与上游同口径）：
   - `rf4info`：社区公开抓取库 `ghoomt03-sketch/rf4info` 的 `public_data/core.sqlite3`，
     表 `spot_activity_events`。仅取 `TRIM(game_time) <> ''` 的记录，按四段计数；
     单鱼样本数至少 5 时直接使用自身分布。各段取 `(count + 2) / (peakCount + 2)`，
     再钳到 `[0.08, 1.0]`，因此峰值固定为 1.0。
   - `gamekee`：GameKee 鱼种页的「日间活跃度 / 夜间活跃度」表情图标（笑/平/愁 = 高/中/低）。
     按日档与夜档的 8 个可能组合归档；当前数据实际覆盖 7 组。无 rf4info 自身样本的鱼，
     取同组合内 rf4info 实测鱼的平均四段形状。
   - `global`：两处都没有的鱼使用全体实测形状的全局均值 `[1.0, 0.67, 0.88, 0.68]`。
3. `diel_mask` 同步由曲线派生（权重 ≥0.5 的段置位），保证只读位图的旧路径不倒退。
4. `profile_version` 升到 **6**（本地此前已用到 5），让线上旧档案按
   `WHERE fish_ecology.profile_version < excluded.profile_version` 刷新出曲线。

当前 253 个鱼种全部写入曲线：`rf4info` 120 条、GameKee 同组均值 111 条、全局均值 22 条。
资产中的 `diel_source` 只用于标记来源，运行逻辑忽略该字段。

## 与本地原有实现的差异（合并时踩过的坑）

- **位序改为「夜在前」**：本地旧实现是 `1 晨 2 昼 4 暮 8 夜`（`worldclock.SegmentOf` 返回
  0=晨），上游是 `1 夜 2 晨 4 昼 8 昏`（`fishActivityForHour` 返回 0=夜）。曲线按
  `[夜,晨,昼,昏]` 编码，位序不一致会让 `curve[bit]` 整体错位一格（拿「夜」的权重配「晨」）。
  因此本次把 `worldclock` 的段位序整体翻转为上游口径，资产得以整份直接沿用上游文件。
- **旧数据重映射**：迁移 `00079` 用 `((m<<1)|(m>>3))&15` 旋转 `fish_ecology` 与
  `fish_ecology_loc` 的历史掩码（8→1、1→2、2→4、4→8、15 不变）。
- **`profile_version` 以代码常量为准**：资产 JSON 里每条仍带 `profile_version`，但
  `defaultFishEcology` 会用常量覆写，避免「抬了常量、资产里还是旧号、刷新不触发」。
- **水域覆盖也支持曲线**：`fish_ecology_loc.diel_curve`（`NULL` = 继承全局，空串 = 该水域
  显式回退位图）。只覆盖掩码不覆盖曲线时，全局曲线会盖住掩码，看起来像「改了没生效」。

## User Stories

1. 作为玩家，我在鳗鱼、江鳕这类夜行鱼的白天几乎钓不到，夜里稳定出货。
2. 作为玩家，我在欧鳊、梅花鲈这类鱼的夜间最容易爆护，白天偶尔也能碰到。
3. 作为玩家，我在葛氏鲈塘鳢这类日行鱼的白天最容易出货，夜里明显变少。

## Implementation Decisions

- 四段划分沿用 `worldclock.SegmentOf`：夜 20–05、晨 05–09、昼 09–16、昏 16–20。
- 曲线是**乘性权重**，参与 `fishBiteEnvironmentWeight` 的 `EcologyInterest` 分组与窝料
  `groundbaitGates`，不改变门限结构。
- 最低权重取 0.08 而非 0：低活跃段仍允许偶发记录，不会让该鱼在整段内彻底消失。
- 取消 `fish_diel_active_boost` / `fish_diel_inactive_penalty` 两个可调项：曲线本身就是数据，
  想改手感请改曲线或覆盖权重。后台「游戏参数 → 昼夜权重」只剩开关。
- 曲线只作用于判鱼与窝料门，不改基础权重、不改档位比例。

## Testing Decisions

- `TestFishDielCurveWeightsParsing`：空串、非四段、非法 JSON 返回 nil，合法值按 [0,1] 钳制。
- `TestFishDielWeightUsesCurveAsWindow`：曲线存在时按段取值。
- `TestFishDielWeightSegmentBoundaries`：逐一覆盖夜/晨/昼/昏边界、跨日归一与负小时。
- `TestFishDielWeightFallsBackToMask`：无曲线时仍按位图 1.0 / 0.35。
- `TestFishDielCurveAssetMatchesDerivedMaskAndSources`：253 条资产都有曲线和来源；曲线在
  `[0.08,1.0]`、峰值为 1、`diel_mask` 与 ≥0.5 派生规则一致，并固定三类来源数量。
- `TestNocturnalFishExcludeDaylight` / `TestDiurnalFishBiteInDaylight`：夜行/昼行鱼的方向
  不能配反（严格夜行鱼白天段 <0.5；大鲶鱼类只要求夜间仍为峰值）。
- `TestDielWeightSeparatesNightAndDay`：同一时刻夜行鱼与昼行鱼方向相反，且任何鱼任何时段
  系数都不超过 1。
- `TestDielCurveClampsToMinimum`：曲线里的 0 与 0.01 都钳到 0.08，不会把鱼从候选池抹掉。
- `TestFishEcologySeedsSpeciesAndMatchesWaterLayers`：旧 profile_version 行升级时同时恢复
  `diel_curve`。

## Out of Scope

- 不修改 `fish_distribution` 的硬编码族名单（独立路径，受 `fish_distribution_enabled` 控制）。
- 不调整 `fish_query_regions` 的分布系数口径。
- 不引入温度/气压与昼夜的交互项。
- 不引入昼夜对档位（未达标/合格/奖杯/蓝冠）比例的影响。

## Further Notes

复核时直接从 `public_data/core.sqlite3` 统计：

```text
SELECT fish_id, game_time
FROM spot_activity_events
WHERE TRIM(game_time) <> '' AND fish_id IS NOT NULL AND fish_id <> '';
```

当前库得到 9,957 条活动记录，其中 7,125 条带游戏时刻、覆盖 167 个鱼种；
样本数 ≥5 的鱼种为 120 个，与资产中的 `diel_source=rf4info` 数量一致。
GameKee 图标只决定鱼种归组，具体四段形状仍由 rf4info 实测样本提供。
