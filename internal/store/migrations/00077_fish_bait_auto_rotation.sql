-- 鱼饵自动轮换的逐鱼开关。
--
-- 为什么单独一张表，而不是复用 fish_bait_override.rotate_mode：
--
--   * 语义不同：rotate_mode（soft / strict）描述「手工编的几组怎么轮」，
--     自动抽取是「按稀有度从爱吃饵里现抽」，没有组，两者互斥但不同层。
--   * 开关必须与规则覆盖解耦：覆盖表以「鱼名」为键、整行替换内置规则。
--     若把 auto 塞进覆盖行，给一条没有覆盖的鱼打开开关就得先把内置 252 条
--     规则复制成覆盖——从此该鱼再也跟不上内置规则表的更新（上游 2026-11-19
--     刚把规则表按官服占比重写了 460 行）。
--   * 键用 fish_key 而非鱼名：不受「泥鳅 / 泥鳅鱼」这类按水域改名、
--     以及 BOM 脏字符影响，后台与运行时都用同一套 fish_key。
--
-- 只存开关：周期与未抽中系数是全局 tunables，抽签结果由
-- hash(fish_key, 时间桶) 实时算出，不落库。
--
-- 回滚：DROP TABLE fish_bait_auto_rotation; 同时回退 resolveBaitRule 的 auto 标记读取。
CREATE TABLE IF NOT EXISTS fish_bait_auto_rotation(
  fish_key   TEXT PRIMARY KEY,
  enabled    INTEGER NOT NULL DEFAULT 1,
  updated_by TEXT NOT NULL DEFAULT '',
  updated_ts REAL NOT NULL DEFAULT 0
);
