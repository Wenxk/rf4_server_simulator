-- 鱼饵规则覆盖：主键从「鱼种中文名」改为 fish_key。
--
-- 为什么改：
--   1) 另外两张覆盖表（fish_ecology_species / species_econ）都以 fish_key 为键，
--      只有这张用中文名。合并进「鱼种档案」单页后，同一条鱼会分裂成两行。
--   2) 中文名在库里不唯一也不稳定：21 个 fish_key 有多个中文名（含 BOM 尾字符、
--      「大西洋鲑 / 大西洋鲑鱼」这类变体），4 组同一条鱼存了两行，其中
--      m_carp_red 的两行 allow 内容完全不同 → 运行时按规范化名建 map，后写覆盖
--      先写，而读取顺序不受控，实际生效哪一份是不确定的。
--
-- 规范化口径：去 BOM(char 65279)、去 NBSP(char 160)、去全角空格(char 12288)、
-- 去制表符、去半角/全角括号。与 gameserver.ruleName 的差异只在异体字与大小写，
-- 实测 211 条覆盖里 210 条精确命中，剩下 1 条靠上述去空白即命中，故不搬异体字表。
--
-- 回滚：DROP 新表，用 fish_bait_override_orphan 与备份库恢复。
-- 迁移器按整文件执行，不要添加 Down 段。

-- ① 名称来源规范化：loc_wiki_fish 与 loc_fish 合并去重。
CREATE TEMP TABLE _bait_name_map(norm_zh TEXT PRIMARY KEY, fish_key TEXT);

INSERT OR IGNORE INTO _bait_name_map(norm_zh, fish_key)
SELECT TRIM(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
        fish_zh, char(65279), ''), char(160), ''),
        char(40), ''), char(41), ''), char(65288), ''), char(65289), '')),
       fish_key
FROM loc_wiki_fish
WHERE fish_zh IS NOT NULL AND fish_zh <> ''
ORDER BY fish_key;

INSERT OR IGNORE INTO _bait_name_map(norm_zh, fish_key)
SELECT TRIM(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
        name_zh, char(65279), ''), char(160), ''),
        char(40), ''), char(41), ''), char(65288), ''), char(65289), '')),
       fish_key
FROM loc_fish
WHERE name_zh IS NOT NULL AND name_zh <> ''
ORDER BY fish_key;

-- ② 覆盖行映射到 fish_key；反查不到的留 NULL，交给 ③ 留档。
CREATE TEMP TABLE _bait_override_stage(
    fish_zh TEXT, fish_key TEXT,
    allow_json TEXT, deny_json TEXT, bottom_json TEXT, condition TEXT,
    enabled INTEGER, updated_by TEXT, updated_ts REAL);

INSERT INTO _bait_override_stage
SELECT o.fish_zh,
       (SELECT m.fish_key FROM _bait_name_map m
        WHERE m.norm_zh = TRIM(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(
                o.fish_zh, char(65279), ''), char(160), ''),
                char(40), ''), char(41), ''), char(65288), ''), char(65289), ''))),
       o.allow_json, o.deny_json, o.bottom_json, o.condition,
       o.enabled, o.updated_by, o.updated_ts
FROM fish_bait_override o;

-- ③ 孤立行留档：反查不到 fish_key 的覆盖不静默丢弃。
CREATE TABLE IF NOT EXISTS fish_bait_override_orphan(
    fish_zh     TEXT PRIMARY KEY,
    allow_json  TEXT NOT NULL DEFAULT '[]',
    deny_json   TEXT NOT NULL DEFAULT '[]',
    bottom_json TEXT NOT NULL DEFAULT '[]',
    condition   TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_ts  REAL NOT NULL DEFAULT 0,
    reason      TEXT NOT NULL DEFAULT 'name-not-mapped');

INSERT OR IGNORE INTO fish_bait_override_orphan(
    fish_zh, allow_json, deny_json, bottom_json, condition,
    enabled, updated_by, updated_ts, reason)
SELECT fish_zh, allow_json, deny_json, bottom_json, condition,
       enabled, updated_by, updated_ts, 'name-not-mapped'
FROM _bait_override_stage WHERE fish_key IS NULL;

-- ④ 新表：主键 fish_key；fish_zh 保留为展示列，不再参与查找。
CREATE TABLE fish_bait_override_new(
    fish_key    TEXT PRIMARY KEY,
    fish_zh     TEXT NOT NULL DEFAULT '',
    allow_json  TEXT NOT NULL DEFAULT '[]',
    deny_json   TEXT NOT NULL DEFAULT '[]',
    bottom_json TEXT NOT NULL DEFAULT '[]',
    condition   TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_ts  REAL NOT NULL DEFAULT 0);

-- ⑤ 同一条鱼多行合并：三个列表取并集去重，enabled 取 OR，updated_ts 取最大。
-- allow/deny/bottom 都是集合语义（判鱼只做成员测试），并集不改变判定含义。
INSERT INTO fish_bait_override_new(
    fish_key, fish_zh, allow_json, deny_json, bottom_json,
    condition, enabled, updated_by, updated_ts)
SELECT
    s.fish_key,
    MIN(s.fish_zh),
    COALESCE((SELECT json_group_array(DISTINCT je.value)
              FROM _bait_override_stage s2, json_each(s2.allow_json) je
              WHERE s2.fish_key = s.fish_key), '[]'),
    COALESCE((SELECT json_group_array(DISTINCT je.value)
              FROM _bait_override_stage s2, json_each(s2.deny_json) je
              WHERE s2.fish_key = s.fish_key), '[]'),
    COALESCE((SELECT json_group_array(DISTINCT je.value)
              FROM _bait_override_stage s2, json_each(s2.bottom_json) je
              WHERE s2.fish_key = s.fish_key), '[]'),
    MAX(s.condition),
    MAX(s.enabled),
    CASE WHEN COUNT(*) > 1 THEN 'merge:' || GROUP_CONCAT(DISTINCT s.updated_by)
         ELSE MAX(s.updated_by) END,
    MAX(s.updated_ts)
FROM _bait_override_stage s
WHERE s.fish_key IS NOT NULL
GROUP BY s.fish_key;

DROP TABLE fish_bait_override;
ALTER TABLE fish_bait_override_new RENAME TO fish_bait_override;
DROP TABLE _bait_override_stage;
DROP TABLE _bait_name_map;