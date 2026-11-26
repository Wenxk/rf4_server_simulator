package gameserver

import (
	"context"
	"strings"
	"testing"

	"rf4server/internal/store"
)

// 钩号推荐必须与判鱼公式同源：入口容量用 hookWeightCapG × gearOverloadFactor，
// 不能另起一套。这条测试钉住「蓝冠线刚好被盖住」这个性质。
func TestFishQueryHookCoversBlueLine(t *testing.T) {
	s, _ := newStaticRigTestServer(t)
	ctx := context.Background()
	if _, err := s.SyncHookBiteProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	// 欧鲌是之前实际排查过的鱼：蓝冠线 160g，结论是 S18（2.50mm）。
	hook := s.recommendFishQueryHook(ctx, "c_bleak")
	if hook == nil {
		t.Fatal("欧鲌没有推荐钩号")
	}
	if hook.DisplaySize != "S18" {
		t.Fatalf("欧鲌推荐钩号=%q，期望 S18（钩长 %.2fmm，入口 %.0fg）",
			hook.DisplaySize, hook.LengthMM, hook.GapeG)
	}
	if hook.GapeG < hook.TargetG {
		t.Fatalf("推荐钩盖不住蓝冠线: 入口 %.0fg < 目标 %.0fg", hook.GapeG, hook.TargetG)
	}
	if hook.Basis != "blue" {
		t.Fatalf("基准来源=%q，期望 blue", hook.Basis)
	}
	// 必须真的是「刚好盖住」：比它小一号的钩就应该盖不住了，否则推荐会偏大。
	smaller := hookSizeForDisplay(hook.DisplaySize) - 1
	if smaller >= 0 {
		for _, p := range mustListHooks(t, s) {
			if p.Size != smaller {
				continue
			}
			if hookGapeForProfile(p) >= hook.TargetG {
				t.Fatalf("刻度 %d（%s）也能盖住 %.0fg，推荐号偏大",
					smaller, p.DisplaySize, hook.TargetG)
			}
			break
		}
	}
}

// 钩越长入口越大，推荐号不该出现「大钩给小鱼」这种反向结果。
func TestFishQueryHookGrowsWithFishSize(t *testing.T) {
	s, _ := newStaticRigTestServer(t)
	ctx := context.Background()
	if _, err := s.SyncHookBiteProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	order := []string{"c_bleak", "ruffe", "s.bream", "c.roach", "crucian", "bream"}
	prev := int64(-1)
	for _, fish := range order {
		hook := s.recommendFishQueryHook(ctx, fish)
		if hook == nil {
			t.Fatalf("%s 没有推荐钩号", fish)
		}
		if prev >= 0 && hook.Size < prev {
			t.Fatalf("%s 推荐号（刻度 %d）比上一个鱼种（刻度 %d）还小，顺序反了",
				fish, hook.Size, prev)
		}
		prev = hook.Size
	}
}

// 覆盖性：全库每个鱼种都必须有推荐钩号，不能有「查了但没钩号」的黑洞。
// 同时确认巨型鱼走的是「拉力不足」而不是「入口盖不住」——最大钩（48mm，入口约
// 2573kg）确实盖得住最大的蓝冠线（姥鲨 2500kg）。
func TestFishQueryHookCoversEverySpecies(t *testing.T) {
	s, _ := newStaticRigTestServer(t)
	ctx := context.Background()
	if _, err := s.SyncHookBiteProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.SQL.QueryContext(ctx, `SELECT fish_key FROM species_weight WHERE super_trophy_line_g>0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(keys) < 200 {
		t.Fatalf("只有 %d 个鱼种，静态数据没加载全", len(keys))
	}
	for _, key := range keys {
		hook := s.recommendFishQueryHook(ctx, key)
		if hook == nil {
			t.Fatalf("%s 没有推荐钩号", key)
		}
		if hook.GapeG+1 < hook.TargetG {
			t.Fatalf("%s 推荐钩盖不住: 入口 %.0fg < 目标 %.0fg", key, hook.GapeG, hook.TargetG)
		}
		if hook.DisplaySize == "" {
			t.Fatalf("%s 推荐钩缺少游戏钩号", key)
		}
	}

	// 欧洲鳇：盖得住，但没有任何型号拉力够 1 吨，必须明说钩子会先断。
	beluga := s.recommendFishQueryHook(ctx, "beluga")
	if beluga == nil {
		t.Fatal("欧洲鳇没有推荐钩号")
	}
	if !beluga.StrengthShort {
		t.Fatalf("欧洲鳇应标记 strength_short，实际拉力 %.1fkg / 需要 %.0fkg",
			beluga.StrengthKg, beluga.TargetG/1000)
	}
	if line := formatFishQueryHook(beluga); !strings.Contains(line, "钩子会先断") {
		t.Fatalf("回复没说明拉力不足: %s", line)
	}
	// 普通鱼不该误报。
	if crucian := s.recommendFishQueryHook(ctx, "crucian"); crucian == nil || crucian.StrengthShort {
		t.Fatalf("鲫鱼不该标记拉力不足: %+v", crucian)
	}
}

// 钩号进回复的位置要对：有方案时跟在标题后，禁口时也要给。
func TestFishQueryReplyCarriesHookLine(t *testing.T) {
	hook := &FishQueryHook{DisplaySize: "S18", LengthMM: 2.5, GapeG: 169, TargetG: 160,
		Basis: "blue", StrengthKg: 3.15, Brand: "CHK"}
	result := &FishQueryResponse{Hook: hook, Plans: []FishQueryPlan{
		{MapName: "测试湖", BaitName: "蚯蚓", RigName: "浮钓", Chance: .2, TheoreticalPerHour: 12},
	}}
	reply := formatFishQueryReply("欧鲌", result)
	if !strings.Contains(reply, "推荐鱼钩：S18") || !strings.Contains(reply, "2.5mm") ||
		!strings.Contains(reply, "169g") {
		t.Fatalf("回复缺少推荐钩号: %s", reply)
	}
	// 标题行后紧跟钩号，方便一眼看到。
	lines := strings.Split(reply, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "推荐鱼钩：") {
		t.Fatalf("钩号行位置不对: %s", reply)
	}

	// 禁口路径同样要带钩号。
	diel := &FishQueryResponse{Hook: hook, Diel: &FishQueryDiel{
		GameHour: 3, GameMinute: 20, Segment: "夜", NextGameHour: 6, NextSegment: "晨", WaitSeconds: 900,
	}}
	if got := formatFishQueryReply("欧鲌", diel); !strings.Contains(got, "推荐鱼钩：S18") {
		t.Fatalf("禁口回复缺少推荐钩号: %s", got)
	}

	// 拿不到档案时不能凭空造一个号出来。
	empty := formatFishQueryReply("欧鲌", &FishQueryResponse{Plans: result.Plans})
	if strings.Contains(empty, "推荐鱼钩") {
		t.Fatalf("没有钩子数据时不应输出钩号: %s", empty)
	}
}

func mustListHooks(t *testing.T, s *Server) []store.HookBiteProfile {
	t.Helper()
	rows, err := s.DB.ListHookBiteProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make([]store.HookBiteProfile, 0, len(rows))
	for _, p := range rows {
		if p.DisplaySize == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
