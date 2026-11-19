# 稀有度回归鱼种固有属性，删除按渔获量分档与重算入口

Status: done

## Problem Statement

鱼种稀有度一直是用**钓获量**反推的，前后换过两版口径，都不成立：

1. 第一版在生成生态档案时按 `species_grade.catches` 降序四分位切，每档强制 25%。
   结果一张只有 26 种鱼的图也必有 6~7 种被打成「极罕见」——纯配额，与好不好钓无关。
2. 第二版改成绝对阈值 30 / 14 / 7，长尾分布有了，但依据本身错位：攻略站的登记渔获数
   反映的是「玩家愿意登记什么、哪张图热门」，不是「这条鱼本身稀不稀有」。剑鱼在挪威海
   占 1.2% 的渔获被判「常规」，锦鲤变种没人登记被判「极罕见」；热门图（铜湖、挪威海）
   所有鱼登记数都高，一条稀有档都不剩，冷门小图相反。

玩家看到的后果：档位与图内实际频率经常矛盾，后台鱼口页还要摆出「本图 N 条·第 M 位」
和「图内常见 / 本图少见」两套标记互相打架，运维得逐条手工覆盖权重。

## Solution

稀有度回到它本来的含义——**鱼种固有属性**：欧洲鳇、剑鱼、各类白化与锦鲤变种就是超稀有，
鲫鱼、拟鲤就是普通，跟谁钓到多少条没关系。

数据早就存在：静态库 `server_data/static.sqlite3` 的 `species_rarity` 表（rf4db 抓取，253 条），
`0 普通 169 / 1 稀有 45 / 2 超稀有 39`。基础权重按这三档取预设 100 / 30 / 5。

后台鱼口页只保留「超稀有 · 档位权重 5」这样一行标注，不再展示本图/全服渔获名次，
也不再提供「重算稀有度」按钮——档位不是算出来的，没有可重算的东西。

## User Stories

1. 作为玩家，我希望「这条鱼稀不稀有」是固定的鱼种属性，不因为某张图热门、某个变种
   没人登记就被判成另一个档。
2. 作为运维，我希望鱼口页只告诉我档位与该档权重，不再拿两套互相矛盾的渔获指标让我判断，
   图内密度不合适时我直接填覆盖权重。
3. 边界：`species_rarity` 里没有条目的鱼保持原档（缺省 0 = 普通），不按「0 条渔获」
   推断成超稀有；已手工设过覆盖权重的鱼不受档位变化影响。

## Implementation Decisions

- 数据来源：`species_rarity`（StaticTables，只由 seed 从攻略站覆盖）。新增
  `store.SyncFishRarityFromStatic`，启动时把该表投影进 `fish_ecology.rarity_tier`，
  幂等（只写有变化的行），排在 `ensureFishEcology` 之后。
- 种子档案 `internal/gameserver/assets/fish_ecology_defs.json` 删除 253 条 `rarity_tier`
  字段，`ensureFishEcology` 的 upsert 也不再写这一列——稀有度只有一个来源。
  列本身保留（`NOT NULL DEFAULT 0`），判鱼权重、档位预设、后台展示都读它。
- 删除：`store/repos_fish_rarity.go` 的阈值/预览/重算实现（改为固有稀有度常量 +
  同步函数）、迁移 `00073` 建的 `fish_rarity_threshold` 表、`/api/fish/rarity`
  三个接口、`AdminFishConfig.RarityCuts/RarityActive` 与 `FishConfigRow` 的
  `loc_catches/loc_rank/loc_species/loc_total/global_catches`。
- 权重预设三档：`fish_weight_preset` 删掉 `rarity_tier=3`，值改为 `(0,100) (1,30) (2,5)`，
  迁移 `00075_fish_rarity_intrinsic.sql` 执行；`FishWeightPresetWeights/Stats/Preset`
  的数组宽度由 `[4]float64` 改为 `[store.FishRarityTiers]float64`。
- 后台 `web/index.html`：`rarityBarHTML` 改为静态说明（253 种的档位分布），删除
  `openRarityRecalc` 整段（预览、保存、恢复默认），`FISH_TIER_NAMES` 收敛为
  `普通/稀有/超稀有`，重设权重弹窗只发 `tier0~tier2`。
- 兼容：`fish_ecology.rarity_tier` 的旧值（0~3）在启动同步时被覆盖；没有静态条目的鱼
  保留旧值，不会被写成 NULL。回滚只需恢复代码，库里三档权重可手工改回。

## Testing Decisions

- 存储层（真实 SQLite）：`repos_fish_weight_preset_test.go` 锁住三档语义——预设优先于
  攻略原值、清空即回滚、缺口行拿档位权重、预览只读。档位夹具用 0/1/2，不再出现 3。
- 前端冒烟（playwright + Edge）：`internal/admin/fish_rarity_ui_test.cjs` 断言说明条只讲
  「鱼种固有属性」且不含任何阈值文案、行内标签是「超稀有 · 档位权重 5」且没有渔获名次、
  重设权重弹窗只有三档且 POST body 为 `{tier0,tier1,tier2}`。
- 期望值来源：`species_rarity` 的实际分布（169/45/39）与迁移里写死的档位值，不是用同样
  代码再算一遍。
- 回滚后变红的测试：上面两条（三档数组宽度、固有属性文案）。

## Out of Scope

- 不做图级稀有度（`fish_rarity(loc, fish_key, tier)`）。图内密度差异继续由
  `fish_weight_override` 手工覆盖承担。
- 不改判鱼链路里 `RarityTier` 的三处用法（RareFish 技能加成、窝料、鱼猎 pit）——
  只是档位取值范围从 0~3 变成 0~2，公式本身不动。
