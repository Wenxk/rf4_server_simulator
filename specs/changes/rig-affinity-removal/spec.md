# 移除钓法倾向层：钓法差异只走几何水层匹配

Status: done

## Problem Statement

`rig-affinity-layer` 加了一层「钓法族 × 鱼种水层档」的可配置乘子（`rig_affinity_weight`，
5×4 = 20 格，乘子原样生效不走幂压缩）。实机与推演下来，这一层在语义上是错的：

1. **和几何水层匹配重复计分。** 几何层（`fishLayerWeightProfile`）已经把钓组折算成水层点，
   与鱼种 `[layer_min, layer_max]` 比对：区间内高斯峰（命中偏好层 ≈1.0，地板 0.75），
   区间外对称指数衰减到 0.02。它回答的就是「饵有没有到鱼待的那一层」。矩阵再按「钓法族 ×
   鱼层档」乘一次，底钓 × 底层 1.35 与几何层对「底钓饵在底层」的加分同向叠加，是同一件事
   的两次计分。
2. **让「到位」失效——这是根本问题。** 出厂矩阵里飞钓 × 表层 1.8、底钓 × 表层 0.35。
   玩家用底钓把饵（长前导 / 调深度）真的送到鱼的表层区间时，几何层给 0.75 地板，
   矩阵仍要再乘 0.35，最终约 0.26；反过来浮钓 × 底层 0.6 同理。
   **玩家按鱼种习性把饵送到了它活跃的水层，却因为「这不是该钓法的主场」被二次惩罚。**
   正确解因此退化成「按鱼种查预设钓组」，而不是「按水层调钓组」——与设计目标相反。
3. 钓法之间真正该有的差别（路亚吃掠食鱼、飞钓偏鲑鳟）本来由饵料/拟饵规则（`fish_bait_prefs`）、
   拟饵动作权重与鱼种 `enabled` 承担，不需要这层水层矩阵。

## Solution

整层删除，判鱼只保留几何水层匹配。

- 删除 `internal/gameserver/rig_affinity.go`（族归属、鱼层分档、矩阵缓存与 `rigAffinityWeight`）
  与 `fishBiteEnvironmentWeight` 里的两处调用；只开昼夜层时直接返回昼夜系数，
  生态全开时最终结果为 `clampFloat(factor, .08, 2.5)`（不再有 `* affinity` 与 `.02~4` 放宽）。
- 删除 `internal/store/repos_rig_affinity.go`（族/档常量、出厂矩阵、读写与矩阵视图）。
- 删除后台 `internal/admin/fish_rig_affinity.go` 与三个接口
  `GET/POST /api/fish/rig_affinity`、`POST /api/fish/rig_affinity/restore`，
  以及 `rig_affinity_enabled` 的开关取值校验。
- 迁移 `00076_drop_rig_affinity.sql` 删表；`rig_affinity_weight` 从 `RuntimeTables` 摘除。
  `00074_rig_affinity.sql` 保持原样（已在现网库执行过，删文件会让迁移历史对不上）。
- 后台鱼口页移除稀有度说明条右侧的「钓法倾向」按钮与弹窗（矩阵输入、保存、恢复默认）。

## User Stories

- 作为玩家：我把饵调到某种鱼活跃的水层后，咬口只取决于水层是否到位、时段、天气、窝料与饵，
  不会因为我用的是浮钓还是底钓再被乘一次。
- 作为运维：后台不再出现一个与几何层语义重叠、却影响更大的矩阵；调「这条鱼该多常见 / 该不该出」
  只有生态档案与覆盖权重两个入口。

## Implementation Decisions

- **不做等价替换**：不引入「钓法族 × 鱼种类群」等新轴。先把错的层删干净，等有明确需求再加；
  任何新的钓法差异都必须表达「饵呈现方式 → 能不能被鱼发现」，而不是「饵到位了还要不要罚」。
- 几何层公式、区间容差、`.75` 区间内地板不动（`TestFishLayerWeight*` 全部保持通过）。
- 开关 `rig_affinity_enabled` 不再被读取；库里残留的 tunable 行不影响任何逻辑（无迁移删除，
  避免动 `tunables` 的历史）。
- 文档口径统一为：**饵到不到位只由几何水层匹配回答，钓法差异交给饵料/拟饵规则。**

## Testing Decisions

- 删除 `internal/gameserver/rig_affinity_test.go`、`internal/store/repos_rig_affinity_test.go`、
  `internal/admin/fish_rig_affinity_test.go`。
- `fish_diel_weight_test.go`：生态关闭时权重**等于**昼夜系数（原为「昼夜系数 × 钓法乘子」），
  期望值直接由 `fishDielWeight` 算出，不用被测函数反算。
- 几何层既有测试不改，作为「到不到位仍按水层判定」的回归防线。
- 前端冒烟（`fish_rarity_ui_test.cjs`）：断言鱼口页不再出现「钓法倾向」按钮与弹窗文案。
- 实机：迁移到 76 后 `rig_affinity_weight` 表不存在；鱼饵偏好编辑器不再报
  `unable to open database file (14)`（该报错与本变更无关，见 `store` 的 `temp_store(MEMORY)` 修复）。

## Out of Scope

- 不重新引入任何「钓法 → 鱼种概率」的乘子层；需要时另开 spec。
- 不改鱼群分布层、饵料规则、拟饵动作权重的现有口径。
- 不删除 `tunables` 里残留的 `rig_affinity_enabled` 行。
