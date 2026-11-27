# 后台 SPA 迁移 · 验收核对报告

> 核对对象：`docs/analysis-admin-spa-migration-2026-11-26.md`（六批全部完成、
> 默认入口已切到 SPA 的那一版）
> 核对时间：2026-11-27
> 分支：`feat/spa-migration`（HEAD `7a168f5`，之后补了 `d50d89c`）
> 结论：**文档里列出的可验证主张全部成立**；发现 4 处文档描述与实际不符（不影响功能），
> 3 处核对工具自身的坑已修在工具里。

---

## 0. 核对方法

不只看「测试绿不绿」——测试绿只证明测试写到的那些点没坏。这次分四层：

| 层 | 做法 | 覆盖 |
|---|---|---|
| 入口与结构 | 直接核文件、`go:embed` 指令、`BASE` 推导逻辑、路由表 | 3 个入口文件 + 15 条路由 |
| 功能主张 | 把文档里写明的每条「口径」翻成一条断言，用真实浏览器取证 | 20 条（`acceptance_check.cjs`） |
| 旧后台能力 | 从 `legacy.html` 提取 13 个视图的可操作件，与 SPA 逐页对照 | 129 个 `onclick` 动作、13 视图 |
| 回归 | 重跑全部 `.cjs` 与 `go test ./...` | 25 文件 / 74 用例；18 个包 |

新增三个可复用的核对工具（都在 `internal/admin/`，不是一次性脚本）：

| 工具 | 作用 |
|---|---|
| `inventory_check.cjs` | 导出旧后台 13 视图与 SPA 15 页的可操作件清单，供人工对照 |
| `inventory_tabs.cjs` | 同上，但会进二级页签（生态 4 页签、钓场 3 页签、水域 2 页签） |
| `acceptance_check.cjs` | 文档口径逐条断言（20 条），失败时打印现场文字 |

---

## 1. 入口与结构 —— 全部属实

| 文档主张 | 实测 | 结论 |
|---|---|---|
| `index.html` 是 516 字节 SPA 空壳 | **516 字节**，`<title>RF4 管理后台</title>` | ✅ |
| `legacy.html` 是旧后台 | **410950 字节**，`<title>RF4 服务器助手</title>` | ✅ |
| `spa.html` 已删除 | 工作树与版本库都没有 | ✅ |
| `//go:embed all:web` 无需改动 | `embedded.go:8` 就是这一行 | ✅ |
| `BASE` 按路径结尾推 `/admin` | `/admin/`→`/admin`、`/admin/index.html`→`/admin`、`/`→空 | ✅ |
| 独立端口无前缀 | `cmd/admin/main.go` 确实挂在根路径 | ✅ |

补充一条文档没写的边界：`/admin`（无尾斜杠）会推出**空前缀**，但
`cmd/launcher/main.go:131` 对它做了 307 → `/admin/`，所以实际触达不到。不是问题，
但记下来：以后若给 admin 换挂载点，这个 307 是必须保留的。

**路由表：15 条**，与文档 3.1 的页面清单对得上。

---

## 2. 功能主张逐条核对 —— 20/20 成立

`acceptance_check.cjs` 的输出：

```
PASS  B1-1   钩号显示成 S2/S25 而不是 #1
PASS  B1-2   玩家在线状态三态（在线 / 离线 / 状态未上报 都能渲染）
PASS  B2-1   鱼猎四阶段 16 个权重表渲染出来
PASS  B2-2   参数页有昼夜权重两键（fish_diel_weight_enabled / fish_diel_inactive_block）
PASS  B2-3   参数页有 bait_rotation_auto_sec 与 bait_evidence_low_scale
PASS  B2-4   参数页不再有本服不读的死键（pace_good_min / skill_grade_mult_1）
PASS  B2-5   参数页「自定义」模式下含 skill_gain_pct_7（配置饵料）
PASS  B3-1   饵料规则页有「立即重抽本期」与自动抽取开关
PASS  B3-2   饵料页有水域筛选（旧后台 baitLoc 对应物）
PASS  B4-1   生态页四个页签齐全（eco/bait/econ/weight）
PASS  B4-2   生态页签切换不改 hash（四个页签共用 #/ecology）
PASS  B4-3   权重档位只有三档（普通 / 稀有 / 超稀有，无第四档）
PASS  B4-4   鱼口表有稀有度与昼夜说明条（253 种 / 普通 169 等）
PASS  B5-1   天气页写明「不含饵料、水层、钓组」的口径
PASS  B5-2   7 天卡片不是 <button>（避开全局 !important 墨块规则）
PASS  B5-3   当前时刻竖线用固定色 #42b8ff
PASS  B5-4   采样点数按 days×24 算出并写在页面上
PASS  B5-5   深链到天气页时 <html data-theme=dark> 也被写上（不只总览页）
PASS  B6-1   参数同步页存在且有导出/导入
PASS  B6-2   审计页空态说明「为什么空 + 下一步」
===== 20/20 通过，0 项不符 =====
```

值得单独说的两条：

- **B4-2**（页签不改 hash）是文档批次 4 特意解释过的设计——四个页签共用 `#/ecology`，
  不用 `core.pageTabs` 的 `#ecology/weight` 子路由，否则刷新会落在子路由上、面包屑末级与
  左栏高亮都对不上。实测点 `weight` 页签后 hash 仍是 `#/ecology`，属实。
- **B5-5** 是批次 5 顺手修的通用缺陷（主题属性只在总览页写）。用深链直接进天气页
  且 `localStorage['rf4.theme']='dark'`，`<html data-theme>` 确实是 `dark`。

---

## 3. 旧后台能力对照 —— 没有静默丢失

文档第 6 节风险表第一条要求「旧后台能做的操作，新平台也能做」，这是迁移里最容易
翻车的一类（编译测试都过、功能没了）。

### 3.1 玩法开关（6 个，一一对应）

| 旧后台 | SPA | 状态 |
|---|---|---|
| `biteGateSwitch` | `f_bite_gate_enabled` | ✅ |
| `fishEcologySwitch` | `f_fish_ecology_enabled` | ✅ |
| `fishWeatherSwitch` | `f_fish_weather_enabled` | ✅ |
| `fishRulesSwitch` | `f_fish_bait_rules_enabled` | ✅ |
| `fishDistributionSwitch` | `f_fish_distribution_enabled` | ✅ |
| `happyHourSwitch` | `f_happy_hour_enabled` | ✅ |

SPA 还多了「全随机 / 真实鱼汛」两个预设，旧后台没有。

### 3.2 出鱼与奖杯（三种模式齐全）

旧后台 `saveGradeRatio` / `saveTwoStage` / `saveFourStage` 三个入口，对应 SPA
「出鱼与奖杯」页签里的三个区块：按比例出鱼（四档权重 + 滑杆 8 个输入框）、
日常/打窝两档、鱼猎四阶段。实测 `#pg-grade-0..3` 与 `f_hunt_stage_*` 都在。

### 3.3 鱼群区域编辑器（20+ 个动作 → 4 种工具）

旧后台有 `zmapSetTool` / `zmapPin` / `zmapResetView` / `zoneStartNew` /
`zoneStartBrush` / `zoneFromLatestCatch` / `zoneClearCells` / `zoneAppendLatest`
等 20 多个动作。SPA 收敛成 4 种工具：

| 旧后台动作 | SPA 工具 | 状态 |
|---|---|---|
| `zoneStartNew` | `rect`（矩形） | ✅ |
| `zoneStartBrush` | `brush`（自由形状） | ✅ |
| （圆形） | `circle`（圆形） | ✅ |
| （海图区号） | `sea_zone` | ✅ |
| `zmapResetView` / `zmapZoomStep` | `pan`（移动）+ 滚轮缩放 | ✅ |
| `zoneFromLatestCatch` / `zoneAppendLatest` | 见下 | ⚠️ |

**唯一的功能收敛**：旧后台能从「最近一次渔获」直接生成区域（`zoneFromLatestCatch`
/ `zoneAppendLatest`），SPA 没有这个入口。这不是丢失——`zones.js` 是手绘区域编辑器，
从渔获反推区域在 253 种鱼 × 18 张图下命中率极低，且要读 `fish_roll_trace`（追踪开关
默认关）。但它确实是一条操作差异，**建议在文档里记一笔**，否则老用户会以为找不到。

### 3.4 其余页面

| 旧后台视图 | SPA 页面 | 结论 |
|---|---|---|
| `view-players`（3 个控件） | `players`（26 个） | SPA 更细：档案/背包/鱼护/记录/技能/邮箱 6 页签 + 新建/发信/踢人/封禁 |
| `view-stores` | `shop` | ✅ 另有下拉选店（旧后台 205 个按钮） |
| `view-hooks` | `hooks` | ✅ 钩号用 `display_size`（B1-1 已验） |
| `view-mail` | `mail` | ✅ 模板、附件、预览都在 |
| `view-params` | `tuning` | ✅ 44 个控件；旧后台此视图运行时生成，静态抓不到（数出 0 个是工具局限） |
| `view-audit` | `audit` | ✅ 同上 |
| `view-about` / `view-release` | `docs.js` | ✅ |
| `view-sync` | `sync` | ✅ 批次 6 移植，183 行 |
| `view-ecology`（14） | `ecology` 4 页签 | ✅ 含「立即重抽本期」 |
| `view-weather`（4） | `weather`（25） | ✅ 六区块 |
| `view-fish`（10） | `rules` + `waters` + `event-gifts` | ✅ |

---

## 4. 回归 —— 与文档 6.6 一致

```
.cjs   : 25 个文件 / 74 个用例，全绿（约 7 分钟）
go test: 18 个包，全绿
```

与文档 6.6 写的「25 个 `.cjs` + `go test ./...`（18 个包）全绿」完全对上。

> 注：批次 5 那节写的是「29 个 `.cjs`（67 个用例）」。数字变化是因为批次 6 把 4 个
> 旧后台测试归档到 `_retired_legacy_tests/`（不再计入 `*_ui_test.cjs`），同时新增
> `spa_empty_state_ui_test.cjs`。文件数 29→25、用例 67→74，不是回归退化。

---

## 5. 发现的文档偏差（4 处，均不影响功能）

| # | 文档写的 | 实际 | 建议 |
|---|---|---|---|
| 1 | 3.1 表格：`zones.js` 是「我方没有的**独立钓点页**」 | `zones.js` 不是独立路由，由 `waters.js` 作为「鱼群区域」页签内嵌（`main.js` 的 `PAGES` 里没有 `zones`） | 3.1 是迁移启动时的盘点、已注明「保留原样作对照」，可不改；但第 7 节「日常使用方式」若提到页面数，应说 15 页而非 16 |
| 2 | 3.1 表格：`view-fish` → `rules.js` + `waters.js` + `zones.js`（1053 行） | 实际三文件 290 + 325 + 588 = 1203 行（迁移后补了功能，本来就该更长） | 表格已注明「行数是上游原始行数」，不用改 |
| 3 | 批次 5：全量回归「29 个 `.cjs`（67 个用例）」 | 批次 6 后是 25 个 / 74 个 | 这是逐批记录，保留历史值合理；6.6 已写当前值 |
| 4 | 未记录：`zoneFromLatestCatch` / `zoneAppendLatest` 没有对应入口 | 见 3.3 | **建议补一条操作差异说明** |

---

## 6. 核对工具自身踩的坑（已修进工具，避免下次复发）

这轮排查有相当一部分时间花在**核对工具写错、误判成产品缺陷**上。这些坑都写进
`acceptance_check.cjs` 与 `inventory_check.cjs` 的注释里了：

| 坑 | 现象 | 正确做法 |
|---|---|---|
| `document.querySelector('#view, main, body')` | 按**文档顺序**返回第一个命中项，`body` 是祖先永远排最前 → 每页都数出同一个大数字（5 个页面都是 44 个控件） | 只给一个具体容器 `#view` |
| `page.goto()` 到同文档的另一个 hash | Playwright 只改 hash、**不重新加载文档**，SPA 的 hashchange 是异步的，连着 goto 会读到上一页残留 | 页面内 `location.hash = …` + 等落定 |
| `render()` 不先清空旧页（设计如此，避免 hero 闪） | 数据没回来时 `#view` 里还是上一页 | 等「连续两次文字完全一致且不 busy」；只比长度会误判（两页长度可能相同） |
| **`page.route` 多条规则的优先级** | 我原以为是「先注册者优先」，实测**后注册的会覆盖先注册的**：先注册专用路由、后注册 `**/*` 兜底，兜底会吃掉专用 | 只用一条 `**/*`，在 handler 内部按路径分支 |
| glob `**/api/x` | 不匹配 `/api/x`（`**/` 要求前面至少一个路径段） | 同上，用单 handler 分支；或正则 `/\/api\/x$/` |
| `{v,src}` 当 `/api/tunables` 的 fixture | 值是字面量 `[object Object]` → 「原值 ≠ 当前值」→ 未保存守卫拦住导航 → 后续所有页卡住。**排查了一整圈，产品本身没问题** | 真实返回是扁平 `map[string]string`（`store.TunableList` 就是 `SELECT key,value`） |
| 无头浏览器 `confirm()` 默认 `false` | 未保存守卫把 hash 拽回原处，页面看起来「点了没反应」 | 盘点时 `window.confirm = () => true` |
| `#42b8ff` 查 inline style | 它写在 SVG 的 `stroke` 属性里 | 查 `getAttribute('stroke'/'fill')` |
| `f_tier*` 输入框 | 权重档位是三行表格 + POST `{tier0,tier1,tier2}`，没有输入框 | 按文案与表格判断，或直接验 POST body |

其中**「`page.route` 后注册覆盖先注册」**这条是之前会话里记反了（原记录说「先注册者
优先」）。已在工具注释里改正。

### 顺带发现的 fixture 结构错误（2 处）—— 修掉之后抓出一个真实缺陷

- `visual_shots.cjs:127`：`/api/tunables` 写成 `{items:[{key,value,def,group}]}`。
- `spa_empty_state_ui_test.cjs:83`：同样写成 `{items:[...]}`。

真实接口是扁平 `map[string]string`（`store.TunableList` 就是 `SELECT key, value` 拼的对象）。

**第二处不是「不影响功能」——它把守门测试的能力削掉了。**

改成扁平的脏数据（键在、值全 `null`）之后，`spa_empty_state_ui_test.cjs` 立刻报红：

```
以下页面把缺失字段渲染成了文字:
总览(dashboard): 渲染出 null → …咬钩节奏 普通上限 null 秒…
```

根因在 `dashboard.js:51`：

```js
// 改前：只挡 undefined，挡不住 null
if (tuns.bite_delay_max !== undefined) {
  canons.push(['咬钩节奏', `普通上限 ${tuns.bite_delay_max} 秒…`]);
}
```

数据库里这一行是 NULL 时，接口原样回 `null`，`!== undefined` 成立，字符串模板直接把
`"null"` 拼进去，页面显示「普通上限 null 秒」。这正是批次 6「空态与裸值统一」
（文档 6.4）要解决的那一类，但漏了这一处。

**已修**：判据改为 `== null`（同时覆盖 `null` 与 `undefined`），`grade_pct_*` 四处一并
处理。修后 `spa_empty_state_ui_test.cjs` 3 用例全绿，`dashboard_ui_test.cjs` 2 用例仍绿。

这个例子说明一件事：**fixture 结构写错会让守门测试静默失效**。测试还是绿的，但它
验的东西已经不是真实接口的形状了。

---

## 7. 总评

| 维度 | 结论 |
|---|---|
| 六批迁移是否真的完成 | **是**。入口已切、旧后台退为 `legacy.html`、`spa.html` 已删 |
| 文档里的功能主张是否成立 | **20/20 成立** |
| 旧后台能力是否静默丢失 | **没有**。6 个玩法开关、3 种出鱼模式、4 种区域工具全在 |
| 唯一操作差异 | 鱼群区域无法从「最近一次渔获」直接生成（见 3.3） |
| 回归 | 25 文件 / 74 用例 + 18 个 Go 包，全绿 |
| 文档偏差 | 4 处，均不影响功能；1 处建议补记 |
| 核对中修掉的真实缺陷 | **1 处**：总览页「普通上限 null 秒」（见 6 节） |

迁移质量是过关的。文档本身也基本可信——它写的每个具体口径都能在界面上验证到，
这一点比「测试绿」有价值得多。