# 基础权重档位化：按稀有度给所有水域一套可解释的预设

Status: done

## Problem Statement

鱼口页每行的「基础权重」直接决定判鱼抽签，但它的来源是攻略站的渔获条数
（`loc_fish.catches`），各图尺度完全不同：

| 水域 | 权重范围 | 权重和 |
|---|---|---|
| 铜湖 | 1 ~ 199 | 1818 |
| 琥珀湖 | 1 ~ 15 | 136 |
| 沃尔霍夫河 | 1 ~ 9 | 72 |

于是「同样是稀有鱼」在铜湖可能是 16、在琥珀湖是 2，差一个数量级；玩家在后台看不出
哪条是常规鱼、哪条是该图的招牌目标，调参也没有参照系。更糟的是 `loc_wiki_fish` 里
有 178 条鱼在 `loc_fish` 里没有对应行，`LocRoster` 走 `COALESCE(catches, 1)` 兜底，
权重恒为 1——它们本来就在判鱼候选里，只是被钉死在地板上，看图也分不清这是「真罕见」
还是「数据缺失」。

## Solution

按 `fish_ecology.rarity_tier`（0 常规 / 1 少见 / 2 罕见 / 3 极罕见）给统一档位，
跨图同档同权重：

| 档位 | 权重 | 语义 |
|---|---|---|
| 常规 | 100 | 到处都是的杂鱼，新手图的主要目标 |
| 少见 | 40 | 要多跑几趟才碰得上 |
| 罕见 | 15 | 需要挑点位/时段/钓组才出 |
| 极罕见 | 5 | 几十上百条才一条，通常是该图的招牌目标 |

只有相对比例有意义（判鱼是加权抽样），跨图同档同权重不会造成问题——一次只在一张图
钓鱼，图与图的差别由「名册里有哪些鱼」承担，这本来就是原作表达水域差异的方式。

**存储放在独立表 `fish_weight_preset`，不改 `loc_fish`。** 原因见下节：
`loc_fish` 属于 `StaticTables`，运行时被建成指向只读静态库的 `TEMP VIEW`，并有
authorizer 拦所有写操作（实测 `ALTER TABLE` 直接报 "Cannot add a column to a view"）。
预设表由 `LocRoster` 在读取时优先取用，判鱼链路一行都不用改。

## User Stories

1. 作为运维，我希望打开任意一张图，权重列就是「100 / 40 / 15 / 5」四档，
   一眼能看出哪些是常规鱼、哪些是招牌目标，而不是 1~199 的乱码。
2. 作为运维，我希望改档位值能立即生效，不用重算几百行、也不用重启。
3. 作为运维，我希望随时能回滚成攻略站原值——这是唯一能对照「原作到底多稀有」的锚点。
4. 边界：没有生态档案的鱼保持原权重（`LEFT JOIN` 落空 → 退回 `loc_fish.catches`），
   不会被 COALESCE 到某一档；手工设过覆盖权重的鱼不受影响（覆盖是更外层）。

## Implementation Decisions

- 新表 `fish_weight_preset(rarity_tier PK, weight, updated_ts)`，迁移写入默认 4 档。
- `LocRoster` 权重优先级：`fish_weight_preset` → `loc_fish.catches` → `1`。
  JOIN 条件用 `p.rarity_tier = e.rarity_tier`（**不加 COALESCE**）：没有档案的鱼
  让 JOIN 落空，才能退回攻略原值；写成 `COALESCE(e.rarity_tier, 1)` 会把它们
  静默归到「少见」档。
- 回退分支（`loc_wiki_fish` 为空时走 `loc_fish`）同样加这层 JOIN。
- `store.PresetFishBaseWeights` 只 UPSERT 4 行，返回逐档统计；`ClearFishWeightPreset`
  清空即回滚。`FishWeightPresetStats` 只读，给后台确认弹窗展示「几鱼几行、攻略原值均值」。
- 档位值以预设表为唯一真相源，**不引入同名 tunable**：两处存同一件事迟早不一致。
- 后台接口：`GET /api/fish/weights/preset`（预览 + 是否已启用）、
  `POST /api/fish/weights/preset`（写入）、`POST /api/fish/weights/restore`（清空）。
- 拒绝四档完全相同的设置：那等于取消稀有度分层，多半是手误。
- 兼容：`GET /api/fish/config` 增加 `rarity_tier` / `diel_mask` / `diel_labels` 等字段，
  旧字段语义不变；表结构只增不改。

## Testing Decisions

- `internal/store/repos_fish_weight_preset_test.go`
  - 清空预设时 `LocRoster` 走攻略原值、缺口行为兜底 1（先证明问题存在）；
  - 启用预设后各档按权重返回，**缺口行也拿到档位权重**（不再恒为 1）；
  - 改档位值立即生效，不需要重算行；
  - 无 tier 的鱼 `LEFT JOIN` 落空、保持攻略原值（守住不加 COALESCE 的决定）；
  - 清空后回滚到攻略原值；
  - 预览接口只读、`active` 标志随表空而变。
- 回滚后变红：把 JOIN 条件改回 `COALESCE(e.rarity_tier, 1)`，无 tier 用例立刻失败。

## Out of Scope

- 不做「按图区分稀有度」：同一种鱼在甲湖常见、在乙湖罕见这件事，仍要靠
  `fish_weight_override` 或未来的 `fish_ecology_loc` 扩展字段表达；这次只做全局档位。
- 不改 `fish_weight_override` 的现有数据（57 行，多为 `enabled=0` 的关闭记录）。
- 不改判鱼链路的权重读取顺序（名册 → 覆盖 → 硬门 → …）。
- 不碰其他也带 `catches` 的表：`spot_fish`（钓点级）、`loc_bait_fish`（饵×鱼证据）。

## Further Notes

- `[事实]` `loc_fish` / `loc_wiki_fish` / `species_weight` 都在 `StaticTables` 里，
  运行时是只读 TEMP VIEW。任何「改静态配置」的想法都要先看 `staticdb.go`。
- `[事实]` 现有数据：`loc_fish` 430 行 / `loc_wiki_fish` 603 行，缺口 178 条。
  `fish_ecology` 253 条全部有 `rarity_tier`，分布 62/63/62/66，与平均权重
  27.4/16.5/11.8/5.6 单调对应——说明这个 tier 本来就是从权重反推的代理值，自洽。
- `[推断]` 极罕见 5 对应单鱼种抽中概率约 0.2%（以一张图 2000 权重和计），
  按 30 秒一杆约 4 小时一条。觉得太稀就抬「极罕见」档，不用改代码。
- `[对比]` 曾有一版实现直接 `ALTER loc_fish ADD COLUMN wiki_catches` 并改写
  `catches`，在 `internal/store` 的单测里能过（那里是空库、是真表），但 gameserver
  的 `OpenWithStatic` 路径下立刻报错。教训：涉及静态配置表先确认运行时形态。
