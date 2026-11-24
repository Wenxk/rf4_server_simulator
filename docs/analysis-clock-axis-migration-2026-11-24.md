# 全量时间戳迁移影响清单（2026-11-24 调研，未改动任何代码）

> 触发：用户报「游戏内天数还是 99182，没跟后台第 3 天同步」。
> 目标：把**客户端**看到的日号从协议轴（自 2015-08-01 起算的 99182）换成玩家轴（开服第 N 天）。
> 本文只做影响面普查与风险评估。§5 结论是**不建议迁移客户端时间轴**；
> **最终决定（2026-11-24）= 放弃客户端轴，改为后台跟客户端**：
> `DayAndHour` 锚回 `ProtocolEpochUnix`、去掉 `+1`、`weatherPlanDayOffset` 归 0，
> 后台面板与天气预览从此与客户端显示同一个数字（99184）。见 §9。

---

## 1. 两条时间轴的本质区别

| | 协议轴 `ProtocolEpochUnix` | 玩家轴 `DayEpochUnix`（已退役） |
|---|---|---|
| 值 | `1438387200`（2015-08-01 00:00 UTC） | `1795438800`（2026-11-23 13:00 UTC） |
| 客户端是否可见 | **是**（全部时间显示都挂它） | **否**（纯服务端展示用） |
| 当前日号 | 99184 | 不再使用 |
| 谁在用 | 307 `f13.f0` = W、篝火、船票、退货窗口、钓鱼游锚点、天气查表、鱼群/饵料轮换、**后台展示日号** | 无（仅测试当相位锚点） |

**客户端「游戏中 N 天」= `floor(W / 1440)`，而 `W = (now − 1438387200) × 24 / 60`。**
化简即 `floor((now − 1438387200) / 3600)`，**从 0 起算，没有 +1**。
所以这个数字是**绝对时间轴的真实年龄**，服务端没有可单独覆盖的天数槽位。
证据：`internal/gameserver/keepalive.go:13`（「客户端把它 ×24 展开成游戏时间，**世界日号也据它推算**」）、
`internal/gameserver/session.go:214`（「客户端根据 2/2 推算世界日号」）。

> **口径修正（2026-11-24）**：初版本文写的是 `floor(W/1440)+1`（多算了 1 天）。
> 实证来自 `reanchorWeather` 下发的预报 `f0` 键集与 `weatherPlanDay` 的对齐测试：
> `weatherPlanDay(now) = 99184` 而 `DayAndHour(now)` 若带 `+1` 就是 99185，
> 7 天预览窗口立刻越界（`TestWeatherPreviewMatchesClientForecast` 报
> 「面板第 99191 天在客户端预报里不存在」）。去掉 `+1` 后两边严丝合缝。

---

## 2. 代码层：38 处引用协议轴

`grep -rn "worldTotalGameMin\|worldProtocolEpochUnix" --include=*.go | grep -v _test` 共 38 处，按语义分四类：

### 2.1 会**直接决定客户端显示**的（改轴必须同步改，共 12 处）

| 位置 | 作用 | 改轴的后果 |
|---|---|---|
| `profilebuild.go:292`（`buildStats322W` → 307.f13.f0） | 下发 W 本身 | 必须改，否则前面白改 |
| `profilebuild.go:220-233`（f23 钓鱼游 `start = wNow + 3`） | 钓鱼游剩余 | **自动跟随**，自洽 ✓ |
| `campfire.go:81`（`clientNowMinute`） | 篝火到期分钟 | 必须同步，否则篝火永不灭/立即灭 |
| `boat.go:287`（船票锚点 `oieepfcgjgn`） | 租期起点 | 必须同步 |
| `shop_return.go:255/257/352/354`（退货凭据 + 48h 判定） | 退货运维 | **必须迁移存量**，见 §4 |
| `spawn.go:348`、`world.go:332/435/649`、`econ.go:125/639`、`handlers_game.go:34/401`、`misc_fix.go:266`、`progress_ops.go:414` | 各处传 `worldTotalGameMin` 给 `BuildProfile307W` | 改 `worldTotalGameMin` 一处即可全跟随 |
| `player_condition.go:641`（`gameDayNumberAt`） | 免费餐冷却日号 | **必须迁移 kv**，见 §4 |

### 2.2 只在服务端内部做周期取模的（4 处，改轴无副作用）

`fish_distribution.go:28`、`fishing_bait_rules.go:560`（每 4 游戏小时重分布）、
`fish_query.go:240/401`（小时相位）、`fishing_weather_context.go:128`（天气查表 `at`）、
`weather_schedule.go:151`（`weatherPlanDay`）。

这些是「对周期取模」，平移整轴只会让快照换个相位，**不会破坏对齐**。但
`weather_schedule.go:151` 与客户端的 `f0` 预报必须**同时**平移，否则面板与游戏算出两段天气
（现有守卫 `TestWeatherPreviewMatchesClientForecast` 会立刻抓到）。

### 2.3 FILETIME 墙钟（27 处，**绝对不能动**）

`WindowsFiletimeNow()` / `UnixToFiletime()` 输出的是**真实 UTC 墙钟**，客户端直接
`DateTime.FromFileTimeUtc` 读取并显示为绝对日期。涉及：
会员到期、邮件日期、咖啡厅订单到期、赛季起止（`seasonBounds`）、物品创建/更新时间、
成就时间、制卡封印时间、乐时光结束墙钟 `f2`、玩家姿态 `308.f7`。

**一旦把这些也平移 99181 小时，全服时间会倒回 2015-08-01**：
会员显示「11 年前到期」、邮件显示 2015 年、赛季失效、物品时间倒流。

### 2.4 混合语义的坑（必须逐个确认）

- `profilebuild.go:105-107` 注释：欢乐时光客户端算 `f1 − (f13.f0 + 真实分钟)`。
  **f1 是游戏分钟轴，f2 是 FILETIME 墙钟轴**——同一个对象里两种轴，改一个不改另一个会算出
  巨大负倒计时。
- `cafe_orders.go:99` 用的是 `ExpiresAt+11644473600)*10000000`（墙钟秒 → FILETIME），
  与 `trip.go` 的游戏分钟无关，**不受影响**。

---

## 3. 数据层：库内时间戳口径普查

逐列按量级判定（`1e9` ≈ 墙钟秒、`1e17` ≈ FILETIME、`9万` ≈ 协议日号）：

| 表 | 列 | 口径 | 影响 |
|---|---|---|---|
| `profile` / `item` / `mail` / `rig` / `catch_seen` / `player_qualifications` | `*_ts` | 墙钟秒 | 无 |
| `cafe_order` | `batch_start` / `expires_at` | 墙钟秒 | 无 |
| `fish_hunt_state` / `fishing_state` / `groundbait_clock` | `*_ts` | 墙钟秒 / 纳秒 | 无 |
| `campfire_state` | `expires_ft` | FILETIME | **表为空（0 行）**，无需迁移 |
| `trip_state` | `*_ts` | — | **表为空（0 行）** |
| **`item.fields_json`** | 内嵌 `pcfflcdpjgk.f2` | **游戏分钟** | **1170 件，必须迁移** |
| **`kv`** | `condition_free_meal:<uid>` | **协议日号** | **1 行，必须迁移** |

**好消息：除这两项，库内所有时间戳都是真实墙钟秒，与游戏轴无关。**

---

## 4. 存量数据迁移：两个陷阱，第二个是决定性的

### 4.1 退货凭据（1170 件）

`item.fields_json` 里 `pcfflcdpjgk` 的 `f2 = BoughtMinute`，实测分布
`142302717 ~ 142791996`，当前 `W ≈ 142825528`，窗口 `69120` 游戏分钟（= 48 游戏日 = 48 现实小时）。

| 场景 | 窗口内件数 | 说明 |
|---|---|---|
| 现状（不迁移） | 4 / 1170 | 正常，1166 件早已过期 |
| 只改 W 不减凭据 | **1170 / 1170 全部复活** | `cur − bought` 变成巨大负数 → 永不过期 |
| 减 offset 后 | 4 / 1170 | 语义与现状一致 ✓ |

### 4.2 **致命陷阱：减完 offset 后全部为负**

`offset = 99181 游戏日 × 1440 = 142820640` 分钟。
凭据最小值 `142302717 − 142820640 = −517923`（负数）。

原因是 `DayEpochUnix`（2026-11-23 21:00）**晚于**这批数据的产生时间
（`profile` / `item` 最早创建于 **2026-11-08 12:17 UTC**，早 15 天 ≈ 360 游戏日）。

后果：若按 `proto.U32(uint32(负值))` 下发，会回绕成 42 亿量级，
客户端 `cur − bought` 变成巨大负数 → **1170 件反而全部可退**，比不迁移更糟。

`shop_return.go:354` 有 `boughtMinute <= 0` 的保护，所以服务端判定不会崩；
但**下发到客户端的 u32 会被回绕**，客户端退货列表会整屏刷出陈年旧货。

### 4.3 免费餐冷却（1 行）

`kv.condition_free_meal:1000001 = '99110'`（协议日号）。
`player_condition.go` 用 `gameDayNumberAt` 生成今日日号做等值比较。
改轴后新日号变成个位/十位量级，与 `99110` **永不相等** → 冷却失效（可无限吃免费餐）。
迁移：`99110 − 99181 = -71`，同样是负数 —— 与 §4.2 同源问题。

---

## 5. 结论：不建议做，三条硬理由

### 理由 1：**历史数据早于 DayEpoch，玩家轴上必然是负数**

`DayEpochUnix` 建立在 2026-11-23 21:00，而最早的数据是 2026-11-08。
这 15 天（≈360 游戏日）的存量在玩家轴上**无法表示**。
把 `DayEpochUnix` 往前挪到 11-08 可以消掉负数，但：
`1794140220 − 1438387200 = 355753020`，`% 3600 = 1020` **不整除**
→ 昼夜相位整体偏移 6.8 游戏小时，违反 `TestDayEpochOffsetIsWholeGameDays`，
所有地图时刻全错。

### 理由 2：**客户端时间是绝对轴，不是「游玩天数」**

客户端这个数字算的是「世界活了多久」，不是「你玩了多久」。
要让它是「第 N 天」，只能伪造整个世界的出生时间——也就是把全服提前 11.3 年。
这与「重置日号」是两件不同的事，前者会污染所有绝对日期显示。

### 理由 3：**风险集中在不可逆的存量改写**

1170 件物品的 `fields_json` 是 JSON 内嵌字段，改写需要逐行解析-修改-回写，
且必须处理 §4.2 的负数回绕。任何漏项都会造成：退货窗口全开、篝火烧不到期、
船票永久有效、免费餐无冷却。这些都是**经济与计时类事故**，且发生时无报错。

---

## 6. 如果非做不可：最小可行路径（供决策参考，未实施）

若用户坚持，必须先满足以下前置条件，缺一不可：

1. **先把 `DayEpochUnix` 挪到 ≤ 最早数据时间**，且保持 `% 3600 == 0`
   （可用 `1438387200 + 整 99181 日 − k*3600` 找最近的合法点，但会牺牲「第 1 天」的语义）。
2. 写迁移脚本，逐项处理：
   - `item.fields_json` 1170 件的 `pcfflcdpjgk.f2`（含负值 clamp 到 0 的策略）
   - `kv.condition_free_meal:*` 的日号
3. **所有 §2.1 的代码点同步改**，且 `f13.f0` / `f23.f4` / `f23.f2` / 篝火 / 船票 / 退货
   六个坐标必须同一次提交改完。
4. 重跑守卫：`TestDayEpochOffsetIsWholeGameDays`、`TestStoreReturnEnforces48HourWindow`、
   `TestWeatherPreviewMatchesClientForecast`、`TestTripSessionClockCalibration`。
5. 停服迁移 + 备份 `data/rf4server.db`。

**预估工作量：代码 12 处 + 迁移脚本 2 项 + 存量 1171 行 + 停服窗口。**
收益仅是「客户端数字从 99182 变成 4」，**不建议**。

---

## 7. 备选：只改客户端文案的可行性

客户端「游戏中 N 天」是本地化模板 `{0} 天` + 数值，数值由客户端自算，服务端不参与。
**没有服务端可控的替代显示位**。

唯一能做的折中是：在后台/文档里明确写清「客户端显示的是世界年龄，不是开服天数」，
避免后续再误判为 bug。

---

## 8. 附：本轮核查用到的命令

```bash
# 协议轴引用点
grep -rn "worldTotalGameMin\|worldProtocolEpochUnix" --include=*.go . | grep -v _test

# FILETIME 下发点（绝对不可动）
grep -rn "WindowsFiletimeNow()\|UnixToFiletime(" --include=*.go . | grep -v _test

# 退货凭据存量
python -c "import sqlite3;db=sqlite3.connect('data/rf4server.db');c=db.cursor();\
c.execute(\"select count(*) from item where fields_json like '%pcfflcdpjgk%'\");print(c.fetchone())"
```

---

## 9. 最终落地：后台跟客户端，而不是客户端跟后台（2026-11-24 已实施）

用户拍板「都用 99182 这个时间吧，包括后台天气预览那里的，也还原到你说的世界时钟」。
于是把方向反过来：**不再动客户端，改后台**。改动小得多，且无存量数据风险。

### 9.1 三处改动

| 文件 | 改动 | 效果 |
|---|---|---|
| `internal/worldclock/worldclock.go` | `DayAndHour` 起算点 `DayEpochUnix` → `ProtocolEpochUnix`，**并去掉 `+1`** | 日号从「第 4 天」变成 99184，与客户端屏幕数字一致 |
| `internal/gameserver/weather_preview.go` | `weatherPlanDayOffset` 由 `99180` 改为 `0`；`formatGameClock` 起算点同源 | 面板日号直接就是模板日号，日历与季节不再打架 |
| `internal/gameserver/spawn.go`、`docs/admin-console.md`、`internal/admin/web/index.html` | 注释/文案同步 | 后来人不会再把 `DayEpochUnix` 接回去 |

### 9.2 为什么去掉 `+1` 是关键

`DayAndHour` 原式 `secs/3600 + 1`，看起来只是「从 1 起算」的表述问题，实际会撞车：

- 客户端「今天」= `floor((now − 1438387200) / 3600)` = `weatherPlanDay(now)`
- 若 `DayAndHour` 带 `+1`，面板「今天」= 客户端「今天」+ 1
- `WeatherPreviewForMap` 从 `nowDay` 起铺 7 天 → 最后一天越出客户端下发的预报范围
- `TestWeatherPreviewMatchesClientForecast` 直接报
  `level_001_lake 面板第 99191 天（模板 99191）在客户端预报里不存在`

去掉 `+1` 后 `DayAndHour == weatherPlanDay`，两边严丝合缝。

### 9.3 为什么这是零风险的

- **所有 `worldDayAndHour` 调用点都写作 `_, hour := ...`**（8 处，全部丢弃 day 只取 hour），
  而两个 epoch 相差整 99181 游戏日，`hour` 零偏差 —— 昼夜判定、鱼口时段、饵料轮换全不受影响。
- 22 处测试用 `worldDayEpochUnix` 当**相位锚点**（`+1500`、`+12*realSecPerGameHour` 等），
  常量值本身没改，只是不再参与日号计算，测试全部照常通过。
- 没有任何存量数据需要迁移：改的是「怎么读 now」，不是「库里存了什么」。
  退货凭据、篝火、船票、`f13.f0` 全部继续用 `ProtocolEpochUnix`，一位不动。
- FILETIME 27 处完全没碰。

### 9.4 守卫

| 测试 | 锁什么 |
|---|---|
| `TestDayAndHour` | 日号从 0 起算、150 秒一小时 |
| `TestDayMatchesClientGameDay` | `DayAndHour` == 客户端 `W/1440` == `weatherPlanDay` 三者同源 |
| `TestSeasonDayDecoupledFromDayEpoch` | 季节标定不被日号轴变更污染（253 种鱼挂在它上面） |
| `TestProtocolEpochStaysIndependentFromDayEpoch` | 协议锚点不许被当成「可重置的日号」 |
| `TestDayEpochOffsetIsWholeGameDays` | 已退役常量的相位仍落在整游戏日边界（22 处测试依赖） |
| `TestWeatherPreviewMatchesClientForecast` | 面板与客户端逐图逐日同一份天气 |

### 9.5 副作用（正向）

后台天气预览的日卡片用 `day % 365` 算伪日历日期。日号还是「第 4 天」时，
`4 % 365 = 1 月 5 日`（冬）却配着「秋季」标签，自相矛盾；
现在日号与 `worldclock.SeasonDay` 同源，`99184 % 365 = 269` → 9 月 27 日（秋），
自动对上了，不需要额外改代码。