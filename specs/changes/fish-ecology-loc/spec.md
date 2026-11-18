# 生态档案按水域覆盖：同一鱼种在不同水域可以有不同习性

Status: done

## Problem Statement

逐鱼种生态档案（`fish_ecology`）主键是 `fish_key`，一条鱼全服只有一套习性。但「同一种鱼在不同
水域的活跃时段、栖息水层、温度窗口、咬口偏好可能完全不同」正是原作生态感的核心，也是攻略
（Wiki 逐图逐鱼）记录的粒度。

现状后果：运维发现「这张图这条鱼口明显不对」时，只能改全局表，一改就波及所有水域；无法只针对
某张图收紧水温窗口或改昼夜活跃时段。水域级只有咬钩**间隔**能覆盖（`fish_bite_setting`），
鱼种**习性**没有对应能力。

## Solution

新增 `fish_ecology_loc`：按（统计名册水域 `stat_loc`，鱼种 `fish_key`）存一份**部分字段覆盖**，
没填的字段继承全局档案。判鱼读取时先合并覆盖再使用。后台在「鱼种名册」每行提供「生态」入口，
就地编辑该水域该鱼种的覆盖值；不配置的水域行为与现在完全一致。

## User Stories

1. 作为运维，我希望只改「白河 · 江鳕」的活跃时段和水温窗口，以便修单图鱼口而不影响其他水域。
2. 作为运维，我希望覆盖只填我关心的字段（例如只改水温上下限），其余继续跟随全局档案，
   以免全局修缮被覆盖行挡住。
3. 边界：没有覆盖行时返回的档案与全局逐字段相同（行为字节级不变）；覆盖行 `enabled=0`
   表示该水域不出这种鱼；保存后 30 秒内生效（队列主动失效，另有 TTL 兜底）。

## Implementation Decisions

- 数据：新增表 `fish_ecology_loc`（迁移 `00071_fish_ecology_loc.sql`），主键
  `(stat_loc, fish_key)`；可空列 = 继承全局。覆盖字段限定为：水层三段、温度三段、
  昼夜/季节掩码、食性与天气 JSON、活跃度、`enabled`。
- 读取：`internal/gameserver/fish_ecology.go` 新增 `fishEcologyFor(ctx, statLoc, key)`，
  全局档案 → 合并覆盖 → 返回；`reloadFishEcologyCache` 同时载入覆盖到
  `fishEcologyLocCache[statLoc][fishKey]`。`fishEcology(ctx, key)` 保留为全局读，既有用例不变。
- 接线：`cachedFishEcology` 用 `cachedResolveStatLoc(ctx, c, c.loc)` 取 `stat_loc`，
  缓存键改为 `stat_loc + key`（区域估算会在一次会话内跨水域）。
- 刷新：管理端保存/删除后 `QueuePush{op:"reload_fish_ecology"}`；gameserver 消费端清两类缓存。
- 管理端：`GET /api/fish/ecology-loc?loc=` / `POST /api/fish/ecology-loc` /
  `POST /api/fish/ecology-loc/delete`，写操作写 `audit_log`，沿用 `fish_zone` 的参数校验风格。
- 兼容：不改协议、不改静态库、不改全局表播种；回滚 = 删表即可回到全局单一档案
  （代码在表缺失时按全局读）。

## Testing Decisions

- 单元（`internal/gameserver/fish_ecology_loc_test.go`）：同一个 `fish_key` 在两个不同
  `stat_loc` 上得到不同温度窗口；只覆盖温度时水层/食性/活跃度与全局完全相等；
  无覆盖行时合并结果与 `fishEcology` 全局结果逐字段相等（防「静默改变现有手感」）。
- 存储（真实 SQLite）：upsert 幂等（同键二次保存不产生第二行）、可空列落 NULL、
  删除后回到继承全局。
- 期望值来源：手写覆盖字面量（例如只把 `temp_pref_c` 从 18 改成 12），断言另一字段仍是全局值。
- 回滚后变红：去掉合并步骤后「两图不同」用例失败；`enabled=0` 覆盖用例失败。
- 既有守护不能变红：`TestFishQueryRegionCaptureMatchesPointCalculation`（区域估算与逐点
  同口径）——本改动只按 `stat_loc` 取档案，区域路径沿用同一函数。

## Out of Scope

- 不做批量导入/导出、不做从攻略文本自动学习生态。
- 不改全局 `fish_ecology` 的播种逻辑与默认值。
- 不改咬钩间隔覆盖（`fish_bite_setting`）语义。
- 不做「按水域覆盖鱼种权重」——那是区域名册（`fish_zone`）的职责。

## Further Notes

- `[事实]` `fish_ecology` 主键为 `fish_key`，见 `internal/store/migrations/00036_fish_ecology.sql`；
  后续迁移补了 `season_mask`/`substrate_json`/`color_json`/`activity` 等列。
- `[事实]` 覆盖语义的同类先例是 `fish_bite_setting`（水域级覆盖全局咬钩参数），本改动沿用其
  查表顺序，避免两套口径。
- `[推断]` 逐图覆盖是攻略数据能直接喂进来的最小结构；是否真能提升「生态感」未做玩家验证。
