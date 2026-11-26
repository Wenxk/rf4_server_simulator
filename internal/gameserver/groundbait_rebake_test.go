package gameserver

// 打窝亲和表（assets/groundbait_affinity.json）的重新烘焙器。
//
// 背景：这张表是**离线预烘焙**出来的固定亲和度，运行时 groundbaitDefsLoad 直接读它，
// 不走实时计算（实时计算只在自制窝料那条路径用）。所以它必须与当前生态档案
// （fish_ecology_defs.json 的 diet_json + 规则表 allow）保持一致。
//
// 一旦鱼种食性 / 饵料规则 / 温度相关的食性推导变了，这张表就会过期。历史上
// 它写着 profile_version=1，而生态档案已经到 7，代码只读不校验版本，于是
// 「表里说吸引、运行时算出来不吸引」这种静默偏差没有任何地方会报警——
// 只有 TestGroundbaitRuntimeAffinityMatchesOfflineProbe 会红（2026-11-25 就是这么发现的）。
//
// 用法（改完生态/规则后必须重跑）：
//
//	go test ./internal/gameserver -run TestRebakeGroundbaitAffinity -v
//
// 不带 -apply 只打印差异摘要；带 -apply 才写回资产文件。
//
//	go test ./internal/gameserver -run TestRebakeGroundbaitAffinity -v -args -apply
//
// 烘焙口径**逐字对齐** TestGroundbaitRuntimeAffinityMatchesOfflineProbe：
//   - 掠食冻结（双票）的鱼不落行；
//   - 只落 appetite > 1.05 的行（饱和下限以下本来就不该出现）；
//   - 纯载体窝料（食性向量为空，如 PVA 棒）不落任何行。

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

var rebakeApply = flag.Bool("apply", false, "写回 assets/groundbait_affinity.json（默认只预演）")

// groundbaitAffinityOut 是写回文件的顶层结构，字段顺序与原文件保持一致。
type groundbaitAffinityOut struct {
	ProfileVersion int64                   `json:"profile_version"`
	Source         string                  `json:"source"`
	Rows           []groundbaitAffinityRow `json:"rows"`
}

func TestRebakeGroundbaitAffinity(t *testing.T) {
	defs := groundbaitDefsLoad()
	if defs.badErr != "" {
		t.Fatalf("窝料资产解析失败: %s", defs.badErr)
	}
	ecology := groundbaitProbeEcology(t)

	// 旧表：用来算差异摘要（哪几行变了、哪几行动增删）
	oldRows := groundbaitProbeAffinity(t)

	rows := make([]groundbaitAffinityRow, 0, 1024)
	itemIDs := make([]string, 0, len(defs.items))
	for id := range defs.items {
		itemIDs = append(itemIDs, id)
	}
	sort.Strings(itemIDs)

	frozenTotal, skipped := 0, 0
	for _, itemID := range itemIDs {
		item := defs.items[itemID]
		prof, ok := defs.profileOf(item.Materials)
		if !ok {
			t.Fatalf("%s 的成分算不出食性向量（用料 %v）", itemID, item.Materials)
		}
		if len(prof.Diet) == 0 {
			skipped++ // 纯载体（PVA 棒）：吸引由制作实例的填充决定，不落固定行
			continue
		}
		fishKeys := make([]string, 0, len(ecology))
		for k := range ecology {
			fishKeys = append(fishKeys, k)
		}
		sort.Strings(fishKeys) // 输出稳定，便于 diff
		for _, fishKey := range fishKeys {
			fish, hasRule, accepted := groundbaitProbeFish(ecology, fishKey)
			if groundbaitPredatorFrozen(fish, hasRule, accepted, 1.5, 0.8, 0.8) {
				frozenTotal++
				continue
			}
			app, spec := groundbaitAppetite(prof.Diet, fish, accepted, 0.08)
			if app <= 1.05 {
				continue
			}
			rows = append(rows, groundbaitAffinityRow{
				ItemID:   itemID,
				FishKey:  fishKey,
				Appetite: rebakeRound4(app),
				// Affinity 是历史遗留列：全库没有任何代码读它（grep 只有结构体
				// 定义与 JSON 反序列化），真正生效的是 Appetite（决定饱和加成）
				// 与 Special（决定专食性）。旧表里它约等于 affinityValue(appetite)
				// 但不完全相等（1013 行里 110 行有 0.001~0.03 的偏差），说明当年
				// 是另算的。这里写原始契合度 app，语义最清楚；读不到也不会坏。
				Affinity: rebakeRound4(app),
				Special:  rebakeRound4(spec),
			})
		}
	}

	// ── 差异摘要 ──
	added, removed, changed := 0, 0, 0
	newIdx := map[string]groundbaitAffinityRow{}
	for _, r := range rows {
		newIdx[r.ItemID+"\x00"+r.FishKey] = r
	}
	for itemID, byFish := range oldRows {
		for fishKey, old := range byFish {
			nw, ok := newIdx[itemID+"\x00"+fishKey]
			if !ok {
				removed++
				if removed <= 5 {
					t.Logf("  删除 %s x %s（旧 appetite=%.4f）", itemID, fishKey, old.Appetite)
				}
				continue
			}
			if !rebakeAlmostEqual(old.Appetite, nw.Appetite, 0.005) {
				changed++
				if changed <= 5 {
					t.Logf("  变化 %s x %s：%.4f -> %.4f", itemID, fishKey, old.Appetite, nw.Appetite)
				}
			}
		}
	}
	for k := range newIdx {
		itemID, fishKey := rebakeSplitKey(k)
		if _, ok := oldRows[itemID][fishKey]; !ok {
			added++
			if added <= 5 {
				t.Logf("  新增 %s x %s（新 appetite=%.4f）", itemID, fishKey, newIdx[k].Appetite)
			}
		}
	}

	t.Logf("烘焙完成：%d 行（旧 %d 行）；掠食冻结跳过 %d 次，纯载体窝料 %d 种",
		len(rows), rebakeCountRows(oldRows), frozenTotal, skipped)
	t.Logf("差异：新增 %d / 删除 %d / 数值变化 %d", added, removed, changed)

	if !*rebakeApply {
		t.Log("预演模式（未写盘）。确认无误后加 -args -apply 写回。")
		return
	}

	out := groundbaitAffinityOut{
		ProfileVersion: fishEcologyProfileVersion,
		Source:         "offline-probe-v1.4-rebaked-" + rebakeItoa(fishEcologyProfileVersion),
		Rows:           rows,
	}
	buf, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	buf = append(buf, '\n')
	path := filepath.Join("assets", "groundbait_affinity.json")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已写回 %s（profile_version=%d）", path, fishEcologyProfileVersion)
}

func rebakeAlmostEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func rebakeRound4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}

func rebakeSplitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

func rebakeCountRows(m map[string]map[string]groundbaitAffinityRow) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

func rebakeItoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
