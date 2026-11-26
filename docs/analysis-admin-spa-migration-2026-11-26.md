# 后台 SPA 并行迁移方案

> 目标：把上游 `index.html` 单页 SPA 那套后台拉进本地，与现有旧后台**并行存在**；
> 旧后台保持可用，功能逐个迁移到新平台，全部迁完再切换默认入口。
>
> 状态：**六批全部完成，已切换默认入口**（2026-11-27）。
> `index.html` 现在是 SPA 空壳，旧后台挪到 `legacy.html` 只作兜底。
> 本文既是方案，也是验收清单；批次 6 那节记了切换后要知道的操作差异。
> 分支：`feat/spa-migration`（worktree `G:/Localfish/rf4-spa-migration`）
> 基线：`fd92304`（main / fork/main 同步点）

---

## 1. 结论先行

上游早在分叉后就走通了这条路，我们可以直接复用它的形态，但**不必照搬它把旧后台改名**
的做法：

| 做法 | 访问方式 | 对旧后台的改动 |
|---|---|---|
| 上游选择 | `/` → SPA，`/legacy.html` → 旧后台 | 旧后台改名 + 52 行路径补丁 + 12 个测试改路径 |
| **本方案** | `/` → 旧后台，`/spa.html` → 新 SPA | **零改动**，旧后台一行不碰 |

关键依据是 SPA 的 API 前缀推导（`app/core.js:81`）：

```js
export const BASE = location.pathname.replace(/\/[^/]*$/, '')
                                  .replace(/\/+$/, '').endsWith('/admin') ? '/admin' : '';
```

它只取「入口文件所在目录」，与文件名无关。实测四种部署形态全部正确：

| 访问 URL | 推导出的 `BASE` | 实际 API 前缀 | 说明 |
|---|---|---|---|
| `/admin/` | `/admin` | `/admin/api` | 旧后台（launcher 统一入口） |
| `/admin/index.html` | `/admin` | `/admin/api` | 旧后台显式文件 |
| `/admin/spa.html` | `/admin` | `/admin/api` | **新 SPA** |
| `/spa.html` | `''` | `/api` | 新 SPA（独立端口） |

所以只要把 SPA 入口命名为 `spa.html` 放进同一个 `web/` 目录，两套就能共存，
且服务端**不需要改任何 Go 代码**。

---

## 2. 已落地的骨架

### 2.1 改动清单

```
internal/admin/web/app/        ← 新增，29 个文件，2.4 MB（上游 origin/main）
internal/admin/web/spa.html    ← 新增，516 B，SPA 入口
internal/admin/web/index.html  ← 未改动（旧后台，410 KB）
```

`git status --porcelain` 只有 2 项 `??`，旧后台与所有 Go 代码零改动。

### 2.2 为什么不用改 Go

`internal/admin/embedded.go` 用的是：

```go
//go:embed all:web
var webRoot embed.FS
```

`all:` 前缀保证新增的 `app/` 目录（含 `.woff2`/`.jpg` 等非文本资源）会被一并嵌入，
新增文件自动生效。

### 2.3 已验证的检查项

| 检查项 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test ./internal/admin/` | 通过 |
| 22 个 `.cjs` UI 测试 | **22 / 22 通过**（旧后台功能零影响） |
| 静态资源可达性（`/admin/spa.html`、`app/*`） | 全部 HTTP 200 |
| SPA 12 个页面真实浏览器渲染 | **14 / 14 PASS，0 JS 错误、0 请求失败** |
| SPA 依赖的 39 个 API 端点 | 我方 `api.go` 全部已注册，**缺失 0** |

> 测试注意：`.cjs` 测试需要真实浏览器。本机 Playwright 浏览器内核未下载，
> 需通过环境变量指向系统 Chrome：
> ```bash
> export RF4_TEST_BROWSER="/c/Program Files/Google/Chrome/Application/chrome.exe"
> ```
> 不设置时 22 个测试会全部失败（`Cannot find module 'playwright'` 或
> `Executable doesn't exist`），这是环境问题，与代码无关——主仓库同样如此。

### 2.4 worktree 的 `node_modules`

worktree 不含 `node_modules`（未入版本库）。已用目录联接复用主仓库依赖，
避免重复下载浏览器内核：

```
G:\Localfish\rf4-spa-migration\node_modules
  → G:\Localfish\rf4_server_simulator\node_modules   (Junction)
```

---

## 3. 两侧功能盘点

### 3.1 页面级对应关系

我方旧后台 13 个视图 / 5013 行 / 347 个函数；上游 SPA 12 个页面 / 约 3100 行 ES module。

| 我方视图 | SPA 页面 | 迁移难度 | 说明 |
|---|---|---|---|
| `view-dashboard` | `pages/dashboard.js`（133 行） | 低 | 结构基本对应 |
| `view-players` | `pages/players.js`（399 行） | 低 | SPA 版反而更细 |
| `view-stores` | `pages/shop.js`（233 行） | 低 | — |
| `view-fish` | `pages/rules.js` + `pages/waters.js` + `pages/zones.js`（1053 行） | 中 | SPA 拆成三页，`zones.js` 588 行是我方没有的独立钓点页 |
| `view-hooks` | `pages/hooks.js`（125 行） | 低 | — |
| `view-mail` | `pages/mail.js`（141 行） | 低 | — |
| `view-params` | `pages/tuning.js`（282 行） | 中 | 参数项需按我方 tunables 重排 |
| `view-audit` | `pages/audit.js`（68 行） | 低 | — |
| `view-about` / `view-release` | `pages/docs.js`（32 行） | 低 | — |
| `view-sync` | `pages/sync.js` | 低 | 原标注「SPA 无对应页」，批次 6 已移植 |
| `view-ecology` | `pages/ecology.js` + `ecology-eco/econ/weight.js` + `pages/bait.js` | **高** | 我方独有 33 个生态函数，已迁 |
| `view-weather` | `pages/weather.js`（约 520 行） | **高** | 我方独有 21 个天气函数，已迁 |
| — | `pages/event-gifts.js`（227 行） | — | SPA 独有，我方在 `view-fish` 内嵌 |

> 下表是迁移**启动时**的盘点，保留原样作为对照。「SPA 页面」一列里的行数是上游
> 原始行数，不是迁移后的行数（迁移后普遍更长，因为要补回我方独有的功能）。

### 3.2 我方独有功能（必须移植，共 89 个函数）

| 领域 | 函数数 | 代表标识符 | SPA 现状 |
|---|---|---|---|
| 生态基线 | 33 | `loadEcology` `ecoMaskWidget` `ecoDielState` `updateEcoDielHint` | 完全没有 |
| 天气 | 21 | `loadWeather` `wx*` 系列 | 完全没有 |
| 饵料规则 | 14 | `loadBaitPrefs` `baitSummaryHTML` `rerollBaitAuto` | `bait.js` 仍是老术语 |
| 昼夜 diel | 9 | `dielMaskTouched` `dielCurveEffective` `dielMaskRead` | 完全没有 |
| 经济四档 | 8 | `loadEcon` `renderEconRows` `runEconPreview` | 完全没有 |
| 权重档位 | 4 | `loadWeightPresetTab` `applyWeightPresetTab` | 完全没有 |

**铁律（用户已确认）**：饵料、生态、天气三块**不合上游**，保留现有玩法设定。
即：迁移时以我方实现为准，不采纳上游的简化版本。

### 3.3 我方饵料选择器 vs SPA 现状的实证差距

SPA `pages/bait.js` 仍在用老术语：

```js
const DIET = [['lure','拟饵'],['worm','蠕虫'],['fish','饵鱼'],['boilie','煮饵'],...];
const WILDCARD = [['各类饵','所有饵料'],['拟饵','所有拟饵'],['活饵类','所有活饵'],...];
```

而我方已实现：多级联动选择器 + `类别@饵名` 写法 + 型号级抽签 + 形态后缀。
截图对比可见，SPA 的鱼饵页只有「搜索框 + 水域下拉 + 鱼种下拉」三个控件。

此外发现 SPA 自身一个 UI 缺陷：列表为空时页面左下角直接渲染出裸 `null`
（`pages/bait.js` 某处 `innerHTML` 拼接未兜底）。迁移时顺手修掉。

---

## 4. 迁移路线（六批，逐批可验收）

原则：**每批都能独立编译、独立测试、独立回滚**；旧后台全程可用。

### 批次 0 · 骨架（本次已完成）

- [x] 引入 `web/app/`、`web/spa.html`
- [x] `go build` / `go test` / 22 个 UI 测试全绿
- [x] 静态资源与 12 页浏览器渲染验证
- [ ] **验收**：`/admin/` 旧后台正常，`/admin/spa.html` 新 SPA 可打开

### 批次 1 · 低风险页面（9 个，纯搬）— **已完成**

`dashboard` `players` `shop` `hooks` `mail` `audit` `docs(about/release)` `zones`

原以为「直接用 SPA 实现」即可，实测发现 SPA 有 5 个必须修的问题，都已修掉：

| # | 问题 | 性质 | 处理 |
|---|---|---|---|
| 1 | **钩号显示成 `#1` 而不是 `S1/0`** | 真缺陷，对不上客户端 | 改用 `display_size`，旁注「刻度 N」 |
| 2 | **钩号下拉硬编码 0–33** | 真缺陷，选项与真实钩号无关 | 改用接口返回的 `sizes`（含各号数量） |
| 3 | **`online_known` 字段缺失** | 真缺陷，快照过期时全员误报离线 | 后端补字段，前端三态（在线/离线/未上报） |
| 4 | **鱼饵页列表尾部渲染裸 `null`** | 真缺陷 | `replaceChildren` 是原生 API，不吃 null，自带过滤 |
| 5 | **缺「新建玩家」「发放物品」** | 功能缺口 | 补两个入口，接 `/api/players/create`、`/api/players/grant` |

另外顺手修掉的：

- **详情响应不是对象时整页渲染成 `undefined`**：原判断 `if (!p)` 挡不住空数组（truthy），
  加了 `typeof p === 'object' && !Array.isArray(p)` 的校验。
- **「最近登录」是死字段**：`profile` 表没有 `last_login`，detail 也不返回，永远显示「—」。
  已删掉这一行，避免看起来像坏了。
- **旧后台自己的在线灯也有同样误报**：`index.html` 原来只有在线/离线两态，
  现在同步改成三态（新增 `.online-dot.unknown`）与图例「未上报」。

**踩到的坑（写下来避免复发）**：

- `Form.row({ kind: 'int' })` 的初值**必须是数字**。给空字符串 `''` 会触发「请填写数值」
  校验失败，按钮点了没反应。「留空自动分配」要用 `0` 表达。
- 原生 `replaceChildren(...)` 收到 `null` 会渲染成字面量 `"null"`；只有 `h()` 内部的
  `append()` 才会跳过 null。两者不能混用同一套心智模型。
- 页面二级页签是 `#tabs` 里的 `<a>`，不是 `<button>`；测试定位要用 `#tabs a`。
- 页面同时存在「档案编辑」和「新建玩家」两个 `f_name_*` 输入框，弹窗后插入 DOM，取 `.last()`。

- [x] **验收**：`spa_batch1_ui_test.cjs` 6 个用例全绿，覆盖上述 5 个修复
- [x] 24 个 `.cjs` UI 测试全绿（批次 0 骨架 10 + 批次 1 的 6 + 原有 8，旧后台零回归）

### 批次 2 · 鱼口与参数（中风险，双向合并）— **已完成**

`rules` + `waters` + `tuning`

原计划只说「把 tunables 的完整键位表灌进 `tuning.js`」。真做的时候发现，问题不是
「漏灌」而是**两侧根本不共用同一套键**——上游 SPA 抄的是上游的清单，其中一批键本服
后端一个都不读，反之一批本服独有的键上游压根没有。这类错位在界面上完全看不出来：
调了没反应，或者根本找不到入口。所以这一批的实际工作是一次**双向对表**。

**口径先说清**：比对基准是「参数页可见键」，不是后端全部键。后端有 146 个字面量
tunable，其中大量是内部诊断项（`fight_*`、`handitem_throw_*`、`multi_hook_*`、
`groundbait_*` 等），旧后台参数页同样不列——它们不该出现在界面上。

**删掉的 14 个键**（本服后端不读，按源码逐个核过）：

| 键 | 本服为什么没有 |
|---|---|
| `pace_good_min` `pace_good_max` `pace_bad_min` `pace_bad_max` `pace_mid_min` `pace_mid_max` | 本服 `bite_pace.go` 走「采样 × Factor 乘数 × clamp(6, `pace_bottom_max`)」，没有「区间内取随机」的分支 |
| `bite_gate_diet_min` `bite_gate_diet_floor` `bite_gate_lure_diet_min` | `fish_bite_gates.go` 注释明确写了 G1 与 G-diet 已删除：252 种鱼都有 allow 规则后，硬门只会造成「allow 写了也不咬」的黑箱，只剩水层地板 |
| `skill_grade_mult_1..4` | 本服熟练度增量固定 0.1 / 0.2，由 `fishingProficiencyLargeGainChance` 的概率决定，无档位倍数 |
| `marine_layer_enabled` | 本服没有海钓水层状态机（上游有 `marine_layer.go`），`rigWaterLayer` 只按钓组族分三种口径 |

**补上的 8 个键**（本服生效、SPA 找不到入口）：

- `pace_bottom_max`（底层杆等待上限，取代上游那套区间）
- `bait_rotation_auto_sec`（自动轮换周期，默认 7200s）、`bait_evidence_low_scale`（证据不足饵降权，默认 0.10）
- `fish_roll_trace_keep`（追踪保留条数，默认 300）、`fish_roll_trace_enabled`（追踪开关）
- `bait_rotation_auto_enabled`（自动轮换开关）
- **整组昼夜权重**：`fish_diel_weight_enabled`、`fish_diel_inactive_block`——新增一个 `diel` 分组
- `skill_gain_pct_7`（配置饵料）：上游清单漏了这一档，导致「一键百分百」时混合诱饵仍走原版衰减公式

**补上的最大缺口：鱼猎四阶段 16 个键**。SPA 原来选到「鱼猎四阶段」只显示一句
「没有要设置的比例」，实际本服 4×4 十六个权重键全部生效。现在按
`HUNT_STAGES` 生成四张表，默认值照抄后端 `fourStageWeightKeys` 的字面量。

**顺手修掉的文案错误**：`rules.js` 原来写「七项规则，预设覆盖前六项」。实际后端
`biteModeKeys` 只统管**五个**开关（硬性门/生态/天气/吃饵/分布），鱼会挑饵与欢乐时光
都不在其中。删掉海钓水层后变成六项，文案一并改为「六项规则，预设覆盖前五项」。

**踩到的坑**：

- **别拿行首正则数键**。`['key', ...` 这个形态漏掉「前缀 + 档位」拼出来的键
  （`hunt_stage_${stage}_`），也漏掉只含一段下划线的键（`xp_gain`、`bite_probability`）。
  第一次扫描因此得出「SPA 缺 54 个」的假结论，实际差集只有 5 个。
- **`Form` 的数值框不是 `type="number"`**，是 `type="text"` + `inputmode="decimal"`
  （为了统一校验与单位后缀）。测试要按 `input[id^="f_<key>_"]` 定位。
- **技能逐项数值只在「自定义」模式渲染**。`native`/`fixed100` 两种模式没有可填的项，
  测试要先喂 `skill_gain_mode: 'custom'` 才看得到九个输入框。

- [x] **验收**：`spa_batch2_ui_test.cjs` 9 个用例全绿——双向键位对表（无死键、无漏键）、
      每个 JS 键在 Go 源码里都有引用、四阶段默认值与后端一致、四张表在浏览器里真渲染出来
- [x] 25 个 `.cjs` UI 测试全绿（51 个用例），`go test ./internal/admin/` 通过
- [x] `waters` 无需改动：5 个逐水域咬钩参数（`bite_probability` / `bite_delay_scale` /
      `bite_delay_max` / `bite_delay_lure_max` / `bite_delay_fixed`）两侧完全一致
- [x] `event-gifts` 无需改动：SPA 版已用本服的 `event_stage_gifts` 与分支标签，预设从 JSON 读

### 批次 3 · 饵料（高风险，我方为准）— **已完成**

后端一行未改：`/api/bait/categories` 已返回完整六层树（含 `term` / `scope_term` /
`shared_with` / `shapes` / `brands` / `lines` / `unused`），`/api/bait/items` 支持
`cat`/`sub`/`kind`/`brand`/`base` 全参数过滤且 `matched` 与 `rows` 分离，
`/api/bait/auto-rotation` 与 `/auto-rotation/reroll` 都已注册。**缺的纯粹是前端。**

改前实测：SPA 的 `pickBait` 只用了 `sub_label` 一个层级字段，其余八个全未使用。
即以下七项能力**全部缺失**，本次逐项补上：

| 能力 | 改前 | 改后 |
|---|---|---|
| 形态层（下沉 / Pop-up） | 无 | `bait-picker.js` 形态层，只在有 `shapes` 的二级类型出现 |
| 品牌层（导航，不进规则） | 无 | 有，且**不给勾选钮**——品牌名匹配不到任何饵 |
| 型号层（写型号名 = 全规格） | 无 | 有，写 `lineToTerm(ln)` = `scope_term \|\| term \|\| base` |
| `类别@饵名` 限定写法 | 无 | 有，`scope_term` 直接来自后端，前端不自己猜 |
| 同名型号提示 | 无 | `同名×N` 徽标 + 预览里点名「同名型号还出现在…」 |
| 规格层（选了型号才拉） | 无 | 有，数量取 `matched` 不取 `rows.length` |
| 批量勾选暂存 | 无 | 有，`已选`（暂存）与`已在列表`（已生效）视觉分开 |

同时补上三处旧后台有、SPA 完全没有的功能：
- **自动抽取开关**（按鱼，`POST /api/bait/auto-rotation`，带 `fish_key` 不带中文名）
- **本期开口**列（`rotation.quota`/`pool_size`/`active_baits`，三种状态分别画）
- **立即重抽本期**（`POST /api/bait/auto-rotation/reroll`，先确认再执行）

顺手修掉的 SPA 缺陷：
- **保存丢主键**：原来只传 `fish_zh`。00083 迁移后规则表主键是 `fish_key`，
  只传中文名会让「同一鱼种多名」（匈红鲤(镜鲤) / 匈红鲤（镜鲤））再次分裂成两行。
  已改为 `fish_key` + `fish_zh` 双传，`reset` 同样带上。
- **手工轮换组整块移除**：SPA 还留着 `groups` / `rotate_sec` / `rotate_mode` 的编辑 UI
  并在保存时一起提交，但旧后台 2026-11-24 已按用户要求删掉这套（改由「自动抽取」管）。
  留着等于让人编了一堆后端不再读的组。已一并移除。
- **前置条件是自由文本框**：改为下拉 + 区号输入，按服务端认的原句拼回（`海图30或34号区域`）。

- [x] **验收**：`spa_batch3_ui_test.cjs` 5 个用例全绿——
      本期开口三态与自动抽取开关、六层级联（越深越窄 / 品牌不进规则）、
      形态层与跨类别同名限定写法、前置条件反解与拼回、手工轮换组已移除
- [x] 旧后台三个测试（`bait_picker_cascade` / `bait_rules_scope` / `bait_rules`）零回归
- [x] `fish_bait_auto_ui_test.cjs` 指旧后台的 `#fish` 钓场页，断言的是旧表格结构。
      批次 6 处置：它验的「本期开口三态」已被 `spa_batch3_ui_test.cjs` 完整覆盖，
      按「分类处理」退役归档到 `_retired_legacy_tests/`，没有重复重写

### 批次 4 · 生态与昼夜（最高风险，我方独有）— **已完成**

四个页签：生态基线 / 饵料规则 / 经济四档 / 权重档位。
后端**一行未改**：`/api/fish/ecology-species`（GET/POST/reset）、`/api/econ/species`
（GET/save/reset/preview）、`/api/fish/weights/preset`（GET/POST）与 `/weights/restore`
都已注册且返回结构够用。缺的纯粹是前端。

**页面形态按用户要求改为卡片式**（2026-11-26）：生态与经济都不再是「一张宽表 + 编辑弹窗」，
改为与鱼饵页一致的卡片列表。253 行的宽表在窄屏上必须横滚，而这两页真正需要横向对照的
只有「窗宽」和「四档阶梯」两项——做成条形图和一排四格比塞进单元格有用得多。
编辑器仍用侧页（`sidePage`），表单密集处保留表格（那是填表，不是浏览）。

文件拆成四个，避免单文件胀到 900 行以上：

| 文件 | 职责 |
|---|---|
| `pages/ecology.js` | 页签切换与共享常量（`ECON_TAB_META` / `TIER_NAMES`），自建 `#ecoTabs` |
| `pages/ecology-eco.js` | 生态基线：卡片列表 + 编辑器（约 480 行） |
| `pages/ecology-econ.js` | 经济四档：卡片列表 + 四档编辑器 + 试算 |
| `pages/ecology-weight.js` | 权重档位：三档表格 + 确认重设 + 回滚 |

**页签不用 `core.pageTabs` 的原因**：它产出 `<a aria-current>` 并写子路由
`#ecology/weight`。四个页签应共用同一条 hash（否则刷新落在子路由上、面包屑末级与左栏
高亮都对不上），且验收测试按 `#ecoTabs button[data-etab="weight"]` 定位并读 `.on`。
所以页签自建为按钮组，切换只重画内容区、不改 hash。

**移植时守住的口径**（每一条都对应一次真实踩坑）：

| 要点 | 为什么 |
|---|---|
| 覆盖值判据是 `!= null`，不是真值 | `0` 是合法覆盖值：水层 0 = 水面、季节掩码 0 = 任何时段都不活跃 |
| 留空下发 `null`，绝不变 `0` | 空串转 `0` 会写进一份「覆盖」，含义与「继承」完全不同 |
| 五个偏好矩阵**根本不下发** | 发 `null` 会把已有矩阵清成「继承基线」= 静默丢数据；界面也无输入框 |
| 曲线与昼夜勾选打架时拦住保存 | 曲线非空时判鱼只认曲线，勾选仅展示。界面写「已覆盖」而咬口没变是最难排查的一类 |
| 只在**本次真的动过**昼夜勾选时才校验 | 库里可能有历史数据本来就曲线/掩码不匹配，用户只想改水温时不该被挡 |
| 冷暖筛选用**四分位**，不写死温度 | 253 种偏好温度实际只落在 9.9~21.2℃，写死 22 会让「暖水鱼」永远 0 行 |
| 试算必须摊开当日行情系数与算式 | 卖价 = 基准价 × 水域倍率 × 当日行情 × 克重 × 新鲜度。不写出来，「1kg 却比每公斤价高」必被当算错 |
| 权重档位严格手写三键 | 验收测试对 POST body 做 `deepEqual({tier0,tier1,tier2})`；第四档不存在 |

顺手补的两处 SPA 缺失：
- **鱼口表的稀有度与昼夜说明条**（`waters.js`）：`.params-bar` 讲「档位是鱼种固有属性
  （253 种：普通 169 / 稀有 45 / 超稀有 39）」，鱼种行标档位 +「档位权重 N」，
  「按稀有度重设权重」跳 `#ecology/weight`。整页不含任何按渔获量切的阈值，也没有
  「重算稀有度」与「钓法倾向」入口。
- **`#baitTable` / `#fishRows` 锚点**：卡片化后容器换了实现，但锚点沿用旧后台，
  否则验收测试等于失明。

- [x] **验收**：`ecology_species_ui_test.cjs`（4 例）、`econ_ui_test.cjs`（1 例）、
      `fish_rarity_ui_test.cjs`（1 例）改指向 SPA 后全绿
- [x] `spa_batch4_ui_test.cjs` 5 个用例全绿——页签懒加载（一进页面只查生态）、
      卡片形态（无 `<table>`）、五个矩阵不下发、试算摊开行情、权重只有三档
- [x] 全量回归：26 个 `.cjs`（61 个用例）+ `go test ./...` 全绿
- [x] 视觉验收：四个页签、生态编辑器、鱼口页逐张截图核对

### 批次 5 · 天气（高风险，我方独有）· **已完成**

移植 21 个天气函数。与客户端预报、判鱼共用同一套数据（游戏 1 小时 = 现实 150 秒）。

**后端零改动**：`/api/weather/maps`、`/api/weather/preview`（不传 map 走全图概览、
传 map 走单图详细）、`/api/weather/active-fish` 三个 handler 在 `admin/weather_preview.go`
里早就注册好了，返回结构与 `gameserver.WeatherPreview` 完全够用。缺的纯粹是前端。

前端集中在一个文件：**`pages/weather.js`**（约 520 行）。六个区块（7 天卡片 / 24 小时曲线 /
温度分档 / 四季对比 / 全区对比 / 活跃鱼种）都在这一页上，拆开反而让「切图要联动五块」
的同步关系散落到五个文件里。

**移植时守住的口径**（每一条都对应一次真实的坑）：

| 要点 | 为什么 |
|---|---|
| 采样点数是 `days × 24` **算出来**的 | 旧后台把「168 个采样点」写死在文案里；天数一改成 3 或 14 就变成假话 |
| 地图清单**只取一次** | 每次切图重拉 `/api/weather/maps` 会重建下拉，正在选的那项会闪 |
| 改天数**不重取**活跃鱼种 | 那张表只跟当前游戏时刻与地图有关；跟着重取要多扫一遍鱼种生态表 |
| 切图**不重取**全区对比，只切高亮行 | 对比表只跟天数有关。18 张图 × 14 天要扫全部天气模板 |
| 前端只画不算 | 水温高斯响应、昼夜曲线都在 gameserver 里。前端抄一份，后台显示的「活跃鱼种」迟早和客户端咬口不一致——那正是这一页存在的理由 |
| 日号是世界时钟轴，**原样显示** | 2026-11-23 曾重置成「开服第 4 天」，页面显示「1 月 5 日」而季节标签是秋。现在与客户端「游戏中 N 天」同源，玩家报天数可直接对数 |
| 当前时刻竖线固定 `#42b8ff` | 它是「此刻」的唯一标记，不随主题变；验收测试也按这个色值确认「只有今天有竖线」 |
| 「不含饵料、水层、钓组」必须写在页面上 | 否则管理员会把它当成「挂这套一定钓得到」 |

**7 天卡片不是 `<button>`**：`theme-aerial.css` 有一条全局
`button:not(...){background:#101d1a!important}`，把普通按钮涂成深墨块。
7 张深色砖在浅色主题下和整页打架（08:00 截图核对时实际踩到，卡片整排是黑的）。
改成 `div + role="button" + tabindex` 并自己补 Enter/Space，键盘可达性没丢。

**顺手修掉一个通用 SPA 缺陷**：主题选择原来只在总览页的 `themeMenu()` 里写
`<html data-theme>`。直接深链到别的页（或刷新在别的页）时属性根本没写上，
而 `theme-aerial.css` 里有几十条 `:root[data-theme=dark] …{…!important}` 依赖它——
结果系统深色下输入框是白底、文字却是浅色，肉眼一片空白。现在 `boot()` 开头
先从 `localStorage` 恢复一次主题属性，再建框架。

**样式全部改走主题变量**：旧后台这一页是硬编码深色（`#0e1b29` / `#0c1826` / `#0a1725` /
`#08131f` / `#1b2e42` / `#172a3d` 一堆字面量），搬到明暗双主题的 SPA 里必须换成
`var()` + `currentColor`，否则浅色主题下卡片是黑砖、温度刻度是灰字看不见。
昼夜分段底色也从 `.55` 压到 `.20`——实测 `.55` 会把曲线、网格、刻度全糊住。

- [x] **验收**：`weather_ui_test.cjs` 改指向 SPA 后全绿（1 例，10 组断言）
- [x] `spa_batch5_ui_test.cjs` 5 个用例全绿——采样点数算出来、地图清单只取一次、
      前端只画不算、口径文案在页面上、卡片不是 `button` 且没搬旧深色硬编码
- [x] 全量回归：29 个 `.cjs`（67 个用例，含 `RF4_TEST_BROWSER` 指定 Edge）+ `go test ./...` 全绿
- [x] 视觉验收：明暗两套主题各一张全页截图，逐块核对（卡片、曲线、分档条、四季、对比表、鱼种表）

### 批次 6 · 切换与收尾 · **已完成**

这一批不再是「移植某个页面」，而是把并行的两套收敛成一套，并把迁移期攒下的
债一次还清：入口切换、测试归属、空态与裸值的统一、文档。

#### 6.1 入口切换

| 文件 | 改动 |
|---|---|
| `web/index.html` | 410 KB 单文件后台 → **516 字节 SPA 空壳**（挂载点 + 两份 CSS + `app/main.js`） |
| `web/legacy.html` | 旧后台原封不动搬过来，**只留兜底，不再更新** |
| `web/spa.html` | 删除（内容进了 `index.html`） |

部署形态与 `BASE` 推导都验证过：`/admin/`、`/admin/index.html`、独立端口 `/` 三种
访问方式下 API 前缀都正确。`//go:embed all:web` 的 `all:` 前缀保证以后新增的
非文本资源自动内嵌，服务端**仍然零改动**。

#### 6.2 旧后台的 18 个测试怎么处置

这是方案文档最初没预料到的问题：这些 `.cjs` 测试大量直接调旧后台的全局函数
（`applyEventRewardPreset`、`saveEventStageGifts`…）、依赖固定 id（`#eventGiftBranch`、
`#eventStageGiftJson`）。SPA 是模块化的，一个全局都不暴露、状态全在闭包里，
**改路径无效** —— 界面在，但驱动方式完全不同。

按「分类处理」拆成三类：

| 类别 | 处理 | 数量 |
|---|---|---|
| 已被 `spa_batch1~5` 覆盖 | 归档到 `_retired_legacy_tests/`，附 README 说明为什么退役 | 4 |
| 有独特业务断言、能改写成 SPA 版 | **整份重写**，业务断言一条不减，只换驱动方式 | 13 |
| 只验「旧后台还能打开」 | 收进 `spa_parallel_ui_test.cjs` 的一条轻量冒烟 | 1 |

重写的原则是「断言照旧、驱动换掉」。以 `event_stage_gifts` 为例，原来靠调
`applyEventRewardPreset` 验「预设只填空阶段」，现在改成点界面上的下拉与按钮，
验的还是「已有内容的阶段不被覆盖、保存前不落库」。
`fish_hunt_stage` 甚至借重写纠正了我方一个旧做法（见下）。

#### 6.3 重写过程中发现并修正的真实缺陷

| 缺陷 | 影响 | 修法 |
|---|---|---|
| 文档页空态只有四个字「没有更新日志」 | 用户分不清是文件真空、没打包进发布包、还是接口坏了 | `docs.js` 的空态补「服务端返回了什么 + 文件本该在哪 + 源码直跑属正常」 |
| 预览版按钮丢了构建号 | 同一天可能有多个预览包，报问题时说不清装的是哪个 | `main.js` 加 `previewLabel`，带上 `preview_version` |
| 长标识撑破侧栏（`shop` 页横向溢出 647px） | 店 ID / 鱼 key / 道具 key 是一长串不折的字符，把 `<dl>` 顶宽、再把整页撑出横向滚动条 | `theme-aerial.css`：`.ib dl dd` 加 `min-width:0` + `overflow-wrap:anywhere`，`.ib dl dt` 钉住不缩，`.storebar select` 补 `min-width:0`（原来只在 ≤1260px 的媒体查询里有） |
| 长说明文字撑破页面 2727px | `max-width:56ch` 只限「按字符平均宽度算的宽度」，管不住单个 420 字符的不可断词 | `.row .lab` 加 `min-width:0`，`.lab b/small` 加 `overflow-wrap:anywhere` |
| `fish_hunt_stage` 的占比保存会漂移 | 旧后台每次把四档归一化后的百分比整组写回，归一化有舍入，反复保存数字会慢慢走样 | SPA 只写用户改过的那一个键、写的是**原始相对权重**，归一化交给后端 `fourStageGradeWeights` 每次读取时算。测试按新语义重写并写明理由 |
| 页面上出现 `Lv.undefined` / `UID null` / `NaN kg` | 数据库里字段为 NULL 或静态库派生不全时，模板直接把 `undefined`/`null` 拼进字符串 | 见 6.4 |

#### 6.4 空态与裸值统一

原来的空态大多只有一句话（「没有记录」「没有数据」「没有找到」）。这三件事
在界面上长得一样，但处置完全不同：**去导入数据 / 放宽筛选 / 报障**。

统一后的写法是「标题 + 一句为什么 + 下一步去哪」，例如：

```js
// 「一条都没有」和「筛选后一条都没有」是两件事：前者要去看看后台是不是刚装好，
// 后者只要放宽筛选。只写「没有记录」时用户分不清是哪一种，会白翻一遍类型按钮。
empty(list.length ? '没有符合的记录' : '还没有任何记录', list.length
  ? `最近 ${list.length} 条里没有符合当前类型和关键词的。换个类型，或者把搜索词清空。`
  : '后台还没人做过修改。第一次改参数、发物品、发邮件、封禁玩家，都会记在这里。')
```

覆盖 19 个路由、约 40 处空态。同时顺手修掉两个同源问题：

- **`catch(() => [])` 把失败伪装成空结果**。`waters` 的鱼种搜索和 `players` 的
  放鱼搜索都这么写过 —— 接口挂掉和「确实没这个鱼种」在界面上完全一样，
  用户会一直换关键词试。改成失败照实报「鱼种库读取失败」。
- **`String(null)` 得到 `"null"`**。`p.uid ?? '—'` 只兜 `undefined`，兜不住 `null`；
  数值字段先用 `Number()` 再 `toFixed()` 会渲染成 `NaN`。补了一个 `numText()` 助手，
  并统一改成 `x == null ? '—' : …`（`==` 同时覆盖 `null` 和 `undefined`）。

#### 6.5 新守门测试

`spa_empty_state_ui_test.cjs` —— 两个用例，防的就是上面这类回退：

1. **脏数据下不渲染裸值**。用「键都在、值全 null」的对象喂 19 个路由，
   抓可见文本里的 `null` / `undefined` / `NaN` / `[object Object]`。
   空数组测不出这个问题（页面走空态分支），必须用「对象在、字段没了」的脏数据。
2. **空态不能是一句话**。接口全回空，逐个路由扫 `#view .empty` 的文字，
   门槛定在 14 字（一句话的标题通常 4–10 字）。加载态（「读取中…」）排除在外。

#### 6.6 验收

- [x] 全量回归：25 个 `.cjs` + `go test ./...`（18 个包）全绿
- [x] `view-sync` 已迁移为 `pages/sync.js`，并有 `spa_parallel_ui_test.cjs` 守门
- [x] 裸 `null` / 空态文案统一（19 路由、约 40 处，附新守门测试）
- [x] 入口切换：`index.html` → SPA，旧后台 → `legacy.html`，`spa.html` 删除
- [x] `docs/admin-console.md` 补「前端入口与目录结构」；`.cjs` 测试路径全改指 SPA；
      `bait_rotation_sync_test.go` 改按 `tap` 字段断言
- [x] 视觉验收：明暗两套主题全页截图

#### 6.7 切换到 SPA 后，配置上要知道的变化

`docs/admin-console.md` 里写的是长期约定，这里只记迁移期的操作差异：

- 刷书签：旧后台的深链是 `#view-hooks` 这种形式，SPA 用 `#hooks`。
  **旧书签会落到默认页**，不是报错。`legacy.html` 里那些深链仍然可用。
- 改前端要**重新编译**：前端是 `go:embed` 进去的，改完 `.js`/`.css` 必须重新 build
  `launcher` 才生效 —— 直接刷新浏览器拿到的是旧二进制里的旧文件。
  这一点和迁移期最不一样：以前改 `index.html` 也是要重编译的，但那时改得少。
- 没有「刷新单页」这回事了：SPA 的路由是 hash，`F5` 会重新加载整个应用（含登录态）。
  切换页面用左栏，不要再按 F5。

---

## 5. 迁移期纪律

1. **旧后台一行不碰**。所有新代码只进 `web/app/`，`index.html` 在批次 6 之前保持原样。
2. **不做双写**。同一功能迁移完成后，旧后台对应代码保留但不再修改，避免两侧同时演进。
3. **每批一个提交**，提交信息写明批次号与本批验收项。
4. **不擅自 push**。本地提交后停下，等明确指令。
5. **迁移编号不要再撞车**。我方 migrations 已用到 `00084`，上游到 `00073`；
   后续新增迁移从 `00085` 起，不重排历史。

---

## 6. 风险与对策

| 风险 | 说明 | 对策 |
|---|---|---|
| **静默功能丢失** | `bait_consumption.go` 类问题：无冲突标记但老版本覆盖新版，编译测试都能过，功能没了 | 每批必须做「旧后台能做的操作，新平台也能做」的逐项对照，不能只看测试绿 |
| SPA 空态渲染裸 `null` | 已实证存在 | **已做**：批次 6 统一 19 个路由的空态与取值兜底，并用 `spa_empty_state_ui_test.cjs` 守门 |
| 参数键位漂移 | 两侧 tunables 键不完全一致 | **已做**：批次 2 全量对表，删 14 个死键、补 8 个漏键 + 四阶段 16 键，并用 `spa_batch2_ui_test.cjs` 双向守门 |
| 迁移编号撞车 | 00070 起全部撞车但内容不同 | 依赖 `retireStaleMigrationRows` 自愈；新增从 00085 起 |
| 浏览器内核缺失 | `.cjs` 测试默认起不来 | 统一用 `RF4_TEST_BROWSER` 指向系统 Chrome |

---

## 7. 日常使用方式

迁移已完成，现在只有一套：

```
管理后台  http://127.0.0.1:9500/admin/
旧后台    http://127.0.0.1:9500/admin/legacy.html   （兜底，不再更新）
```

`legacy.html` 只在 SPA 出问题、需要翻旧界面确认某个数据怎么改时用。
两边共用同一个后端与数据库，改任何一边的数据，另一边刷新即可见 ——
但**别再往 legacy 里加东西**，它不会跟着前端演进。

工作目录：

```
主仓库（旧平台稳定版）  G:/Localfish/rf4_server_simulator        分支 main
迁移仓库（已完成合并）  G:/Localfish/rf4-spa-migration          分支 feat/spa-migration
```

`main` 与迁移分支互不干扰；迁移分支合回 `main` 之后，这两个 worktree 就可以收成一个。