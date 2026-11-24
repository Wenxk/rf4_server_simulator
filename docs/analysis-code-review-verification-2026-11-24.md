# 代码审查报告复核（2026-11-24）

复核对象：[analysis-code-review-2026-11-24.md](analysis-code-review-2026-11-24.md)（下称「原报告」）。
复核基准：与原报告同一工作区。关键文件（`world.go` / `handlers_game.go` / `competition_handlers.go` / `config.go`）均无未提交 diff，与 HEAD 一致；未提交改动实测 104 行（原报告 103，差值即原报告文件自身，untracked）。
本文所有行号为本次实测（函数行数 = 签名行 → 闭合括号行），路径相对仓库根。

## 0. 一句话结论（大白话）

原报告指出的问题**大多数是真的**：买断协议确实注册了两份、后一份把前一份盖掉了；config 里确实有 4 个改了不生效的键；文档确实还在描述已经删掉的机制。这些都可以放心按它修。

**但它的"可删代码清单"不能照单全收**：清单里有 2 处把**还在使用的代码**误判成了死代码（一处是后台查鱼配置页正在用的函数，一处是启动器正在用的 5 个进程管理函数）。如果照清单删，会删坏功能。另外死代码总量"约 560 行"高估了约三成，实测约 430 行。

## 1. 复核范围与方法

- **逐条对照源码**：原报告 §0–§9 全部断言（约 40 余条）逐一验证。
  - P0 注册覆盖、P1 死配置、门禁复跑：lead 亲自核实。
  - §4 死代码清单、§5 文档 8 条 + §6 逻辑、§7 P3 + 测试 + §0 统计：各由一个独立子代理全量核查。
  - 所有"判原报告错"的反证，lead 再亲自抽查确认（本文 §4 各条均已二次确认）。
- **门禁复跑**：`go vet ./...` exit 0 零告警；`go run ./tools/doccheck` → issues=0 notes=1（仅 `specs/capabilities/bite-bait-and-spawn.md` 125 行超 80 行软上限）。与原报告一致。`gofmt` 未复跑（原报告称剥离 CRLF 后全绿）。
- **未复测项**：原报告的"函数总数 3167"与">200 行函数共 20 个"依赖其临时 AST 工具 `tools/tmp_funcsize`（现已不存在，无法复现）；本文只逐一实测了它点名的函数行数。

## 2. 总体判定

| 原报告章节 | 判定 | 说明 |
|---|---|---|
| §2 P0 注册覆盖 | ✅ 完全属实 | 3 个重复键、覆盖顺序、死 handler 全部确认 |
| §3 P1 死配置 | ✅ 完全属实 | 4 字段零运行时读取，活路径确认 |
| §4 P1 死代码清单 | ⚠️ 方向成立，质量不合格 | **2 处活代码误判 + 2 处事实错误 + 行数系统性虚高** |
| §5 P2 文档矛盾 8 条 | ✅ 全部属实 | 证据链完整，含两处同文自相矛盾 |
| §6 P2 逻辑清晰度 | ✅ 基本属实 | 嵌套层数夸大（9→6），两处小数字出入 |
| §7 P3 小冗余 | ✅ 基本属实 | 11 条中 10 条属实，1 条描述略不完整 |
| §7 测试隐患 | ⚠️ 定性成立，统计需修正 | skip 数、文件数、钩槽类数量有出入 |
| §0 范围统计 | ⚠️  minor 出入 | 测试文件 429 非 424；生产行数略高估 |
| §8 值得保留 | ✅ 属实 | 门禁、econ 拆分、注释质量均确认 |
| 门禁复跑 | ✅ 与原报告一致 | vet 0 告警 / doccheck issues=0 notes=1 |

## 3. 确认属实的部分（证据）

### 3.1 P0：注册表隐式覆盖（原报告 §2）

- `internal/gameserver/dispatch.go:133-137`：`Register` 即 `d.exact[key2{main, sub}] = h`，map 覆盖语义成立。
- 全仓扫描 160 处生产注册（含测试共 172 处），**恰好 3 个重复键**，157 个唯一键，与原报告一致：

| 协议 | 注册点 A | 注册点 B | 最终生效 |
|---|---|---|---|
| 3/37 | `internal/gameserver/handlers_game.go:1829` → `hPlaceBuyout` | `internal/gameserver/world.go:61` → `hPlaceBuyoutTx` | B（Tx 版） |
| 6/2 | `internal/gameserver/timeline.go:21` → `hNull` | `internal/gameserver/competition_handlers.go:22` → `hCompetitionLoad` | B（实实现） |
| 24/3 | `internal/gameserver/misc_fix.go:29` → `hSocialCurrent` | `internal/gameserver/timeline.go:22` → 同一函数 | 纯冗余 |

- `registerSessionHandlers`（`handlers_game.go:1813`）在 `handlers.go:24` 调用，`registerWorldHandlers`（`world.go:46`）在 `:29` 调用 → Tx 版覆盖旧版，成立。
- `hPlaceBuyout`（`handlers_game.go:377-411`，实测 **35 行**，原报告 38）全仓仅 3 处引用：`:372` 注释、`:377` 定义、`:1829` 注册 → **死代码成立**。
- `hCompetitionLoad`（`competition_handlers.go:145-159`，15 行）实查 DB；competition 在 `handlers.go:35` 最后注册 → 6/2 功能未坏，成立。
- 3/38（`hPlaceTripTx`）、3/33（`hBuyHouseTx`）各仅注册一次，无残留旧注册，成立。
- **原报告小误**：`handlers.go:16-36` 实为 **11 个** `registerXxx` 调用（原报告"10 行"），且漏提 `extraRegistrations` 路径——7 个模块（boat / craft_ops / econ_market / misc_fix / missing_econ_handlers / rodpod / skills_ops）经 init 注册、在 `handlers.go:17-19` 最先执行。不影响覆盖结论。

### 3.2 P1：死配置（原报告 §3）

- `internal/config/config.go:66-69` 的 Gateway 四字段（`BiteProbability` / `BiteDelayScale` / `BiteDelayFixed` / `KeepaliveInterval`）**零运行时读取**，成立。`defaults()` 在 `:148-151`（原报告 148-150），`applyEnvOverrides` `:210` 绑 `RF4_KEEPALIVE_INTERVAL_SEC`、`:211` 绑 `RF4_BITE_PROBABILITY`。
- 其余 `BiteProbability` 命中是不同类型（`store.FishBiteSetting`，`internal/store/repos_admin_panel.go:138-140`；admin `features.go:443-444` 的 `req.Setting`），不构成读取。
- 活路径确认：tunables 表（`repos_admin_panel.go:571-573`）→ `internal/gameserver/fishing.go:464-467`（延迟项）、`:476-477`（区域覆盖）、`:591`（`setting.BiteProbability`）。原报告写 `:468-470`，行号漂移。
- 保活活键：`Game.KeepaliveInterval()`（`config.go:81`）被 `session.go:221`、`drink_bonus.go:25`、`happy_hour.go:44` 共 3 处调用，成立。
- `config.yaml:35-38` 与 `config.example.yaml:41-44` 确在 gateway 段写这 4 个不生效键，example `:38` 注释误导成立；`game.keepalive_interval_sec` 才是活键（`config.yaml:45` / example `:52`）。

### 3.3 P2：文档矛盾 8 条（原报告 §5）——全部属实

| # | 复核证据（实测） |
|---|---|
| 1 | 文档侧三处原文确认（`docs/mechanism-map.md:17`、`docs/parameter-dictionary.md:41`、`specs/capabilities/bite-pace.md:21-23`）。代码侧 `internal/gameserver/bite_pace.go:126-135` 三档窗口、`:140-148` 概率 1.0/0.5/0.12、`fishing.go:507-521` pace 分支。`parameter-dictionary.md:89-92` 同文又写对三档 → 自相矛盾成立。`specs/changes/bite-pace/spec.md:78-79` 记录 2026-11-22 概率上调，:41 是过期旧值。**小遗漏**：取消封顶的是中+差两档（`tier < 2`），原报告只提差档 |
| 2 | `docs/admin-console.md:105-111` 原文确认。`ActiveBoost` 在 .go **0 处**；`fish_diel_active_boost` / `fish_diel_inactive_penalty` 在 .go **0 处**（仅 `snapshots/tuning-2026-11-20.json:40-41` 残留）；只剩 `fish_ecology.go:711` 的 `fish_diel_inactive_block`。旁证：`docs/fishing-pipeline-status-2026-11-23.md:383`、`internal/admin/api.go:481-482` 注释 |
| 3 | `docs/parameter-dictionary.md:263-264` 的 `reel_wear_multiplier` / `cast_distance_multiplier` 全仓（.go/.html/迁移/校验表/TUNABLE_DEFS）仅 3 处命中：文档两行 + 原报告自身 → 零读取点，纯幽灵参数 |
| 4 | `specs/capabilities/bite-bait-and-spawn.md:27-32` 原文确认。`internal/store/migrations/00080_drop_bait_manual_rotation.sql` 已删 groups_json/rotate_sec/rotate_mode；`rotationState` / `ExportedRotationState` 在 .go 0 引用；`docs/gameplay.md:111` 已声明删除 |
| 5 | `docs/parameter-dictionary.md:360` 仍列三列，同文 `:142-143` 又写已删 → 同文自相矛盾且与表结构矛盾 |
| 6 | `docs/parameter-dictionary.md:78`（及 :354）"有行即全换"。实际 `internal/store/repos_admin_panel.go:568-605` `GetFishBiteSetting`：`:571-575` 先填全局，`:589-603` 逐列 `.Valid` 才替换 → **列级回落**（原报告范围 568-608，实际止 605）。`fishing-pipeline-status-2026-11-23.md:134-136` 的描述才对 |
| 7 | `docs/parameter-dictionary.md:80`、`specs/changes/bite-pace/spec.md:90-91` "无后台入口/不做 UI"。实际 `internal/admin/web/index.html:917-931` TUNABLE_DEFS 登记 15 项 pace 键，`:2257` 有「咬钩节奏」卡片 |
| 8 | `docs/fishing-pipeline-status-2026-11-23.md:226` 称 fishSeasonAt 在 :517 从 worldDayAndHour 取天数。实际 `internal/gameserver/fish_bite_environment.go:519` `func fishSeasonAt(nowUnix int64) int`，`:522` `worldclock.SeasonDay(nowUnix)`；worldDayAndHour 在 `:542` 才出现且只取 hour。另 `internal/worldclock/worldclock.go:77-84` SeasonDay 用 SeasonEpochUnix、`:68-71` DayAndHour 用 ProtocolEpochUnix，两套历元 |

### 3.4 P2：逻辑清晰度（原报告 §6）

- **hCraft**：`internal/gameserver/craft_ops.go:687-1238` 恰 **552 行**，精确。8 个语义块全部抽查存在（配方解析 :694/:709、产出构造 :720、路亚选钩 :726、标本嵌鱼 :744/:1093-1117、前导目录 :749、贩卖机定价 :757/:762/:820、批量份数 :710/:711、事务扣料 :772/:777/:842-868/:1156）。**但"9 层嵌套"不属实**，见 §4.3。
- **四个超长函数行数逐一精确**：`fishingEndSettle` `progress_ops.go:359-690`=332；`workshopOrder` `econ_market.go:1371-1699`=329；`lineLossTx` `gear_damage.go:1239-1528`=290；`handleBaitPrefs` `internal/admin/bait_prefs.go:170-425`=256。
- **两种校验风格并存**：`internal/admin/api.go:477` 单条 `||` 链恰覆盖 15 个开关键；摩擦片参数用声明式表 `reelWearTunableRanges`（var :448、条目 :450-465、消费 :550-558）。成立。
- **7 个开关漏校验**：`bait_rotation_auto_enabled`、`bite_gate_enabled`、`bite_pace_enabled`、`groundbait_enabled`、`level_gifts_enabled`、`loot_box_enabled`、`player_condition_enabled` 全部不在 `||` 链，也不在 `handleTunableSet` 任何其他校验分支（:484/:491/:498/:507/:514/:524/:531/:537/:550 逐一核对）。7 键运行时全部有人读（无死键）。
- **静默回退链路准确**：写 `"true"` → 无校验放行 → `api.go:559` 入库 → `internal/gameserver/fishing.go:605-632` `ensureTunablesLocked` 中 `:619-622` `jsonFloat`（定义 :656-659，`fmt.Sscanf %g`）解析失败 → 该键不进 tunCache → `tunableF`（:635-643）缓存未命中 `:642 return def` 静默回退默认值。"设了等于没设"成立。

### 3.5 P3：小冗余（原报告 §7）

- 常量：`internal/worldclock/worldclock.go:52` `RealSecPerGameHour=150`；硬编码 `drink_rules.go:28`、`groundbait.go:40`；正确用法 `spawn.go:41`、`weather_preview.go:361`。✓
- `internal/admin/web/index.html:3552` `zmapRenderSeaChips` 空函数、`:4030` 唯一调用。✓
- CSS 逐字重复 :95≡158、:96≡159、:196≡199。✓
- DOM id 重复 9 个 / 6 组（zoneName 4057/4185、zoneModeParams 4062/4193、zoneFishSearch 4064/4195、zoneFishResults 4065/4196、zoneFishRows 4066/4197、zoneGX0/GZ0/GX1/GZ1 4051/4192）；`saveZone` :4130-4168 于 :4133 `$('#zoneName')` 取首匹配。`:4180` 注释自述"字段 id 同源避免共存"、弹窗仅无纸地图（kind==='none'，:3975）用 → 属刻意设计，但共存风险描述成立。✓
- 前端死交互：`:4191` `input#zoneSea` 从不被 saveZone 读；选海图区号 `:4187` onchange → `:4183` 重置 grid_rect；`zmapSeaStartRect` :3447 / `zmapSeaFinishRect` :3451 零引用；`zmapSeaDown` :3508 仅 :3515 自身回调自调用（仍死）；:3530 注释 vs :3531-3533 无条件 hidden。✓（原报告"只认 sea_zone"略不完整：sea 分支 :4138-4141 用 `ZONE_EDIT.sea_zones` 缺省回退 `[sea_zone]`）
- `:453` `fetch('/api/update/dismiss')` 唯一注册 `cmd/launcher/main.go:147`；`cmd/admin/main.go:86` 用 `admin.NewServer` 服务同一 SPA，`internal/admin/api.go:71-163` 约 70 路由无 `/api/update/*` → 独立启动 404。✓
- 鱼数口径 :2639=252、:2952=253（169+45+39）、:344=253。✓
- 测试定性：`economy_tx_test.go:312` `TestPlaceBuyoutAtomicAndIdempotent` 于 :327/:338/:358 调 `hPlaceBuyoutTx`；旧版 `hPlaceBuyout` 零测试。✓

### 3.6 值得保留（原报告 §8）

门禁全绿（复跑确认）；econ 拆分文件存在且职责清楚（`internal/gameserver/econ_override.go` / `econ_export.go`、`internal/store/repos_species_econ.go`）；踩坑注释主题属实（行号有漂移：boat.go 租期注释实际 :257-261 与 :282-286，原报告 262-275；profilebuild.go f0 注释 :90-93，原报告 88-93）。

## 4. 原报告说错的部分（更正）

### 4.1 两处活代码被误判为死代码（最严重）

**① admin 版 `dielCurveWeights` 是活代码**（原报告 §4 第 5 行）

- `internal/admin/fish_diel_view.go:33-46`（14 行）有完整调用链：`:95` 调用 ← `applyDielWeightView`（:81）← `internal/admin/features.go:425` ← `GET /api/fish/config`（`internal/admin/api.go:106`）。lead 已亲自 grep 确认。
- gameserver 版（`internal/gameserver/fish_ecology.go:687-700`，:719 调用）也是活的。
- 两版**并非逐行相同**：admin :43 用 `math.Max/Min`，gameserver :697 用 `clampFloat`（语义等价）。
- "两边各写一套迟早会漂"的注释确在 `fish_diel_view.go:23`。
- **正确定性**：这是真实的"两套实现重复维护"，应**抽公共包**，不是死代码，**不能删**。

**② 框架层 8 个函数里 5 个是活的**（原报告 §4 第 9 行）

| 函数 | 定义 | 实际调用方（lead 亲自 grep 确认） |
|---|---|---|
| `RestartOne` | `internal/supervisor/supervisor.go:386` | `cmd/launcher/main.go:448` |
| `StartAll` | `supervisor.go:181` | `main.go:210/275/287/432/446` |
| `SetStateCallback` | `supervisor.go:146` | `main.go:190` |
| `LastError` | `supervisor.go:399` | `main.go:353` |
| `MetricLabels` | `internal/buildinfo/buildinfo.go:21` | `main.go:541` |

真正死的只有 `internal/logging/logging.go:57-69` 的 `WithSession` / `WithUserid` / `WithCmd`，共 **9 行**（原报告该行记 73 行）。

**根因**：原报告自称"全仓 grep + 生产代码引用计数确认无人调用"，但这两处误判说明其扫描**漏了 `cmd/` 目录和 admin 包内部引用**。这是方法论漏洞，不是笔误。

### 4.2 两处事实性错误

**① `pushTicketRemaining`"量纲不一致"不成立**（原报告 §4 第 3 行）

lead 通读 `internal/gameserver/boat.go:463-515`：
- `nowMin`（:472）= `time.Now().Unix()/60`，现实 Unix 分钟；
- `start` = 租用对象 f2，由 `startTicketRent`（`boat.go:314`）写入 Unix 分钟（`:312-313` 注释明言"起始必须是当前分钟数（unix 分钟）"；`repairActiveTicketAnchor` :415-417 注释亦确认）；
- `dur` 是游戏分钟，经 `realDur=(dur+23)/24`（:498）换算成现实分钟；
- `remain = start + realDur - nowMin`（:504）**全程同量纲**。

函数确实是死的（无调用方，`remain` 只进日志 :513 未推送），但"死因"写错了。

**② "踩坑注释只留在死版"不成立**（原报告 §4 第 2 行、§4 末段）

活版 `hCheckTicket` 在 `boat.go:172-173` 有同主题注释："客户端构造租用记录读的是本地票对象，必须在回包前把'租用中'推给它，否则它用旧的（未租用）票构造记录，看门狗判定失败并传回岸边。"死版注释在 :239-240。另：死版头注释 :213"(不写租用状态)"与函数体 :241 调 `startTicketRent` 自相矛盾（原报告未提）。

### 4.3 数字夸大与统计出入

| 项 | 原报告 | 实测 | 性质 |
|---|---|---|---|
| 死代码总量 | ~560 行（表内合计 558） | 真死 **427 行**（不含传递性死代码）；计入遗漏的传递性死代码约 **477+ 行** | 高估约 31% |
| hCraft 嵌套 | 9 层 | **6 层**（全文件最深 7 个 tab，仅 `craft_ops.go:864` 一行；函数体基线 1 tab）。grep `^\t{7,}\S` 全文件仅 1 命中 | 夸大 |
| `handleTunableSet` 范围 | `api.go:468-558` | :468-**578**（:576 QueuePush、:577 writeJSON、:578 闭合） | 行号漂移 20 |
| `||` 链长度 | ~300 字符 | **~630 字符**（15 个纯键名合计已 331 字符） | 低估一半 |
| 测试文件数 | 424 | **429** = internal 424 + cmd 5（漏数 cmd/） | 统计口径 |
| `t.Skip` | 60 处 | **57 处** | 统计出入 |
| 拟饵钩槽类 skip | 12 处 | **14 处**（全在 `lure_hooks_test.go`，12 是其子集） | 统计出入 |
| 缺物品定义类 skip | ~20 处 | ~22-23 处 | 大致符 |
| 生产行数 | ~10.0 万 | 99,298（测试行 93,638≈9.4 万 ✓，比值 0.943 ✓，Go 文件 725 ✓） | 略高 0.7% |
| `registerHandlers` 调用 | 10 行 | 11 个 registerXxx + extraRegistrations 循环（见 §3.1） | 漏计 |
| 各死函数行数 | 见 §5 表 | 普遍虚高 2~17 行/函数（如 hPlaceBuyoutTx 79→67、store 四函数 54→37、框架层 73→9） | 系统性虚高 |

行数虚高疑似计数口径不同（含注释/空行或工具误差）；其 AST 工具 `tools/tmp_funcsize` 已删除，无法复现对账。

### 4.4 其他不准确描述

- §4 材料扣除行"活实现 = 同文件 `*Tx` 系列"：**不存在** `consumeMaterialsTx` 等；`removeItemsTx` 在 `economy_tx.go`。实际活路径：`hCraft` → `consumeItemQtyTx`（`economy_tx.go:349`）+ `consumeKeepnetForDemandTx`（`craft_ops.go:558`，:872 调用）。
- §4 store 行"有活事务版取代"：四函数均**无同名 Tx 版**。实际取代者：`AddXP` → 事务内 `UPDATE profile SET xp`（`catch_settlement.go:270` / `catch_release.go:68`）；`CatchLogTotals` → `buildStats321`（`statsbuild.go:76`，非 Tx）；`RemoveRigComponent` → `DeleteRigComponentAt`（`repos_rigcomponent.go:102`，非 Tx）+ `ReplaceRigComponentsTx`（:78）；`ListWornItems` → `ListRepairItems`（`repos_market.go:289`，语义不同）。
- §3 `fishing.go:468-470` → 实际 :464-467；§5 第 6 条 `repos_admin_panel.go:568-608` → 实际止 :605。行号漂移，不影响结论。

## 5. 修正后的死代码清单（可执行版）

### 5.1 确认可删（真死， lead 或子代理逐一确认零生产引用）

| # | 函数 | 位置（实测行数） | 备注 |
|---|---|---|---|
| 1 | `fishingEnd` | `internal/gameserver/econ.go:76-171`（96） | 活版 `fishingEndSettle`（`progress_ops.go:359-690`，`handlers_game.go:1774` 调用） |
| 2 | `grantXp` | `internal/gameserver/econ.go:61-73`（13） | **原报告遗漏**：唯一调用者是死函数 fishingEnd:109，传递性死 |
| 3 | `pickStationTicket` | `internal/gameserver/boat.go:214-255`（42） | 活版 `hCheckTicket`（boat.go:81-183，注册 :77） |
| 4 | `pushTicketRemaining` | `internal/gameserver/boat.go:463-515`（53） | 无活实现；量纲实际一致（见 §4.2） |
| 5 | `consumeMaterials` | `internal/gameserver/craft_ops.go:1291-1323`（33） | 绕事务，优先删 |
| 6 | `consumeDeclared` | `craft_ops.go:1327-1367`（41） | 同上 |
| 7 | `removeItems` | `craft_ops.go:1670-1680`（11） | 同上 |
| 8 | `slotsByDemand` | `craft_ops.go:509-518`（10） | 仅被上述死函数引用 |
| 9 | `consumeByDemand` | `craft_ops.go:1371-1407`（37） | **原报告遗漏**：唯一调用者是死函数 consumeDeclared:1353，传递性死 |
| 10 | `BuildStats322` | `internal/gameserver/profilebuild.go:83-88`（6） | 活版 `buildStats322W`（:94-99，:292 调用）；两段注释互相矛盾属实 |
| 11 | `rodRigidity` / `reelGearRatio` / `effectiveLineBuffer` | `internal/gameserver/gear_physics.go:203 / :358 / :551` | 8 个零引用函数共 54 行 |
| 12 | `pushScoreRate` / `fishQuality` / `rollFishPools` | `internal/gameserver/fishing.go:1192 / :1201 / :1901` | 被包装的 `rollFishPoolsWithRules` 是活的 |
| 13 | `fishLayerWeight` | `internal/gameserver/fish_ecology.go:559` | `fishLayerWeightProfile` 是活的 |
| 14 | `workshopSlots` | `internal/gameserver/econ_market.go:1264` | — |
| 15 | `AddXP` | `internal/store/repos.go:71-76` | store 四函数共 37 行；取代者见 §4.4 |
| 16 | `CatchLogTotals` | `internal/store/repos2.go:344-351` | 同上 |
| 17 | `RemoveRigComponent` | `internal/store/repos2.go:1064-1066` | 同上 |
| 18 | `ListWornItems` | `internal/store/repos_market.go:263-282` | 同上 |
| 19 | `DeleteRigComponentsBySlot` | `internal/store/repos_rigcomponent.go:110` | **原报告遗漏**：仅测试引用，生产死 |
| 20 | `WithSession` / `WithUserid` / `WithCmd` | `internal/logging/logging.go:57-69`（9） | 框架层唯一真死的一组 |
| 21 | `hPlaceBuyout` + 注册行 | `internal/gameserver/handlers_game.go:377-411`（35）+ `:1829` | 见 §3.1；零测试 |

合计：真死 **427 行**（#1-21 中不含传递性项）；计入 #2、#9 传递性死代码约 **477+ 行**。仍低于原报告的 558。

### 5.2 不可删（原报告误判的活代码）

| 函数 | 位置 | 活证据 |
|---|---|---|
| `dielCurveWeights`（admin 版） | `internal/admin/fish_diel_view.go:33-46` | `GET /api/fish/config` 调用链（§4.1） |
| `RestartOne` / `StartAll` / `SetStateCallback` / `LastError` | `internal/supervisor/supervisor.go:386 / :181 / :146 / :399` | `cmd/launcher/main.go` 多处调用（§4.1） |
| `MetricLabels` | `internal/buildinfo/buildinfo.go:21` | `cmd/launcher/main.go:541` |

### 5.3 真实重复但都活着（重构项，非删除项）

`dielCurveWeights` 在 admin 与 gameserver 各一套（语义等价、实现略异）。建议抽到公共包统一维护——原报告引用的"两边各写一套迟早会漂"注释（`fish_diel_view.go:23`）是对的，但处方开错了（不是删一边）。

## 6. 对原报告 §9 修复建议的影响

| 原建议 | 复核后判定 |
|---|---|
| 1. 删 `hPlaceBuyout` + 注册行 | ✅ 可执行（实测 35 行，非 38） |
| 2. 删 4 个死配置字段 + example 对应行（或写成 tunables 初值） | ✅ 可执行，二选一的建议合理 |
| 3. 按 §4 清单删 ~560 行死代码 | ⚠️ **必须先按本文 §5 修正清单**：admin `dielCurveWeights` 与 supervisor/buildinfo 5 函数不能删；其余按修正清单成组删。"优先删绕事务的材料扣除四函数"依然成立（连同遗漏的 `consumeByDemand`） |
| 4. 修四处文档硬矛盾 + 行号改函数名 | ✅ 可执行；实际应修 8 条（原报告 §5 全列了，建议 4 只点名 4 处） |
| 5. `||` 链换声明式表 + 补 7 个漏检开关 | ✅ 可执行且重要（静默回退链路已确认）；注意函数实际止于 :578 |
| 6. 拆 hCraft | ✅ 方向成立；按 552 行 / **6 层**嵌套评估，语义块划分建议可用 |

"前 3 项零行为变更"的说法在修正清单后依然成立；第 5 项改变接口校验行为需单独提交，同意。

## 7. 方法论教训（为什么原报告会在同一类错误上翻车）

1. **"零引用"扫描必须覆盖 `cmd/` 与包内引用**。原报告两处活代码误判都源于此：supervisor/buildinfo 的调用方全在 `cmd/launcher`，admin `dielCurveWeights` 的调用方在 admin 包内部。建议删除前以 `go build ./...` + 全仓引用计数（含 cmd/、含测试）为准，导出符号尤其要查跨包调用。
2. **行数/嵌套测量工具要可复现**。`tools/tmp_funcsize` 随用随删导致其数字无法对账；实测行数普遍虚高、嵌套 9 层实为 6 层，提示该工具口径有问题。临时工具要么提交、要么在报告里附测量方法。
3. **"死因"描述要读完整调用链**。量纲错误（§4.2①）与注释归属错误（§4.2②）都是只读了死函数本身、没读活版与写入方造成的。
4. **统计口径写明范围**。测试文件 424 只数了 internal/；"10 行调用"漏了 extraRegistrations。小出入不伤结论，但会侵蚀报告可信度。

## 8. 复核证据索引

- lead 亲自核实：P0 注册表全扫（160 处）、hPlaceBuyout/Tx 逐行、死配置全链路、门禁复跑、git 状态；反证抽查：admin dielCurveWeights 调用链 grep、cmd/launcher 5 函数调用点 grep、boat.go:165-184/:295-339/:405-424/:455-519 通读、craft_ops.go 缩进 grep、api.go:468-487 通读。
- 子代理 verify-deadcode：§4 死代码清单 10 行逐条（引用计数 + 行数实测 + 传递性死代码排查）。
- 子代理 verify-docs-logic：§5 文档 8 条 + §6 逻辑 3 条（含 7 开关逐一核对、静默回退链路追踪）。
- 子代理 verify-p3-tests：§7 P3 11 条 + 测试统计 + §0 规模数字。
- 未复测：函数总数 3167、>200 行函数计数 20、gofmt 门禁（见 §1）。