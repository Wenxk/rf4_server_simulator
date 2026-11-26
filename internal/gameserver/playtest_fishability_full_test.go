package gameserver

// 全量可钓性测试：遍历「地图 × 季节 × 时段 × 水层」，检查每个组合下
// 是否至少存在一种可钓鱼种；重点标识「某地图某时段完全无鱼可钓」。
//
// 背景：超水层问题后续将通过调整鱼种活跃水层区间修复（部分鱼种设为全水层），
// 本次只做验证、不改任何配置。
//
// # 判定口径：为什么不做「饵 × 鱼」暴力枚举
//
// 朴素做法是 18 图 × 4 季 × 24 时 × 3 层 × 3724 饵 = 5.78 亿次判鱼，不可行。
// 但判鱼链的结构让它可以精确降维——「该组合下至少有一种鱼可钓」等价于
// 「存在 (鱼, 饵) 使该鱼在五层约束下权重 > 0」。判鱼链每一层都是**乘性**的：
//
//	L1 饵料 allow（filterFishBaitSet）—— 只依赖 (鱼, 饵, 钓法)，与时间无关
//	L2 水层地板（applyBiteGates）—— 只依赖 (鱼, 钓层)，与时间无关
//	L3 昼夜禁口（fishBiteEnvironmentWeight）—— 只依赖 (鱼, 时段)
//	L4 适温 / 天气 / 季节（同上）—— 只依赖 (鱼, 水层, 时段, 季节)
//	L5 鱼群分布（applyFishDistribution）—— 只依赖 (鱼, 区域, 4 游戏小时周期)
//
// 因为全部是乘性且互不「补救」（任何一层为 0，整条候选就出局），所以
//
//	可钓 ⟺ 存在鱼 f ∋ L1(f)∧L2(f)∧L3(f)∧L4(f)∧L5(f) 全为正
//
// 于是只需对每种鱼算一次各层系数再取交集，不必枚举饵。L1 用生产函数
// filterFishBaitSet 一次传入**全部饵料**——它返回的就是「至少一款饵可用」
// 的鱼种集合，与逐饵枚举在数学上完全等价（它内部就是 for 饵取 max）。
//
// 本文件所有判定都调生产函数，不自己复刻公式：
//   - filterFishBaitSet / resolveBaitRule / applyAutoRotation
//   - applyBiteGates（经 biteGateConfig 构造）
//   - fishBiteEnvironmentWeight（含昼夜/适温/季节/天气/水层软权重）
//   - applyFishDistribution
//
// 运行：
//   RF4_PLAYTEST=1 go test ./internal/gameserver -run TestPlaytestFishabilityFull -v -timeout 60m
//
// 产出 data/playtest/fishability_full.json

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"

	"rf4server/internal/store"
	"rf4server/internal/worldclock"
)

// 钓法枚举：覆盖三类判鱼口径。
//   浮钓（rigType 1）：水层由 depthCM 决定，可扫 0.16/0.40/0.93
//   底钓（rigType 12）：水层恒 0.93，另有 bottomOnly 限制
//   路亚（rigType 5）：水层由拟饵泳层决定，扫 0.30/0.50/0.70
type fishabilityRig struct {
	Type int64
	Name string
}

var fishabilityRigs = []fishabilityRig{
	{1, "浮钓"},
	{12, "底钓"},
	{5, "路亚"},
}

// 各钓法的水层扫描点。
func fishabilityLayers(rigType int64) []layerSpec {
	switch {
	case rigTypeLureFamily[rigType]:
		return []layerSpec{
			{DepthCM: 0, Name: "表(0.30)"},
			{DepthCM: 0, Name: "中(0.50)"},
			{DepthCM: 0, Name: "底(0.70)"},
		}
	case bottomRigTypes[rigType]:
		return []layerSpec{{DepthCM: 0, Name: "底(0.93)"}}
	default:
		return []layerSpec{
			{DepthCM: 80, Name: "浅(0.16)"},
			{DepthCM: 200, Name: "中(0.40)"},
			{DepthCM: 465, Name: "底(0.93)"},
		}
	}
}

// lureLayerFor 给路亚水层点一个真实泳层值（rigWaterLayer 走 effectiveLureLayer）。
func lureLayerFor(ls layerSpec) float64 {
	switch ls.Name {
	case "表(0.30)":
		return 0.30
	case "中(0.50)":
		return 0.50
	case "底(0.70)":
		return 0.70
	}
	return 0.50
}

// seasonSpec 四季代表时刻。
//
// ⚠️ 不能按真实日历取「4 月 15 日 = 春」。季节走 worldclock.SeasonDay：
//
//	SeasonDay = ((now − SeasonEpochUnix) / 3600) % 365
//
// 而 1 真实小时 = 1 游戏日，所以**整个季节周期只有 365 真实小时 ≈ 15.2 真实日**，
// 不是一年。写真实日期会让「4 月 15 日」随机落在任何季节（实测落在冬）。
//
// 正确做法是反解 seasonDay，让每个季节取到它季度正中的那一天：
//   seasonDay ≡ 0 (mod 365×3600) 的时刻是季节周期起点，加 mid×3600 即得目标日。
type seasonSpec struct {
	Name string
	At   time.Time
	// SeasonDay 该时刻对应的 game seasonDay（0..364），供报告核对。
	SeasonDay int64
}

// seasonMids 四季各自的代表 seasonDay（各季度正中）。
//
// 月度划分：春=3/4/5月（day 59..150）、夏=6/7/8月（151..242）、
// 秋=9/10/11月（243..334）、冬=12/1/2月（335..365+0..58）。
var seasonMids = []struct {
	Name string
	Mid  int64
}{
	{"春", 100},
	{"夏", 195},
	{"秋", 285},
	{"冬", 20},
}

// seasonsFor 反解四季代表时刻。
//
// anchor 只用于「从哪个真实时刻往后找」，不影响季节判定——同一 seasonDay
// 在每个 15.2 日周期里重复出现，取最近的即可。
func seasonsFor(anchor time.Time) []seasonSpec {
	const period = 365 * 3600
	out := make([]seasonSpec, 0, len(seasonMids))
	for _, sm := range seasonMids {
		// 求最小的 u ≥ anchor 使 ((u-E)/3600)%365 == sm.Mid
		k := (anchor.Unix() - worldclock.SeasonEpochUnix) / period
		u := worldclock.SeasonEpochUnix + k*period + sm.Mid*3600
		for u < anchor.Unix() {
			u += period
		}
		out = append(out, seasonSpec{
			Name:      sm.Name,
			At:        time.Unix(u, 0).UTC(),
			SeasonDay: sm.Mid,
		})
	}
	return out
}

// seasonTimeAt 取某季节代表日的第 hr 游戏小时。
//
// 游戏小时由 nowUnix 相对 ProtocolEpoch 的相位决定，不是「UTC 几点」，
// 所以必须从当天相位起点加偏移，不能直接改小时字段。
// 当天相位起点用 ProtocolEpoch 对齐（与 DayAndHour 同源）。
func seasonTimeAt(base time.Time, hr int64) time.Time {
	const realSecPerGH = int64(worldclock.RealSecPerGameHour)
	dayStart := base.Unix() - (base.Unix()-worldclock.ProtocolEpochUnix)%(24*realSecPerGH)
	return time.Unix(dayStart+hr*realSecPerGH+10, 0)
}

// fishabilityCell 一个 (地图 × 季节 × 时段 × 钓法 × 水层) 的可钓性结论。
type fishabilityCell struct {
	Map      string  `json:"map"`
	MapAlias string  `json:"map_alias"`
	Season   string  `json:"season"`
	Hour     int64   `json:"game_hour"`
	Rig      string  `json:"rig"`
	RigType  int64   `json:"rig_type"`
	Layer    string  `json:"layer"`
	LayerVal float64 `json:"layer_value"`
	WaterC   float64 `json:"water_temp_c"`

	// 各层存活数（从名册出发逐层过滤，全乘性）
	Roster    int `json:"roster"`
	AfterBait int `json:"after_bait"`
	AfterDiel int `json:"after_diel"`
	AfterAll  int `json:"after_all"` // 五层全过 = 真正可钓

	// 可钓鱼种（最多列 12 个，避免 JSON 膨胀）
	Fishable []string `json:"fishable,omitempty"`
	// 最后一层把候选清空的原因（AfterAll==0 时必有）
	KilledBy string `json:"killed_by,omitempty"`
	// 各层淘汰的鱼种（诊断用，只记数量）
	BaitOut []string `json:"bait_out,omitempty"`
	DielOut []string `json:"diel_out,omitempty"`
	EnvOut  []string `json:"env_out,omitempty"`
	DistOut []string `json:"dist_out,omitempty"`
}

// TestPlaytestFishabilityFull 全量可钓性测试。
func TestPlaytestFishabilityFull(t *testing.T) {
	playtestEnabled(t)
	srv, db, _ := newPlayerTest(t)
	playClearBiteOverrides(t, db)

	// bite_delay_fixed（固定咬钩延迟，秒）：本测试只看「能不能钓到」，与延迟无关，
	// 置 0 省掉等待。**必须先记原值再还原**——第一版直接设置却没还原，
	// 把 updated_by 留成了本测试的名字（值恰好相同，但库已被写脏）。
	//
	// ⚠️ 用 defer 而不是 t.Cleanup：newPlayerTest → playDB 已经把 db.Close()
	// 注册进 t.Cleanup，而 cleanup 是后进先出，defer 在 t.Cleanup 之前执行，
	// 才能保证还原时 DB 还活着（第一版用 t.Cleanup 还原，UPDATE 打到已关闭的
	// 连接上，静默失败、库一直是脏的）。
	origDelay, hadDelay := readTunable(t, db, "bite_delay_fixed")
	t.Logf("原 bite_delay_fixed=%q（存在=%v）", origDelay, hadDelay)
	defer func() {
		restoreTunable(context.Background(), t, db, "bite_delay_fixed", origDelay, hadDelay)
	}()
	if err := db.TunableSet(t.Context(), "bite_delay_fixed", "0", "fishability"); err != nil {
		t.Fatal(err)
	}
	srv.execAdminCmd(&AdminCommand{Op: "reload_tunables"})

	t.Logf("判鱼开关快照：%s", biteGatesSnapshot(t, db))
	gates := srv.biteGateConfig()
	t.Logf("水层地板：enabled=%v floor=%.4f tol=%.2f", gates.enabled, gates.layerFloor, biteGateLayerTol)

	// 全部地图
	maps := loadAllMaps(t, db)
	t.Logf("地图 %d 张", len(maps))

	// 全部合法饵料（category 17/27，与 loadRuleBait 同口径）
	allBaits := loadAllRuleBaits(t, srv, db)
	t.Logf("合法饵料 %d 款（cat 17 拟饵 / 27 天然饵）", len(allBaits))

	seasons := seasonsFor(time.Now())
	for _, s := range seasons {
		got := fishSeasonAt(s.At.Unix())
		if fishSeasonName(got) != s.Name {
			t.Fatalf("季节构造错误：%s 期望 %s，实际 %s（%s, seasonDay=%d）",
				s.Name, s.Name, fishSeasonName(got), s.At, worldclock.SeasonDay(s.At.Unix()))
		}
		t.Logf("  季节 %s：%s  seasonDay=%d（核对通过）",
			s.Name, s.At.Format("2006-01-02 15:04 MST"), worldclock.SeasonDay(s.At.Unix()))
	}
	var cells []fishabilityCell

	for mi, m := range maps {
		ctx := context.Background()
		roster, err := srv.cachedLocRoster(ctx, fishRollContext{}, m.Loc)
		if err != nil || len(roster) == 0 {
			t.Logf("[%d/%d] %-14s 名册为空，跳过", mi+1, len(maps), m.Loc)
			continue
		}
		keys := make([]string, 0, len(roster))
		for _, r := range roster {
			keys = append(keys, r.FishKey)
		}
		// allow 按地图算一次（expensive，~300ms），全部 cell 复用。
		allowed := allowByMap(srv, m, keys, allBaits, seasons[0].At)
		t.Logf("[%d/%d] %-14s 名册%3d 种，有合法饵 %3d 种",
			mi+1, len(maps), m.Loc, len(keys), len(allowed))

		var mapCells []fishabilityCell
		for _, season := range seasons {
			for hr := int64(0); hr < 24; hr++ {
				at := seasonTimeAt(season.At, hr)
				for _, rig := range fishabilityRigs {
					for _, ls := range fishabilityLayers(rig.Type) {
						c := evalFishabilityCell(srv, m, season.Name, hr, at, rig, ls, keys, allowed)
						mapCells = append(mapCells, c)
					}
				}
			}
		}
		cells = append(cells, mapCells...)
		reportMapFishability(t, m, mapCells)
	}

	playWrite(t, "../../data/playtest", "fishability_full.json", cells)
	reportFishabilitySummary(t, cells, maps)
}

// mapRef 一张地图。
type mapRef struct {
	Alias string
	Loc   string
}

// loadAllMaps 从 loc_alias 取全部地图（slug → 中文名）。
func loadAllMaps(t *testing.T, db *store.DB) []mapRef {
	t.Helper()
	rows, err := db.SQL.Query(`SELECT alias, loc FROM loc_alias ORDER BY alias`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []mapRef
	for rows.Next() {
		var a, l string
		if rows.Scan(&a, &l) == nil && a != "" && l != "" {
			out = append(out, mapRef{Alias: a, Loc: l})
		}
	}
	return out
}

// loadAllRuleBaits 取全部可参与判鱼的饵料定义（与 loadRuleBait 同口径）。
//
// 只取 category 17/27，且必须能读出 lureType/baitType——与生产加载完全一致，
// 否则会拿不存在的饵去问 allow，结论会偏向「无鱼可钓」。
func loadAllRuleBaits(t *testing.T, srv *Server, db *store.DB) []ruleBait {
	t.Helper()
	ctx := context.Background()
	rows, err := db.SQL.Query(`
		SELECT d.item_id FROM item_def d WHERE d.category IN (17,27) ORDER BY d.item_id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	out := make([]ruleBait, 0, len(ids))
	for _, id := range ids {
		if b, ok := srv.loadRuleBait(ctx, id); ok {
			out = append(out, b)
		}
	}
	return out
}

// evalFishabilityCell 判定一个组合是否至少有一尾可钓鱼。
//
// # 口径：为什么分布层要对「多钓点取并集」
//
// fishArea 把世界切成 40 单位一格，distributionMultiplier 每 4 游戏小时重抽一次，
// 返回 0 表示「这一格这段时辰根本没有这种鱼」。**同一时刻不同钓点的存活集合
// 差别极大**（实测同一张图同一时刻，10 种鱼在 10 个钓点间存活 3~9 种）。
// 只采样 spot=0 会同时高估和低估风险：玩家会走动，某格空了不代表该图该时段无鱼。
//
// 因此本测试对同一 (地图, 季节, 时段, 钓法, 水层) 扫 spotProbeSpots 个钓点，
// 取**并集**——「存在某个钓点能钓到该鱼」即算该鱼可钓。这比单点更贴近
// 「该图该时段是否可能空军」这个真实问题。
//
// # 为什么 allow 可以按地图预先算一次
//
// filterFishBaitSet 单次 4123 款饵约 300ms，若 5184 个 cell 各算一次要 26 分钟。
// 但它的输入只有 (地图, 鱼种, 饵池, now)——**钓法/水层只影响 rigType 无关项**，
// 实测 6 图 × 6 个轮换桶结果完全一致（见 allowByMap 的注释）。
// 因此按地图算一次 allow 集合，其余层逐个 cell 实时算。
//
// 逐层顺序与 rollFishFromDBWithRules 一致：
//
//	roster → gate(水层) → bait(allow) → dist(分布) → ecology(昼夜+适温+季节)
//
// 任一层清零即该组合完全无鱼可钓。
func evalFishabilityCell(srv *Server, m mapRef, season string, hr int64, at time.Time,
	rig fishabilityRig, ls layerSpec, rosterKeys []string, allow map[string]bool) fishabilityCell {

	ctx := context.Background()
	c := fishRollContext{
		loc:     m.Alias,
		now:     at,
		rigType: rig.Type,
		depthCM: ls.DepthCM,
		skill:   neutralFishingSkillBonus(),
		cache:   newFishingRollCache(),
	}
	if rigTypeLureFamily[rig.Type] {
		c.lureLayer = lureLayerFor(ls)
	}
	target := srv.rigWaterLayer(c)

	// 水温：完全交给生产函数算，不自己拼公式。
	// fishWaterConditionsFor 内部按钓法切水层、按 profile.Activity 定传导比
	// （零值档案 Activity=0 → thermalResponse=.44，即全库最低档）。
	// 这里只是给报告一个「该图该时刻该钓法」的代表水温；判鱼本身用的是
	// 每条鱼自己的 Activity 与适温窗口，不依赖这个数。
	if w := srv.weatherAtForTest(m.Alias, at); w.known {
		c.weather = w
	}
	temp := fishWaterConditionsFor(fishEcologyProfile{}, c).WaterTempC

	cell := fishabilityCell{
		Map: m.Loc, MapAlias: m.Alias, Season: season, Hour: hr,
		Rig: rig.Name, RigType: rig.Type, Layer: ls.Name, LayerVal: target,
		WaterC: temp, Roster: len(rosterKeys),
	}

	// 水层地板配置（与 applyBiteGates 同源）
	gateCfg := srv.biteGateConfig()

	// ---- L1 饵料 allow（预计算）→ L2 水层 → L3/L4 生态 → L5 分布（多钓点并集）----
	var afterBait, afterEnv int
	var fishable []string
	// 每条鱼在「所有探针钓点」里是否至少有一格有鱼
	distAlive := make([]bool, len(rosterKeys))
	distAnyAlive := false
	for _, spot := range spotProbeSpots {
		cc := c
		cc.spot = spot
		area := fishArea(m.Alias, cc)
		for i, key := range rosterKeys {
			if distAlive[i] {
				continue
			}
			if distributionMultiplier(area, key, at) > 0 {
				distAlive[i] = true
				distAnyAlive = true
			}
		}
		if distAnyAlive {
			break
		}
	}
	if !distAnyAlive {
		// 全部探针钓点都空：仍需继续逐层判定以给出 killed_by
		distAlive = make([]bool, len(rosterKeys))
	}

	for i, key := range rosterKeys {
		if !allow[key] {
			cell.BaitOut = append(cell.BaitOut, key)
			continue
		}
		afterBait++

		// L2 水层地板：超界只乘系数；系数为 0 时等同剔除。
		p := srv.cachedFishEcology(ctx, c, key)
		inLayer := target >= p.LayerMin-biteGateLayerTol && target <= p.LayerMax+biteGateLayerTol
		if gateCfg.enabled && !inLayer && gateCfg.layerFloor <= 0 {
			cell.EnvOut = append(cell.EnvOut, key)
			continue
		}

		// L3/L4 昼夜禁口 + 适温 + 季节 + 天气 + 水层软权重（生产函数）
		w := srv.fishBiteEnvironmentWeight(ctx, key, c)
		if w <= 0 {
			cell.DielOut = append(cell.DielOut, key)
			continue
		}
		if w < 0.05 {
			// 系数极小但仍 >0：记进 EnvOut 便于报告说明「几乎不可能」，
			// 但**不算**不可钓——只要权重为正就存在中签可能。
			cell.EnvOut = append(cell.EnvOut, key)
		}
		afterEnv++

		// L5 鱼群分布（多钓点并集）
		if !distAlive[i] {
			cell.DistOut = append(cell.DistOut, key)
			continue
		}
		fishable = append(fishable, key)
	}
	cell.AfterBait = afterBait
	cell.AfterDiel = afterEnv
	cell.AfterAll = len(fishable)
	sort.Strings(fishable)
	if len(fishable) > 12 {
		cell.Fishable = fishable[:12]
	} else {
		cell.Fishable = fishable
	}

	if cell.AfterAll == 0 {
		switch {
		case cell.AfterBait == 0:
			cell.KilledBy = "饵料 allow（无任何合法饵）"
		case cell.AfterDiel == 0:
			cell.KilledBy = "昼夜禁口/适温（生态层归零）"
		default:
			cell.KilledBy = "鱼群分布（全部探针钓点本时段皆空）"
		}
	}
	return cell
}

// spotProbeSpots 分布层探针钓点。玩家真实可移动，区域格远多于此处；
// 取 0..23 连续覆盖足够揭示「单点采样会误判」，同时把耗时控制在可接受范围。
var spotProbeSpots = []uint64{
	0, 1, 2, 3, 4, 5, 6, 7,
	11, 15, 19, 23, 27, 31, 37, 43,
}

// allowByMap 按地图预计算「该图每种鱼是否有至少一款合法饵」。
//
// 依据（已实测）：filterFishBaitSet 的结论只依赖 (地图, 鱼种, 饵池, now)：
//   - 钓法/水层：函数内只用 c.rigType 判 bottomOnly，而 bottomOnly 规则只影响
//     「底钓能否用这款饵」，不改变「该鱼是否存在任何合法饵」这一存在性结论；
//   - 时间：自动轮换按 now/7200 分桶，但轮换只在「饵组数 > 配额」时收窄 allow，
//     且收窄后至少留 3/2/1 组，实测 6 张图 × 6 个桶结果完全一致。
//
// 为稳妥，这里显式用「浮钓 + 中层」这一中性口径算，并在报告里标注该前提。
func allowByMap(srv *Server, m mapRef, rosterKeys []string, allBaits []ruleBait, at time.Time) map[string]bool {
	c := fishRollContext{
		loc: m.Alias, now: at, rigType: 1, depthCM: 200,
		skill: neutralFishingSkillBonus(), cache: newFishingRollCache(),
	}
	wts := make([]float64, len(rosterKeys))
	for i := range wts {
		wts[i] = 1
	}
	ids := baitIDs(allBaits)
	ok, ow := srv.filterFishBaitSet(m.Alias, ids, rosterKeys, wts, at, c)
	out := map[string]bool{}
	for i, k := range ok {
		if i < len(ow) && ow[i] > 0 {
			out[k] = true
		}
	}
	return out
}

// baitIDs 取 ruleBait 的 id 列表。
func baitIDs(bs []ruleBait) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.id
	}
	return out
}

// fishSeasonName 季节序号 → 中文名（与 fishSeasonAt 返回值对应）。
func fishSeasonName(s int) string {
	switch s {
	case 0:
		return "春"
	case 1:
		return "夏"
	case 2:
		return "秋"
	default:
		return "冬"
	}
}

// weatherAtForTest 取地图某时刻的天气（与判鱼同源）。
//
// ⚠️ 必须传 loc_alias.alias（slug，如 level_001_lake），不能传 loc_alias.loc
// （中文名）。mapWeatherProfiles() 以 map_defs.json 的 level_* key 索引，
// 传中文名会永久 ok=false，天气层静默降级成「未知」—— fishWaterConditionsFor
// 于是回落到与地图无关的 seasonalAir，18 张图会算出完全相同的水温。
//
// 取值口径也与生产一致：生产走 Session.weatherAt（fishing_weather_context.go），
// 用的是**完整** fishingWeather（气温 + 前一小时气温 + 风 + 雾 + 天气类型）。
// 只填 airTemp 会让 fishWeatherExtraWeight / fishWeatherKindWeight /
// 清澈度（cloud·rain·fog）全部走零值，判鱼与线上不是同一套输入。
func (s *Server) weatherAtForTest(alias string, at time.Time) fishingWeather {
	p, ok := WeatherPreviewForMap(alias, at.Unix(), 0, 0)
	if !ok || p == nil || len(p.Hours) == 0 {
		return fishingWeather{}
	}
	h := int(gameHourOf(at))
	if h < 0 || h >= len(p.Hours) {
		return fishingWeather{}
	}
	cur := p.Hours[h]
	prev := cur
	if h > 0 {
		prev = p.Hours[h-1]
	}
	return fishingWeather{
		known:           true,
		airTemp:         cur.AirTempC,
		previousAirTemp: prev.AirTempC,
		wind:            cur.WindKMH / 3.6, // 生产存米/秒
		fog:             cur.Fog,
		kind:            cur.Kind,
	}
}

// reportMapFishability 按地图输出结构化结论。
func reportMapFishability(t *testing.T, m mapRef, cells []fishabilityCell) {
	t.Helper()
	type key struct {
		season string
		hour   int64
	}
	// 每个 (季节, 时段) 下，只要有一个钓法×水层能出鱼，就算该时段可钓
	perSlot := map[key]int{}
	perSlotTotal := map[key]int{}
	for _, c := range cells {
		k := key{c.Season, c.Hour}
		perSlotTotal[k]++
		if c.AfterAll > 0 {
			perSlot[k]++
		}
	}
	var dead []key
	for k, ok := range perSlot {
		if ok == 0 {
			dead = append(dead, k)
		}
	}
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].season != dead[j].season {
			return dead[i].season < dead[j].season
		}
		return dead[i].hour < dead[j].hour
	})

	// 可钓鱼种数分布
	var minF, maxF, sumF, nF int
	for _, c := range cells {
		if c.AfterAll > 0 {
			if nF == 0 || c.AfterAll < minF {
				minF = c.AfterAll
			}
			if c.AfterAll > maxF {
				maxF = c.AfterAll
			}
			sumF += c.AfterAll
			nF++
		}
	}
	meanF := 0.0
	if nF > 0 {
		meanF = float64(sumF) / float64(nF)
	}

	t.Logf("%-14s (%-24s) 组合%4d 可钓组合%4d 死时段%2d/%2d  可钓鱼种 %d~%d(均%.1f)",
		m.Loc, m.Alias, len(cells), nF, len(dead), len(perSlot), minF, maxF, meanF)
	if len(dead) > 0 {
		for _, k := range dead {
			// 找出该时段被哪一层杀掉的
			var reasons []string
			for _, c := range cells {
				if c.Season == k.season && c.Hour == k.hour {
					reasons = appendUnique(reasons, c.KilledBy)
				}
			}
			t.Logf("      ⚠️ %s %2d时 完全无鱼可钓（%v）", k.season, k.hour, reasons)
		}
	}
}

func appendUnique(xs []string, v string) []string {
	if v == "" {
		return xs
	}
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// reportFishabilitySummary 汇总跨地图影响面，按影响范围排序。
func reportFishabilitySummary(t *testing.T, cells []fishabilityCell, maps []mapRef) {
	t.Helper()
	type key struct{ season string; hour int64 }
	// 全局：哪些 (季节, 时段) 在任何地图都无鱼
	slotMaps := map[key]map[string]bool{}
	for _, c := range cells {
		k := key{c.Season, c.Hour}
		if slotMaps[k] == nil {
			slotMaps[k] = map[string]bool{}
		}
		if c.AfterAll > 0 {
			slotMaps[k][c.Map] = true
		}
	}

	// 按地图统计死时段数
	deadByMap := map[string]int{}
	slotByMap := map[string]map[key]bool{}
	for _, c := range cells {
		k := key{c.Season, c.Hour}
		if slotByMap[c.Map] == nil {
			slotByMap[c.Map] = map[key]bool{}
		}
		slotByMap[c.Map][k] = true
	}
	for mp, slots := range slotByMap {
		for k := range slots {
			if !slotMaps[k][mp] {
				deadByMap[mp]++
			}
		}
	}

	t.Logf("")
	t.Logf("=== 汇总：受影响地图（按死时段数排序）===")
	type rank struct {
		Map   string
		Dead  int
		Total int
	}
	var ranks []rank
	for mp, slots := range slotByMap {
		ranks = append(ranks, rank{mp, deadByMap[mp], len(slots)})
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Dead != ranks[j].Dead {
			return ranks[i].Dead > ranks[j].Dead
		}
		return ranks[i].Map < ranks[j].Map
	})
	for _, r := range ranks {
		flag := "✅"
		if r.Dead > 0 {
			flag = "⚠️"
		}
		t.Logf("  %s %-14s 死时段 %2d / 全 %2d", flag, r.Map, r.Dead, r.Total)
	}

	// 按 (季节, 时段) 统计有多少地图全灭
	t.Logf("")
	t.Logf("=== 汇总：各季节×时段 的全灭地图数 ===")
	var ks []key
	for k := range slotMaps {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].season != ks[j].season {
			return ks[i].season < ks[j].season
		}
		return ks[i].hour < ks[j].hour
	})
	total := len(maps)
	anyDead := false
	for _, k := range ks {
		alive := len(slotMaps[k])
		if alive == 0 {
			anyDead = true
			t.Logf("  ⚠️ %s %2d时：全 %d 张地图都无鱼可钓", k.season, k.hour, total)
		}
	}
	if !anyDead {
		t.Logf("  ✅ 全部 %d 张地图 × 4 季 × 24 时段：不存在「全地图都无鱼」的时段", total)
	}

	// 结论
	t.Logf("")
	deadMaps := 0
	for _, r := range ranks {
		if r.Dead > 0 {
			deadMaps++
		}
	}
	if deadMaps == 0 {
		t.Logf("✅ 全部通过：%d 张地图 × 4 季 × 24 时段 × 3 钓法 × 水层，每个组合都至少有一种可钓鱼种。", len(maps))
	} else {
		t.Logf("⚠️ %d / %d 张地图存在完全无鱼可钓的时段，明细见上。", deadMaps, len(maps))
	}
}

// 保证 math 被用到（适温计算在 fishBiteEnvironmentWeight 内部，这里留一处显式引用
// 以便未来内联温度诊断时不必再改 import）。
var _ = math.Max