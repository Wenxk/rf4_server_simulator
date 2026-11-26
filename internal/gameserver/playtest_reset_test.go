package gameserver

// 重建全新玩家基线库 data/playtest/fresh.db。
//
// 为什么需要它：作钓体验测试会真实扣饵、拆装钓组、把数量清零，
// 反复跑下来 fresh.db 会被改成「弹尽粮绝 + 钓组残缺」的状态，
// 后续测试就会测出 0/12 空竿这种假结论。手工修库既慢又容易漏。
//
// 做法：从 snapshot.db 拷一份，删掉除 playtest01 以外的玩家记录，
// 再用服务端真实的 EnsurePlayer（走 starter_items.json 播种）重建
// 新玩家的初始装备。这样结果与「玩家第一次登录」完全一致。
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestResetBaseline -v

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"rf4server/internal/store"
)

// TestPlaytestResetBaseline 重建基线库。
func TestPlaytestResetBaseline(t *testing.T) {
	playtestEnabled(t)
	if os.Getenv("RF4_REBUILD") != "1" {
		t.Skip("设置 RF4_REBUILD=1 才会重建基线库（会覆盖 fresh.db）")
	}

	src := "../../data/playtest/snapshot.db"
	dst := playSnapPath
	if _, err := os.Stat(src); err != nil {
		t.Skipf("缺少源快照 %s: %v", src, err)
	}

	// 1) 拷贝 snapshot.db → fresh.db（WAL 已合并：snapshot 是离线快照）
	in, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读源库: %v", err)
	}
	// 先移除可能存在的 -wal/-shm，否则旧 WAL 会被回放到新库上
	for _, suf := range []string{"-wal", "-shm"} {
		_ = os.Remove(dst + suf)
	}
	if err := os.WriteFile(dst, in, 0o644); err != nil {
		t.Fatalf("写目标库: %v", err)
	}
	t.Logf("已拷贝 %s → %s (%.1f MB)", src, dst, float64(len(in))/1024/1024)

	// 2) 删掉除 playtest01 外的所有玩家数据
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatalf("打开: %v", err)
	}
	defer db.Close()

	keep := playUID
	// 只保留 playtest01 的 profile；其余玩家一律清空
	playerTables := []string{
		"profile", "item", "rig", "rig_component", "rod_setting", "container_slot",
		"keepnet", "catch_log", "catch_seen", "fish_roll_trace", "trophy",
		"txn", "player_stats", "fishing_state", "qualification_state",
		"achievement_state", "daily_market", "player_location", "bait_override",
	}
	cleared := 0
	for _, tbl := range playerTables {
		var hasUID, hasGUID int
		_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='uid'`, tbl).Scan(&hasUID)
		_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='guid'`, tbl).Scan(&hasGUID)
		var res sql.Result
		var err error
		switch {
		case hasUID > 0:
			res, err = db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE uid<>?`, tbl), keep)
		case hasGUID > 0:
			// 没有 uid 的表（rig/rig_component/rod_setting）：清空，
			// 反正 EnsurePlayer 会为新玩家重建。
			res, err = db.Exec(fmt.Sprintf(`DELETE FROM %s`, tbl))
		default:
			continue
		}
		if err != nil {
			t.Logf("清理 %s 跳过: %v", tbl, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			cleared += int(n)
			t.Logf("清理 %-22s %d 行", tbl, n)
		}
	}

	// 3) 清空 playtest01 自己的装备与渔获，但保留 profile
	for _, q := range []string{
		`DELETE FROM rig_component`,
		`DELETE FROM rod_setting`,
		`DELETE FROM rig`,
		`DELETE FROM container_slot`,
		`DELETE FROM item WHERE uid=?`,
		`DELETE FROM keepnet WHERE uid=?`,
		`DELETE FROM catch_log WHERE uid=?`,
		`DELETE FROM catch_seen WHERE uid=?`,
		`DELETE FROM trophy WHERE uid=?`,
		`DELETE FROM fishing_state WHERE uid=?`,
	} {
		_, _ = db.Exec(q, keep)
	}
	// profile 恢复到 1 级 / 0 经验 / 5000 银币
	if _, err := db.Exec(`UPDATE profile SET level=1, xp=0, silver=5000, gold=0 WHERE uid=?`, keep); err != nil {
		t.Logf("重置 profile 失败: %v", err)
	}
	// 参数回到出厂状态
	for _, kv := range [][2]string{
		{"bite_delay_fixed", "0"},
		{"bite_delay_scale", "1"},
		{"bite_delay_max", "20"},
		{"bite_delay_lure_max", "6"},
		{"grade_ratio_enabled", "0"},
		{"fish_bait_rules_enabled", "1"},
		{"fish_weather_enabled", "1"},
	} {
		_, _ = db.Exec(`UPDATE tunables SET value=? WHERE key=?`, kv[1], kv[0])
	}
	_, _ = db.Exec(`DELETE FROM fish_bite_setting`)
	t.Logf("已清理 %d 行、重置 playtest01、参数归零", cleared)

	// 4) 用服务端真实逻辑重新播种 starter
	srv := playServer(t, mustOpenBaseline(t, dst))
	if err := srv.EnsurePlayer(context.Background(), keep); err != nil {
		t.Fatalf("EnsurePlayer: %v", err)
	}
	t.Logf("已用 EnsurePlayer 重新播种 starter")
}

func mustOpenBaseline(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.OpenWithStatic(path, staticTestDBPath())
	if err != nil {
		t.Fatalf("打开基线库: %v", err)
	}
	if err := db.EnableStaticReadOnly(t.Context()); err != nil {
		_ = db.Close()
		t.Fatalf("静态只读: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}