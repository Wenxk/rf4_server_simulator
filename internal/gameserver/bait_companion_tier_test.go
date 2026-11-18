package gameserver

import (
	"testing"
	"time"
)

// 回归（2026-11-18 实机）：普通鲤鱼（carp）用鱼块（fish_pieces）被钓上来，
// 判鱼追踪里饵料层 11 → 6.64，正好是 maxBaitPreference×0.35 的中档倍率。
//
// 根因：中档兜底只看 companion 的"在列"档位（tier=1），不看这饵在不在该鱼的
// 食谱里。普通鲤鱼×鱼块的 companion 行是 tier1/evidence=11 的孤例，攻略 allow
// 全是植物饵、生态 diet 里没有 fish 项，本该按碎尾档排除，却被抬成 0.6。
//
// 判据：companion 证据只能在**食性族内**加权（allow 命中或同族替代），
// 鱼根本不吃的东西不因为"官服有人误中过"就变成常规饵；tier3 强证据仍不被食性族限制。
func TestBaitCompanionEvidenceStaysInsideDietFamily(t *testing.T) {
	s, db := seedBaitRuleTest(t)
	for _, q := range []string{
		`INSERT INTO loc_wiki_fish(loc,fish_key,fish_zh) VALUES('rules-test','carp','普通鲤鱼'),('rules-test','gudgeon','葛氏鲈塘鳢')`,
		// 客户端真实鱼块：category 27 + baitType 1（杂类），名称"鱼块"。
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999841,'fish_pieces',27,'{"fopbhlmkcpe":1}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('fish_pieces','鱼块')`,
		// 虫饵（baitType 6）：常见拟鲤的 allow 含蚯蚓 → 同族；普通鲤鱼的 allow 无虫类 → 跨族。
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999842,'tb_worm',27,'{"fopbhlmkcpe":6}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('tb_worm','测试蠕虫')`,
		// 跨族但 tier3 的强证据饵
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(999843,'tb_fish_strong',27,'{"fopbhlmkcpe":1}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('tb_fish_strong','测试鱼块')`,
		`INSERT INTO companion_bait_fish(bait_id,fish_key,tier,evidence) VALUES
			('fish_pieces','carp',1,11),
			('fish_pieces','gudgeon',1,4),
			('tb_worm','c.roach',1,30),
			('tb_worm','carp',1,30),
			('tb_fish_strong','carp',3,900)`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// 开启分级（seedBaitRuleTest 默认关闭）
	s.tunMu.Lock()
	delete(s.tunCache, "bait_share_strong")
	s.tunMu.Unlock()
	s.loadRf4Share() // 无 rf4 纪录：share 全部 -1，走 companion 兜底

	cache := newFishingRollCache()
	now := time.Unix(worldDayEpochUnix+1500, 0)
	weight := func(fish, bait string) float64 {
		t.Helper()
		keys, w := s.filterFishBaitSet("rules-test", []string{bait}, []string{fish}, []float64{1}, now, fishRollContext{cache: cache})
		if len(keys) == 0 {
			return 0
		}
		return w[0]
	}

	// 实机问题本体：植物食谱的鲤鱼 × 鱼块（tier1 孤例）→ 排除。
	if got := weight("carp", "fish_pieces"); got != 0 {
		t.Fatalf("普通鲤鱼×鱼块应排除，实得 %f", got)
	}
	// 明写"吃鱼肉"的葛氏鲈塘鳢 × 鱼块 → allow 命中（鱼块已归一到鱼肉族）→ 满分。
	if got := weight("gudgeon", "fish_pieces"); got < 1 {
		t.Fatalf("葛氏鲈塘鳢×鱼块应满分，实得 %f", got)
	}
	// 同族证据保留既有行为：常见拟鲤的 allow 含蚯蚓，虫饵 tier1 → 中档 0.35 倍率。
	if got := weight("c.roach", "tb_worm"); got < maxBaitPreference*0.34 || got > maxBaitPreference*0.36 {
		t.Fatalf("常见拟鲤×虫饵应中档 ~%f，实得 %f", maxBaitPreference*0.35, got)
	}
	// 跨族的 tier1 证据不成立：鲤鱼食谱里没有虫类，也不吃鱼块。
	if got := weight("carp", "tb_worm"); got != 0 {
		t.Fatalf("普通鲤鱼×虫饵（tier1 跨族）应排除，实得 %f", got)
	}
	// 边界：tier3 是官服强证据（真实钓法），不受食性族限制。
	if got := weight("carp", "tb_fish_strong"); got < 1 {
		t.Fatalf("tier3 强证据应满分，实得 %f", got)
	}
}
