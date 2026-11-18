-- 稀有度分档口径：从「全服渔获数四分位」改为「全服渔获数绝对阈值」。
--
-- 旧口径（生成 fish_ecology_defs.json 时写死）是把 species_grade.catches 降序后
-- 四分位切：前 62 名常规、63~125 少见、126~184 罕见、其余极罕见。问题是每档
-- 强制 25%，不管实际差多少——一张只有 26 种鱼的图也必有 6~7 种被打成「极罕见」。
-- 实测反例：琥珀湖鳞鲤 15 条占该图 11%、排第 1，却被判罕见（权重 15）；
-- 而普通鲤鱼在全服 58 条被判常规（权重 100），同一张图里权重方向是反的。
--
-- 新口径按 species_grade.catches 取绝对阈值，天然呈长尾：常规 44% / 少见 35% /
-- 罕见 16% / 极罕见 9%。稀有档只留给真正冷门的鱼，不再有配额。
--
-- 关键点：species_grade 属于 StaticTables，只由 seed 从攻略站数据覆盖，**与本地
-- 游戏进度无关**——玩家测试期怎么调权重、钓上来多少条，都不会影响这个分档依据。
--
-- 本表只存三个下限（极罕见下限恒为 0，不存）。改完调用 RecalcFishRarity 重算
-- fish_ecology.rarity_tier 即可，基础权重会按新档位跟着变。
--
-- 语义：catches >= tier0 → 常规；>= tier1 → 少见；>= tier2 → 罕见；其余 → 极罕见。
-- 必须满足 tier0 > tier1 > tier2 > 0，否则拒绝保存。
CREATE TABLE IF NOT EXISTS fish_rarity_threshold(
    tier        INTEGER PRIMARY KEY,   -- 0 常规 / 1 少见 / 2 罕见
    min_catches INTEGER NOT NULL,      -- 进入该档所需的最小全服渔获数
    updated_ts  REAL    NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO fish_rarity_threshold(tier, min_catches) VALUES
    (0, 30),   -- 常规：全服登记 ≥30 条，到处都是
    (1, 14),   -- 少见：14~29 条，要多跑几趟
    (2, 7);    -- 罕见：7~13 条，得挑点位/时段（≤6 条为极罕见）

-- 回滚：把 rarity_tier 恢复成生成时的四分位口径需要重跑 seed 的生态档案
-- （fish_ecology 的 ON CONFLICT 不会覆盖已有行，所以只能整表重建或手工改）。
-- 只想恢复阈值不动档位的话：DELETE FROM fish_rarity_threshold;
