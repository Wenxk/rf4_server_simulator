# 判鱼主链现状快照（2026-11-23）

本文回答一个问题：**这一杆鱼从排期到入护，现在到底哪些因素在参与、哪些配了但没生效。**

- 依据：代码 `2ee1e9b` + `data/rf4server.db` 的实际 `tunables` 与表行数（读取时未停服，配置可能已被继续修改）
- 机制原理看 `docs/gameplay.md`；路由表看 `docs/mechanism-map.md`；11-10 的旧调查 `docs/后台配置对发鱼流程影响的调查.md` 有些结论已过期
- 文中「默认」指 `tunableF(key, 默认值)` 的第二个参数；「实际」指库里现有的行

## 0. 当前发鱼模式

三选一的总闸，同时只能有一个生效：

| 开关 | 实际值 | 结论 |
|---|---|---|
| `grade_ratio_enabled` | 0 | 档位比例模式**未启用** |
| `two_stage_hunt_enabled` | 0 | 两档制**未启用** |
| `fish_hunt_enabled` | 1 | **四阶段鱼猎模式生效** |

## 1. 六阶段总览

```
① 排期 scheduleBiteState → 延迟链（含钩型因子）
② 到点掷骰 bite_probability → 窝料死窝闸 → 随机
③ 选鱼权重链 rollFishFromDBWithRules（11 层，依次相乘）
④ 重量/档位 sampleHuntWeight（四阶段权重 + 钩型软匹配 + 档内分布）
⑤ 中钩确认 applyHookBiteQuality / hookRetentionFails（14/11 首次确认判一次）
⑥ 结算 gradeFromLines + 经验/价格
```

## 2. 阶段①：排期与延迟

### 2.1 谁触发排期

| 入口 | 参数（preserveHooked / resetCast） | 场景 |
|---|---|---|
| `scheduleBite` | false / **true** | 新抛竿、重抛；清 `CastTs`，等 14/7 确认入水 |
| `scheduleBiteForHooked` | true / false | 14/14 尚未到达时的补排；保留 `Hooked` 与原 `CastTs` |
| `finishNibbleHook` 内调用 | false / false | 嘬饵/脱钩后重新排下一口 |
| 收竿/离场后调用 | false / false | 竿仍在水中，继续等鱼 |

排期第一步是拒绝条件：**渔线轮已损坏**（`rigBrokenReelGUID`）→ 直接不排。

每次排期都会 `castGeneration++` 并取消旧 timer，旧 timer 到点后因 ticket 不匹配直接返回，不会重复推鱼。

### 2.2 延迟公式

先取配置 `biteDelaySettings`（四项，可被水域级 `fish_bite_setting` 整组覆盖，当前该表 0 行 → 全用全局值）：

| 参数 | 中文释义 | 当前值 |
|---|---|---|
| `bite_delay_fixed` | 固定延迟（秒），>0 时直接返回它，绕过整条延迟链 | 0 |
| `bite_delay_scale` | 延迟总倍率，最后乘 | 1 |
| `bite_delay_max` | 普通竿延迟上限（秒） | 20 |
| `bite_delay_lure_max` | 路亚延迟上限（秒） | 6 |

**非路亚（底钓/浮钓/海钓）：**

```
d = officialPushDelay()            # 从 32 条官服抓包样本里随机抽，只用 ≥6s 的 26 条
d = d × weatherBiteDelayFactor     # 天气等待因子 = 1/√(水域加权平均活跃度)，越活跃等越短
d = max(6, d × BiteSpeed)          # 技能「咬钩速度」，下限 6 秒
d = min(d, bite_delay_max)         # 上限 20 秒
d = d × bite_delay_scale
d = d × hookScore 因子             # 0.90 + 0.30×hookScore，大钩慢、小钩快
```

**路亚：**

```
d = (1.5 + rand×4.5) × weatherBiteDelayFactor   # 1.5~6 秒窗口
d = max(0.8, d × BiteSpeed)
d = min(d, bite_delay_lure_max)                 # 上限 6 秒
d = d × bite_delay_scale
d = d × hookScore 因子
```

两点容易被忽略：

1. **钩型因子在 cap 之后乘**，所以普通竿最大能到 20×1.20 = 24 秒，最小 6×0.90 = 5.4 秒。
2. **官方样本本身就有 42% 超过 20 秒**，被 `bite_delay_max` 截平后，近一半的杆固定落在 20 秒。

### 2.3 当前实际等待分布

按当前配置（weather=1、scale=1、上限 20、钩型取中性）对 20 万杆模拟：

| 指标 | 数值 |
|---|---|
| 样本原始均值 | 19.8 秒 |
| 截平后均值 | 15.2 秒 |
| 中位数（p50） | 17.7 秒 |
| p75 / p90 / p95 | 20.0 / 20.0 / 20.0 秒 |
| 顶到 20 秒上限的比例 | **42.3%** |

这意味着玩家有四成以上的杆子等到的是同一个 20 秒，节奏上会明显发闷。想改善要么降 `bite_delay_max`，要么把 >20s 的官服样本按比例缩进来（现在是被硬截，不是缩）。

### 2.4 到点后的四道门

`onRodBiteTimerTicket` 依次检查，任何一道不通过都不会判鱼：

| 门 | 条件 | 不通过的后果 |
|---|---|---|
| ticket 校验 | `castGeneration` / 定时器序号与当前一致 | 静默返回（旧 timer 作废） |
| 状态门 | `CastActive` 且未 `Resolved`、未 `PushSent`、无在途推送 | 返回 |
| 入水门 | `InWater`（14/7 已上报「在水中」） | 置 `NeedBite`，等 14/7 到了再派 |
| 落水保护 | 距落水（`FallTs`）/抛竿（`CastTs`）不足 floor | 顺延：普通竿 6 秒、路亚 1.2 秒，最小重试 1 秒（路亚 500ms） |
| 就位门 | 非路亚、非「行程」、抛竿未满 12 秒、`!RigSettled` | 每秒轮询，最多到 12 秒 |
| 饵门 | 钩上已无饵（`canFishWithBait`） | 停排，`Resolved=true`，整杆结束 |

之后才进入阶段②的掷骰；掷骰失败走 `emptyWeatherBiteLocked`，**重置后按同样的延迟链重排下一轮**，不消耗这一杆。

### 2.5 参数状态

| 因素 | 状态 | 说明 |
|---|---|---|
| `bite_delay_scale`（延迟总倍率） | 生效 | 1 |
| `bite_delay_max`（普通竿上限） | 生效 | 20 秒，42% 的杆被它截平 |
| `bite_delay_lure_max`（路亚上限） | 生效 | 6 秒 |
| `bite_delay_fixed`（固定延迟） | 生效但为 0 | 设了就绕过整条链 |
| `hookBiteDelayFactor`（钩型延迟因子） | 生效 | 0.90~1.20，在 cap 之后乘 |
| `weatherBiteDelayFactor`（天气等待因子） | 生效 | 需 `fish_weather_enabled`（天气鱼口开关）=1 |
| `bite_pace_enabled`（饵效控速） | **未生效** | 库里没这行，默认 0；整条 pace 分支不走 |
| `fish_bite_setting`（水域级鱼口覆盖） | **未生效** | 0 行，全部走全局值 |
| `bite_empty_retry_scale`（空轮重排缩放） | 生效但默认 1 | 未配置，空轮与正常咬钩同节奏 |

### 2.6 细拆 A：`fish_bite_setting`（水域级鱼口覆盖）

表结构（`migrations/00014_admin_panel.sql`）：主键 `loc`，五个可空 REAL 列 + `updated_ts` / `updated_by`。

读取顺序（`effectiveBiteDelaySettings` → `GetFishBiteSetting`）：

1. 先用全局四个 tunable 填满结构体：`bite_delay_fixed`（固定延迟）0、`bite_delay_scale`（总倍率）1、`bite_delay_max`（普通竿上限）20、`bite_delay_lure_max`（路亚上限）6；
2. `ResolveStatLoc(loc)` 把场景 id 解析成静态图 id（走 `loc_alias`，18 行）；
3. 查表：**没有行** → `HasOverride=false`，返回全局值；
4. **有行** → `HasOverride=true`，逐列判断 `NULL`：非 NULL 才替换，`NULL` 继续沿用全局。

所以覆盖粒度是**列级**的，但后台保存会一次写满五列（都非 NULL），实际效果是「保存即锁定整组」。

几个坑：

- `HasOverride` 只看**行是否存在**，不看列值。手工插一行全 NULL，也会让该水域切到覆盖分支（只是四个值恰好等于全局）。
- 同一张表还管 `bite_probability`（开口概率），走 `biteProbabilityForLoc`；`HasOverride` 时它也被一起替换。
- 覆盖粒度是**水域**（loc），不是钓点（spot）。
- 后台保存前的钳位：概率 0~1、固定延迟 ≥0、倍率 ≥0、普通竿上限 ≥1、路亚上限 ≥0.5。
- 「恢复默认」是 `DELETE` 该 loc 的行，不是把值改回全局值。

当前 `fish_bite_setting` **0 行**，全部走全局。

### 2.7 细拆 B：`weatherBiteDelayFactor`（天气等待因子）

生效前提：`fish_weather_enabled`（天气鱼口开关）=1 **且** 天气已上报 **且** `st != nil`；任一不满足直接返回 1。

计算分四步：

**① 取权重池**：本图名册 `LocRoster` 的 `catches` → 叠加后台鱼口覆盖（`enabled` 且 `weight>0` 才替换，否则置 0）→ 名册为空则退回内置鱼池 → **strict 模式下再过一遍 `filterFishBaitSet`**。所以它算的是「当前这套饵能钓到的鱼」的平均活跃度，不是整片水域。

**② 逐鱼算活跃度**（`weatherFishActivity`）：

```
水温 = 表面温×(1-水层) + 深水温×水层
  表面温 = 季节气温 + (实时气温-季节气温)×(0.36+0.16×activity) + 日照×1.2 + 前后温差×0.18 - min(0.8, 风速×0.035)
  深水温 = 季节深温 + (表面温-季节深温)×0.22
  水层：底钓 0.93、路亚按 lureLayer、其余按 抛投深度/500
热响应 = 0.35 + 0.65×exp(-0.5×z²)，z=(水温-最适温)/max(2.2,(最高温-最低温)/2)
活跃度 = 季节权重(不在 season_mask → 0.55，否则 1)
        × 天气种类权重(weather_json[kind]，钳 0.4~1.5)
        × 热响应
        × (1 - |清澈度偏好-实际清澈度|×0.20)
        × 鱼种 activity
        → 钳 [0.15, 1.25]
```

**③ 加权平均**：`active/total`，权重就是第 ① 步的池子。

**④ 取倒数平方根**：`factor = 1/√(active/total)`。活跃度高 → 等得短；活跃度低 → 等得长。

**0.89~2.58 的来源**：不是实测，也不是「好/坏天气」清单，而是第 ② 步 `clampFloat(activity*p.Activity, .15, 1.25)` 的反推——`1/√1.25 = 0.894`、`1/√0.15 = 2.582`。钳位区间决定公式边界，仅此而已。

按库里真实参数（鲫鱼，全季分布、最适温 19.2℃、activity 0.938）实算：

| 场景 | 水温 | 活跃度 | 因子 | 等待变化 |
|---|---|---|---|---|
| 盛夏 晴 26℃ | 17.1℃ | 0.904 | 1.05 | +5% |
| 盛夏 雷暴 26℃ | 17.1℃ | 0.694 | 1.20 | +20% |
| 秋季 多云 13℃ | 11.7℃ | 0.915 | 1.04 | +4% |
| 冬季 晴 2℃ | 3.5℃ | 0.522 | 1.38 | +38% |

**各乘子实测取值范围**（253 个鱼种 × 四季 × 六种天气 × 气温 −15~39℃ × 全水层 × 雾 0~1 × 风 0~12）：

| 乘子 | 最小值 | 中位数 | 最大值 | 倍数 |
|---|---|---|---|---|
| 热响应 | 0.363 | 0.864 | 1.000 | **2.76×** |
| 季节权重 | 0.550 | 1.000 | 1.000 | 1.82× |
| 天气种类权重 | 0.730 | 1.018 | 1.229 | 1.68× |
| 鱼种 activity | 0.870 | 1.006 | 1.245 | 1.43× |
| 清澈度项 | 0.878 | 0.963 | 1.000 | 1.14× |

**主导项是热响应，不是天气种类。** 晴朗/雷暴这类切换只带来 ±15% 左右的差异，水温离适温多远能带来 2.76 倍的差异。所以在这套模型里「好天气」约等于「水温对这群鱼合适」，不是「出太阳」。

**实际可达区间**：18 个水域全名册、9.3 万组条件实测 **0.953 ~ 1.907**。单鱼能触到钳位两端（253 种里 38 种能顶到 1.25、3 种能掉到 0.15），但因子是整池加权平均，多鱼混合后会被拉回中间；只有饵把池子筛到剩一两种鱼时才会靠近单鱼极值（0.89~2.5）。

**同一片水域的对照**（底钓层 0.93、12 点、该季常态气温）：

| 天气 | 铜湖 春 | 铜湖 夏 | 铜湖 秋 | 铜湖 冬 |
|---|---|---|---|---|
| clear 晴 | 1.154 | 1.026 | 1.105 | 1.398 |
| cloudy 多云 | 1.102 | **0.981** | 1.055 | 1.334 |
| rain 雨 | 1.107 | 0.986 | 1.060 | 1.341 |
| heavy_rain 大雨 | 1.211 | 1.077 | 1.159 | 1.467 |
| thunderstorm 雷暴 | **1.310** | 1.166 | 1.255 | **1.586** |

同季内晴↔雷暴只差 7%~13%，而夏↔冬差 30%~40%。盛夏天晴（1.026）反而不如多云（0.981）——库里多数鱼 `weather_json` 的多云权重高于晴。

**和上限的相互作用**：天气因子乘在 `bite_delay_max` 截平**之前**。按实测区间 0.95~1.91 代入 26 条官方样本：

| 天气因子 | 均值 | 中位数 | 被压成 20 秒的比例 |
|---|---|---|---|
| 0.95（实测最好） | 14.8 s | 16.8 s | 42.3% |
| 1.12（实测中位） | 16.0 s | 19.9 s | 42.3% |
| 1.59（冬季雷暴） | 18.0 s | 20.0 s | 61.5% |
| 1.91（实测最差） | 18.8 s | 20.0 s | 76.9% |

准确说法是：**上限抹掉的是同一时段内杆与杆之间的分辨力，不是天气好坏的总体差异**。均值仍在从 14.8 爬到 18.8（+27%），但从因子 1.12 起中位数就贴到 20 秒，玩家看到的是「又是 20 秒」而不是「今天更慢」。好天气那一侧同样被 6 秒下限夹，只是幅度小（因子 0.89 时 7.7%）。

### 2.8 季节的定义与轮换速度

**季节不是客户端概念，服务器自己算。** `fishSeasonAt`（`fish_bite_environment.go` 内，签名 `func fishSeasonAt(nowUnix int64) int`）用 `worldclock.SeasonDay(nowUnix)` 取**季节轴**（锚 `SeasonEpochUnix`，与协议轴 `ProtocolEpochUnix` 是两套历元）的游戏天数，`day % 365` 后按公历月长（31/28/31…）分月，再按 `3~5 春 / 6~8 夏 / 9~11 秋 / 12~2 冬` 映射：

| 月份 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 季节 | 冬 | 冬 | 春 | 春 | 春 | 夏 | 夏 | 夏 | 秋 | 秋 | 秋 | 冬 |

**时间基准**（`internal/worldclock`）：历元 2015-08-01 00:00 UTC，`RealSecPerGameHour`（每游戏小时对应现实秒数）= 150 秒。也就是 **1 游戏日 = 1 现实小时**，24 倍速。

于是季节的轮换速度是：

| 单位 | 游戏内 | 现实时间 |
|---|---|---|
| 1 游戏日 | 24 游戏小时 | 1 小时 |
| 1 游戏月 | 28~31 游戏日 | 1.17~1.29 天 |
| **1 个季节** | 约 90 游戏日 | **约 3.75 天** |
| 1 整年 | 365 游戏日 | **约 15.2 天** |

也就是说**四季每 15 天走完一轮，每个季节只持续 3~4 天**。实测：2026-11-15 06:00 入春、11-19 02:00 入夏、11-22 22:00 入秋、11-26 17:00 入冬——平均 3.8 天换一季。

**为什么玩家感觉不到四季**：

1. **客户端没有季节信号，气温也不随季节变。** 各图气温来自 `map_defs.json` 的静态预报模板（8 天循环，每图固定区间，例如 `level_001_lake` 是 15~20℃），`weather_schedule.go` 只按绝对游戏日重新排列这 8 天，不引入季节。所以玩家看到的温度计一年到头都在同一区间。
2. **季节只改两条内部量**：`seasonalAir`（季节基准气温，春 10 / 夏 23 / 秋 13 / 冬 1℃）与 `seasonalDeep`（深水基准，春 9 / 夏 14 / 秋 11 / 冬 4℃），且这两个基准还要与客户端气温按 `0.36+0.16×activity` 混合，**客户端气温权重占大头**。算下来 `level_001_lake` 四季水温分别是 10.4 / 15.5 / 12.1 / 5.5℃，看起来分得很开，但客户端气温始终显示 14~16℃，玩家无法对应。
3. **季节掩码的压制很弱**：`season_mask`（适季位图，1 春 2 夏 4 秋 8 冬）未命中时只乘 **0.55**，不是禁口。253 种鱼里 174 种四季全适，实际影响：

| 季节 | 不适季鱼种权重占比（18 图平均） | 最高 |
|---|---|---|
| 春 | 2.3% | 5.3% |
| 夏 | **13.5%** | **44.7%**（亚马河） |
| 秋 | 1.0% | 5.6% |
| 冬 | 7.0% | 11.4% |

折算成全池平均系数，最极端的情况（亚马河夏季）也只有 **0.80**——即整个池子的鱼口只慢了约 20%，而不是「夏季钓不到冷水鱼」。

**只有夏冬有明显差异**：夏季有一批北方冷水鱼（`a_char`、`a_omul`、`taimen`、各种 `sig_*` 白鲑）不适季，共 51 种标成「春秋冬」；冬季则是一批温水鱼（鲤、鲶、某些鲌类）不适季。春秋两季几乎全员适季，所以这两个季节在体感上等于不存在。

**结论**：季节在这套系统里是**判鱼内部的一个温和乘子 + 水温基准切换**，没有对外表现（客户端无季节指示、气温不变），轮换周期又只有 3.8 天，且只对 79 种鱼起作用、最大压制 20%。玩家感觉不到四季是符合当前实现的——它确实存在，但几乎不可感知。

### 2.9 鱼种档案维护（`fish_ecology_species`，迁移 00082）

判鱼侧读生态档案时是三层合并，顺序不能颠倒：

```
资产基线 fish_ecology            ← 由 assets/fish_ecology_defs.json 按 profile_version 重灌
  ↓ 只改有值的字段
鱼种覆盖 fish_ecology_species    ← 后台「鱼种档案」页，改的是物种本身，全水域生效
  ↓ 只改有值的字段
水域覆盖 fish_ecology_loc        ← 后台「钓场与鱼口 → 生态覆盖」弹窗，只影响一张图
```

实现入口是 `Server.fishEcologyFor(ctx, statLoc, key)`（`fish_ecology.go:405`）：`statLoc` 为空（位置未知或静态库不可用）时**只跳过水域层，鱼种层照常合并**——水温窗口属于物种本身，不该因为定位缺失退回合成基线。

**为什么单开一张表**：`fish_ecology` 由 `ensureFishEcology` 按 `WHERE profile_version < excluded.profile_version` 从资产 JSON 重灌，手工值写进去会在下次版本升级时被冲掉。所以覆盖写独立表，迁移 00082 的注释里写明了这个理由。新表已登记到 `repos_integrity.go` 的 `RuntimeTables`，漏登记会被 `ValidateTableOwnership` 拒绝。

**后台**：「鱼种档案」页（`data-view="ecology"`），三条接口：

| 接口 | 作用 |
|---|---|
| `GET /api/fish/ecology-species` | 返回每个鱼种的 `base`（资产基线，全字段）与 `override`（覆盖原文，nil 字段=继承）。`?q=` 模糊过滤，`?only=overlaid` 只看已覆盖 |
| `POST /api/fish/ecology-species` | 保存覆盖。全字段为 nil 时等价于删除，避免留一条语义为「无覆盖」的脏行 |
| `POST /api/fish/ecology-species/reset` | 删除该鱼种的覆盖，回到资产基线 |

保存后投递 `{"op":"reload_fish_ecology"}`，主服热加载，最迟 30 秒生效。列表里「金色＝已覆盖、灰色＝基线」的对比**不在后端算生效值**，而是前端用 `base` + `override` 现算（`ecoEffective`）——真正的合并由 `fishEcologyFor` 决定，后端再抄一份展示用逻辑迟早会走偏。

**能维护的 20 个字段**：水层三件（`layer_min`/`layer_pref`/`layer_max`，0=水面 1=水底）、水温三件（`temp_min_c`/`temp_pref_c`/`temp_max_c`，℃）、`diel_mask`（昼夜掩码，1夜2晨4昼8昏）、`diel_curve`（昼夜曲线四段）、`season_mask`（季节掩码，1春2夏4秋8冬）、五个 JSON 矩阵（`diet_json` 食性 / `weather_json` 天气 / `action_json` 拟饵动作 / `substrate_json` 底质 / `color_json` 水色）、`current_pref`（水流偏好）、`activity`（活跃度）、`light_pref`（光照偏好）、`clarity_pref`（清澈度偏好）、`aggression`（攻击性）、`enabled`（是否参与判鱼）。

**弹窗里实时显示窗宽与热响应容差**：因为判鱼侧的热响应是

```go
tolerance := math.Max(2.2, (p.TempMaxC-p.TempMinC)/2)
z := (water.WaterTempC - p.TempPrefC) / tolerance
thermal := .35 + .65*math.Exp(-.5*z*z)
```

窗宽直接决定曲线陡缓。**当前 253 种的中位窗宽是 20.2℃，容差≈10℃，高斯极平——温度几乎不筛鱼。** 后台在温度三个输入框下方实时给出「窗口宽 X℃ · 热响应容差 Y℃ · 窄/中/宽」的判定，宽窗口标红。这也是后续用文献值收紧温度区间的抓手。

**批量导入走 `ReplaceFishEcologySpeciesCurated`**：它先 `DELETE FROM fish_ecology_species WHERE origin<>'manual'` 再插，**手填行（`origin='manual'`）原样保留**，所以可以反复导入文献值而不会冲掉人工校对结果。导入行标 `origin='curated'`，后台列表状态列显示为「文献值」。

## 3. 阶段②：这一杆有没有鱼咬

| 因素 | 状态 | 说明 |
|---|---|---|
| `bite_probability`=0.5 | 生效 | 全局 50%；`fish_bite_setting` 表 0 行 → 无水域级覆盖 |
| 窝料死窝闸门 | 生效 | `groundbait_enabled`=1 且落点窝料耗尽时压概率 |
| `happy_hour_enabled`=1 | 生效 | 欢乐时光窗口内抬咬口接受度 |

## 4. 阶段③：选鱼权重链（11 层）

按 `rollFishFromDBWithRules` 内的执行顺序，每层都能把权重乘成 0：

| # | 层 | 数据/开关 | 状态 |
|---|---|---|---|
| 1 | 静态名册 | `loc_fish` 430 行、`spot_fish` 365 行 | 生效 |
| 2 | 稀有度档位权重 | `fish_weight_preset` = **80 / 40 / 20** | 生效，优先于 `loc_fish.catches` |
| 3 | 后台鱼口覆盖 | `fish_weight_override` **0 行** | **未生效** |
| 4 | 水层地板 | `bite_gate_enabled`=1，超出 ±0.15 乘 0.01 | 生效（不剔除，只压权重） |
| 5 | 区域名册 | `fish_zone` **0 行** | **未生效**（无配置，函数直接返回） |
| 6 | 饵规则 strict | `fish_bait_rules_enabled`=1 + `fish_bait_override` 211 行 | 生效：allow/deny 硬过滤 → ×(1.0~1.5 随机) → 多饵最多 +15% |
| 7 | 鱼群分布 | `fish_distribution_enabled`=1 | 生效：40m 格 × 4 游戏小时，返回 0 或 0.15~4.0 |
| 8 | 生态总层 | `fish_ecology_enabled`=1 | 生效：水层匹配、季节、天气种类、天气额外项、水温、清澈度、光照、钓点环境、路亚动作、运动条件、稀有度技能、拟饵权重、技能环境 |
| 9 | 昼夜 | `fish_diel_weight_enabled`=1 + `fish_diel_inactive_block`=1 | 生效：曲线 <0.5（未置位段）**整段归零禁口** |
| 10 | 钓点偏差 | `applyFishSpotBias`，`spot_def` 205 行 | 生效 |
| 11 | 窝料吸引 | `groundbait_enabled`=1，`groundbait_deposit` 2 行 | 生效 |
| 12 | 鱼口接受度 | `forRig`=true（因 strict=1） | 生效：`acceptHappyHourBite` 再掷一次，失败空竿 |

**这一层里被跳过的：**

- **companion 饵效 8×/4×/2× 倍率**：`if !strict` 才读 tier；strict=1 时整段跳过。饵的作用只剩 allow/deny + 1.0~1.5 随机分。
- `bait_pref_weight_shift`（体重峰值右移）同样只在非 strict 路径中参与。

## 5. 阶段④：重量与档位

| 因素 | 状态 | 说明 |
|---|---|---|
| 四阶段权重表 | 生效 | 四张表 **below 全为 0**、qualified 30~60、trophy 30~45、super 10~25 |
| 鱼猎进度 | 生效 | `fish_hunt_state` 121 行；pity/补偿/巨物阶段 |
| `TrophyLuck` | 部分生效 | 只在 `stage > daily` 时加 `w3`（最高 +50%）；日常阶段无效 |
| 钩型软匹配 | 生效 | `hook_spawn_match_strength`=1，满强度改写非蓝冠三档 |
| 档内分布 | 生效 | `triangularLog`，与钩号无关 |
| 蓝冠上限 | 生效 | clamp 到 1.6×蓝冠线 |
| `grade_pct_*`（3/87.5/9/0.5） | **未生效** | 只属于 `grade_ratio_enabled=1` 的档位比例模式 |
| `fish_weight_cap`=0 | **未生效** | 承力截断关闭 |
| `hook_entry_cap` 未设 | **未生效** | 钩入口硬截断关闭 |

## 6. 阶段⑤：中钩确认

| 因素 | 状态 | 说明 |
|---|---|---|
| 保持率 `hookSolidHold` | 生效 | `r=鱼重/钩容量`；r≤1 恒 1，r>1 从 0.98 降到 **0.50** 下限 |
| 实口/嘬饵 solid | **未生效** | `hook_nibble_enabled`=0，`applyHookBiteQuality` 不掷骰 |
| 钩型档案倍率 | **未生效** | `hook_bite_profile` 1141 行齐全，但 `solid_scale_bp`/`hold_scale_bp` 全 10000、两个 floor 全 0 |
| 蓝冠免检 | 生效 | `Grade>=4` → `LuckProtected`，保持率 1 |
| 脱钩后果 | 生效 | 改判 `nibble` → 鱼离场 → 重排下一口 |

## 7. 阶段⑥：结算

- 档位 = `gradeFromLines`（重量 vs 达标线/奖杯线/蓝冠线），`fish_grade_lines` 253 行
- 经验、银价、图鉴、成就走 `settleCatchOnceWithQualification`，与判鱼权重无关
- `TrophyLuck` 不在结算层二次改档

## 8. 未生效清单

| 项 | 现状 | 原因 |
|---|---|---|
| `grade_ratio_enabled` 及 `grade_pct_below/qualified/trophy/super` | 0 / 3 / 87.5 / 9 / 0.5 | 模式未启用，四个占比数字完全没参与 |
| `two_stage_*` 八个权重 | 有值 | 两档制未启用 |
| `fish_weight_override` | 0 行 | 没配置 |
| `fish_zone` / `fish_zone_fish` | 0 行 | 没配置，区域名册不走 |
| `fish_weight_cap` | 0 | 承力截断默认关 |
| `hook_entry_cap` | 未设置 | 钩入口截断默认关 |
| `hook_nibble_enabled` | 0 | 实口/嘬饵判定关 |
| `hook_bite_profile` 四个调参列 | 全默认 | 只有几何（长度/钩号）在起作用 |
| 四档 below 权重 | 全 0 | 不出「未达标」鱼 |
| `bite_pace_enabled` | 未设置 | 饵效控速关 |
| `loot_box_enabled` | 0 | 战利品箱关 |
| `condition_health_enabled` / `condition_fishing_pause_enabled` | 0 | 状态条不参与 |
| `event_newyear_enabled` | 0 | 活动关 |
| `fish_diel_active_boost`=2 / `fish_diel_inactive_penalty`=0.3 | 有值 | **代码里已无引用**，2026-11-20 对齐上游时删除，属残留脏数据 |

## 9. 容易误解的五个点

1. **`fish_weight_preset` 是 80/40/20，不是 100/30/5**。高稀有度的压制强度只有 4:1，不是 20:1。想恢复陡梯度直接改这张表，不用改代码。
2. **`grade_pct_*` 那四个数（3 / 87.5 / 9 / 0.5）当前没有任何作用**。玩家看到的四档占比由四阶段权重表决定。
3. **companion 饵效的 8×/4×/2× 没在跑**。strict 模式下饵只回答「准不准吃」，不回答「吃了多大概率」。`companion_bait_fish` 31231 行、`loc_bait_fish` 3304 行都只在后台展示与 allow 生成时用。
4. **「每条鱼都达标」是配置结果**：四张表的 below 都是 0，派鱼根本不会选未达标档；只有档位线缺失回退时才会意外出现。
5. **钩型档案 1141 行看起来配得很全，实际只有尺寸在起作用**。四个调参列全默认，型号级微调等于没开。

## 10. 数据底数（判断"有没有配置"用）

| 表 | 行数 | 用途 |
|---|---|---|
| `loc_fish` / `spot_fish` | 430 / 365 | 静态名册 |
| `fish_ecology` | 253 | 生态档案（rarity_tier 分布 169/45/39） |
| `fish_grade_lines` | 253 | 三条档位线 |
| `companion_bait_fish` | 31231 | 饵证据（判定用 tier，占比不参与） |
| `loc_bait_fish` | 3304 | 本图渔获（**不参与判鱼**） |
| `rf4records_catch` | 580816 | 官服统计（**不参与判鱼**，仅后台参考） |
| `hook_bite_profile` | 1141 | 钩型档案 |
| `fish_bait_override` | 211 | 饵规则覆盖 |
| `fish_hunt_state` | 121 | 鱼猎进度 |
| `fish_zone` / `fish_weight_override` | 0 / 0 | 未使用 |
| `fish_ecology_species` | 0 | 鱼种档案覆盖（00082，后台「鱼种档案」页；空=全部跟随资产基线） |
