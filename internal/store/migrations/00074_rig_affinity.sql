-- 钓法倾向：把「这一杆的钓法」对「鱼种所在水层」的偏向做成可配置矩阵。
--
-- 背景：水层匹配此前只有一条几何公式（fishLayerWeightProfile），把钓组折算成
-- 一个水层点（底钓恒 0.93，浮钓按有效饵深）再和鱼种区间比。方向对，但玩家既看不
-- 见也调不了，实测底钓 100% 出底层鱼、浮钓 64% 还是底层鱼——"不同钓法钓不同鱼"
-- 只有一半成立，且无从解释。
--
-- 现在补一层显式乘子：钓法族（浮钓/底钓/路亚/海钓/飞钓）× 鱼种水层档
-- （表层 <0.30 / 中上 0.30~0.50 / 中下 0.50~0.72 / 底层 ≥0.72）。
-- 乘子原样生效，不参与判鱼链的幂压缩，所以后台填 1.6 就是 1.6。
--
-- 与几何层的关系：几何层管"区间内高斯峰 + 区间外衰减"，是本表的前置；
-- 本表只表达"即使在同一区间，钓法族也有偏好"。两层的乘积决定最终生态系数，
-- 因此单行不建议给到 3 以上，避免把某族彻底封死（要封鱼种请用 enabled=0）。
--
-- 回滚：rig_affinity_enabled=0，或把 20 行全填 1.0（等价中性）。
CREATE TABLE IF NOT EXISTS rig_affinity_weight(
    rig_family TEXT    NOT NULL,          -- float/bottom/lure/marine/fly
    layer_band TEXT    NOT NULL,          -- surface/mid_upper/mid_lower/bottom
    weight     REAL    NOT NULL,
    updated_ts REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY(rig_family, layer_band)
);

INSERT OR IGNORE INTO rig_affinity_weight(rig_family, layer_band, weight) VALUES
    -- 浮钓：饵在下沉行程/水层中，天然更适合中上层鱼；底层鱼只能算杂口
    ('float',  'surface',   1.6),
    ('float',  'mid_upper', 1.3),
    ('float',  'mid_lower', 0.8),
    ('float',  'bottom',    0.6),
    -- 底钓：沉底族（喂食器/发丝/方法饵）贴底，底层鱼是主场
    ('bottom', 'surface',   0.35),
    ('bottom', 'mid_upper', 0.6),
    ('bottom', 'mid_lower', 0.9),
    ('bottom', 'bottom',    1.35),
    -- 路亚：按拟饵泳层走，几何层已按 effectiveLureLayer 折算，本表只做轻微倾向
    ('lure',   'surface',   1.4),
    ('lure',   'mid_upper', 1.2),
    ('lure',   'mid_lower', 0.9),
    ('lure',   'bottom',    0.7),
    -- 海钓：垂直铁板/沉底都有，整体偏中下层，不做太强偏向
    ('marine', 'surface',   0.8),
    ('marine', 'mid_upper', 1.0),
    ('marine', 'mid_lower', 1.1),
    ('marine', 'bottom',    1.1),
    -- 飞钓：毛钩在水面/表层拖动，表层鱼是主场
    ('fly',    'surface',   1.8),
    ('fly',    'mid_upper', 1.3),
    ('fly',    'mid_lower', 0.7),
    ('fly',    'bottom',    0.4);
