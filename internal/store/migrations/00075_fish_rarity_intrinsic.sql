-- 稀有度回归「鱼种固有属性」，删除按渔获量分档的那套。
--
-- 之前两版都在用钓获量反推稀缺度（先四分位、后绝对阈值 30/14/7），思路本身是错的：
-- 攻略站的登记渔获数反映的是「有没有人愿意登记」，不是「这条鱼本身稀不稀有」。
-- 反例：剑鱼在挪威海占 1.2% 渔获被判常规；锦鲤变种没人登记被判极罕见。
--
-- 真正的依据早就存在：静态库 server_data/static.sqlite3 的 species_rarity 表
-- （rf4db 抓取，253 条，0 普通 169 / 1 稀有 45 / 2 超稀有 39），这是鱼种属性，
-- 与渔获量、与本地进度都无关。档位改由 gameserver 启动时
-- SyncFishRarityFromStatic 从该表同步进 fish_ecology.rarity_tier，不再落任何阈值。
--
-- 本迁移做两件事：
--   1. 删掉阈值表（连同「重算稀有度」接口与后台弹窗一起下线）；
--   2. 权重预设从四档收敛到三档，值改为 100/30/5。
--
-- 第二步必须重写旧值：档位语义已经从「常规/少见/罕见/极罕见」变成
-- 「普通/稀有/超稀有」，旧的 40/15 对新档位没有意义，留着会让人以为
-- 稀有鱼被削到 1/7。清空整表也能用（回滚到攻略原值），但默认配法请看注释。
DROP TABLE IF EXISTS fish_rarity_threshold;

DELETE FROM fish_weight_preset WHERE rarity_tier < 0 OR rarity_tier > 2;

INSERT INTO fish_weight_preset(rarity_tier, weight, updated_ts) VALUES
    (0, 100, 0),   -- 普通：169 种
    (1, 30,  0),   -- 稀有：45 种
    (2, 5,   0)    -- 超稀有：39 种（剑鱼、欧洲鳇、各类白化/锦鲤变种等）
ON CONFLICT(rarity_tier) DO UPDATE SET weight=excluded.weight, updated_ts=excluded.updated_ts;

-- 回滚：本迁移不回滚阈值表（口径已废弃）。若要恢复四档权重，
-- 手工改 fish_weight_preset 即可；档位仍由 species_rarity 决定。
