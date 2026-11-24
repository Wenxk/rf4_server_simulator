# 鱼种经济后台（按鱼种覆盖经验与鱼价）

Status: done

## Problem Statement

经验速率（`fishXPRateFor`）与鱼市基准价（`fish_market_prices.json`）都编译在服务里：
前者写死在 Go 表，`species_weight.xp_c/xp_q` 两列全表为空；后者只能整包替换 JSON。
运维想给某个鱼种单独调经验或调价，只能改代码重新发版，改完全服生效，也无法回滚单个鱼种。
玩家侧能看到的现象是：某鱼种经验明显偏高/偏低、某鱼种卖价与官服不符，但运营端没有任何入口修正。

## Solution

Control Deck 新增「鱼种经济」页：按鱼种列出四档的内置经验速率与基准鱼价，
逐档填写覆盖值；留空即跟随内置默认。保存后写入独立覆盖表，主服 30 秒内热加载
（保存同时投递刷新指令，正常情况下立即生效）。页面内置试算：给定重量与水域，
当场算出这一尾的经验与卖价，避免"改完不知道会变成多少"。

## User Stories

1. 作为运维，我希望在后台按鱼种逐档改「XP/克」与「银/公斤」，以便修正个别鱼种的数值而不动代码。
2. 作为运维，我希望只改其中一档或只改经验、不改价格，其余继续跟随内置表。
3. 作为运维，我希望清空覆盖后该鱼种立刻回到内置表的值，并且能一眼看出哪些鱼种被改过。
4. 作为运维，我希望改完能试算"某重量在某水域卖多少银、给多少经验"，不用进游戏验证。
5. 边界：覆盖值非法（≤0、档位越界、鱼种不存在）时拒绝保存并给出原因；主服离线时保存仍成功，恢复后按缓存节奏生效。

## Implementation Decisions

- 存储：新增迁移 `00081_species_econ.sql`，表 `species_econ(fish_key, grade, xp_c, price_per_kg, updated_by, updated_ts)`，
  主键 `(fish_key, grade)`；两列均可空，两列都空的行删除。只写玩家库，不动静态库。
- 语义：`xp_c` 单位 XP/克，与 `fishXPRateFor` 同量纲；`price_per_kg` 是**基准水域**（阿赫图巴，倍率 1.0）
  的银/公斤，其他水域仍按 `map_market_multipliers.json` 折算，不逐图存价。
- 游戏服：`gameserver` 新增覆盖缓存（30 秒 TTL，对齐 tunables），经验在 `catchValue` 的速率处替换，
  鱼价在 `marketPriceCentsForMap` 的 per-kg 处替换；`reload_tunables` 指令同时失效该缓存。
  覆盖缺失/读库失败时完全走内置表，不影响结算。
- 后台接口（`internal/admin`）：`GET /api/econ/species`（列表，含默认值、覆盖值与阈值线）、
  `POST /api/econ/species/save`（按鱼种整体提交四档）、`POST /api/econ/species/reset`（清空该鱼种覆盖）、
  `GET /api/econ/preview`（试算）。保存与恢复都写审计并投递 `reload_tunables`。
- 回滚：删表或清空覆盖行即可回到内置数值，不需要回滚代码。

## Testing Decisions

- 存储层（真实 SQLite）：`repos_species_econ_test.go` 覆盖写入/读取/清空、非法档位与非法值拒绝。
- 游戏服（单元 + 真实 SQLite）：`econ_override_test.go` 断言同一条鱼在覆盖前后 `catchValue` 与
  `marketPriceCentsForMap` 的差值符合手算值；清空覆盖后回到内置值。期望值用
  `2.0 XP/克 × 500 克 = 1000` 这类手算样例，不用同样的公式再算一遍。
- 后台（HTTP）：`econ_test.go` 断言列表返回四档、保存后主库有行且有审计、恢复后行被删除、
  非法值返回 400。回滚（去掉覆盖读取）后游戏服测试会变红。

## Out of Scope

- 不逐地图单独定价（仍用基准价 × 水域倍率）。
- 不改客户端展示的结算卡银价口径（`catchValue` 的 silver 仍只入 catch_log）。
- 不提供批量导入/导出与按水域批量调价。
- 不改 `species_weight.xp_c` 列，也不把内置表迁进数据库。

## Further Notes

- [事实] `species_weight.xp_c/xp_q` 在静态库中 253 行全为 NULL，经验真源仍是 Go 表
  （`internal/gameserver/fish_xp_rate_table.go`）。
- [事实] `fish_market_prices.json` 只存基准水域四档价（原 251 种，2026-11-23 补齐为 253 种），其余水域按倍率折算
  （`specs/changes/fish-market-prices/spec.md`）。
- [推断] 覆盖值以「基准水域单价」表达，运维用试算页确认跨图价格后再落库。
