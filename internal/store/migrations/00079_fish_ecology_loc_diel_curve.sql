-- 水域级生态覆盖支持昼夜曲线，并把全库已有的昼夜掩码重映射到新位序。
--
-- 一、fish_ecology_loc 加 diel_curve：
--   覆盖行沿用「NULL = 继承全局」的部分覆盖语义。曲线为空串表示该水域该鱼种
--   显式回退到掩码判断（与全局口径一致）；NULL 表示连掩码判断也不接管。
--
-- 二、掩码位序调整（本迁移一次性重映射）：
--   旧位序 1 晨 / 2 昼 / 4 暮 / 8 夜  →  新位序 1 夜 / 2 晨 / 4 昼 / 8 昏
--   原因是生态资产的 diel_curve 按 [夜,晨,昼,昏] 编码，位序不一致会让曲线整体
--   错位一格（拿「夜」的权重配「晨」）。旋转公式：((m<<1)|(m>>3))&15。
--   例：8（旧·仅夜）→ 1（新·仅夜）；1（旧·仅晨）→ 2（新·仅晨）；15 不变。
--
-- 必须同时改 fish_ecology 与 fish_ecology_loc：
--   fish_ecology 虽会被启动时的资产刷新按 profile_version 覆盖，但只要有一条鱼的
--   档案版本已经到 5（或资产缺该鱼），漏改就会留下错位一格的数据。
ALTER TABLE fish_ecology_loc ADD COLUMN diel_curve TEXT;

UPDATE fish_ecology
   SET diel_mask = ((diel_mask << 1) | (diel_mask >> 3)) & 15
 WHERE diel_mask BETWEEN 0 AND 15;

UPDATE fish_ecology_loc
   SET diel_mask = ((diel_mask << 1) | (diel_mask >> 3)) & 15
 WHERE diel_mask IS NOT NULL AND diel_mask BETWEEN 0 AND 15;

-- 回滚：反向旋转 ((m>>1)|(m<<3))&15，并删列（SQLite 3.35+ 支持 DROP COLUMN）。
