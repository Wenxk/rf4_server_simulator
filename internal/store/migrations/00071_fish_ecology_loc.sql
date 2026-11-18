-- 生态档案按水域覆盖：同一种鱼在不同水域可以有不同习性。
--
-- fish_ecology 的主键是 fish_key，一条鱼全服只有一套习性；但原作里同一种鱼在不同水域的
-- 活跃时段、栖息水层、温度窗口、咬口偏好都可能不同，攻略也是按「图 × 鱼」记录的粒度。
-- 本表按（统计名册水域 stat_loc，鱼种 fish_key）存**部分字段覆盖**：
-- 列为 NULL 表示继承全局档案，因此只改一个水温窗口也能生效，不会被覆盖行挡住。
--
-- 查表顺序：fish_ecology_loc（本表）→ fish_ecology（全局），与 fish_bite_setting 的
-- 「水域级覆盖全局」口径一致。没有覆盖行的水域行为与现在完全一致。
CREATE TABLE IF NOT EXISTS fish_ecology_loc(
    stat_loc     TEXT    NOT NULL,           -- loc_alias 解析出的统计名册水域键
    fish_key     TEXT    NOT NULL,
    layer_min    REAL,
    layer_pref   REAL,
    layer_max    REAL,
    temp_min_c   REAL,
    temp_pref_c  REAL,
    temp_max_c   REAL,
    diel_mask    INTEGER,
    season_mask  INTEGER,
    diet_json    TEXT,
    weather_json TEXT,
    activity     REAL,
    enabled      INTEGER,                    -- 0=该水域不出这种鱼；NULL=继承
    updated_by   TEXT    NOT NULL DEFAULT '',
    updated_ts   REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY(stat_loc, fish_key)
);

CREATE INDEX IF NOT EXISTS idx_fish_ecology_loc_fish
    ON fish_ecology_loc(fish_key);
