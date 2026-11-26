package gameserver

// 渔轮与鱼竿零件的耐久消耗与渔轮本体耐久聚合。
//
// 外面那一格耐久不是独立账本：它是轮内零件剩余寿命的加权结果，最弱的一件还会额外
// 拉低整体。普通轮聚合六件；电轮的电机（category 81）暂不计入——官服没有电轮受力
// 数据，磨损通道停扣（2026-11-11 裁定），等抓包样本再补。
// 零件按"搏鱼受力秒数"缓慢消耗（官服 4/22 抓包实测：磨损只按受力时间扣，空竿收线
// 零磨损；速率见 specs/changes/wear-fight-time/spec.md §1）。每竿基准、泡水、断件
// 冲击、锁轮烧穿曲线等无官服数据支撑的通道已全部删除。摩擦片不参与本矩阵：它是
// 烧件，寿命由泄力打滑单独决定。
// 口径见 specs/changes/wear-fight-time/spec.md。

import (
	"context"
	"math"

	"rf4server/internal/proto"
	"rf4server/internal/store"
)

// reelPartWearRates 是单个零件的消耗速率，单位是百分点/受力秒（1 = 1% 剩余寿命）。
// 速率缺省值取官服实测档位（lure2/底钓2 两场量级带），全部可经 tunable 覆盖。
type reelPartWearRates struct {
	perStressSec float64 // 每搏鱼受力秒
}

// reelStressRateTunables 是各件受力磨损速率的 tunable 名（缺省值取 reelPartWearMatrix）。
// 官服量级带（%/s）：油/齿轮/导环 ≈0.0004，轴承/线杯 ≈0.00012，竿坯 ≈0.00003。
var reelStressRateTunables = map[int64]string{
	reelPartCatOil:      "wear_stress_oil_per_s",
	reelPartCatGear:     "wear_stress_gear_per_s",
	reelPartCatBearing:  "wear_stress_bearing_per_s",
	reelPartCatSpool:    "wear_stress_spool_per_s",
	reelPartCatGuides:   "wear_stress_guides_per_s",
	reelPartCatRodBlank: "wear_stress_rodblank_per_s",
}

// reelPartWearMatrix 是各件每受力秒的消耗速率缺省值（包级口径，测试与回落用）。
// 旋钮（category 34）与手柄（4）官服实测不磨损，不入矩阵；电机（81）停扣待官服电轮数据。
//
// 2026-11-26 用户要求：所有类型装备的单次损耗统一压到 1e-7，钓鱼时不必持续关注磨损。
// 原值取官服实测档位带（油/齿/导环 0.0004、轴承/线杯 0.00012、竿坯 0.00003 %/s），
// 现全部改为 1e-7（%/受力秒）——一场 300 秒的拉扯也只磨 3e-5%。
// 需要恢复官服手感时把这里的值改回去即可，tunable 覆盖优先级更高。
const reelStressWearPerSec = 0.0000001

var reelPartWearMatrix = map[int64]reelPartWearRates{
	reelPartCatOil:      {perStressSec: reelStressWearPerSec},
	reelPartCatGear:     {perStressSec: reelStressWearPerSec},
	reelPartCatBearing:  {perStressSec: reelStressWearPerSec},
	reelPartCatSpool:    {perStressSec: reelStressWearPerSec},
	reelPartCatGuides:   {perStressSec: reelStressWearPerSec},
	reelPartCatRodBlank: {perStressSec: reelStressWearPerSec},
}

// reelPartCounts 是本竿攒下来要结算的受力账。
type reelPartCounts struct {
	StressSecs float64 // 搏鱼受力累计秒数（14/8 真实间隔）
	SlipSecs   float64 // 泄力打滑真实秒数（只供摩擦片通道与诊断）
	SlipTicks  int     // 泄力打滑判定次数（诊断）
	BreakHits  int     // 断件次数（仅诊断：断件冲击磨损无官服数据，不扣寿命）
}

// total 报告这笔账有没有要结算的磨损。断件次数不计入：它不扣寿命，只进诊断日志。
func (c reelPartCounts) total() float64 {
	return c.StressSecs + c.SlipSecs
}

// reelPartLifeFactor 是品质寿命系数 Fq = clamp(0.25 + 0.75 x 品质/100, 0.35, 1)：
// 品质 0/15/40/100 分别约 0.35/0.36/0.55/1.00。写库前除以它，低档货寿命明显更短。
func reelPartLifeFactor(quality float64) float64 {
	if quality < 0 {
		quality = 0
	}
	if quality > 100 {
		quality = 100
	}
	factor := 0.25 + 0.75*quality/100
	if factor < 0.35 {
		factor = 0.35
	}
	if factor > 1 {
		factor = 1
	}
	return factor
}

// reelStressRateFor 返回零件的受力磨损速率（百分点/受力秒）：生产口径读 tunable
// （wear_stress_<件>_per_s），缺失、非数值或负值回落矩阵缺省档位。
func (s *Server) reelStressRateFor(category int64) float64 {
	def := reelPartWearMatrix[category].perStressSec
	key, ok := reelStressRateTunables[category]
	if !ok {
		return def
	}
	v := s.tunableF(key, def)
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return v
}

// reelPartWearFor 按受力秒速率算出本竿该零件要扣掉的寿命比例（0~1）。perStressSec
// 由调用方解析（生产走 s.reelStressRateFor，测试直接给包级缺省）。摩擦片/旋钮/手柄
// 不在矩阵里，返回 0：摩擦片的账由泄力打滑单独结，旋钮与手柄官服实测不磨损。
func reelPartWearFor(category int64, quality float64, counts reelPartCounts, perStressSec float64) float64 {
	if _, ok := reelPartWearMatrix[category]; !ok {
		return 0
	}
	points := perStressSec * counts.StressSecs
	if points <= 0 {
		return 0
	}
	return clampUnit(points / 100 / reelPartLifeFactor(quality))
}

// reelChildDurability 读零件子实例的剩余耐久。字段缺失与 FLOAT 的 0 都按满耐久
// （历史存档没写过该字段），只有 U32 承载的 0 才是真的磨完。
func reelChildDurability(child *proto.Value) float64 {
	if child == nil || child.Kind != proto.KindObj {
		return 1
	}
	idx := fieldIdxByCS(child.Class, csDurability)
	if idx < 0 || idx >= child.FieldCount() {
		return 1
	}
	switch fieldOpAt(child.Class, idx) {
	case "FLOAT", "DOUBLE":
		if v := child.Fields[idx].Float; v > 0 {
			return math.Min(1, v)
		}
		return 1
	default:
		raw := child.Fields[idx].Num
		if raw == nil {
			return 1
		}
		return clampUnit(float64(proto.F32FromBits(uint32(*raw))))
	}
}

// setReelChildDurability 写零件子实例耐久，一律夹进 0~1。
func setReelChildDurability(child *proto.Value, v float64) bool {
	if child == nil || child.Kind != proto.KindObj {
		return false
	}
	idx := fieldIdxByCS(child.Class, csDurability)
	if idx < 0 {
		return false
	}
	return setWireFloat(child.Fields, child.Class, idx, clampUnit(v))
}

// reelPartQuality 从零件定义读静态品质。读不出时按 100 结算，与品质口径一致。
func reelPartQuality(def *store.ItemDef) float64 {
	if def == nil {
		return 100
	}
	return math.Max(0, math.Min(100, float64(itemDefInt(def, reelPartQualityAttr))))
}

// reelWearPartCategories 是参与受力磨损的轮侧件。旋钮（34）官服实测不磨损已摘除；
// 摩擦片不在此列（烧件，走打滑账）。
var reelWearPartCategories = []int64{
	reelPartCatOil, reelPartCatGear, reelPartCatBearing, reelPartCatSpool,
}

// reelRodWearPartCategories 是竿侧受力磨损件：竿坯与导环（手柄 4 官服不磨损，不入列）。
var reelRodWearPartCategories = []int64{reelPartCatRodBlank, reelPartCatGuides}

// reelWearCategoriesInFields 返回参与受力磨损的轮侧件。电机（81）停扣后不再追加；
// 保留方法作为修理/聚合的共用入口。
func (s *Server) reelWearCategoriesInFields(ctx context.Context, fields []proto.Field) []int64 {
	return reelWearPartCategories
}

// wearPartsInFields 按受力秒速率扣字段树里指定品类零件的耐久，返回零件定义 ID 到本次
// 扣掉寿命比例的映射（供损伤日志）。已经磨完的件不再扣；缺件的树什么都不做。
func (s *Server) wearPartsInFields(ctx context.Context, fields []proto.Field, counts reelPartCounts, categories []int64) map[string]float64 {
	out := map[string]float64{}
	if s == nil || counts.StressSecs <= 0 {
		return out
	}
	for _, category := range categories {
		child, def, _, ok := s.selectedReelPart(ctx, fields, category)
		if !ok || child == nil || def == nil {
			continue
		}
		cur := reelChildDurability(child)
		if cur <= 0 {
			continue
		}
		worn := reelPartWearFor(category, reelPartQuality(def), counts, s.reelStressRateFor(category))
		if worn <= 0 {
			continue
		}
		next := cur - worn
		if next < 0 {
			next = 0
		}
		if !setReelChildDurability(child, next) {
			continue
		}
		out[def.ItemID] = cur - next
	}
	return out
}

// wearReelPartsInFields 扣渔轮树内的轮侧零件（轮油/主齿轮/轴承/线杯）。
func (s *Server) wearReelPartsInFields(ctx context.Context, fields []proto.Field, counts reelPartCounts) map[string]float64 {
	return s.wearPartsInFields(ctx, fields, counts, reelWearPartCategories)
}

// wearRodPartsInFields 扣竿树内的竿侧零件（竿坯/导环）。
func (s *Server) wearRodPartsInFields(ctx context.Context, fields []proto.Field, counts reelPartCounts) map[string]float64 {
	return s.wearPartsInFields(ctx, fields, counts, reelRodWearPartCategories)
}

// reelWasherLife 摩擦片剩余寿命 = 当前制动力 / 额定制动力（含母轮工艺）。
// 摩擦片的权威是制动力，耐久字段只是等比镜像，用来让背包里能看到损耗。
func reelWasherLife(currentDrag, ratedDrag float64) float64 {
	if ratedDrag <= 0 {
		return 0
	}
	if currentDrag <= 0 {
		return 0
	}
	return clampUnit(currentDrag / ratedDrag)
}

// 本体耐久聚合权重：摩擦片与齿轮最重，保养品轮油与其余件较轻。
// 电轮电机（81）磨损停扣（官服无电轮数据），聚合不再单列电轮权重。
const (
	reelBodyWeightWasher  = 0.25
	reelBodyWeightGear    = 0.25
	reelBodyWeightBearing = 0.20
	reelBodyWeightSpool   = 0.10
	reelBodyWeightHandle  = 0.10
	reelBodyWeightOil     = 0.10
	// reelBodyWeakSpotTolerance 是最弱熔断件对整体耐久的容差：外面那格不能比里面的
	// 短板好太多，"综合体检"才有意义。
	reelBodyWeakSpotTolerance = 0.15
)

// reelBodyFuseCategories 是能把渔轮本体直接拉到损坏的熔断件。轮油只进加权不熔断：
// 油干了是变差，不是坏。
var reelBodyFuseCategories = []int64{
	reelPartCatWasher, reelPartCatGear, reelPartCatBearing, reelPartCatSpool, reelPartCatHandle,
}

// reelPartLivesInFields 收集轮内零件的剩余寿命；电轮存在 cat81 时一并收集。
// ok=false 表示这只轮子读不出摩擦片额定制动力（老档或品类不认识），这时不做聚合，
// 免得把读不懂的库存误判成损坏。
func (s *Server) reelPartLivesInFields(ctx context.Context, reel *store.Item, fields []proto.Field) (map[int64]float64, bool) {
	lives := map[int64]float64{}
	if s == nil || reel == nil {
		return nil, false
	}
	child, def, _, ok := s.selectedReelPart(ctx, fields, reelPartCatWasher)
	if !ok || child == nil || def == nil {
		return nil, false
	}
	rated := itemDefF32(def, gearAttrDragMax) * reel.Workmanship
	if rated <= 0 {
		return nil, false
	}
	current, present, _ := frictionWasherDrag(child)
	if !present {
		// 字段缺失是新物品或历史存档：按满寿命，不能当成已烧穿。
		current = rated
	}
	lives[reelPartCatWasher] = reelWasherLife(current, rated)
	for _, category := range []int64{reelPartCatOil, reelPartCatGear, reelPartCatBearing, reelPartCatHandle, reelPartCatSpool} {
		part, _, _, found := s.selectedReelPart(ctx, fields, category)
		if !found || part == nil {
			lives[category] = 1
			continue
		}
		lives[category] = reelChildDurability(part)
	}
	return lives, true
}

// reelBodyDurabilityInFields 按"本体耐久 = 内部零件综合"算出渔轮本体此刻的耐久：
// 任一熔断件寿命归零就是 0；否则取 min(加权寿命, 最弱熔断件 + 容差)。
// 缺件按满寿命处理，不会因为老存档少了某个件就误判成损坏。
func (s *Server) reelBodyDurabilityInFields(ctx context.Context, reel *store.Item, fields []proto.Field) (float64, bool) {
	lives, ok := s.reelPartLivesInFields(ctx, reel, fields)
	if !ok {
		return 0, false
	}
	weighted := reelBodyWeightWasher*lives[reelPartCatWasher] +
		reelBodyWeightGear*lives[reelPartCatGear] +
		reelBodyWeightBearing*lives[reelPartCatBearing] +
		reelBodyWeightSpool*lives[reelPartCatSpool] +
		reelBodyWeightHandle*lives[reelPartCatHandle] +
		reelBodyWeightOil*lives[reelPartCatOil]
	weakest := math.Inf(1)
	for _, category := range reelBodyFuseCategories {
		life := lives[category]
		if life <= 0 {
			return 0, true
		}
		if life < weakest {
			weakest = life
		}
	}
	return clampUnit(math.Min(weighted, weakest+reelBodyWeakSpotTolerance)), true
}

// rodBodyFuseCategories 是竿侧熔断件：竿坯或导环磨穿即整竿不可用。
// 手柄（cat 4）官方实测不磨损，不入熔断也不入加权。
var rodBodyFuseCategories = []int64{reelPartCatRodBlank, reelPartCatGuides}

// rodBodyDurabilityInFields 按「竿本体耐久 = 竿内零件综合」算竿此刻的耐久。
// 竿没有摩擦片那种权威量，所以直接取最弱件：一根竿的可用性由最薄弱的环节决定
// （导环断了线就过不去，竿坯断了整根报废），加权平均会把单点损坏掩盖掉。
// 缺件（老档没有物化出厂子件）按满寿命处理，避免把读不懂的库存误判成损坏。
func (s *Server) rodBodyDurabilityInFields(ctx context.Context, fields []proto.Field) (float64, bool) {
	if s == nil {
		return 0, false
	}
	found := false
	weakest := 1.0
	for _, category := range rodBodyFuseCategories {
		child, def, _, ok := s.selectedReelPart(ctx, fields, category)
		if !ok || child == nil || def == nil {
			continue
		}
		found = true
		life := reelChildDurability(child)
		if life <= 0 {
			return 0, true
		}
		if life < weakest {
			weakest = life
		}
	}
	return clampUnit(weakest), found
}

// rodBodyDurability 是 rodBodyDurabilityInFields 的建档入口（从行里解字段树）。
func (s *Server) rodBodyDurability(ctx context.Context, row *store.Item) (float64, bool) {
	if s == nil || row == nil {
		return 0, false
	}
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		return 0, false
	}
	return s.rodBodyDurabilityInFields(ctx, fields)
}

// recalcReelBodyDurability 换件或修理之后重算本体耐久，并允许上调（不走单调保护）。
// 本体是里面零件的综合：换掉烧穿的摩擦片必须能把外面那格抬起来，否则永远修不好。
// 返回是否真的抬起来了。
func (s *Server) recalcReelBodyDurability(ctx context.Context, row *store.Item) bool {
	if s == nil || s.DB == nil || row == nil {
		return false
	}
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		return false
	}
	body, ok := s.reelBodyDurabilityInFields(ctx, row, fields)
	if !ok || body <= row.Durability {
		return false
	}
	if err := s.DB.SetItemDurability(ctx, row.GUID, body); err != nil {
		return false
	}
	row.Durability = body
	return true
}

// ---------------------------------------------------------------------------
// 钓获磨损的单层化（2026-11-26）
//
// 修掉的历史问题：外层 item.durability 原本按「每尾鱼固定率」线性扣，内部零件按
// 搏鱼受力秒扣，两条路径互不校准，于是同一个装备有两本账。实测 conv_5706 外层
// 0.99302 而零件聚合 0.99915（差 0.61%）；竿更明显——cast_10059 外层 -0.22% 而
// 导环 -0.093%，spin_10158 外层 -0.06% 而导环 -0.171%，两个方向都有。
//
// 现在的口径：**外层不再单独扣减**，外层的值一律由内部零件聚合得出。
// 「每次钓获」这条通道改为只扣内部零件（轮内四件 / 竿侧竿坯+导环），
// 再由聚合把结果写回外层，这样背包里看到的那格与拆开看到的零件永远一致。
// ---------------------------------------------------------------------------

// reelCatchWearPartsInFields 按「每次钓获」给轮内零件扣耐久，返回零件定义 ID → 扣掉比例。
// 只动轮侧磨损件（轮油/主齿轮/轴承/线杯）；摩擦片是烧件走泄力打滑，旋钮/手柄/电机不磨损。
//
// 口径：amountPerPart 是「绝对耐久比例」，与旧外层扣减（durability-1e-7）同一单位，
// **不再按百分点除以 100**。受力通道的速率矩阵是「百分点/受力秒」才要除 100；
// 这里若也除 100，1e-7 会变成 1e-9，低于 float32 在 1.0 附近的 1.192e-07 分辨率，
// 每次舍回原值 —— 表面看是"永不磨损"，实际是精度把账吃掉了。
func (s *Server) reelCatchWearPartsInFields(ctx context.Context, fields []proto.Field, amountPerPart float64) map[string]float64 {
	out := map[string]float64{}
	if s == nil || amountPerPart <= 0 {
		return out
	}
	for _, category := range reelWearPartCategories {
		child, def, _, ok := s.selectedReelPart(ctx, fields, category)
		if !ok || child == nil || def == nil {
			continue
		}
		cur := reelChildDurability(child)
		if cur <= 0 {
			continue
		}
		// 品质好的零件更耐：与受力通道同一口径，除以品质寿命系数。
		worn := clampUnit(amountPerPart / reelPartLifeFactor(reelPartQuality(def)))
		if worn <= 0 {
			continue
		}
		next := cur - worn
		if next < 0 {
			next = 0
		}
		if !setReelChildDurability(child, next) {
			continue
		}
		out[def.ItemID] = cur - next
	}
	return out
}

// rodCatchWearPartsInFields 按「每次钓获」给竿侧零件扣耐久（竿坯/导环），返回零件定义 ID → 扣掉比例。
// 与 applyRodStressWear 同源：先补齐出厂子件，再按品类扣。amountPerPart 是绝对耐久比例，
// 与 reelCatchWearPartsInFields 同口径（不除 100）。
func (s *Server) rodCatchWearPartsInFields(ctx context.Context, row *store.Item, fields []proto.Field, amountPerPart float64) map[string]float64 {
	out := map[string]float64{}
	if s == nil || row == nil || amountPerPart <= 0 {
		return out
	}
	if !s.ensureRodWearChildren(ctx, row, fields) {
		return out
	}
	for _, category := range reelRodWearPartCategories {
		child, def, _, ok := s.selectedReelPart(ctx, fields, category)
		if !ok || child == nil || def == nil {
			continue
		}
		cur := reelChildDurability(child)
		if cur <= 0 {
			continue
		}
		worn := clampUnit(amountPerPart / reelPartLifeFactor(reelPartQuality(def)))
		if worn <= 0 {
			continue
		}
		next := cur - worn
		if next < 0 {
			next = 0
		}
		if !setReelChildDurability(child, next) {
			continue
		}
		out[def.ItemID] = cur - next
	}
	return out
}

// restoreReelInternalsInFields 把轮内零件恢复到额定状态，只改内存中的物品树。
func (s *Server) restoreReelInternalsInFields(ctx context.Context, row *store.Item, fields []proto.Field) (bool, error) {
	if s == nil || s.DB == nil || row == nil {
		return false, nil
	}
	changed := false
	child, def, _, ok := s.selectedReelPart(ctx, fields, reelPartCatWasher)
	if ok && child != nil && def != nil {
		if _, present, idx := frictionWasherDrag(child); present && idx >= 0 && idx < len(child.Fields) {
			if rated := itemDefF32(def, gearAttrDragMax) * row.Workmanship; rated > 0 &&
				setWireFloat(child.Fields, child.Class, idx, rated) {
				changed = true
			}
		}
		if setReelChildDurability(child, 1) {
			changed = true
		}
	}
	for _, category := range s.reelWearCategoriesInFields(ctx, fields) {
		part, _, _, found := s.selectedReelPart(ctx, fields, category)
		if !found || part == nil {
			continue
		}
		if setReelChildDurability(part, 1) {
			changed = true
		}
	}
	return changed, nil
}

// restoreReelInternals 进店修理渔轮时把轮内零件一并恢复：摩擦片制动力回到额定、其余件
// 耐久回满。本体修好了但摩擦片还是 0 的话，轮子照样收不了线，修理就成了白扣钱。
// 只改零件字段，本体耐久由调用方按修理流程写。老档没有轮内零件时 changed=false 且
// err=nil，修理照常生效；写库失败必须让调用方放弃这次修理，别只把本体刷回满值。
func (s *Server) restoreReelInternals(ctx context.Context, row *store.Item) (bool, error) {
	if s == nil || s.DB == nil || row == nil {
		return false, nil
	}
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		return false, err
	}
	changed, err := s.restoreReelInternalsInFields(ctx, row, fields)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	bumpCreatedTicks(fields, row.WireClass)
	fj, err := FieldsToJSON(fields)
	if err != nil {
		return false, err
	}
	snapshot := *row
	snapshot.FieldsJSON = fj
	if s.canonicalizeReelStrength(ctx, &snapshot, fields) {
		if fj, err = FieldsToJSON(fields); err != nil {
			return false, err
		}
	}
	if err := s.DB.UpdateItemFields(ctx, row.GUID, fj, row.Qty, row.Durability, row.Workmanship, row.RigGUID); err != nil {
		return false, err
	}
	row.FieldsJSON = fj
	return true, nil
}
