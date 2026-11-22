# 路亚饵效控速：饵分 × 动作分

Status: done

## Problem Statement

`bite_pace_enabled` 开启后，路亚族（含海钓铁板 50-54）的节奏只看**挂的什么饵**：
对口分是排 timer 时的静态快照，延迟窗恒为 1.5~6 秒，节奏只靠 fire 时低概率掷骰
（`pace_lure_prob_good/mid/bad` = 0.30/0.10/0.02）。后果：

1. 动作完全不影响来鱼速度——同一支 jerkbait 匀速收和狂抽停，来鱼节奏一模一样；
2. 窗口 1.5~6 秒封顶让差饵也几乎每几秒掷一次骰，与底钓差饵「长时间才有鱼」
   的手感完全不同；
3. 「好的动作出来了才视为好的饵效」没有落点——动作只影响「出什么鱼」（判鱼链
   `lureActionTypeWeight`），不影响「多快来鱼」。

## Solution

pace 开启时，路亚族的档位由**复合分**决定（乘法复合）：

```text
复合分 = 饵分 × (0.4 + 0.6 × 动作分)
动作分 = clamp((lureActionTypeWeight(路亚类型, 当前动作) − 0.8) / 0.54, 0, 1)
无动作上报（刚落水、尚未收线）= 0.45（中性，不给好档）
```

- 饵分沿用既有 `st.PaceScore`（证据票 + 食性票，排 timer 时算好，一杆不变）；
- 动作分按「路亚类型 × 当前动作」实时算，复用鱼猎阶段的 19 类路亚表；
- 三档阈值沿用 `pace_score_good/mid`（0.75/0.45）。

| 档位 | 等待窗（秒） | fire 开口率 | `bite_delay_lure_max` |
|---|---|---|---|
| 好 ≥0.75 | `[pace_lure_min 1.5, pace_lure_max 6]` | `pace_lure_prob_good` **0.30→1.0** | 仍封顶 |
| 中 ≥0.45 | `[pace_lure_mid_min 10, pace_lure_mid_max 60]`（新键） | `pace_lure_prob_mid` **0.10→0.5** | 不再封顶 |
| 差 <0.45 | `[pace_lure_bad_min 20, pace_lure_bad_max 60]`（新键） | `pace_lure_prob_bad` **0.02→0.12** | 不再封顶 |

- **fire 动态取档**：`rodPaceProb` 用 `st.PaceScore`（饵分快照）+ 当前
  `st.LureType/LureAction` 现算复合档位——装排后动作变差的轮次按低开口率空掷，
  随后空轮重排按差档长窗重排；同时把实际档位写回 `PaceTier`，让后续「动作改善」
  的比较基于本轮真实生效的档位（不能拿排 timer 时的旧好档）。
- **动作改善重排**：14/7、14/9 解锁后调用 `paceLureActionRearm`，复合档位**升高**时
  立即按新档重排 timer（只升不降 + 2 秒防抖 `PaceRearmAt`）——好动作一出来即进
  1.5~6 秒短窗；恶化不重排，由 fire 时的动态开口率自然处理。
- 天气 × 技能 BiteSpeed × `hookBiteDelayFactorForRig` × `bite_delay_scale` ×
  0.8 秒地板 × 1.2 秒落水保护全保留；pace 关闭回原版 1.5~6 窗，逐位一致。

## Implementation Decisions

- 核心逻辑在 `bite_pace.go`：`paceLureActionQuality` / `paceLureCombinedScore` /
  `paceLureWindowFor` / `paceLureProbForTier` / `paceLureStateFor` /
  `refreshLurePaceTierLocked` / `paceLureActionRearm`（防抖 `paceLureRearmDebounce = 2s`）。
  `paceTierFor` 的路亚概率委托 `paceLureProbForTier`，档位→概率只有一处来源。
- `FishState` 新增 `LureType`（14/1 播种，`lureCoreForGear` 取最深拟饵的类型名）
  与 `PaceRearmAt`，均仅进程内；14/1 清空两者。
- `fish_query_rate.go`：`meanBiteDelay` 的窗口与封顶由调用方传入，查鱼助手的路亚
  平均间隔与生产同源；`fishQueryPaceState` 的路亚分支用复合分（假设动作沿用
  `fishQueryMotion` 的 drift/jig 口径）。
- 与上游差异：本地没有采纳「底层杆延迟按档位区间 clamp」那一次重构，底钓窗仍是
  按钓组族的 `[6,60]` / `[3,30]`；本次只动路亚分支。
- 参数面：新键 `pace_lure_mid_min/max`、`pace_lure_bad_min/max`；默认值
  `pace_lure_prob_good/mid/bad` 改为 1.0/0.5/0.12（旧库若已有这三行仍按旧值生效，
  需要后台清行或改值）。

## Testing Decisions

- `TestPaceLureActionQualityNeutral`：无动作为 0.45；顶格动作 1.0；中性动作 ≈0.37。
- `TestPaceLureCombinedScore`：对口饵+对口动作=1.0；对口饵+无动作=0.67；
  差饵+顶格动作仍到不了好档；中饵最高只到中档。
- `TestPaceLureWindowByTier`：好档 1.5~6 且被 `lureMax` 收窄、不会被放大；
  中 10~60、差 20~60。
- `TestRodPaceProbFollowsLureAction`：无动作→0.5、对口动作→1.0 并刷新 `PaceTier`、
  差饵→0.12、非路亚仍取静态快照、热关闭回 1。
- `TestPaceDelayBoundsAndLureUnchanged` 改写：好档留在 1.5~6，中/差档分别落在
  10~60 / 20~60。
- `TestMeanBiteDelay` 新增 `lure-mid-window`、`lure-single-point` 用例。

## Out of Scope

- 不改判鱼候选与鱼种权重——动作仍只通过判鱼链影响「出什么鱼」，本次让节奏通路
  也看动作。
- 不改海底钓族 55-60 的窗口（3~30 秒，挂天然饵为主，动作语义不适用）。
- 不改 14/7/14/9 协议形状与动作位映射（`classifyLureMotion` 位表不动）。
- 不改非路亚族的 pace 行为（延迟链、打窝压缩、落水保护全部沿用）。
- 换饵 mid-cast 不刷新 `LureType`/`PaceScore`（既有口径，非本次引入）。
