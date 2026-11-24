# 参数字典（调参唯一对照表）

> 用途：改任何影响手感的数值前，先在本表查三件事——**它管哪一段**、**默认值/合法区间**、
> **改它会不会被别的开关压住**。本表以代码为准，四条来源已对账：
> `internal/admin/web/index.html` 的 `TUNABLE_DEFS`（后台标签与提示）、
> `internal/admin/api.go` 的写入校验、`internal/gameserver` 的读取点与默认值、
> `docs/gameplay.md` 的机制口径。来源分散说明见文末「附录 A」。
>
> 读表约定：
> - **改哪里**：`后台` = 管理后台有输入框；`直写库` = 只能 `tunables` 表；`表` = 玩家库配置表；
>   `编译期` = 改代码要重编译。没有后台入口的参数**不要用 SQL 顺手改**，先确认生效链路。
> - **生效延迟**：`tunables` 30 秒（后台保存会推 `reload_tunables`，通常立即）；饵料覆盖 15 秒；
>   `fish_ecology*` / `fish_zone` 30 秒。已咬钩的鱼不重抽。
> - **默认值列**里的 `范围` 是后台/接口的硬校验；没写范围表示接口不校验，靠代码 clamp。

---

## 1. 参数分四层，别跨层找

| 层 | 存在哪 | 谁写 | 生效方式 | 典型参数 |
|---|---|---|---|---|
| 部署配置 | `config.yaml` / 环境变量 | 运维 | 重启进程 | 端口、证书、数据库路径、隧道密钥 |
| 热更参数 | 玩家库 `tunables` 表 | 管理后台 | 30 秒缓存 | 绝大多数玩法手感 |
| 配置表 | 玩家库各 `fish_*` / `hook_*` 表 | 管理后台 | 30 秒 / 立即 | 水域覆盖、鱼种权重、生态档案、区域名册 |
| 编译期常量 | Go 源码 | 开发 | 重编译 | 延迟样本池、钩型自动因子、档位带边界 |

**同一个名字在两层都出现时，配置表压过 `tunables`**：`fish_bite_setting` 有行就是五项整组替换全局，
`fish_weight_override` 单鱼替换档位预设，`fish_bait_override` 整行替换内置 252 条规则。
这是「后台改了没反应」的第一大原因。

---

## 2. 一杆鱼经过哪些参数（按执行顺序）

```
① 排咬钩定时器
   延迟 = 样本池 × 天气因子 × 技能 × 保底6s × 上限 × 倍率 × 钩型自动因子
   参数：bite_delay_fixed（>0 直接短路整条链）/ bite_delay_scale / bite_delay_max /
         bite_delay_lure_max / fish_weather_enabled / fish_bite_setting（水域整组覆盖）
   开 bite_pace_enabled 时，底层杆再乘饵效节奏因子（好/中/差 = ×1.0/1.6/3.0）；
   路亚按「饵分 × 动作分」复合档位取等待窗：好 1.5~6s、中 10~60s、差 20~60s
   （好档受 bite_delay_lure_max 封顶，中/差档不封顶），单轮开口率 1.0/0.5/0.12
   ↓
② 到点掷骰：这轮咬不咬
   p = bite_probability（水域可覆盖）→ 若开了饵料规则或天气鱼口则封顶 0.95
       → × 窝料死窝闸门（>15 份开始压，≥22 份为 0）
   ↓
③ 选鱼：出什么鱼（权重链，逐层相乘/替换）
   静态名册 → 水域覆盖 → 水层地板 → 区域名册 → 饵料规则 → 鱼群分布
            → 逐鱼种生态 → 钓点观测 → 窝料 → 饵效证据 → 加权抽签
   ↓
④ 定档：出多大
   档位比例（grade_ratio）/ 鱼猎四阶段 / 两档制 → 奖杯保底与回访 → 重量上限 → 钩型软匹配
   ↓
⑤ 中钩确认：咬了能不能中、中了保不保得住
   钩型入口公式 × 钩子手感页四参数 → 嘬饵判定（hook_nibble_enabled）/ 保持率抽样
   ↓
⑥ 搏鱼与结算
   拉力曲线 / 鱼体力 / 玩家精力 / 装备磨损 / 经验倍率
```

**关键顺序**：③ 的每一层都作用在上一层算完的权重上；④ 一旦开了档位比例（默认开），
鱼猎阶段权重、奖杯保底、钩型软匹配**整组不参与**；⑤ 与 ③④ 完全独立，改钩子手感不会改变咬钩快慢。

---

## 3. `tunables` 字典

### 3.1 咬钩节奏（鱼多久来咬）

| 键 | 默认（范围） | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `bite_probability` | `1.0`（0~1） | 每轮判鱼的基础命中概率。1=每轮必出。**开了饵料规则或天气鱼口后被封顶 0.95** | 以为设 1 就必出，其实已经被 cap；水域有覆盖行时此项对该图完全失效 |
| `bite_delay_fixed` | `0`（秒） | `>0` 时**整条延迟链短路**：天气、技能、倍率、上限全部无效，只余钩型因子（实际出 `fixed×[0.90,1.20]`） | 调到 10 秒仍会抖 9~12 秒；改完忘关会让后续所有节奏参数「看起来没生效」 |
| `bite_delay_scale` | `1.0` | 延迟整体倍率，乘在上限之后。0.5=快一倍，2=慢一倍 | `fixed>0` 时无效；cap 在前，倍率能把结果顶破上限 |
| `bite_delay_max` | `20.0`（秒） | 普通竿延迟上限（先保底 6s 再截断） | 设 <6 会把落水保护一起缩到该值 |
| `bite_delay_lure_max` | `6.0`（秒） | 路亚延迟上限（保底 0.8s） | 与普通竿分开算，改错只影响路亚 |
| `bite_empty_retry_scale` | `1` | 空轮（无符合条件的鱼）后下一轮等待的倍率。0.25=空轮 5 秒重试 | 只改「空竿后重试」节奏，不改咬钩概率；调太小会刷屏式空竿 |
| `fish_bite_setting`（表） | 无行 | 水域级**五项逐列覆盖**：`bite_probability` + 四个延迟参数。非 NULL 的列才替换全局值，NULL 继续沿用 | 该图从此与全局参数脱钩，后台「游戏参数」页对这张图全部失效且**界面无提示** |

**饵效控速（`bite_pace_enabled`，默认关，后台「游戏参数 → 咬钩节奏」有 15 项）**——独立于判鱼候选的一层，
只改「多快来鱼」，不改鱼种权重、档位与候选集合：

| 键 | 默认 | 作用 / 影响范围 |
|---|---|---|
| `bite_pace_enabled` | `0` | 总开关；关=完全走 legacy 延迟链。三处消费点（延迟分支 / 单轮概率 / 保底收缩）都受当前开关门控，热关立即回退 |
| `pace_score_good` / `pace_score_mid` | `0.75` / `0.45` | 饵效对口分的两档阈值：≥好、≥中，其余为差 |
| `pace_factor_mid` / `pace_factor_bad` | `1.6` / `3.0` | 底层杆（非路亚）延迟乘数；好档为 1.0 |
| `pace_prob_good` / `pace_prob_mid` / `pace_prob_bad` | `1.0` / `0.5` / `0.12` | 底层杆单轮判定概率 |
| `pace_lure_prob_good` / `_mid` / `_bad` | `1.0` / `0.5` / `0.12` | 路亚 fire 开口率，按复合档位（饵分 × 动作分）动态取 |
| `pace_lure_min` / `pace_lure_max` | `1.5` / `6` | 路亚**好档**等待窗；再受 `bite_delay_lure_max` 封顶，不会被放大 |
| `pace_lure_mid_min` / `_max` | `10` / `60` | 路亚中档等待窗（饵对口但动作一般），不封顶 |
| `pace_lure_bad_min` / `_max` | `20` / `60` | 路亚差档等待窗（饵不对口），不封顶 |
| `pace_bottom_max` / `pace_marine_max` / `pace_marine_min` | `60` / `30` / `3` | 延迟 clamp：底/浮 `[6,60]`，海底钓族 `[3,30]` |
| `pace_groundbait_gain` / `_floor` / `_min_factor` | `0.62` / `3.0` / `0.35` | 打窝压缩：`1−gain×窝强度×死窝闸门`，下限降到 3 秒；死窝不加速 |

### 3.2 判鱼权重链开关（决定「哪些机制参与选鱼」）

这五个开关由「鱼汛模式」卡片原子统管：**真实鱼汛=五开，遗留=五关**，手改单个开关后卡片显示 `mixed`。

| 键 | 默认 | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `bite_gate_enabled` | `0` | 水层地板：目标水层超出鱼种区间 ±0.15 时乘 `bite_gate_layer_floor`；只压权重、不剔候选。吃不吃已统一由饵料规则 allow/deny 决定 | 关了等于不压水层错配；不会再改变「这条鱼吃不吃这款饵」 |
| `fish_ecology_enabled` | `0` | 逐鱼种生态总开关：水层/季节/天气/水温/光照/底质/动作等约 14 个因子，整体 clamp [0.08,2.5] | 关=整层中性，但**不影响昼夜层**（昼夜独立默认开）；表单鱼 `enabled=0` 仍会彻底封鱼 |
| `fish_weather_enabled` | `0` | 天气与温度鱼口：按小时天气修正权重，并让底钓/浮钓间歇试口；同时参与延迟因子 | 与饵料规则任一开启都会把 `bite_probability` 封顶 0.95 |
| `fish_bait_rules_enabled` | `0` | 鱼类吃饵偏好：有 allow 时命中满分（1.0~1.5），名单外直接 0；只有无 allow 规则的鱼才按 companion tier3 满分 / tier1~2 低权重兜底 | 关掉后「鱼饵偏好」页保存的规则**全部不生效**；allow 是唯一准入，companion 不能绕过 |
| `fish_distribution_enabled` | `0` | 鱼群随机分布：按「区域×鱼种×4 游戏小时」确定性哈希，`v<0.3` 直接权重 0 | 只改空间分布，不参与昼夜；关掉后同图任何位置权重一致 |
| `happy_hour_enabled` | `1` | 欢乐时光：**不是权重乘子**，是选鱼定稿后的二次掷骰，`chance=interest×1.1×天气活跃度`，interest≤0.1 封顶 | 以为「鱼口+10%」会变成「低兴趣鱼几乎必过」的误判；三开关全关时此掷骰整体跳过 |

### 3.3 发鱼档位（出多大）

| 键 | 默认（范围） | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `grade_ratio_enabled` | `1`（0/1） | **档位比例总闸**。开：按四档带采样，并**压制**鱼猎四阶段权重、奖杯保底/回访、`hook_spawn_match_strength`、TrophyLuck | 这是最强的压制开关，开着时改鱼猎或钩型软匹配都「没效果」；三选一模式互斥，只能通过发鱼模式卡片原子切换 |
| `grade_pct_below` | `3`（≥0） | 未达标档相对权重 | 只写相对值，保存自动归一；运行时对非 100 总和再归一 |
| `grade_pct_qualified` | `87.5`（≥0） | 合格档相对权重 | 同上 |
| `grade_pct_trophy` | `9`（≥0） | 奖杯档相对权重 | 调高奖杯会挤占合格档份额，不是「额外加鱼」 |
| `grade_pct_super` | `0.5`（≥0） | 蓝冠档相对权重 | 蓝冠绝对概率同时受 `capG` 与档位带剔除影响 |
| `fish_hunt_enabled` | `1`（0/1） | 鱼猎四阶段总开关；四张阶段表见下方 `hunt_stage_*` 16 个键 | 被 `grade_ratio_enabled` 压制；两档制下必须保持状态机合并门，否则进度丢失 |
| `hunt_stage_daily_*` | `74.7 / 24 / 1 / 0.3`（≥0） | 日常阶段四档相对权重（未达标/达标/奖杯/蓝冠）；保存按行归一到 100 | 四档全 0 不计，服务端回退该行默认；负值按 0 |
| `hunt_stage_gather_*` | `54.5 / 43 / 2 / 0.5`（≥0） | 聚鱼阶段四档相对权重 | 同上 |
| `hunt_stage_active_*` | `30 / 61.2 / 6.8 / 2`（≥0） | 发窝阶段四档相对权重 | 同上 |
| `hunt_stage_giant_*` | `10 / 64 / 20 / 6`（≥0） | 巨物发窝阶段四档相对权重 | 同上；海钓/路亚也读这四张表 |
| `two_stage_hunt_enabled` | `0`（0/1） | 两档制（底浮）：淡水只有日常/打窝两张表，海钓与路亚仍走四阶段 | 只作用于淡水底钓/浮钓分支，改它不会影响海图与路亚 |
| `two_stage_daily_below` 等 8 个 | `10/86.5/3/0.5`、`1/92/6/1`（≥0） | 两档制日常/打窝四档相对权重 | 前缀 `two_stage_daily_` / `two_stage_baited_` 各四个，漏改一半会出现「打窝档没变」 |
| `hook_spawn_match_strength` | `0`（0~2） | 钩型发鱼软匹配：小钩偏小鱼、大钩偏大鱼，蓝冠档同比缩放保持绝对概率 | `grade_ratio_enabled=1` 时被强制视为 0；明确**不读**钩子手感页四参数 |
| `fish_weight_scale` | `1` | 全地图体重整体缩放。与装备上限联动比较（`capG/scale` 对档位线） | 放大后同一竿能「够到」更高档；NaN/≤0 按不可钓处理 |
| `fish_weight_cap` | `0`（0/1） | 恢复「最弱承力件×1.35」派鱼重量上限 | 只压重量**不挡鱼种**；与 `hook_entry_cap` 同开取较小值 |
| `hook_entry_cap` | `0`（0/1） | 恢复按钩体长度估算入口鱼重上限 | 同上；两个都是旧模型调试开关，默认关 |

### 3.4 饵料、钩型与中钩确认

| 键 | 默认（范围） | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `bait_pref_scale` | `1` | companion 饵效证据层的强度（**只在饵料规则关闭时生效**）：tier3×8 / tier2×4 / 在列×2，未上榜按覆盖度打折。设 0=整层关闭 | 开着饵料规则时改它完全无效；设 0 会连带关掉 `bait_pref_weight_shift` |
| `bait_pref_weight_shift` | `1` | 不改权重，改**体重分布峰值**：tier≥2 时 mode=1+0.12×tier×shift | 只影响「上什么体型」，不影响「上什么鱼」；被 `bait_pref_scale=0` 关闭 |
| `bite_gate_layer_floor` | `0.01`（0~1） | 水层地板：钓层落在 `[LayerMin−0.15, LayerMax+0.15]` 之外时权重乘此系数；只乘权重、不剔除 | 0=完全杜绝杂口，0.01≈保留 1%；容差 0.15 是编译期常量 |
| `bite_gate_pity_rounds` | `0`（轮） | 连续空杆 N 轮后放宽分布/水层地板重掷一次 | **allow/deny 不放宽**，别指望它救「饵不在名单里」 |
| `hook_nibble_enabled` | `0`（0/1） | 嘬饵总开关：开启后首次抬竿按鱼重与钩入口容量判 `solidP/nibbleP`，可能嘬饵离场 | 关掉只是不判嘬饵，**中钩后的保持率与后台保持率倍率始终生效**；蓝冠永远免检 |
| `hook_bite_profile`（表） | `solid_scale/hold_scale=1.0`，两个 floor=0 | 钩子手感页四参数：咬口倍率、保持率倍率、小鱼咬口下限、超载保持率下限 | UI 叫「咬口倍率」但**只管中钩确认**，不影响咬钩快慢、不参与发鱼软匹配 |

**自动轮换**——按鱼种稀有度，从「爱吃饵」展开池里抽本期开口饵。
**参与资格是独立开关**（表 `fish_bait_auto_rotation`，键为 `fish_key`）；手工轮换组已删除，
自动抽取是唯一的动态 allow 来源。

| 键 | 默认（范围） | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `bait_rotation_auto_enabled` | `0`（0/1） | 总开关。关=所有鱼的自动抽取都不生效，回到「名单内全部满分」 | 逐鱼的参与开关在鱼饵偏好表格的「自动抽取」列（表 `fish_bait_auto_rotation`），**两处都满足才轮换** |
| `bait_rotation_auto_sec` | `7200`（≥60 秒） | 全局周期。所有鱼在同一时间桶切换；改周期后下一次切换按新长度重新对齐 | 太短（如 5 分钟）玩家会觉得鱼口在乱跳；一个周期内抽样恒定，不会每次判鱼重抽 |
| `bait_rotation_auto_round` | `0`（整数，**别手改**） | 人工轮次偏移，只由后台「立即重抽本期」按钮 +1。时间桶 = `floor(now/周期) + 偏移`：加 1 等于立刻换一批开口饵，而周期长度与下次自然切换时刻都不变（`nextAt` 只按自然网格算）。重启不丢，也没有把抽签结果写库 | 手改会让「第几轮」失去意义；要换一批就点按钮。注意后台表格与游戏内读的是同一个偏移，点完两边同步 |
| `bait_evidence_low_scale` | `0.10`（0~1） | **没有 allow 规则的鱼**，companion tier1~2 这一档的权重系数。0=完全不吃、0.10=很难钓到 | 有 allow 规则的鱼不再看这个值：名单外直接 0 |

- **不改判鱼主循环**：抽样结果直接替换该鱼本期的 `allow`，后面完全走原路——allow 命中=满分档，
  没抽中的直接不在 allow 内（权重 0）。auto 只是 allow 的一个动态来源。
- **抽样单位是「型号 × 形态」，不是单个 item**。客户端里同一款饵常有多条 `item_def`
  （红蚯蚓 10/14/18、Lurker 100 的 8 个颜色），按 item 抽会让名额撞进同一型号、或抽中
  14mm 而玩家手里的 10mm 不算开口。抽中一组就把该组所有规格/颜色一起写进 `allow`。
- **先分组、再挑与 `allow` 有交集的组**（不是先按 allow 命中项分组）。`allow` 里经常只点了
  一款具体饵——「甜奶油 PopUp 16」「河螯虾14」——按命中项分组就只剩那一颗，抽中后只开这个
  尺寸。现在的口径是：组内任一成员在名单里，整条产品线一起开口。
- **名称要先归一**：客户端用 NBSP 分隔规格（`甜奶油 PopUp\xa016`），Go 的 `\s` 不含 U+00A0，
  剥离规格时必须显式带上，否则 12/16/20 会被拆成三个组；规格号**只剥一层**，
  `Lurker 100-04` 剥成 `Lurker 100` 是对的，再剥就把 100/200/300/400 并成一组。
- **同名去重键要带组键**：同一个中文名可能是两种产品（「河螯虾 24」既有 Pop-up 也有颗粒），
  只按名字去重会把后一个形态整条吃掉。
- **折叠名始终带形态词**（`玉米（8/14/18/24）· 颗粒`、`甜奶油 PopUp（12/16/20）· Pop-up`）：
  「玉米」这种名字光看名字分不出是颗粒还是玉米粒。`fopbhlmkcpe=1` 是混装，`boil_*` 记
  「沉底煮饵」、其余（玉米粒/马铃薯块/面包/鱼块…）记「天然饵」。
- **形态必须进分组键**：同为「红虫」，活饵(`baitType=2`) / 沉底煮饵(`1`) / Pop-up(`8`) /
  颗粒(`9`) / 玉米(`10`) 是不同产品，钓组与用法不同，不合并；同口味的跨形态更不合并
  （客户端数据里也不存在跨 3 种形态的产品线）。折叠名一律带形态后缀，
  如「红蚯蚓（10/14/18）· Pop-up」与「红蚯蚓 · 活饵」。
- 配额取自 `fish_ecology.rarity_tier`：`0` 普通 3 组 / `1` 稀有 2 组 / `2` 超稀有 1 组。
- 抽样由 `hash(fish_key, 时间桶)` 决定：同周期所有玩家一致、重启不丢、不写库；池子按规则指纹缓存，
  规则或名单一变即失效重建（单条鱼约 1ms；饵名的归一化结果另有进程内缓存，见 §3.4 词表那段）。
- **立即重抽**：后台「鱼饵偏好」右上角的「↻ 立即重抽本期」把 `bait_rotation_auto_round` +1，
  时间桶整体后移一轮，所有开了自动抽取的鱼立刻换一批。周期与下次自然切换时刻不变，
  后台表格与游戏内用同一个偏移，不会出现「表里是新的一轮、游戏里还是旧的一轮」。
- 池子 = `allow` 展开后的实际饵（「拟饵」「蠕虫类」会展开成实际成员），再聚成型号组并排除 `deny`；
  组数不超过配额时不轮换（等于全开）。`condition` 前置条件照旧先判。
- **整类词**：`allow` / `deny` / `bottom_only` 里可以直接写类型名，代表这一类全部饵。
  后台「选择鱼饵 → 按大类加入」就是往规则里写这些词（紫色标签）。可选词：
  - 顶层：`各类饵`（全部）/ `饵料`（全部天然饵）/ `拟饵`（全部路亚）；
  - 天然饵 16 类：`沉底饵`(1) `虫类`(2) `蛙类`(3) `活饵类`(4) `蚯蚓类`(6) `昆虫类`(7)
    `浮水饵`(8 Pop-up) `颗粒`(9) `玉米浮`(10) `坚果`(11) `钩虾类`(12) `面团类`(13)
    `鱼片类`(14) `死饵鱼类`(15) `海钓饵`(16) `Zig浮水球`(17)；
  - 拟饵 20 类：`旋转亮片`(1) `莫斯科路亚`(2) `软饵类`(3) `Crank摇摆`(4) `Popper波扒`(5)
    `Pilker铁板`(6) `Jerk抽停`(7) `Tube软管`(8) `W-Stick软虫`(9) `Mouse鼠型`(10) `Fly飞蝇`(11)
    `大号软饵`(12) `Gummi软鳗`(13) `Octopus软饵`(14) `虾型`(15) `Flyingworm飞虫`(16) `鱼卵珠`(17)
    `Jig铅头`(18) `FlipJig铁板`(19) `Vibra震动`(20)（括号里是客户端 `lureType`）；
  - 历史写法继续有效：`昆虫幼虫`(2) `蠕虫`/`蠕虫类`(6) `昆虫`(7) `饵鱼`/`饵鱼类`/`各类饵鱼`(4+15)
    `鱼肉类`(14) `亮片`/`亮片类`/`亮片系列饵`(1+2) `旋转饵`/`旋转饵类`/`旋转亮片路亚`(1) `拟真软饵`(3)。
  词表定义在 `internal/gameserver/bait_type_terms.go`，后台按钮、分类标签、匹配谓词同一份；
  类型名不能含 `/`（规则文件用 `/` 分词）。
- 名单里显式点名的饵与类目词覆盖的饵一视同仁，都参与抽签——这正是「范围太广」要收的地方。
  只写一个整类词时，池子就是该类型的全部型号组（例：`颗粒` = 98 款 / 30 组），
  每期在类内抽配额，类外的饵全部落到「规则外」那一档。
- 实测规模：全库 4,123 款 → 9,426 个型号组（压缩 5.4×）；按组抽 3 组后平均开口约 10 款，
  但**有效种类仍是 3 种**——多出来的是同型号的不同规格/颜色。

### 3.5 生态、昼夜与稀有度

| 键 | 默认（范围） | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `fish_diel_weight_enabled` | `1`（0/1） | 昼夜权重层。**独立于生态总开关**，生态关着也生效 | 设为 0 只是回到全天同一权重，不会让鱼消失 |
| `fish_diel_inactive_block` | `1`（0/1） | 非活跃时段禁口。曲线值 <0.5 的段（等同 `diel_mask` 未置位、鱼种卡上的活跃时段之外）该鱼权重直接归零，整段时间钓不到它；0=回到旧口径只乘曲线系数 | 关掉后边缘段又会出现「卡片写夜行、凌晨照样连竿」；只作用于非活跃段，峰值段与季节/天气层不受影响 |
| `fish_ecology.diel_curve`（列） | 253 条资产全部有值 | 四段**乘性**曲线 `[夜,晨,昼,昏]`：峰值固定 1.0、最低钳 0.08，**只压不抬** | 空串 = 该鱼回退 `diel_mask` 的 1.0 / 0.35；曲线里想「突出」某条鱼应改权重，不要指望昼夜系数 >1 |
| `fish_ecology_loc.diel_curve`（列） | `NULL` = 继承 | 按水域覆盖曲线（空串 = 该水域显式回退位图） | 只改 `diel_mask` 而不管曲线，曲线会盖住掩码——两者要成套改 |
| `fish_weight_preset`（表） | `0→100 / 1→30 / 2→5` | 稀有度档位预设，三档。读取优先级：预设 → `loc_fish.catches` → `1`；手工覆盖（`fish_weight_override`）更外层 | 稀有度是**鱼种固有属性**（静态库 `species_rarity`：0 普通 169 / 1 稀有 45 / 2 超稀有 39），不要试图用渔获量反推；清空表=回滚攻略原值，各图尺度重新不一致 |
| `fish_ecology[]`（表） | `diel_mask=15`、`enabled=1` | 逐鱼种生态档案：水层/温度三段、昼夜与季节掩码、食性/天气/动作 JSON、活跃度、是否出鱼 | `enabled=0` 是**全局封鱼**，不受昼夜层影响；`diel_mask` 位值 1夜/2晨/4昼/8昏（与 `diel_curve` 同序） |
| `fish_ecology_loc[]`（表） | 空列继承 | 按（统计水域，鱼种）**部分覆盖**习性；空列继承全局 | 整行覆盖会让全局修缮被永久挡住；体长体重与稀有度不接受水域覆盖 |

### 3.6 窝料（`groundbait_*`，全部无后台入口）

默认关（`groundbait_enabled=0`）。开启后一份=0.6 游戏小时、单批上限 6 小时，
对口鱼候选权重最多 ×2.60，同时按份数进「死窝闸门」压开口率——**同一份数既抬权重又压概率**，
这是最容易调反的一层。

| 键 | 默认 | 作用 / 影响范围 |
|---|---|---|
| `groundbait_enabled` | `0` | 总开关；关=整层不存在（闸门与权重都不参与） |
| `groundbait_portion_hours` | `0.6` | 1 份窝料折算的游戏小时 |
| `groundbait_hours_cap` | `6.0` | 单批最多持续的游戏小时 |
| `groundbait_gain` | `1.50` | 窝料强度增益系数 |
| `groundbait_cap` | `2.60` | 对口鱼权重乘子上限 |
| `groundbait_sat` / `groundbait_sat_cap` / `groundbait_sat_floor` | `4.0` / `2.05` / `1.05` | 饱和度曲线：`S=1−exp(−份数/4)`，乘子上下钳制 |
| `groundbait_special_gain` / `groundbait_special_margin` | `0.30` / `0.08` | 专食加成与「主力食性压过次族」的判定余量 |
| `groundbait_soft_max` / `groundbait_soft_beta` | `0.60` / `0.45` | 软门（水温/低温/水色）被对口窝料软化的上限与曲线 |
| `groundbait_overdose_from` / `_step` / `_lure_exempt` | `15` / `0.15` / `1` | 死窝闸门：净份数 >15 起 `P=1−0.15×(份数−15)`，≥22 为 0；路亚族默认豁免 |
| `groundbait_range_core_m` / `_mid_m` / `_outer_m` | `4` / `7` / `10` | 按投放点与浮标落点的二维距离衰减：0~4 全额，4~7 降到 60%，7~10 降到 0 |
| `groundbait_diffuse_fall` | `0.40` | 无精确落点的旧记录保守折算系数；已不使用相邻格衰减 |
| `groundbait_pred_lure_min` / `_plant_max` / `_boilie_max` | `1.5` / `0.8` / `0.8` | 掠食鱼冻结条件：lure≥1.5 且植物/煮饵低于阈值 |
| `groundbait_hand_portions` | `1` | 手抛一发算几份 |
| `groundbait_consume_bite` | `0.20` | 每次吃口扣减的窝料份数 |
| `groundbait_prior` / `groundbait_throw_pos_fallback` | `0.35` / `1` | 无历史数据时的先验亲和、投掷落点缺失时的回退 |

### 3.7 搏鱼、体力与装备磨损

| 键 | 默认 | 作用 / 影响范围 | 误改风险 |
|---|---|---|---|
| `fight_power_scale` | `1.0` | 搏斗拉力整体倍率，越低鱼挣扎越小、越不易断线 | 只改拉力，不改鱼体力与档位 |
| `fight_curve_tension_scale` | `1` | 拉力曲线张力缩放（曲线表整体） | 与 `fight_power_scale` 同向叠加，别同时调两个 |
| `fish_pull_ln_slope` | `0.35` | `f8=k×体重kg` 的种内 `ln(w_kg)` 斜率，调大则大鱼相对更沉。官方样本 0.3~0.4 | 只改种内相对关系，不改鱼种基值 |
| `fish_pull_fallback_k` | `17.5` | 不在拉力 K 表/习性组表的鱼种拉力基值（N/kg） | 只对未测种生效；实测种有自己的量化表 |
| `fish_stamina_scale` | `1.0` | 鱼体力整体缩放 | 影响「耗多久乏」，不改拉力 |
| `fish_initial_stamina_ratio` | `0.5628`（0.05~1） | 未实测鱼种的 f6 抗疲劳系数；实测种改用逐鱼种 f6 表，**本参数对它们无效**；未实测海水种固定 0.50 | 0=回退默认；调到 1 会让未测种几乎不疲劳 |
| `fish_f6_scale` | `1` | f6 系数整体缩放（仅非 1 时生效） | 与上一个参数叠加，容易双倍放大 |
| `fish_fight_group_fallback` | `1` | 无习性组时回退到默认习性组 | 关掉会让缺组鱼种走另一套体力曲线 |
| `fish_stamina_energy_penalty` | `1.0`（clamp 0~4） | 鱼乏力后对玩家精力的惩罚系数 | 超过 1 会让搏鱼精力掉得更快 |
| `electric_locked_multiplier` | `3` | 电轮锁轮有效承力 = min(机械, 电机) × 该倍率 | 影响断线与过载判定 |
| `electric_motor_kg_per_power` | `1` | 电机功率折算承力公斤的系数 | 实机校准用 |
| `reel_slip_wear` | `0.0001`（1e-12~1） | 泄力打滑基准磨损率（不分档位），÷ 校准秒数 ≈ 0.00024%/s | 低于 1e-12 会被 float64 精度吃掉，参数静默失效 |
| `reel_slip_calibration_seconds` | `41.7`（0.01~3600） | 把基准换算成每秒磨损率的除数，越大磨损越少 | 与上一个参数是**除法关系**，改一个必须复核另一个 |
| `reel_slip_deadband_ratio` | `1.05`（1~10） | 拉力 ≤ 泄力×该倍率不算打滑；1=只要超泄力就磨 | 调 1 会让官服实测的「1.04× 零磨损」失效 |
| `wear_stress_oil_per_s` | `0.0004`（0~0.01） | 轮油受力磨损率（%/受力秒） | 0=关该通道 |
| `wear_stress_gear_per_s` | `0.0004`（0~0.01） | 主齿轮受力磨损率 | 同上 |
| `wear_stress_bearing_per_s` | `0.00012`（0~0.01） | 轴承受力磨损率 | 同上 |
| `wear_stress_spool_per_s` | `0.00012`（0~0.01） | 线杯受力磨损率 | 同上 |
| `wear_stress_guides_per_s` | `0.0004`（0~0.01） | 导环受力磨损率 | 同上 |
| `wear_stress_rodblank_per_s` | `0.00003`（0~0.01） | 竿坯受力磨损率 | 同上 |
| `wear_leader_per_stress_sec` | `0.00005`（0~0.01） | 前导受力磨损率 | 同上 |
| `reel_load_tick_seconds` | `1.9`（0.01~600） | 首条 14/8 负载上报没有前序时刻时按此秒数起算 | 太小会让单次上报折算极短，磨损几乎不涨 |
| `reel_load_max_tick_seconds` | `5`（0.01~600） | 两条 14/8 之间最多折算的秒数 | 防止迟到上报一次烧完 |
| `line_break_loss_m` | `0`（>0 生效） | 断线固定扣线米数；≤0 走清杯/按上报口径 | 与断件上报口径冲突时以客户端 17/9 为准 |

### 3.8 玩家状态（精力 / 饱食 / 健康）

| 键 | 默认 | 作用 / 影响范围 |
|---|---|---|
| `player_condition_enabled` | `1` | 状态条总开关 |
| `condition_energy_max` | `1.0` | 精力上限（归一值） |
| `condition_energy_regen_per_hour` | `100` | 每小时精力恢复量 |
| `condition_energy_regen_cap` | `0.85` | 自然恢复上限比例 |
| `condition_energy_regen_fishing_mult` | `0.0` | 垂钓中恢复倍率；配合 `condition_fishing_pause_enabled` 决定「竿在水里是否停摆」 |
| `condition_fishing_pause_enabled` | `1`（0/1） | 1=竿在水里时精力暂停恢复，收竿后快速回满；0=照常恢复 |
| `condition_energy_regen_food_cost` | `0.5` | 恢复折算的饱食消耗系数 |
| `condition_hunger_decay_per_hour` | `0.24` | 每小时饱食下降 |
| `condition_hunger_regen_floor` | `0.25` | 低于该饱食度停止恢复 |
| `condition_cast_energy_cost` | `0.01` | 每次抛竿精力消耗 |
| `condition_pull_energy_cost` | `0.005` | 每次抬竿/收线精力消耗 |
| `condition_food_hunger_restore` | `0.25` | 食物恢复饱食 |
| `condition_coffee_energy_restore` | `0.15` | 咖啡恢复精力 |
| `condition_cocoa_hunger_restore` / `_energy_restore` | `0.35` / `0.05` | 可可恢复饱食 / 精力 |
| `condition_tea_comfort_restore` | `0.30` | 茶恢复舒适度 |
| `condition_comfort_rain_penalty` / `_cold_penalty` / `_cold_temp_c` | `0.35` / `0.35` / `5` | 雨/低温降舒适度；低于该气温算「冷」 |
| `condition_comfort_campfire_boost` / `_sun_boost` | `0.5` / `0.1` | 篝火/日照抬舒适度 |
| `condition_comfort_drift_per_hour` | `0.5` | 舒适度每小时自然回落 |
| `condition_sun_energy_regen_mult` | `1.25` | 晴天精力恢复倍率 |
| `condition_gear_energy_regen_cap` / `_max_cap` | `0.5` / `0.15` | 装备提供的精力恢复上限 / 上限加成 |
| `condition_max_backfill_hours` | `12` | 离线补算最多回填小时数 |
| `condition_health_enabled` | `1`（0/1） | 健康动态（饥寒掉血）总开关；0=不掉血且药品不扣数量 |
| `condition_health_decay_per_hour` / `_regen_per_hour` | `0.10` / `0.05` | 掉血 / 自愈速率 |
| `condition_health_hunger_threshold` / `_comfort_threshold` | `0.10` / `0.30` | 低于阈值才开始掉血 |
| `condition_medicine_health_restore` | `0.50` | 药品恢复量 |
| `bait_dig_energy_cost` | `0.15` | 挖饵精力消耗 |
| `campfire_duration_game_min` | `60`（游戏分钟） | 篝火燃烧时长 |

### 3.9 掉落、活动与经济

| 键 | 默认 | 作用 / 影响范围 |
|---|---|---|
| `loot_box_enabled` | `0` | 钓获礼包总开关 |
| `loot_box_chance` | `-1`（<0 回退礼包定义里的 `chance`；定义未配则不开） | 礼包触发概率 0~1 |
| `loot_box_trophy_guarantee` | `1` | 奖杯/蓝冠必定开一包 |
| `card_drop_normal` / `card_drop_star` / `card_drop_blue` | `0.001` / `0.5` / `0.8` | 普通·达标 / 星鱼 / 蓝冠 掉卡率 |
| `card_drop_rare_scale` | `0.25` | 稀有、超稀有鱼统一乘此系数 |
| `xp_gain` | `1` | 结算经验整体缩放 |
| `release_xp_ratio` | `0.5` | 放生拿到入护经验的几成 |
| `level_gifts_enabled` | `1` | 升级礼物卡片 |
| `cafe_order_time_scale` | `1` | 咖啡厅订单时间缩放 |
| `event_newyear_enabled` / `event_anniversary_enabled` | `1` / `0` | 限时活动开关，**两者互斥**，开启任一个自动关闭另一个 |
| `event_branches` | `9,10,11,12,13,14,15,16,17,18,19,20` | 下发的活动分支 ID（9~20）；留空=不下发任何分支 |
| `event_anniversary_end_unix` | `1825632000` | 新年与周年面板共用的倒计时终点（UTC） |
| `event_stage_gifts` | 空 | 活动阶段奖励 JSON，保存时校验物品目录可发放 |

### 3.10 文案与诊断（不参与判鱼数值）

| 键 | 默认 | 作用 |
|---|---|---|
| `fish_escape_taunt` | `1` | 1=跑鱼随机嘲讽文案；0=官方「鱼脱钩了」 |
| `fish_hooked_chat` | `0` | 上钩播报（仅本人可见，含竿号/鱼种/克重） |
| `fish_roll_trace_enabled` | `1`（0/1） | 判鱼追踪：每杆真实咬钩落一份决策快照，供后台排错；0=零开销 |
| `fish_roll_trace_keep` | `300`（0~5000） | 追踪表环形保留条数；0=不落库 |
| `fish_trophy_probe_all` / `fish_trophy_probe_place` | `0` / 空 | 奖杯面板逐格探针，只对指定水域生效 |
| `bait_share_min_count` | `5` | **仅后台展示**：官服占比的每饵最小样本量，低于此值不计入。rf4 占比已不参与判鱼 |
| `fish_model_variant` | `1` | 鱼模型族总开关；0=每鱼种只用默认族 |
| `fish_variant_parr_max_g` / `_smolt_max_g` / `_juvenile_max_m` | `1500` / `4000` / `0.35` | 幼鱼/降海幼鱼族的体重与体长上限，需同时满足 |
| `tc_laminate_gift_source` | 见代码 | 塑封卡册礼物来源码，改库 30 秒生效 |

### 3.11 调试/实验开关（无后台入口，**不要当调参手段**）

这些键在代码里可读、`tunables` 里可写，但都是排障或 A/B 用的，默认值即「正常行为」。
误改的典型症状是「客户端表现异常但服务端日志正常」：

`fight_legacy_model`、`fight_official_strength`、`fight_curves_fixture`、`fight_phase_scales`、
`fight_hook_full_sync`、`fight_load_probe`、`settle_scale_debug`、`sonar_echo`、
`marine_hook_confirm`、`marine_hook_item_sync`、`marine_full_sync`、`float_f19_force_one`、
`fish_tv_kind` / `fish_tv_channel` / `fish_tv_kv`、`multi_hook_bite` 及其 8 个子参数、
`handitem_throw_*` 10 个、`handitem_load_one`、`handitem_switch_refresh`。

其中三条值得单独记住：

- `marine_hook_confirm=1` 会在中钩后推 14/15「鱼离场」，**刚上钩的鱼会被直接放走**，默认必须为 0。
- `fish_tv_*` 控制 14/14 载荷的调试字段，改了客户端可能解析异常。
- `multi_hook_*` 是多钩实验通道，`multi_hook_bite=0` 时其余子参数全部无效。

---

## 4. 配置表字典（管理后台可直接编辑）

| 表 | 主键 | 谁覆盖谁 | 关键列与含义 |
|---|---|---|---|
| `fish_bite_setting` | `loc` | **整组替换**全局五项咬钩参数 | `bite_probability` / `bite_delay_fixed` / `bite_delay_scale` / `bite_delay_max` / `bite_delay_lure_max`。有行即覆盖，删行=恢复全局 |
| `fish_weight_override` | `(loc, fish_key)` | 替换档位预设与攻略原值，优先级最高 | `weight`（≤0 或 `enabled=0` = 硬过滤）/ `enabled`。⚠ 全部为 0 且三个规则开关全关时整组覆盖被静默忽略 |
| `fish_weight_preset` | `rarity_tier` | 被 `fish_weight_override` 覆盖 | 三档权重 100/30/5；清空=回滚攻略原值 |
| `fish_ecology` | `fish_key` | 全局习性基线 | `layer_min/pref/max`、`temp_min/pref/max`、`diel_mask`、`diel_curve`、`season_mask`、`diet_json`、`weather_json`、`action_json`、`aggression`、`enabled`、`rarity_tier`（由静态库同步，勿手改） |
| `fish_ecology_loc` | `(stat_loc, fish_key)` | **部分覆盖**：空列继承全局 | 同上但仅习性字段；`enabled=0` = 该水域不出这种鱼 |
| `fish_zone` / `fish_zone_fish` | `id` / `(zone_id, fish_key)` | 名册与分布乘法复合 | `kind`（grid_rect/sea_zone）、`mode`（exclusive/boost）、`boost` 默认 3.0、`keep_others` 默认 0.35。多区重叠：独占取交集、增益连乘 |
| `fish_bait_override` | `fish_zh` | **整行替换**内置 252 条规则；只给确有增量的鱼写覆盖，其余继续跟随内置表 | `allow_json`（吃，命中给满分档）/ `deny_json` / `bottom_json`（底钓只吃）/ `condition`（前置条件，能填什么见 4.1）/ `enabled`。**写覆盖必须把内置的 deny/bottom/condition 原样带过去**，否则禁饵、底钓只吃与海图/jig 限制整条失效；批量补齐用 `tools/merge_bait_allowlist.py`。手工轮换三列（`groups_json`/`rotate_sec`/`rotate_mode`）已随迁移 00080 删除 |
| `fish_bait_auto_rotation` | `fish_key` | 逐鱼自动轮换开关，**只存开启项**（关掉即删行）。与上面那张覆盖表完全解耦：给没有覆盖的鱼开开关不会复制内置规则 | 只有 `fish_key`+`enabled`+审计字段；周期/系数/抽签结果都不落库（`hash(fish_key,时间桶)` 实时算）。后台入口是鱼饵偏好表格的「自动抽取」列 |
| `hook_bite_profile` | `item_id` | 四列调参，物理列只读 | `solid_scale_bp` / `hold_scale_bp`（基点，10000=1.0）/ `small_fish_floor_bp` / `over_capacity_hold_floor_bp` |
| `fish_grade_lines` | `fish_key` | 档位线来源 | `qualified_g < trophy_g < blue_trophy_g`（CHECK 约束）；改它等于改「什么算星鱼/蓝冠」 |
| `fish_roll_trace` | 自增 | 纯诊断，不参与判鱼 | 逐层权重快照：roster→gate→zone→bait→dist→ecology→spot→groundbait→bait_tier→final |

### 4.1 前置条件（`condition`）能填什么

后台「编辑鱼饵规则」里前置条件是**下拉**，选项与内置规则表 `fishing_bait_rules.txt` 里的原文一一对应
（服务端 `deriveConditionFlags` 是按关键词子串解析的，**改措辞＝条件直接失效**，所以不要在框里手改字）。
全库 252 条规则里出现过的写法只有 8 类（含「无」），外加「自定义」：

| 后台下拉 | 落库文本 | 服务端是否真的拦鱼 |
|---|---|---|
| 不限制（留空） | 空串 | 不校验 |
| 海图 · 指定区号 | `海图30或34号区域`（多区号用「或」连接） | **生效**。落点区号不在列表内→不判该鱼；客户端没上报「在海图上」→不判；完全拿不到落点上下文→放行 |
| 海图（区号留空） | `海图，区域不限` | **生效**，只要求「在海图上」，不限区号 |
| 拟饵 · jig 阶梯 | `使用拟饵时须出现“jig阶梯”` | **生效**，仅拟饵。没有 jig 动作位→不判；本次没有任何钓组动作上报（掩码为 0）→放行，避免误杀 |
| 拟饵 · 路亚+船票+上船+运动 | `使用拟饵时须采用路亚钓法，使用有效船票并已上船，饵在水中运动` | **生效**，仅拟饵；「已上船」这一条在知道船状态时先判 |
| 路亚 · 船票+上船+运动 | `路亚钓法，使用有效船票并已上船，饵在水中运动` | **生效**，同上但不带「使用拟饵时」前缀 |
| 拟饵 · jig 或路亚上船运动 | `使用拟饵时须出现“jig阶梯”，或采用路亚钓法且使用有效船票并已上船，饵在水中运动` | **生效**，**二选一**：有 jig 位即通过，否则要同时满足上船+在水中运动 |
| 浮漂 · 立漂 / 顺水漂流 | `浮漂钓法；手竿或博洛尼亚竿须浮漂立起，其他浮漂竿须出现“顺水漂流”` | **半生效**：只解析出「顺水漂流」，且只在拟饵时校验 |
| 仅手竿 / 博洛尼亚竿 + 立漂 | `仅手竿或博洛尼亚竿，浮漂立起` | **不生效**（纯备注）。「手竿/博洛尼亚竿」「浮漂立起」服务端没有对应上报 |
| 自定义… | 任意文本 | 按关键词解析，见下 |

自定义文本的解析规则（`deriveConditionFlags`）：含「海图」→ 抽取全部数字当区号（无数字=区域不限）；
含「顺水漂流」→ 要求漂流位；含「jig阶梯」→ 要求 jig 位；含「饵在水中运动」→ 要求水中运动位；
含「已上船」→ 要求船；**「jig阶梯」与「饵在水中运动」同时出现且含「或」**→ 合并成二选一，不再各判一项。
天然饵（非拟饵）不套用动作与船条件。

---

## 5. `config.yaml` 部署参数（改完要重启）

| 键 | 默认 | 说明 |
|---|---|---|
| `env` | `dev` | `dev` / `prod` |
| `launcher.hosts_switch` | `false` | 自动把 `api.rf4game.ru/com` 指向 127.0.0.1，需管理员权限 |
| `gateway.login_addr` / `login_compat_addr` / `realtime_addr` | `:443` / `127.0.0.1:7515` / `:9341` | 登录（HTTPS）/ 旧版明文兼容口 / 游戏 realtime |
| `gateway.advertise_host` / `advertise_port` | `127.0.0.1` / `9341` | 登录应答里返回给客户端的 realtime 地址 |
| `gateway.max_conns` | `512` | realtime 最大并发连接 |
| `gateway.ack_client_frames` | `true` | 处理客户端帧前先 ACK |
| `gateway.transport_keepalive_sec` | `5.0` | 空传输帧保活间隔 |
| `gateway.token_ttl_days` | `365` | 登录 token 有效期 |
| `gateway.userid_start` | `1000001` | 首个 user ID |
| `tunnel.addr` / `tunnel.psk` | `127.0.0.1:9401` / 占位 | gateway 与 gameserver 内部隧道，两端必须一致 |
| `sqlite.path` / `static_path` | `data/rf4server.db` / `server_data/static.sqlite3` | 主库 / 只读静态库（TEMP VIEW，不可写） |
| `admin.http_addr` / `jwt_secret` / `username` / `password` | `:8080` / 占位 | 仅 `cmd/admin` 独立调试用；launcher 不监听此端口 |

支持环境变量覆盖，完整清单见 `config.example.yaml` 注释。

> 钓鱼手感参数（`bite_probability` 起竿必出鱼概率 / `bite_delay_scale` 延迟总倍率 /
> `bite_delay_fixed` 固定延迟秒）**不在配置层**：`config.yaml` 里写这几项不会生效，
> 它们只存在于 `tunables` 表，请用管理后台「游戏参数」修改。
> 世界时钟推送间隔在配置层的键是 `game.keepalive_interval_sec`（`gateway` 段下没有这一项）。

---

## 6. 覆盖 / 压制 / 静默失效（改参数前必读）

| 关系 | 主导方 → 被作用方 | 性质 |
|---|---|---|
| 档位比例开 | `grade_ratio_enabled=1` → 鱼猎四阶段权重、奖杯保底/回访、`hook_spawn_match_strength`、TrophyLuck | **压制**（整组不参与） |
| 水域覆盖 | `fish_bite_setting` 有行 → 全局五项咬钩参数 | **整组替换** |
| 饵料覆盖 | `fish_bait_override.enabled=1` → 内置 252 条规则 | **整行替换** |
| 鱼种覆盖 | `fish_weight_override` → 档位预设 / 攻略原值 | 单鱼替换，禁用=硬过滤 |
| 固定延迟 | `bite_delay_fixed>0` → 天气、技能、倍率、上限 | **短路**（钩型因子仍在最外层） |
| 概率封顶 | 饵料规则 ∨ 天气鱼口 → `bite_probability` | 条件性 cap 0.95 |
| 饵效层 | `bait_pref_scale=0` → 饵效证据 + `bait_pref_weight_shift` | 整层关闭 |
| 饵料证据分档 | 有 allow：命中 → 满分，名单外 → 0；无 allow：companion tier3 → 满分、tier1~2 → 低权重 | 严格 allow，非逐档降权 |
| 官服占比 / 本图渔获 | `rf4records_catch` / `loc_bait_fish` → 判鱼与节奏打分 | **不参与**（占比仅后台展示） |
| 自动轮换 | `bait_rotation_auto_enabled=1` 且 `fish_bait_auto_rotation` 有该鱼 → 本期 `allow` 换成抽中的 3/2/1 个型号（整组规格放行） | 未抽中的饵直接不在 allow 内（权重 0）；手工轮换组已删除 |
| 生态层 | `fish_ecology_enabled=0` → 约 14 个因子 | 整层中性，**不影响昼夜层** |
| 鱼种覆盖静默失效 | `fish_weight_override` 全 0 且三规则开关全关 → 整组覆盖 | **静默回退静态名册** |
| 多钩实验 | `multi_hook_bite=0` → 其余 `multi_hook_*` | 子参数全部无效 |

**三条最容易踩的**：

1. 水域保存过一次咬钩参数后，游戏参数页对那张图就**永远不生效**，界面上没有任何标记。
2. `grade_ratio_enabled` 默认是开的（1），所以「调鱼猎阶段」「调钩型软匹配」在默认配置下都没反应，
   要先把发鱼模式切成鱼猎/两档制。
3. 稀有度不是调参项：它由静态库 `species_rarity` 在启动时投影进 `fish_ecology.rarity_tier`，
   想改「这张图里该多常见」请用 `fish_weight_override` 的覆盖权重，不要动 `rarity_tier`。

---

## 7. 调参 SOP

1. **定位阶段**：先在第 2 节管线图里指出「我要改的是哪一步」。改「出多大」的问题不要动 3.2 的开关。
2. **查压制**：在第 6 节表里确认没有更高优先级的开关把它压住（尤其 `grade_ratio_enabled`、
   `fish_bite_setting`、`bite_delay_fixed`）。
3. **改一个**：一次只改一个参数，记下改前值；默认值就是「官方/基线手感」，不要同时调两个同向参数
   （如 `fight_power_scale` + `fight_curve_tension_scale`、`reel_slip_wear` + `reel_slip_calibration_seconds`）。
4. **等 30 秒再判断**：`tunables` 有 30 秒缓存，后台保存会推刷新但跨进程仍受 TTL 约束。
5. **要证明有效**：用「判鱼追踪」（`fish_roll_trace_enabled`）看逐层权重，确认改的那一层数字真的变了；
   只看结果容易把「被上层压制」误判成「参数没用」。

---

## 附录 A：这份字典是怎么来的（原始仓库的说明分布）

原始仓库**没有**单一一篇参数字典，说明分散在四处，且各自只覆盖一段：

| 来源 | 覆盖范围 | 局限 |
|---|---|---|
| `internal/admin/web/index.html` 的 `TUNABLE_DEFS` | 约 47 个键的中文标签与一句话提示，含默认值 | 只覆盖有后台输入框的参数；大量 `groundbait_*`、`condition_*`、调试键不在内 |
| `specs/capabilities/admin-and-tunables.md` | 热更通道的**使用规则**（30 秒缓存、覆盖优先、恢复=删除） | 不讲单个参数含义 |
| `docs/后台配置对发鱼流程影响的调查.md` | 判鱼管线 A~E 阶段逐参数影响与覆盖关系 | 快照于 2026-11-10，饵料口径已两次变更，文中公式部分过时 |
| `docs/gameplay.md` | 机制口径（距、稀有度、窝料、消耗、狩猎） | 按机制组织，不按参数组织 |
| `internal/admin/api.go` | 写入校验区间（开关只能 0/1、昼夜 0.05~3、追踪 0~5000 等） | 只有区间，没有说话 |

**本表的口径优先级**：代码读取点与默认值 > `api.go` 校验 > `gameplay.md` > 调查快照。
两者冲突时以代码为准，并回头修正本表。

## 附录 B：维护约定

- 新增 `tunables` 键：必须同时补本表对应分组，并在后台加标签/提示（`TUNABLE_DEFS`）。
- 删除或改名：同一次改动里删本表行；不要留「历史参数」注释。
- 改了默认值：本表与 `TUNABLE_DEFS` 的默认值一起改，否则「恢复默认」会把玩家带到旧值。
- 参数影响范围跨阶段（例如既改延迟又改概率）时，在两个分组都登记并互相指向，不要只写一处。
