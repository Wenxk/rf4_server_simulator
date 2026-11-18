# 稀有度分档：从四分位配额改为全服渔获数的绝对阈值

Status: done

## Problem Statement

`rarity_tier` 决定基础权重的档位（常规 100 / 少见 40 / 罕见 15 / 极罕见 5），
但它的生成方式是「把 `species_grade.catches` 降序后四分位切」：前 62 名常规、
63~125 少见、126~184 罕见、其余极罕见。**每档强制 25%，与实际好不好钓无关。**

直接后果是「不稀有的鱼被打成稀有」：

| 鱼 | 旧档位 | 全服渔获 | 本图实况 |
|---|---|---|---|
| `l_carp` 鳞鲤 | 罕见（权 15） | 19 | 琥珀湖 15 条，占该图 11%，**排第 1** |
| `barsch` 河鲈 | 罕见（权 15） | 17 | 分布 3 张图 |
| `g_carp` 金鲫 | 罕见（权 15） | 27 | 梅德韦杰湖/琥珀湖都常见 |
| `m_carp_red` 红镜鲤 | 极罕见（权 5） | 9 | 琥珀湖 9 条，排第 4 |
| `crawfish` 小龙虾 | 极罕见（权 5） | 16 | 白河/惟有诺克河各 5 条 |

反向也有：`x_gladius` 剑鱼全服 50 条落在前 25%，拿到常规 100，而它在挪威海只占 1.2%。

一张只有 26 种鱼的图必有 6~7 种被打成「极罕见」，纯属配额。玩家在后台看到的是
「普通鲤鱼 11 条拿 100 权重、鳞鲤 15 条只拿 15」，权重方向反着来。

另有一个混淆源：玩家测试期为了刷稀有鱼人为调高过权重，本地 `catch_log` 已累积 211 条
被污染的记录。任何拿本地渔获当稀有度依据的做法都会把测试结果当事实。

## Solution

按 `species_grade.catches`（攻略站**全服**登记渔获数）取**绝对阈值**分档，取代四分位配额：

| 档位 | 阈值 | 鱼种数 | 占比 |
|---|---|---|---|
| 常规 | ≥ 30 | 110 | 44% |
| 少见 | 14~29 | 86 | 35% |
| 罕见 | 7~13 | 30 | 12% |
| 极罕见 | ≤ 6 | 22 | 9% |

长尾分布，稀有档只留给真正冷门的鱼，不再有「每档四分之一」的强制配额。

### 为什么依据只能是 species_grade

| 候选 | 否决理由 |
|---|---|
| `loc_fish.catches` | 按图分摊，各图总量差十几倍（琥珀湖 136 / 挪威海 1503）；同一鱼种在不同图名次互相矛盾——欧鳊在小河里排第 1、全服榜上却极罕见 |
| `catch_log` / `catch_seen` | 本服玩家渔获。测试期人为调高权重，211 条记录已被污染 |
| `species_weight.samples` | 253 条里 215 条为 0/NULL，覆盖不足 |
| `species_grade.catches` | ✅ 属于 `StaticTables`，只由 `seed` 从攻略站覆盖，与本地进度、后台操作都无关 |

## Implementation

### 迁移 `00073_fish_rarity_threshold.sql`

建 `fish_rarity_threshold(tier PK, min_catches, updated_ts)`，`INSERT OR IGNORE` 默认三档
`(0,30) (1,14) (2,7)`；极罕见无下限（`≤ tier2-1` 兜底）。表登记进 `RuntimeTables`。

### `internal/store/repos_fish_rarity.go`

- `NormalizeFishRarityCuts`：校验三个下限严格递减且 ≥1，不合法直接拒绝。
- `FishRarityTierFor(catches, cuts)`：`catches<0`（无全服数据）返回 `ok=false`，
  调用方保持原档——**没有依据就不改**，不能按 0 条算成极罕见。
- `FishRarityThresholds` / `FishRarityPreview`（只读）/ `RecalcFishRarity`（写入）。
- `RecalcFishRarity` 在**同一事务**里写阈值和档位：两者若不一致，后台文案说「这一档是
  7 条以上」而库里实际档位不同，后面调权重会一头雾水。幂等，只 UPDATE 真正换档的行。

### 启动时校正

`gameserver.attachDB` 在 `ensureFishEcology` 之后调用一次 `RecalcFishRarity`。
`fish_ecology` 的写入用 `WHERE profile_version < excluded.profile_version` 做冲突判定，
库里改过的档位不会被普通启动覆盖；但种子 `profile_version` 提升时会整行重灌，
这一步保证重灌后口径仍然正确。无变更时不产生写入。

### 后台

- `GET /api/fish/rarity[?cut0=&cut1=&cut2=]`：当前阈值 + 各档分布（带参则按给定阈值预览）。
- `POST /api/fish/rarity`：保存阈值 → 重算档位 →（可选 `reset_weights`）按档位重设基础权重。
  改完档位不重设权重的话，库里还是按旧档位写的，看起来像「改了没生效」，所以默认勾选。
- `POST /api/fish/rarity/restore`：回到 30/14/7 并重算。
- 鱼口页的稀有度说明条改为**动态**读阈值（`AdminFishConfig.RarityCuts` 由 admin 层填充），
  避免文案写死旧数字；右侧加「重算稀有度」按钮，弹窗可改三个下限并实时预览每档鱼种数。

## Testing

- `TestFishRarityRecalcByAbsoluteThreshold`：故意把初始档位写成四分位残留（全服 60 条的
  被判极罕见、5 条的被判常规），重算后落位正确、换档 3 种、阈值落库、二次调用幂等。
- `TestFishRarityKeepsSpeciesWithoutGlobalData`：无 `species_grade` 行的鱼保持原档，
  `skipped=1`、`changed=0`。
- `TestFishRarityPreviewIsReadOnly`：预览后库里的档位没被改。
- `TestFishRarityCutsValidation`：非递减 / 等于 / 含 0 / 负数全部拒绝。
- `TestFishRarityRecalcDrivesRosterWeights`：端到端——旧档位下名册权重 5，重算后变 100。
- 真实库：`migrate ok, max version = 73`，换档 150 种 / 参与 248 / 跳过 5，
  分布 110/86/30/22（原 62/63/62/66），基础权重重设 4 档影响 603 行。
  琥珀湖名册 36 条：`5×4 15×8 40×6 100×18`。

## Further Notes

- **固有局限**：这个口径衡量「全世界这条鱼多不多钓」，不是「这一张图里多不多」。
  热门图（铜湖总渔获 1818、挪威海 1503）所有鱼的登记数都高，可能一条稀有档都不剩
  （实测铜湖 28 常规 + 2 少见、挪威海 33 常规 + 21 少见 + 3 罕见，均无极罕见）；
  冷门小图相反。图内排序仍要靠「覆盖权重」逐条修，鱼口表里的 `图内常见` / `本图少见`
  标记就是给这个用的。
- 彻底解决图内排序要给稀有度加 `loc` 维度（`fish_rarity(loc, fish_key, tier)`），
  这轮没做——玩家明确要的是「鱼种本身的稀有程度」。
- 环境未开启时（无 `fish_rarity_threshold` 表）`FishRarityThresholds` 退回默认 30/14/7，
  `RecalcFishRarity` 会自动补建行。
