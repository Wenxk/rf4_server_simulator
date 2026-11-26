-- 游戏内钩号（S24 / S20 / S1/0 / S14/0 …）。
--
-- 为什么需要这一列：
--   item_def 的 gkfbafmbpam 是 0..33 的内部刻度，游戏界面显示的是 S 号，两者是
--   两套编号，玩家看到的永远是 S 号。后台和查鱼助手此前只能报内部刻度，跟客户端
--   对不上。刻度与 S 号是严格一对一的（34 档刻度对应 34 个 S 号，且每个 S 号只对应
--   唯一一个钩长），所以这里存派生的展示号，不引入第二套事实。
--
-- 为什么放在 hook_bite_profile：
--   item_catalog 属于静态只读表（启动时按种子重建），派生列放进去会被刷掉。
--   hook_bite_profile 本来就是 syncHookBiteProfiles 从 item_def 派生的运行时表，
--   展字号跟着一起派生最自然，也不会被参数导出包当成「玩家改过的调参」带走。
--
-- 空串表示该刻度没有对应的游戏 S 号（已知刻度 0..33 全有，保留空串只为容错）。
ALTER TABLE hook_bite_profile ADD COLUMN display_size TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_hook_bite_profile_display_size
    ON hook_bite_profile(display_size);