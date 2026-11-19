# 恢复「每种鱼爱吃的饵」硬约束

Status: done

## Problem Statement

2026-11-18 的 `bait-diet-only` 把「爱吃饵」设定连同渔获记录一起从判鱼里删掉了，
判鱼资格只剩 `fish_ecology.diet_json` 的粗粒度食性键。结果玩家在后台看到
「青蛙 吃 苍蝇」「镜鲤 吃 椰子类/玉米类/蜂蜜类…」，实际却能用鱼块钓上青蛙、
用苍蝇钓上镜鲤：

- `frog.diet_json` 有 `fish=1`、`worm=1.69`，`fish_pieces → fish` 直接放行；
- `m_carp.diet_json` 有 `worm=1.55`，`fly → worm` 被 `baitPreferenceCap=1.5`
  当成主食级满分；
- 设定载体（`fishing_bait_rules.txt` 的 `allow`、`fish_bait_pref`、
  `companion_bait_fish` 实证、本图 `loc_bait_fish`）里都没有这两对组合。

管理后台「鱼饵偏好」页仍写着「规则来自 fishing_bait_rules.txt（严格模式生效）」，
钓场页列名叫「爱吃的饵」，`InStrict` 注释也按旧规则描述，玩家按界面设定预期
必然对不上。用户 2026-11-19 实测两条鱼后指出设定没有生效。

## Solution

在 `filterFishBaitSet` 里为「爱吃饵」加一道硬资格，不恢复 `bait-diet-only` 删掉的
记录依赖：

- 规则表（或后台覆盖）写了「吃 X」的鱼，钩上的饵必须命中 `allow` 才进候选；
  当前轮换组主推的饵视同命中。
- 规则表未收录的鱼不套此约束，仍只由 `diet_json` 食性门裁定，避免攻略没覆盖的
  鱼种整图绝迹。
- 饵定义缺中文名时无法与攻略文案比对，按中性放行，与「读不到饵定义整体放行」
  「食谱为空不判不吃」同口径，绝不因数据缺失把整图封死。
- 命中之后权重仍取食谱强度（低值难钓、≥1.5 按主食封顶）；食谱里没有这类食物的
  饵照旧剔除，两道门都要过。
- `deny`、`bottomOnly`、海图/jig/顺流/上船前置条件、饵料轮换保持原有语义与优先级。
- companion 官服榜、本图渔获、rf4 实测占比依然完全不参与判鱼：它们是「谁钓到过」
  的记录，增删不改变任何一条鱼对任何一款饵的权重。

只在 `fish_bait_rules_enabled=1`（严格模式）时生效；开关关闭则回到旧流程，与后台
「规则只在开关打开时参与判鱼」的说法一致。

## User Stories

1. 作为玩家，我希望青蛙只吃设定里的苍蝇，挂鱼块不该上青蛙。
2. 作为玩家，我希望镜鲤不咬设定里没写的苍蝇，挂煮饵/玉米类才对口。
3. 作为管理员，我希望后台「鱼饵偏好」页编辑的吃/不吃列表真的决定鱼口，
   而不是只做展示。
4. 边界：攻略未收录的鱼、缺中文名的饵不因数据缺失被判死。
5. 边界：同一鱼种换钓场、补渔获纪录，鱼口权重一分不变。

## Implementation Decisions

- `internal/gameserver/fishing_bait_rules.go`：`filterFishBaitSet` 评分段加
  `hasRule := len(r.allow) > 0`；饵循环中 `hasRule && b.name != "" &&
  !b.matchesAny(r.allow) && !轮换组命中` 时 `continue`。不恢复 rf4 占比分级、
  companion/local 加权、`legalAlternative` 等记录依赖分支。
- 门层 `applyBiteGates` 不动：G-diet 仍只回答「食谱里有没有这类食物」，
  爱吃饵资格在紧随其后的饵料层裁定。
- 测试：`bait_allow_qualification_test.go` 覆盖 allow 命中/不命中、无规则鱼、
  记录不参与，并用 `frog × fish_pieces`、`m_carp × fly` 复现实测两条鱼；
  `bait_preference_test.go`、`fishing_bait_alternatives_test.go`、
  `fish_bite_gates_test.go`、`fishing_bait_rules_test.go` 同步改为新口径。
- 后台文案：`index.html` 鱼饵偏好页说明、钓场页「无规则」提示、
  `bait_prefs.go` 的 `InStrict` 注释改为「资料完整度」；`docs/gameplay.md`、
  `docs/mechanism-map.md`、`specs/capabilities/bite-bait-and-spawn.md` 同步。
- 不新增 tunable、不新增表、不改 `diet_json` 数据；回滚只需移除
  `filterFishBaitSet` 里的 allow 资格段。

## 验收

- `TestFrogRejectsFishPiecesAndMirrorCarpRejectsFly`：苍蝇只钓到青蛙，鱼块两条都钓不到。
- `TestBaitPreferenceFollowsAllowList`：设定外的虫饵权重为 0，设定内蚯蚓按食谱满分。
- `TestBaitBiteIgnoresHumanRecords`：补满本图渔获 + companion tier3 后权重一分不变。
- `go vet ./...`、`go test ./...`、`go run ./tools/doccheck` issues=0。

## Out of Scope

- 不恢复 companion 饵效层在严格模式下的执行（`fishing.go:1416` 的 `!strict` 条件保持），
  后台文案已改为「实证仅作参考」。
- 不重生成 `fish_ecology.diet_json`：`frog.fish`、`m_carp.worm` 这类过宽食谱键
  仍会让部分饵在 allow 命中后拿到高权重，属于数据口径，另行评估。
- 不把 `fish_bait_pref` 表接进判鱼：设定以 `fishing_bait_rules.txt` 与后台
  `fish_bait_override` 为准，该表继续只作静态资料登记。

## 风险与开放问题

1. 攻略 `allow` 用风味词（椰子类/中立类/自然风味…）描述煮饵，客户端饵名不一定
   带这些前缀；恢复硬约束后鲤族用部分煮饵可能明显变难。管理员可在「鱼饵偏好」页
   编辑该鱼的吃饵列表来对齐实际饵名。
2. 252 条内置规则未覆盖的鱼种不套 allow，仍按食谱放行；后台「无规则」标记用于
   提示这类鱼需要补规则。
