# 咬钩节奏与饵效控速

> 用法：改 `bite_pace_*`、饵效对口分、打窝压缩、路亚单轮概率时对照本页。
> 饵料与候选的判定见 `bite-bait-and-spawn.md`；玩法数值说明见 `docs/gameplay.md`。

## 1. 咬钩节奏（bite_pace_enabled，默认关）

- ✅ 节奏因子（对口分 → 三档乘数/概率）是**独立于判鱼候选的通路**：改变的是
  「多快来鱼」，不得改写鱼种权重、档位采样或硬性鱼口门的候选集合；与
  `fish_bait_rules_enabled`（改候选）可独立组合、互不替代。
  - ❌ 用节奏开关顺手改候选权重 → 饵效一变鱼群组成跟着变，无法独立回退。
- ✅ 对口分的证据源与判鱼**同源**（companion 档位 + 生态食性 `diet_json`；
  本图渔获与 rf4 官服占比不参与，2026-11-19 口径），拟饵与天然饵共用同一套
  证据票 + 无证据 0.30 基线；食性票封顶 0.6——家族对口只给「不差」，
  「好」必须由证据票给出；拟饵另取名册内最大 `diet_json.lure/2`。
  - ❌ 名册内没有任何证据就把无证据饵判满分 → 换什么饵都快，饵效失去意义。
- ✅ 多钩钓组按 `baitedHookSets` 钩位分组、每钩独立评分取最好一钩（产品决策：
  与 `rollFishForRig` 任意钩可出鱼同语义，不引入「选中主钩」概念——判鱼时
  钩位是随机洗牌的）。
  - ❌ 只拿第一枚钩或全部饵混评 → 与实际出鱼钩位脱节。
- ✅ 路亚族（含海钓铁板 50-54）在 pace 模式下**延迟链零改动**（1.5~6 秒窗、
  1.2 秒落水保护、`bite_delay_lure_max` 全部沿用），节奏只走单轮判定概率
  `PaceLureProb`——保住「14/14 不晚于收线窗口」的既有约束。
  - ❌ 放宽路亚延迟窗 → 鱼在收线停顿/收回到位后到达，与客户端行为系统打架。
- ✅ 底层杆延迟 clamp 跟钓组族走：底/浮 `[6, pace_bottom_max=60]`、海底钓族
  `[3, pace_marine_max=30]`；打窝压缩（`groundbaitPaceFactor`）同时压下限到
  `pace_groundbait_floor`（3 秒）并同比缩短落水保护，但**只缩短不拉长**、
  任何倍率下不早于 800ms（bite-delay-floor 契约）。
  - ❌ 死窝（overdoseP→0）还继续压缩 → 腻死的窝反而变成「发窝快口」。
- ✅ 三处消费点（延迟分支、`rodPaceProb`、`paceFloorShrink`）都受**当前开关**
  门控：运行中热关闭 `bite_pace_enabled` 后下一次判定/重排立即回 legacy，
  已排定 timer 的绝对时刻不重排。
  - ❌ 只检查快照里的 `PaceKnown` → 热关闭后旧因子残留（PR #531 审查 P2-1）。
- ✅ 名册食性聚合缓存按 statLoc 独立 30 秒过期，`fish_ecology` 重载/失效时
  整体清空（`invalidatePaceDietCache`）。
  - ❌ 单一全局时间戳 → 新图写缓存复活其他图的过期条目（审查 P2-4）。
- ✅ pace 因子随 `FishState` 走一杆：重启/重连恢复（`PaceKnown=false`）时在
  timer 触发前惰性重算（`ensurePaceState`），不得按中性因子（无节奏控制）判鱼；
  补排/嘬饵否决/脱钩重排复用快照时必须带回 `Known=true`（审查 P2-2）。
- ✅ 查鱼助手的理论条/小时与生产同源：Chance 乘同一档位概率、底层杆平均
  间隔用 `meanPaceStandardDelay` 同一套 clamp；口径差异（不含
  `hookBiteDelayFactorForRig` 钩型倍率）与既有 `meanBiteDelay` 一致，非本次引入。
  - ❌ 查询链漏乘 pace 因子 → 「查鱼说很快、实钓等一分钟」。

## 提交前快速自检

- [ ] 节奏开关（bite_pace_enabled）关掉时输出与旧公式逐位一致；路亚窗口没被放大

