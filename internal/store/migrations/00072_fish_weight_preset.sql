-- 基础权重档位化：新增预设表，按稀有度给所有水域一套可解释的权重基准。
--
-- 为什么不是直接改 loc_fish.catches：
--   loc_fish 属于 StaticTables，运行时由 staticdb.go 建成 TEMP VIEW 指向只读静态库，
--   并有 authorizer 拦截任何写操作（"Cannot add a column to a view" / not authorized）。
--   攻略原值必须原样保留，所以预设只能放在独立的运行时表里，读取时优先。
--
-- 背景：loc_fish.catches 是攻略站抓的渔获条数，各图尺度完全不同——琥珀湖 1~15、
-- 铜湖 1~199，同一档稀有度的鱼在不同图能差十倍以上。更有 178 条鱼在 loc_wiki_fish
-- 里有、loc_fish 里没有，LocRoster 走 COALESCE(catches, 1) 兜底，权重恒为 1。
--
-- 现在按 fish_ecology.rarity_tier（0 常规 / 1 少见 / 2 罕见 / 3 极罕见）给统一档位：
-- 跨图同档同权重，图与图的差别由「名册里有哪些鱼」承担。
--
-- 查表优先级（LocRoster）：fish_weight_preset → loc_fish.catches → 1。
-- 清空本表即回滚到攻略原值：
--   DELETE FROM fish_weight_preset;
CREATE TABLE IF NOT EXISTS fish_weight_preset(
    rarity_tier INTEGER PRIMARY KEY,     -- 0 常规 / 1 少见 / 2 罕见 / 3 极罕见
    weight      REAL    NOT NULL,
    updated_ts  REAL    NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO fish_weight_preset(rarity_tier, weight) VALUES
    (0, 100),   -- 常规：到处都是的杂鱼，新手图的主要目标
    (1, 40),    -- 少见：要多跑几趟才碰得上
    (2, 15),    -- 罕见：需要挑点位/时段/钓组才出
    (3, 5);     -- 极罕见：几十上百杆才一条，通常是该图的招牌目标
