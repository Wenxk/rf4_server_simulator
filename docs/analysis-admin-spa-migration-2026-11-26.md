# 后台 SPA 并行迁移方案

> 目标：把上游 `index.html` 单页 SPA 那套后台拉进本地，与现有旧后台**并行存在**；
> 旧后台保持可用，功能逐个迁移到新平台，全部迁完再切换默认入口。
>
> 状态：**骨架已落地，功能零迁移**。本文既是方案，也是验收清单。
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
| `view-sync` | **无** | 待定 | SPA 无对应页 |
| `view-ecology` | **无**（`bait.js` 仅 218 行，且是老术语） | **高** | 我方独有 33 个生态函数 |
| `view-weather` | **无** | **高** | 我方独有 21 个天气函数 |
| — | `pages/event-gifts.js`（227 行） | — | SPA 独有，我方在 `view-fish` 内嵌 |

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

### 批次 2 · 鱼口与参数（中风险，双向合并）

`rules` + `waters` + `tuning`

- 以 SPA 结构为骨架
- 把我方 `view-fish` 里的活动奖励、阶段礼包逻辑并入 `event-gifts`
- 把我方 tunables 的完整键位表灌进 `tuning.js`
- **注意**：`bite_pace_enabled`（饵效控速开关）等上游新增键，我方已有等价物，以我方为准

- [ ] **验收**：参数项数量两侧一致；鱼口页出鱼结果与旧后台同输入同输出

### 批次 3 · 饵料（高风险，我方为准）

移植 14 个饵料函数到 `pages/bait.js`：
- 多级联动选择器（分类 → 子类 → 具体饵）
- `类别@饵名` 写法与解析
- 型号级抽签（`红蚯蚓（10/14/18）· Pop-up` 视为等价）
- 形态后缀（煮饵/Pop-up/颗粒/玉米/活饵）
- `rerollBaitAuto`（立即重抽本期）

- [ ] **验收**：`bait_rules_ui_test.cjs`、`bait_picker_cascade_ui_test.cjs`、
      `bait_rules_scope_ui_test.cjs`、`fish_bait_auto_ui_test.cjs` 改指向 SPA 后全绿

### 批次 4 · 生态与昼夜（最高风险，我方独有）

移植 33 + 9 个函数，这是整个迁移的核心工作量。

四个页签：生态基线 / 饵料规则 / 经济四档 / 权重档位。

要点：
- 温度窗口 → 热响应曲线陡缓（中位宽度 20.2℃）
- 季节掩码 1春/2夏/4秋/8冬，昼夜掩码 1夜/2晨/4昼/8昏
- 水层 0=水面 1=水底，±0.15 容差，带外权重 ×0.01
- 经济四档阶梯：经验 1:3.27:20.4:40.8、鱼价 1:1.5:3:6

- [ ] **验收**：`ecology_species_ui_test.cjs`、`econ_ui_test.cjs`、
      `fish_rarity_ui_test.cjs` 改指向 SPA 后全绿；逐鱼种抽查覆盖保存与回读

### 批次 5 · 天气（高风险，我方独有）

移植 21 个天气函数。与客户端预报、判鱼共用同一套数据（游戏 1 小时 = 现实 150 秒）。

- [ ] **验收**：`weather_ui_test.cjs` 改指向 SPA 后全绿；天气预览与客户端一致

### 批次 6 · 切换与收尾

- [ ] 全量回归：22 个 `.cjs` + `go test ./...` 全绿
- [ ] `view-sync` 决定去向（迁移或废弃）
- [ ] SPA 各页面的裸 `null`、空态文案统一修一遍
- [ ] **切换**：把 `index.html` 换成 SPA 入口，旧后台改名 `legacy.html` 留存
- [ ] 收尾：更新 `docs/admin-console.md`、`.cjs` 测试路径、`bait_rotation_sync_test.go:26`

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
| SPA 空态渲染裸 `null` | 已实证存在 | 批次内顺手修，列入验收 |
| 参数键位漂移 | 两侧 tunables 键不完全一致 | 批次 2 做一次全量键位对表 |
| 迁移编号撞车 | 00070 起全部撞车但内容不同 | 依赖 `retireStaleMigrationRows` 自愈；新增从 00085 起 |
| 浏览器内核缺失 | `.cjs` 测试默认起不来 | 统一用 `RF4_TEST_BROWSER` 指向系统 Chrome |

---

## 7. 日常使用方式

迁移期间：

```
旧后台（日常用）  http://127.0.0.1:9500/admin/
新 SPA（迁移中）  http://127.0.0.1:9500/admin/spa.html
```

两套共用同一个后端与数据库，改任何一边的数据，另一边刷新即可见。

工作目录：

```
主仓库（旧平台稳定版）  G:/Localfish/rf4_server_simulator        分支 main
迁移仓库（骨架+迁移）    G:/Localfish/rf4-spa-migration          分支 feat/spa-migration
```

`main` 与迁移分支互不干扰；随时可在主仓库构建部署旧平台。