package gameserver

import (
	"context"
	"math"
	"testing"

	"rf4server/internal/config"
	"rf4server/internal/logging"
	"rf4server/internal/store"
)

// 后台覆盖生效：同一尾鱼的经验与卖价都按覆盖值走，清空覆盖后回到内置表。
func TestSpeciesEconOverridesXPAndPrice(t *testing.T) {
	srv, db := newEconomyTxTestServer(t)
	ctx := context.Background()
	now := 1750000000.0
	fish := &store.KeepnetEntry{Species: "bream", Cat: 3, WeightG: 1000, TS: now}

	baseXP, _ := srv.catchValue("bream", 3, 1000)
	if baseXP != 1499 { // trunc(1.4999 × 1000)
		t.Fatalf("内置经验=%d，期望 1499", baseXP)
	}
	basePrice := srv.marketPriceCentsForMap(fish, now, marketBaseMap)
	if basePrice <= 0 {
		t.Fatalf("内置报价必须为正，实得 %d", basePrice)
	}

	xp, price := 2.0, 50.0
	if err := db.SpeciesEconSave(ctx, "bream", []store.SpeciesEcon{
		{Grade: 3, XPC: &xp, PricePerKg: &price},
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	srv.invalidateEconOverrides()

	if got, _ := srv.catchValue("bream", 3, 1000); got != 2000 {
		t.Fatalf("覆盖后经验=%d，期望 trunc(2.0×1000)=2000", got)
	}
	gotPrice := srv.marketPriceCentsForMap(fish, now, marketBaseMap)
	if gotPrice <= basePrice {
		t.Fatalf("覆盖后报价 %d 应高于内置 %d", gotPrice, basePrice)
	}
	// 50 银/kg 对内置 9.93 银/kg（bream 基准 3.31 × 上星 3 倍）：
	// 同一日系数与新鲜度约掉，比值应接近 50/9.93。
	if ratio := float64(gotPrice) / float64(basePrice); math.Abs(ratio-50.0/9.93) > 0.05 {
		t.Fatalf("覆盖价/内置价=%.3f，期望 ≈%.3f", ratio, 50.0/9.93)
	}
	// 水域倍率仍要生效：1 图（0.831）必须是基准水域（1.0）的 0.831 倍。
	lake := srv.marketPriceCentsForMap(fish, now, "level_001_lake")
	if ratio := float64(lake) / float64(gotPrice); math.Abs(ratio-0.831) > 0.01 {
		t.Fatalf("1 图/基准=%.4f，期望 0.831", ratio)
	}

	// 后台试算必须与主服同价：同一条鱼、同一时刻、同一覆盖值。
	if _, cents := PreviewSpeciesEcon("bream", 3, 1000, xp, price, marketBaseMap, now, now); cents != gotPrice {
		t.Fatalf("试算=%d，主服报价=%d", cents, gotPrice)
	}

	if _, err := db.SpeciesEconReset(ctx, "bream"); err != nil {
		t.Fatal(err)
	}
	srv.invalidateEconOverrides()
	if got, _ := srv.catchValue("bream", 3, 1000); got != baseXP {
		t.Fatalf("恢复默认后经验=%d，期望 %d", got, baseXP)
	}
	if got := srv.marketPriceCentsForMap(fish, now, marketBaseMap); got != basePrice {
		t.Fatalf("恢复默认后报价=%d，期望 %d", got, basePrice)
	}
}

// 只覆盖经验时，鱼价继续走内置表；只覆盖鱼价时经验不变。
func TestSpeciesEconPartialOverride(t *testing.T) {
	srv, db := newEconomyTxTestServer(t)
	ctx := context.Background()
	now := 1750000000.0
	fish := &store.KeepnetEntry{Species: "bream", Cat: 3, WeightG: 1000, TS: now}
	basePrice := srv.marketPriceCentsForMap(fish, now, marketBaseMap)

	xp := 0.5
	if err := db.SpeciesEconSave(ctx, "bream", []store.SpeciesEcon{{Grade: 3, XPC: &xp}}, "tester"); err != nil {
		t.Fatal(err)
	}
	srv.invalidateEconOverrides()
	if got, _ := srv.catchValue("bream", 3, 1000); got != 500 {
		t.Fatalf("覆盖经验=%d，期望 500", got)
	}
	if got := srv.marketPriceCentsForMap(fish, now, marketBaseMap); got != basePrice {
		t.Fatalf("只改经验时报价应不变：%d ≠ %d", got, basePrice)
	}

	price := 20.0
	if err := db.SpeciesEconSave(ctx, "bream", []store.SpeciesEcon{{Grade: 3, PricePerKg: &price}}, "tester"); err != nil {
		t.Fatal(err)
	}
	srv.invalidateEconOverrides()
	if got, _ := srv.catchValue("bream", 3, 1000); got != 1499 {
		t.Fatalf("清掉经验覆盖后应回到 1499，实得 %d", got)
	}
	if got := srv.marketPriceCentsForMap(fish, now, marketBaseMap); got <= basePrice {
		t.Fatalf("只改鱼价时报价应上调：%d ≤ %d", got, basePrice)
	}
}

// 没有 DB 的降级路径：不查库、不 panic，直接按内置表返回。
func TestSpeciesEconWithoutDB(t *testing.T) {
	srv := NewServer(config.Game{Tunnel: config.Tunnel{PSK: "econ"}}, logging.Discard(), nil)
	if got, _ := srv.catchValue("bream", 3, 1000); got != 1499 {
		t.Fatalf("无 DB 时经验=%d，期望 1499", got)
	}
}

// 试算页恒等式：卖价 = 每公斤价 × 当日行情系数 × 克重/1000 × 新鲜度。
// 这条测试存在的原因：后台只显示「每公斤价」和「卖价」时，1kg 卖价却比每公斤价高几个点，
// 被当成算错（2026-11-23 报障）。真正的乘数是 dailyMarketFactor（0.95–1.05）。
func TestPreviewPriceEqualsPerKgTimesMarketFactor(t *testing.T) {
	overlayOnce.Do(loadMarketOverlay)
	now := 1795438800.0 // 固定时刻，保证系数可复现
	for _, c := range []struct {
		species string
		grade   int64
	}{
		{"a_omul", 3}, {"bream", 1}, {"eel", 1}, {"n_carp", 1},
	} {
		perKg := marketSilverPerKgOverlay(marketBaseMap, c.species, c.grade)
		if perKg <= 0 {
			t.Fatalf("%s g%d 基准价缺失", c.species, c.grade)
		}
		factor := SpeciesDailyMarketFactor(c.species, now)
		if factor < 0.95 || factor > 1.05 {
			t.Fatalf("%s 行情系数 %v 越界（应 0.95–1.05）", c.species, factor)
		}
		want := int64(math.Round(perKg * factor * 1.0 * 100))
		_, got := PreviewSpeciesEcon(c.species, c.grade, 1000, 0, 0, marketBaseMap, now, now)
		if got != want {
			t.Errorf("%s g%d 1kg 卖价=%d 分，期望 %d 分（每公斤 %.2f × 行情 %.4f）",
				c.species, c.grade, got, want, perKg, factor)
		}
	}
}
