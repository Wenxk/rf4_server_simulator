-- 判鱼决策追踪：一次真实咬钩判定留一行快照，回答「为什么空军 / 为什么是这条鱼」。
--
-- 判鱼权重由名册、硬性鱼口门、区域名册、饵料规则、鱼群分布、生态环境、钓点观测、
-- 窝料、饵效共约九层依次相乘。在此之前只有一行日志里的「空因」，运维无法判断是哪一层
-- 把候选筛空，玩家也无法把空军归因到自己选的点位/水层/饵上。
--
-- payload 是紧凑 JSON：{"stages":[...层名],"candidates":[{"key":..,"w":[..各层权重]}],"empty":".."}。
-- 只保留 Top5 候选（按最终权重），表按保留条数环形裁剪，不参与任何判鱼数值。
CREATE TABLE IF NOT EXISTS fish_roll_trace(
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           REAL    NOT NULL DEFAULT 0,
    uid          INTEGER NOT NULL DEFAULT 0,
    loc          TEXT    NOT NULL DEFAULT '',
    spot         INTEGER NOT NULL DEFAULT 0,
    x            REAL    NOT NULL DEFAULT 0,
    z            REAL    NOT NULL DEFAULT 0,
    rig_type     INTEGER NOT NULL DEFAULT 0,
    bait         TEXT    NOT NULL DEFAULT '',
    hit          INTEGER NOT NULL DEFAULT 0,   -- 1=中鱼，0=空轮
    chosen       TEXT    NOT NULL DEFAULT '',  -- 中鱼时的鱼种 key
    weight_g     INTEGER NOT NULL DEFAULT 0,
    empty_reason TEXT    NOT NULL DEFAULT '',
    total_weight REAL    NOT NULL DEFAULT 0,   -- 抽签前候选权重总和
    payload      TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_fish_roll_trace_loc
    ON fish_roll_trace(loc, id);
