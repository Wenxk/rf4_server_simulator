-- 鱼种经济覆盖：后台「鱼种经济」页按 (鱼种, 档位) 覆盖经验速率与基准鱼价。
--
-- 未覆盖的组合不落行；xp_c / price_per_kg 都为空的行会被删除，语义是「跟随内置表」。
-- xp_c 单位 XP/克（与 fishXPRateFor 同量纲）；price_per_kg 是基准水域（level_009_ahtuba，
-- 倍率 1.0）的银/公斤，其他水域仍按 map_market_multipliers.json 折算。
-- 回滚：清空本表即回到内置数值，不需要回滚代码。
CREATE TABLE IF NOT EXISTS species_econ(
    fish_key     TEXT NOT NULL,
    grade        INTEGER NOT NULL CHECK (grade BETWEEN 1 AND 4),
    xp_c         REAL CHECK (xp_c IS NULL OR xp_c > 0),
    price_per_kg REAL CHECK (price_per_kg IS NULL OR price_per_kg > 0),
    updated_by   TEXT NOT NULL DEFAULT '',
    updated_ts   REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (fish_key, grade)
);
