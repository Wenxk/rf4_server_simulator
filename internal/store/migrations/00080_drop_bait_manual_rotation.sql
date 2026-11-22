-- 移除手工定时轮换组。
--
-- 自动抽取（fish_bait_auto_rotation，00077）上线后，手工轮换组已无实际使用：
-- 本地 211 条规则覆盖里 0 条填写 groups_json，154 个鱼种全部走自动轮换。
-- 保留 groups_json / rotate_sec / rotate_mode 会让「吃口判定」出现第二套 allow
-- 来源（当前组、soft 降权、strict 组外禁咬），与「统一遵守 allow」的口径冲突。
--
-- 本次直接重建表，去掉三列；历史数据全是空组，无需迁移。
-- 回滚：按 00080 之前的表结构重建 fish_bait_override，并恢复
-- gameserver 的 activeBaitGroup/rotationState 与后台手工编组 UI。
CREATE TABLE fish_bait_override_new(
    fish_zh     TEXT PRIMARY KEY,
    allow_json  TEXT NOT NULL DEFAULT '[]',
    deny_json   TEXT NOT NULL DEFAULT '[]',
    bottom_json TEXT NOT NULL DEFAULT '[]',
    condition   TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_ts  REAL NOT NULL DEFAULT 0
);

INSERT INTO fish_bait_override_new(
    fish_zh, allow_json, deny_json, bottom_json, condition,
    enabled, updated_by, updated_ts)
SELECT fish_zh, allow_json, deny_json, bottom_json, condition,
       enabled, updated_by, updated_ts
FROM fish_bait_override;

DROP TABLE fish_bait_override;
ALTER TABLE fish_bait_override_new RENAME TO fish_bait_override;
