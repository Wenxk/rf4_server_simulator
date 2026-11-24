# 代码审查报告（2026-11-24）

审查对象：`G:/Localfish/rf4_server_simulator` 当前工作区（含 103 个未提交改动）。
审查目标：逻辑是否清晰、是否存在非必要冗余、是否存在前后矛盾。

> **修订说明（同日）**：本报告经第三方复核（[analysis-code-review-verification-2026-11-24.md](analysis-code-review-verification-2026-11-24.md)），
> 确认 §2/§3/§5/§8 全部成立，但 §4 死代码清单有 **2 处把活代码误判为死代码**、
> 2 处事实错误（`pushTicketRemaining` 量纲、踩坑注释归属）、以及行数/嵌套统计虚高。
> 已按下文 §10 逐条更正；§4/§6/§7 的数字是**更正后**的版本。误判根因见 §10.3。
>
> **行号口径**：全文引用的是 2026-11-24 审查时的源码快照。§9 列出的删除项现在已不存在，
> 按行号找不到时请改用函数名（`go run ./tools/refscan` 可查零引用函数）。

## 0. 范围与方法

| 项 | 数值 |
|---|---|
| Go 文件 | 725 个（生产 99,298 行 + 测试 93,638 行） |
| 测试文件 | 429 个（internal 424 + cmd 5；测试:生产 ≈ 0.94:1） |
| 函数总数 | 5621 个（含测试；非测试函数 3167 个） |
| 协议处理点 | 157 个 `(main, sub)`，生产注册 160 处 |

手段：
- 门禁：`go vet ./...`、`go run ./tools/doccheck`、`gofmt`（先 `tr -d '\r'` 判 CRLF）
- **AST 级统计**（复现方法）：`go/parser` + `ast.Inspect` 统计每个 `*ast.Ident` 的出现次数，
  分「非测试 / 测试」两桶；函数行数取 `fset.Position(fd.Pos()).Line` 到 `fd.End()`；
  嵌套深度按 `If/For/Range/Switch/TypeSwitch/Select/CaseClause` 累加（函数体记 1 层）。
- 注册表扫描（`d.Register` 重复键）、tunable/配置键 与读取点对账
- 逐条人工复核：本文每条结论都回到源码确认过，附精确行号

**已知的方法缺陷（已修正一次）**：初版用「正则去注释/字符串 + 全文标识符计数」，
在遇到注释里的**反引号**时会把它当成字符串起点，吞掉后续整段代码，导致 `cmd/launcher`
与 admin 包内的调用被漏计——这是 §10.3 两处误判的直接原因。AST 版本不存在此问题。

未覆盖：客户端报文逐字节正确性（需重新 dump 客户端）、运行时性能与并发压测。

## 1. 结论摘要

**门禁是干净的**：`go vet` 0 告警，`doccheck` 0 issue（仅 1 条软上限提示），构建链完整。
> 标记「已修/已删」的章节已在同日处理完毕（见 §9），保留原文是为了留下判断依据；
> 未标记的（§6 超长函数、§7 小冗余）本次未动。分层（`store` → `gameserver` / `admin` → `cmd`）清楚，注释解释"为什么"的比例明显高于同类项目。

问题集中在四类，且都不是"写错了"，而是"改完之后留下的旧轨"：

1. **隐式覆盖 + 忘删旧注册**：一个 35 行的完整 handler 永远不会执行（P0）
2. **死配置**：`config.yaml` 里 4 个键写了不生效，其中 1 组同名键一个死一个活（P1）
3. **一机制两实现**：收竿结算、上船选票、材料扣除、昼夜曲线解析各存一套死实现，共 479 行含注释（P1）
4. **文档漂移**：参数字典/机制图/spec 仍在描述已删除的机制与旧默认值（P2）

## 2. P0：注册表隐式覆盖，一个完整 handler 永不执行（已修）

`Dispatch.Register` 是 map 覆盖语义（`dispatch.go:133-137`：`d.exact[key2{main, sub}] = h`），最终生效者由 `registerHandlers`（`handlers.go:16-36`）里 11 个 `registerXxx` 调用与 `extraRegistrations` 循环的**排列顺序**决定。扫描出 3 个 `(main, sub)` 被注册了两次：

| 协议 | 注册点 A | 注册点 B | 最终生效 |
|---|---|---|---|
| `3/37` 买断 | `handlers_game.go:1829` → `hPlaceBuyout`（35 行，`handlers_game.go:377`） | `world.go:61` → `hPlaceBuyoutTx`（67 行） | **B（Tx 版）** |
| `6/2` 竞赛加载 | `timeline.go:21` → `hNull`（兜底占位） | `competition_handlers.go:22` → `hCompetitionLoad`（16 行实查 DB） | **B（实实现）** |
| `24/3` 社交频道 | `misc_fix.go:29` → `hSocialCurrent` | `timeline.go:22` → `hSocialCurrent`（同一函数） | 任一，纯冗余 |

**`hPlaceBuyout`（35 行）是死代码**：它位于 `registerSessionHandlers`，而 `registerWorldHandlers` 在 `handlers.go:29` 晚于 `handlers.go:24` 执行，Tx 版把它覆盖了。旁证——同一批事务化改造里 `3/38`（`hPlaceTripTx`）、`3/33`（`hBuyHouseTx`）都没有残留旧注册，只有 `3/37` 漏删。买断逻辑写在一处却在另一处生效，属于典型的"能跑但改错地方"陷阱。

`6/2` 的功能**没有坏**（`registerCompetitionHandlers` 在 `handlers.go:35` 最后执行，实实现覆盖了兜底），但这条正确性完全靠 `handlers.go` 的调用顺序维持，没有任何测试或断言守着。`handlers.go:31` 的注释只说"动态协议…需在基础处理器之后注册"，没有指名哪几个键存在竞争。

风险点：往 `registerHandlers` 里插一行调用、或调整两个 `registerXxx` 的先后，就会静默改变线上行为，且 `go vet` 与现有测试都不会报。

## 3. P1：死配置——config.yaml 里写了不生效（已修）

`internal/config/config.go:64-69` 定义、`defaults()`（`:148-150`）赋默认值、`applyEnvOverrides()`（`:210-211`）绑环境变量，但**没有任何运行时代码读取**：

| 配置键 | 位置 | env | 实际读取情况 |
|---|---|---|---|
| `gateway.bite_probability`（起竿必出鱼概率） | `config.go:66` | `RF4_BITE_PROBABILITY` | 仅出现在 env 绑定处，无人读 |
| `gateway.bite_delay_scale`（咬钩延迟总倍率） | `config.go:67` | 无 | 无人读 |
| `gateway.bite_delay_fixed`（固定咬钩延迟秒） | `config.go:68` | 无 | 无人读 |
| `gateway.keepalive_interval_sec`（世界时钟推送间隔） | `config.go:69` | `RF4_KEEPALIVE_INTERVAL_SEC` | 无人读 |

真正生效的是另外三处，同名不同物：

- 咬钩三项走 tunables 表（`bite_probability` 起竿必出鱼概率 / `bite_delay_scale` 延迟总倍率 / `bite_delay_fixed` 固定延迟秒），由后台读写（`store/repos_admin_panel.go:571-573`），判鱼侧在 `gameserver/fishing.go:468-470`、`:591` 读取。
- 保活走 `game.keepalive_interval_sec`（`config.go:77`）→ `Game.KeepaliveInterval()`（`:81`），被 `session.go:221` 等 3 处调用。

后果：`config.example.yaml:41-44` 与当前 `config.yaml:35-38` 都在 `gateway:` 段下写了这 4 项，注释还写着"运行参数默认值，钓鱼限制可通过管理后台覆盖"。用户照着改 config 或设 `RF4_BITE_PROBABILITY` / `RF4_KEEPALIVE_INTERVAL_SEC`，**一律不生效，且无任何提示**。这是本次审查里最容易被当成"服务端坏了"的坑。

## 4. P1：一机制两实现（479 行已删）

全部经 AST 级引用计数 + 人工核对确认无人调用；行数为「含函数上方紧邻注释」的实测值：

| 机制 | 死实现 | 行数 | 活实现 | 备注 |
|---|---|---|---|---|
| 14/4 收竿结算 | `econ.go:76 fishingEnd` | 96 | `progress_ops.go:359 fishingEndSettle`（332 行，由 `handlers_game.go:1774` 调用） | 两份都做经验/熟练度/磨损结算，名字只差后缀；`grantXp`（13 行）唯一调用者就是它，属传递性死 |
| 3/7 上船选票 | `boat.go:214 pickStationTicket` | 42 | `boat.go:81 hCheckTicket` 内联同逻辑 | 死版按站点遍历背包，活版按客户端 GUID。两版都有踩坑注释，活版在 `boat.go:172-173` |
| 船票剩余推送 | `boat.go:463 pushTicketRemaining` | 53 | 无（`expireRentalTickets` 只管到期移除） | 量纲**一致**（rent.f2 写 unix 分钟，`dur` 经 `/24` 换成现实分钟）：算出的 `remain` 只进日志、从不推送。死因是没接线，不是算错 |
| 材料扣除（非事务） | `craft_ops.go:1291/1327` `consumeMaterials`/`consumeDeclared`、`:1670 removeItems`、`:509 slotsByDemand`、`:1371 consumeByDemand` | 141 | `hCraft` → `consumeItemQtyTx`（`economy_tx.go:349`）+ `consumeKeepnetForDemandTx`（`craft_ops.go:558`） | **旧版绕过事务**，误用会破坏「扣料与产出同事务」；`consumeByDemand` 唯一调用者是 `consumeDeclared`，属传递性死 |
| 协议构造 | `profilebuild.go:83 BuildStats322` | 6 | `buildStats322W`（`profilebuild.go:94`） | 死版注释说「f0 是游戏日内分钟」，活版说「f0 下发连续游戏分钟」，**两段注释互相打脸** |
| 其余零引用 | `gear_physics.go:551/358/203`（线缓冲/齿轮比/竿硬度）、`fishing.go:1192/1201/1901`（评分/品质/内置鱼池）、`fish_ecology.go:559 fishLayerWeight`、`econ_market.go:1264 workshopSlots` | 54 | — | 纯历史残留 |
| store 层零引用 | `repos.go:71 AddXP`、`repos2.go:344 CatchLogTotals`、`repos2.go:1064 RemoveRigComponent`、`repos_market.go:263 ListWornItems` | 42 | — | 无同名 Tx 版；实际取代者是事务内 `UPDATE profile SET xp` / `buildStats321` / `DeleteRigComponentAt` / `ListRepairItems` |
| 日志封装零引用 | `logging.go` 的 `WithCmd`/`WithSession`/`WithUserid` | 9 | — | 框架层唯一真死的一组 |
| 被覆盖的 handler | `handlers_game.go:377 hPlaceBuyout` | 35 | `world.go:276 hPlaceBuyoutTx`（67 行） | 见第 2 节 |

> **更正**：初版把 admin 版 `dielCurveWeights`（`fish_diel_view.go:33`）与 `supervisor`/`buildinfo` 的 5 个函数也列为死代码，**这是错的**：
> admin 版经 `applyDielWeightView`（`:95`）← `features.go:425` ← `GET /api/fish/config` 被调用，是「两份都活的重复实现」，应抽公共包、不能删一边；
> `RestartOne`/`StartAll`/`SetStateCallback`/`LastError`/`MetricLabels` 全部由 `cmd/launcher/main.go` 调用，根本不进清单。详见 §10。


## 5. P2：文档与代码矛盾（已逐条核对，已修）

| # | 文档说 | 代码实际 |
|---|---|---|
| 1 | `docs/mechanism-map.md:17`、`docs/parameter-dictionary.md:41`、`specs/capabilities/bite-pace.md`：pace 模式下"路亚窗口不动，只改单轮概率（0.30/0.10/0.02）" | `bite_pace.go:126-135` 三档窗口（好 1.5~6 / 中 10~60 / 差 20~60），`bite_pace.go:140-148` 概率 **1.0 / 0.5 / 0.12**；`fishing.go:507-521` 差档还取消封顶。**同一份 `parameter-dictionary.md` 的 :89-92 又写对了三档**，与 :41 自相矛盾 |
| 2 | `docs/admin-console.md:105-111`：昼夜权重「三个参数：总开关 + 活跃峰值 2.0 + 非活跃 0.30」，折算见 `internal/worldclock.ActiveBoost` | 三者全部不存在。`worldclock` 无 `ActiveBoost`；`fish_diel_active_boost`/`fish_diel_inactive_penalty` 在 `.go` 中 0 处；只剩 `fish_diel_inactive_block`（非活跃时段禁口开关，`fish_ecology.go:711`） |
| 3 | `docs/parameter-dictionary.md:263-264`：`reel_wear_multiplier`（搏鱼磨损总倍率）、`cast_distance_multiplier`（抛竿距离总倍率）可调 | 全仓（含 `admin` 校验表与后台 `TUNABLE_DEFS`）**零读取点**，只有文档这一行。照它调参不会发生任何事 |
| 4 | `specs/capabilities/bite-bait-and-spawn.md:27-32`：手工轮换组 `soft/strict` 与 `rotationState`/`ExportedRotationState` | 符号不存在，列已被 `migrations/00080_drop_bait_manual_rotation.sql` 删除；`docs/gameplay.md:111` 已写"手工轮换组已删除" |
| 5 | `docs/parameter-dictionary.md:360`：`fish_bait_override` 有 `groups_json`/`rotate_sec`/`rotate_mode` 三列 | 迁移后表里没有这三列；同文 `:142-143` 又写"手工轮换组已删除" |
| 6 | `docs/parameter-dictionary.md:78`：`fish_bite_setting`"有行即全换，不再逐项回落" | `store/repos_admin_panel.go:568-608` 用 `sql.NullFloat64` **逐列回落**；`docs/fishing-pipeline-status-2026-11-23.md:134-136` 的描述才是对的 |
| 7 | `docs/parameter-dictionary.md:80`、`specs/changes/bite-pace/spec.md:90`：`bite_pace_enabled`（咬钩节奏开关）"无后台入口" | 后台 `index.html:917-931` 登记了 `bite_pace_enabled` 等 15 项，`:2257` 有「咬钩节奏」卡片 |
| 8 | `docs/fishing-pipeline-status-2026-11-23.md:226`：`fishSeasonAt` 在 `fish_bite_environment.go:517`，从 `worldDayAndHour` 取天数 | 函数在 `:519`，第 522 行是 `worldclock.SeasonDay(nowUnix)`；`worldDayAndHour` 在 `:542` 才出现且只取 `hour` |

行号引用抽查 19 处，7 处已漂移（`docs/analysis-fishing-chain-2026-11-07.md` 的 4 个行号全部失效，最大偏差 1400 行）。这类文档适合改成"函数名 + 文件"而非"文件:行号"，否则每次重构都在制造矛盾。

## 6. P2：逻辑清晰度（hCraft 拆分未做，其余已修）

**超长函数**（3167 个函数中 20 个 > 200 行）：

| 函数 | 行数 | 嵌套 | 位置 |
|---|---|---|---|
| `hCraft`（制作请求总入口） | **552** | **6 层** | `craft_ops.go:687` |
| `fishingEndSettle`（收竿结算） | 332 | 4 | `progress_ops.go:359` |
| `workshopOrder`（工坊订单） | 329 | 4 | `econ_market.go:1371` |
| `lineLossTx`（断线处理） | 290 | 6 | `gear_damage.go:1239` |
| `handleBaitPrefs`（鱼饵偏好后台） | 256 | 4 | `admin/bait_prefs.go:170` |

`hCraft` 一个函数里同时处理：配方解析、产出物构造、路亚选钩、标本嵌鱼、前导线目录预载、贩卖机定价、批量份数、事务扣料。6 层嵌套意味着最内层代码缩进 7 个 tab（函数体基线 1 层）。它的问题是"没有分层"而不是"写错了"——建议按已存在的语义块（`isTrophyMount` / `isLeaderCraft` / `isVendCraft`）抽成 3~4 个子函数，风险可控。

**同一类校验的两种写法**：`admin/api.go:468-578 handleTunableSet` 里，摩擦片参数用声明式表 `reelWearTunableRanges`（`:450-466`，好），而 15 个开关用一条 629 字符的 `||` 链（`:477`）硬编码（差）。同一函数两种风格，且开关白名单与代码实际读取的集合已经不同步：`bait_rotation_auto_enabled`（自动轮换开关）、`bite_gate_enabled`（咬口门开关）、`bite_pace_enabled`（咬钩节奏开关）、`groundbait_enabled`（打窝开关）、`level_gifts_enabled`（等级礼包开关）、`loot_box_enabled`（箱子开关）、`player_condition_enabled`（玩家状态开关）**共 7 个开关不在 0/1 校验内**（2026-11-24 已补齐：白名单扩到 22 键并加逐键测试）。写入 `"true"` 这类值时 `jsonFloat` 解析失败（`fishing.go:620`），该键不进 `tunCache`，`tunableF` 静默回退默认值——开关变成"设了等于没设"。

**注册分散**：`3/37` 买断注册在 `registerSessionHandlers`（会话）。协议号与注册位置的语义没有对应关系，靠读 11 个 `registerXxx` 加 `extraRegistrations` 循环才能拼出全貌，这也是第 2 节那个漏删能长期存在的原因。

## 7. P3：小冗余（本次未处理）

- **常量重复定义**：`worldclock.RealSecPerGameHour = 150`（1 游戏小时 = 150 现实秒）已有导出常量，但 `drink_rules.go:28 gameHourReal = 150 * time.Second`、`groundbait.go:40 groundbaitRealSecPerGameHour = 150.0` 各自硬编码。`spawn.go:41` 和 `weather_preview.go:361` 的写法是对的。
- **空函数仍被调用**：`index.html:3552 zmapRenderSeaChips()` 函数体只剩注释，`index.html:4030` 照旧调用它。
- **CSS 逐字重复**：`index.html:196` 与 `:199`、`:95-96` 与 `:158-159`。
- **DOM id 跨模板重复**：`zoneName`（`:4057` 内联面板 / `:4185` 弹窗）、`zoneFishSearch`、`zoneFishRows`、`zoneModeParams`、`zoneGX0` 等 6 组。目前靠"有纸地图走内联、无纸地图走弹窗"保证不同时出现，但 `$('#id')` 取文档首个匹配——两套模板一旦共存，弹窗保存会读到内联面板的值（`saveZone` 在 `:4130`，读的是 `#zoneName`）。同类 id 撞车在本项目已出过一次事（`wxDays`）。
- **前端死交互**：`index.html:4191` 的「海图区号」输入框，`saveZone`（`:4133-4149`）从不读取它，只认 `ZONE_EDIT.sea_zone`；选「海图区号」类型还会被 `:4183` 立即重置回 `grid_rect`。挪威海建区的 `zmapSeaDown` / `zmapSeaStartRect` / `zmapSeaFinishRect` 无调用点，注释与实际隐藏行为也不一致（`:3530` 说"隐藏全部建区入口"，`:3531-3533` 是无条件隐藏）。
- **前端绕过统一封装**：`index.html:453` 直接 `fetch('/api/update/dismiss')`，该路由只在 `cmd/launcher/main.go:147` 注册，`cmd/admin` 独立启动时 404。
- **图例行数口径**：`index.html:2639` 写 252 种鱼，`:2952`/`:344` 写 253 种。

**测试体系的一处隐患**（不改代码，只提示）：429 个测试文件（internal 424 + cmd 5）、57 处 `t.Skip`，其中约 20 处是"静态库缺某物品定义"、14 处是"测试拟饵没有出厂钩/N 个钩槽"。这类跳过在静态库升级、物品改名时会静默生效——测试仍显示绿，实际断言已不执行。建议给关键跳过加 `-run` 白名单或把"缺定义"变成 `t.Fatalf`（静态库是本仓库的固定资产，不该缺）。另外第 2 节的 `3/37` 两套实现中只有 `hPlaceBuyoutTx` 有测试（`economy_tx_test.go:327`），被覆盖的 `hPlaceBuyout` 零测试——这既解释了为什么漏删能长期存在，也说明"注册表最终生效谁"没有任何断言。

## 8. 值得保留的做法

- `go vet` / `doccheck` / `gofmt`（剥离 CRLF 后）全绿，说明日常门禁在执行。
- `docs/parameter-dictionary.md`、`docs/mechanism-map.md` 这类"机制 → 文件 → 测试"的对照表，本身是对抗代码腐化的正确方向，问题只在没跟上最近两轮改动（pace 控速、昼夜曲线）。
- 测试注释里大量"为什么不是显然写法"的记录（例如 `boat.go:262-275` 关于租期锚点、`profilebuild.go:88-93` 关于 f0 连续分钟），是本项目最值钱的部分。
- `econ` 新模块的拆分（`econ_override` / `econ_export` / `repos_species_econ`）职责清楚，没有跟着引入新冗余。

## 9. 修复执行记录（2026-11-24）

| # | 动作 | 状态 |
|---|---|---|
| 1 | 删 `handlers_game.go` 的 `d.Register(3, 37, hPlaceBuyout)` 与 35 行函数体 | ✅ 已删，`go build`/`go vet` 通过 |
| 2 | 删 `config.go` 的 4 个死字段 + `defaults()` 3 行 + env 绑定 2 行；`config.example.yaml` / `config.yaml` 同步删键并改写注释 | ✅ 已删，`RF4_BITE_PROBABILITY` / `RF4_KEEPALIVE_INTERVAL_SEC` 一并移除 |
| 3 | 删除 §4 表内 25 个死函数（含 `grantXp`/`consumeByDemand` 两处传递性死），共 479 行 | ✅ 已删，`go test ./...` 全绿 |
| 4 | 修 §5 的 8 条文档硬矛盾 + 3 条 spec（bite-pace / fish-market-prices / bite-bait-and-spawn） | ✅ 已修，`doccheck` 0 issue |
| 5 | `handleTunableSet` 的 15 键 `||` 链换成声明式表 `tunableSwitchKeys`，补 7 个漏检开关，并加逐键测试 | ✅ 已改，`TestAdminAPI` 通过 |
| 6 | `hCraft` 拆分（552 行 / 6 层） | ⏸ 未做。属纯重构、无行为变更，建议单独一次改动 + 单独回归 |

未做且需要单独决策的两项：
- **admin/gameserver 两份 `dielCurveWeights`**：都活着，正确处方是抽公共包（例如 `internal/fishdiel`），
  而不是删一边。属跨包重构，本次不动。
- **`hWorkshopRepair`（158 行）**：注释写「构造 21/3 或 21/4 修理处理器」，但 21/3/21/4 实际注册的是
  `hWorkshopPreview`/`hWorkshopOrder`，它只被 3 个测试文件调用——是**废弃协议实现**，测试测的是一条
  不存在的链路。删它会牵连测试改造。**状态：待二次确认后再决定**。

## 10. 复核差异与二次验证结论

第三方复核（[analysis-code-review-verification-2026-11-24.md](analysis-code-review-verification-2026-11-24.md)）指出本报告 2 处误判 + 2 处事实错误 + 统计虚高。逐条二次验证结果：

| # | 争议点 | 复核主张 | 二次验证（AST 级） | 结论 |
|---|---|---|---|---|
| 1 | admin 版 `dielCurveWeights` | 是活代码，调用链到 `GET /api/fish/config` | `prod=4 test=11`，两处定义各有调用 | **复核正确，本报告错** |
| 2 | `supervisor` 4 函数 + `buildinfo.MetricLabels` | 活代码，调用方在 `cmd/launcher` | `RestartOne` prod=2、`StartAll` prod=6、`SetStateCallback` prod=2、`LastError` prod=2、`MetricLabels` prod=2 | **复核正确，本报告错** |
| 3 | `pushTicketRemaining` 量纲 | 一致：rent.f2 是 unix 分钟 | `startTicketRent` 写 `time.Now().Unix()/60`，`dur` 经 `(dur+23)/24` 换算，全程 unix 分钟 | **复核正确，本报告错** |
| 4 | 踩坑注释只在死版 | 活版 `boat.go:172-173` 有同主题注释 | 确认存在 | **复核正确，本报告错** |
| 5 | `hCraft` 嵌套 | 6 层不是 9 层 | 最大缩进 7 tab（仅 `craft_ops.go:851` 一行），基线 1 → 6 层 | **复核正确，本报告夸大** |
| 6 | `handleTunableSet` 范围 | 止于 `:578` | 确认 `:576 QueuePush` / `:577 writeJSON` / `:578 }` | **复核正确** |
| 7 | `||` 链长度 | ~630 字符 | 629 字符 / 15 键 | **复核正确，本报告低估** |
| 8 | 死代码总量 | 427 行（不含传递性） | 本次实删 479 行含注释（函数体本体约 427 + 传递性 50） | **复核正确** |
| 9 | 测试文件数 | 429（internal 424 + cmd 5） | `find` 实测 424 + 5 | **复核正确** |
| 10 | `t.Skip` 数 | 57 | 57 | **复核正确** |
| 11 | `hPlaceBuyout` 行数 | 35 行 | 35 行 | **复核正确** |
| 12 | `registerHandlers` 调用数 | 11 个 `registerXxx` + `extraRegistrations` 循环 | 确认 | **复核正确** |
| 13 | 材料扣除活实现 | 不存在 `consumeMaterialsTx`，活路径是 `consumeItemQtyTx` + `consumeKeepnetForDemandTx` | 确认 | **复核正确** |
| 14 | store 四函数取代者 | 无同名 Tx 版，取代者是别的函数 | 确认（`buildStats321` / `DeleteRigComponentAt` / `ListRepairItems` / 内联 UPDATE） | **复核正确** |
| 15 | `fishing.go` 读取行号 | 464-467 不是 468-470 | 确认 | **复核正确** |

**复核未覆盖、本次新发现的两项**：
- `hWorkshopRepair`（`econ_market.go:1704`，158 行）是废弃的 21/3·21/4 修理处理器，只被测试调用（见 §9）。
- `DeleteRigComponentsBySlot` 不是"仅测试引用"：它有生产调用者 `RemoveRigComponent`（`repos2.go:1065`），
  属传递性死代码；但**同时被测试直接调用**，因此本次保留。**状态：待二次确认后再决定**。

### 10.3 为什么会误判（可复用的教训）

初版扫描用「正则剥注释与字符串 → 全文标识符计数」。剥字符串的正则 `` `[^`]*` `` 在遇到**注释里的单个反引号**
（本项目注释大量使用 `` `fish.go:123` `` 这种引用）时，会把从该反引号到下一个反引号之间的整段代码当成字符串删掉，
导致其间的调用点不被计数。`cmd/launcher/main.go` 与 `internal/admin` 恰好命中——两处误判由此而来。

正确做法（本次已改用）：`go/parser` + `ast.Inspect` 统计 `*ast.Ident`，不存在字符串/注释误判；
删除前用 `go build ./...` + `go vet ./...` 双重把关，并优先按「函数名 + 接收者」定位而非行号。
