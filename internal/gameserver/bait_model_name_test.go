package gameserver

// 型号名写进规则必须命中该型号的全部规格（回归守卫）。
//
// # 这个测试守的是什么
//
// 后台「鱼饵层级选择器」的第三层是**型号**（`Spiker #2`、`Baby Craw 3.5`），
// 点一下写进规则的就是这个型号名，而不是某一条商品的完整名。用户的心智模型是
// 「该型号的所有规格都吃」，所以判鱼侧必须让型号名覆盖整组。
//
// 2026-11-25 实测发现：873 个型号组里有 **37 组零命中**。根因在 matchOne 的
// 数字边界判定——型号名以数字结尾时（`Baby Craw 3.5`、`Salmon T1 Crank 10`），
// 代码看到尾字符是数字就要求「剩余部分不是数字」，而实际商品名是补零编号
// （`Baby Craw 3.5 001`），于是整组被判为不匹配。表现是后台能选、抽查签按型号
// 分组、判鱼却一条都不认，属于静默失效。
//
// 修复后命中率 872/873。剩下 1 组是「虾」——它是拟饵 lureType=15 的整类词，
// 按枚举优先解析，属预期行为，因此本测试允许整类词豁免。
//
// # 为什么按「型号组」而不是按「单款饵」断言
//
// 单款饵用完整名匹配一直是对的；真正会失效的是跨规格的型号名。按组断言才能
// 抓住「部分规格命中、部分不命中」这种半失效——它比全失效更难被发现。

import (
	"sort"
	"strings"
	"testing"
)

// TestModelNameMatchesAllSpecs 检查每个型号组的组名能命中该组全部规格。
//
// 必须用 openStaticTestDB：item_def 只存在于静态资料库
// （server_data/static.sqlite3），openItemTestDB 那种只复制迁移库的写法
// 读不到任何饵，会让本测试空跑成 0 组。
func TestModelNameMatchesAllSpecs(t *testing.T) {
	db := openStaticTestDB(t)
	srv := &Server{DB: db}
	baits := loadAllRuleBaits(t, srv, db)
	if len(baits) < 100 {
		t.Skipf("测试库饵料只有 %d 条，不足以校验型号组", len(baits))
	}

	groups := map[string][]ruleBait{}
	for _, b := range baits {
		base := baitBaseName(b.name)
		if base == "" {
			continue
		}
		groups[base] = append(groups[base], b)
	}

	type failure struct {
		base string
		hit  int
		tot  int
		miss []string
	}
	var fails []failure
	modelGroups := 0

	for base, list := range groups {
		if len(list) < 2 {
			continue // 单规格不构成型号组
		}
		// 整类词（如「虾」）优先按枚举解析，不参与型号名语义，豁免。
		if _, isTypeTerm := baitTypeTermOf(ruleKey(base)); isTypeTerm {
			continue
		}
		modelGroups++
		hit := 0
		var miss []string
		for _, b := range list {
			if b.matches(base) {
				hit++
			} else {
				miss = append(miss, b.name)
			}
		}
		if hit != len(list) {
			fails = append(fails, failure{base, hit, len(list), miss})
		}
	}

	if modelGroups < 50 {
		t.Skipf("测试库型号组只有 %d 个，样本不足", modelGroups)
	}
	sort.Slice(fails, func(i, j int) bool { return fails[i].tot > fails[j].tot })
	if len(fails) > 0 {
		for i, f := range fails {
			if i >= 10 {
				break
			}
			t.Errorf("型号组 %q 只命中 %d/%d，未命中样例: %v", f.base, f.hit, f.tot, f.miss[:min(3, len(f.miss))])
		}
		t.Fatalf("共 %d 个型号组里 %d 组未覆盖全部规格（见上）", modelGroups, len(fails))
	}
	t.Logf("型号组 %d 个，型号名全部命中该组规格", modelGroups)
}

// TestModelNameZeroPaddedIndex 固定住「补零编号算规格号」这条判据。
//
// 这是 37 组零命中的直接病灶：`Baby Craw 3.5 001` 的尾巴是 `001`，
// 属于规格编号；而 `Hunter 1` 后面接 `000`（即 `Hunter 1000`）不是规格号，
// 是另一款产品的名字，不能误判成同一型号。
func TestModelNameZeroPaddedIndex(t *testing.T) {
	for _, c := range []struct {
		rest string
		want bool
		why  string
	}{
		{"001", true, "标准补零规格号"},
		{"012", true, "补零规格号"},
		{"002", true, "补零规格号"},
		{"000", false, "全零不是规格号，是产品名的一部分（Hunter 1000）"},
		{"00", false, "两位全零同样不是"},
		{"0", false, "单字符太短"},
		{" 001", false, "带空格说明已经跨到别的词边界，不走这条判据"},
		{"abc", false, "非数字"},
	} {
		if got := isZeroPaddedIndex(c.rest); got != c.want {
			t.Fatalf("isZeroPaddedIndex(%q)=%v，期望 %v（%s）", c.rest, got, c.want, c.why)
		}
	}
}

// TestModelNameCoversSpecVariants 覆盖数据里实际出现的四种规格写法。
//
// 这些写法都曾在真实库里导致整组零命中，逐条固定住，避免以后改 matchOne 时回归。
func TestModelNameCoversSpecVariants(t *testing.T) {
	for _, c := range []struct {
		model string // 写进规则的型号名
		name  string // 库里的完整商品名
		why   string
	}{
		{"Spiker #2", "Spiker #2 001", "规格是补零编号"},
		{"Baby Craw 3.5", "Baby Craw 3.5 001", "型号名本身带小数点，规格仍是补零编号"},
		{"板条路亚 120", "板条路亚 120-1", "规格用连字符"},
		{"Baby Tube 2.4", "Baby Tube\u00a02.4\u00a0002", "全角 NBSP 分隔"},
		{"Stor Fisk L33-450", "Stor Fisk L33-450 #1", "`#序号` 是规格号"},
	} {
		b := ruleBait{name: c.name, category: 17}
		if !b.matches(c.model) {
			t.Errorf("型号 %q 未命中商品 %q（%s）", c.model, c.name, c.why)
		}
	}
	// `Gummi Makk` 是整类词（lureType=13），按枚举匹配，名字完全不参与，
	// 所以这里要用真实的 lureType 才成立——不能靠名字前缀。
	if !(ruleBait{name: "Gummi Makk 10-001", category: 17, lureType: 13}).matches("Gummi Makk") {
		t.Error("整类词 Gummi Makk 应命中 lureType=13 的饵")
	}
	if (ruleBait{name: "Gummi Makk 10-001", category: 17, lureType: 3}).matches("Gummi Makk") {
		t.Error("整类词 Gummi Makk 不应命中其它 lureType")
	}
	// 反向：不能因为放宽边界就把别的产品吞进来。
	for _, c := range []struct {
		model string
		name  string
	}{
		{"Hunter 1", "Hunter 1000"}, // 数字边界仍在：1000 不是 1 的规格
		{"Baby Craw 2.5", "Baby Craw 3.5 001"},
	} {
		b := ruleBait{name: c.name, category: 17}
		if b.matches(c.model) {
			t.Errorf("型号 %q 不应命中 %q（不同产品）", c.model, c.name)
		}
	}
}

// TestBaitBaseNameStable 型号名归一化规则。
//
// 注意「只剥一层」是设计而非缺陷：`Salmon T1 Crank 10 001` 剥一层得
// `Salmon T1 Crank 10`（型号名），再剥就把型号号 10 也吃了，
// 会让 5/10/15 三个不同型号并成一组。因此这里不测幂等，只测单层结果。
func TestBaitBaseNameStable(t *testing.T) {
	for _, c := range []struct {
		in, want, why string
	}{
		{"Spiker #2 001", "Spiker #2", "规格是补零编号，型号号 #2 要留住"},
		{"Baby Craw 3.5 001", "Baby Craw 3.5", "型号名带小数点"},
		{"板条路亚 120-1", "板条路亚 120", "规格用连字符"},
		{"Gummi Makk 10", "Gummi Makk", "尺寸号是规格，不是型号"},
		{"Aikkila 4g", "Aikkila 4g", "以字母结尾，不剥"},
		{"Stor Fisk L33-450 #1", "Stor Fisk L33-450", "`#1` 是规格号，不能留下孤立井号"},
		{"Hunter 1000", "Hunter", "尾部数字照剥（1000 本身是型号号，属已知取舍）"},
		{"", "", "空名原样返回"},
	} {
		got := baitBaseName(c.in)
		if got != c.want {
			t.Fatalf("baitBaseName(%q)=%q，期望 %q（%s）", c.in, got, c.want, c.why)
		}
	}
	// 剥出来的型号名不能以分隔符收尾：否则界面上会出现 `Stor Fisk L33-450 #`
	// 这种残名，自动轮换也会把它当成一个独立型号分组。
	for _, in := range []string{"Stor Fisk L33-450 #1", "Spiker #2 001", "板条路亚 120-1"} {
		b := baitBaseName(in)
		if strings.HasSuffix(b, "#") || strings.HasSuffix(b, "-") || strings.HasSuffix(b, "_") {
			t.Fatalf("baitBaseName(%q)=%q 以分隔符结尾", in, b)
		}
	}
}

// 内置泛型 min（Go 1.21+）直接用，不要在本文件定义同名函数——
// 包内定义会遮蔽内置 min，导致 cafe_pricing.go 等处的 int64 调用编译失败。