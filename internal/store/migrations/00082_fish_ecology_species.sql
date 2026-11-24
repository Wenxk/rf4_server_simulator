-- 鱼种档案覆盖：后台「鱼种档案」页按鱼种维护全套生态基线。
--
-- 为什么单独建表而不改 fish_ecology：那张表是**由资产种子生成的**。
-- 启动时 ensureFishEcology 会按 `WHERE profile_version < excluded.profile_version`
-- 整行覆盖写库（internal/gameserver/fish_ecology.go），一旦抬资产版本号，
-- 手工改过的行会被种子值冲掉。所以运营侧的手工值必须落在独立表里，
-- 作为最外层覆盖，资产的 profile_version 再涨也不会伤到它。
--
-- 层级（优先级由低到高）：
--   资产基线 fish_ecology → 鱼种覆盖 fish_ecology_species → 水域覆盖 fish_ecology_loc
-- 判鱼侧 fishEcologyFor 按这个顺序合并；后两者都是「nil = 继承上一层」。
--
-- 未覆盖的字段不落 NULL 以外的值；整行字段全空的行由后台删除，
-- 语义是「完全跟随资产基线」。清空本表即回到资产基线，不需要回滚代码。
CREATE TABLE IF NOT EXISTS fish_ecology_species(
    fish_key       TEXT PRIMARY KEY,
    layer_min      REAL CHECK (layer_min IS NULL OR (layer_min >= 0 AND layer_min <= 1)),
    layer_pref     REAL CHECK (layer_pref IS NULL OR (layer_pref >= 0 AND layer_pref <= 1)),
    layer_max      REAL CHECK (layer_max IS NULL OR (layer_max >= 0 AND layer_max <= 1)),
    temp_min_c     REAL CHECK (temp_min_c IS NULL OR (temp_min_c >= -5 AND temp_min_c <= 45)),
    temp_pref_c    REAL CHECK (temp_pref_c IS NULL OR (temp_pref_c >= -5 AND temp_pref_c <= 45)),
    temp_max_c     REAL CHECK (temp_max_c IS NULL OR (temp_max_c >= -5 AND temp_max_c <= 45)),
    diel_mask      INTEGER CHECK (diel_mask IS NULL OR (diel_mask >= 0 AND diel_mask <= 15)),
    diel_curve     TEXT,
    season_mask    INTEGER CHECK (season_mask IS NULL OR (season_mask >= 0 AND season_mask <= 15)),
    diet_json      TEXT,
    weather_json   TEXT,
    action_json    TEXT,
    substrate_json TEXT,
    color_json     TEXT,
    current_pref   REAL CHECK (current_pref IS NULL OR (current_pref >= 0 AND current_pref <= 1)),
    activity       REAL CHECK (activity IS NULL OR (activity >= 0 AND activity <= 5)),
    light_pref     REAL CHECK (light_pref IS NULL OR (light_pref >= 0 AND light_pref <= 1)),
    clarity_pref   REAL CHECK (clarity_pref IS NULL OR (clarity_pref >= 0 AND clarity_pref <= 1)),
    aggression     REAL CHECK (aggression IS NULL OR (aggression >= 0 AND aggression <= 5)),
    enabled        INTEGER CHECK (enabled IS NULL OR enabled IN (0,1)),
    -- 数据来源标记：'manual' 后台手填、'curated' 批量导入的文献值。
    -- 用途是将来重跑导入脚本时能把 'curated' 覆盖掉、保留 'manual'。
    origin         TEXT NOT NULL DEFAULT 'manual',
    updated_by     TEXT NOT NULL DEFAULT '',
    updated_ts     REAL NOT NULL DEFAULT 0
);