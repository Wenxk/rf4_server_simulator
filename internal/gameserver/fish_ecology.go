package gameserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"rf4server/internal/worldclock"
)

// 6：昼夜改用四段乘性曲线（diel_curve，[夜,晨,昼,昏]，峰值 1.0、最低 0.08），
// 并把掩码位序改成与上游一致（1 夜 / 2 晨 / 4 昼 / 8 昏）。旧库的掩码由迁移
// 00079 旋转过，这里抬高版本号是为了把 253 条资产的曲线灌进已有的档案行——
// 版本号停在 5 的话 `WHERE profile_version < excluded.profile_version` 全不成立，
// 曲线上不来，等于白改。
//
// 稀有度不在这里：rarity_tier 现在是鱼种固有属性（species_rarity：0 普通 /
// 1 稀有 / 2 超稀有），由启动时的 SyncFishRarityFromStatic 写入，种子 JSON
// 里不再带这个字段——少一个会随文件漂移的档位来源。
const (
	fishEcologyProfileVersion = 6
	// fishDielCurveMin 是曲线的最低系数：低活跃段仍允许偶发记录，
	// 但不置 0，避免该鱼在整段内彻底消失（与上游同口径）。
	fishDielCurveMin = 0.08
)

//go:embed assets/fish_ecology_defs.json
var fishEcologyDefsJSON []byte

var (
	fishEcologyDefsOnce sync.Once
	fishEcologyDefs     map[string]fishEcologyProfile
)

// fishEcologyProfile 是鱼口系统使用的鱼种生态基线。水层使用 0=水面、1=水底
// 的归一化坐标，后续可叠加具体钓点水深，不把某条河的实际米数写死在鱼种上。
type fishEcologyProfile struct {
	FishKey   string  `json:"fish_key"`
	LayerMin  float64 `json:"layer_min"`
	LayerPref float64 `json:"layer_pref"`
	LayerMax  float64 `json:"layer_max"`
	TempMinC  float64 `json:"temp_min_c"`
	TempPrefC float64 `json:"temp_pref_c"`
	TempMaxC  float64 `json:"temp_max_c"`
	DielMask  int64   `json:"diel_mask"`
	// DielCurve 是四段乘性权重 `[夜,晨,昼,昏]`，峰值固定 1.0、最低 0.08。
	// 空串表示该鱼只有位图，fishDielWeight 会回退到 1.0 / 0.35 的旧口径。
	DielCurve     string  `json:"diel_curve"`
	SeasonMask    int64   `json:"season_mask"`
	DietJSON      string  `json:"diet_json"`
	WeatherJSON   string  `json:"weather_json"`
	ActionJSON    string  `json:"action_json"`
	SubstrateJSON string  `json:"substrate_json"`
	ColorJSON     string  `json:"color_json"`
	CurrentPref   float64 `json:"current_pref"`
	Activity      float64 `json:"activity"`
	LightPref     float64 `json:"light_pref"`
	ClarityPref   float64 `json:"clarity_pref"`
	Aggression    float64 `json:"aggression"`
	// 固有稀有度（0 普通 / 1 稀有 / 2 超稀有）。不来自种子 JSON，而是
	// store.SyncFishRarityFromStatic 从静态表 species_rarity 投影进库，
	// 这里只负责读出来给判鱼权重用。
	RarityTier     int    `json:"-"`
	Enabled        bool   `json:"enabled"`
	ProfileVersion int    `json:"profile_version"`
	Source         string `json:"-"`
}

func (s *Server) ensureFishEcology(ctx context.Context) (int, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	rows, err := s.DB.SQL.QueryContext(ctx, `SELECT fish_key FROM species_weight`)
	if err != nil {
		return 0, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil && strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, err
	}
	now := float64(time.Now().Unix())
	inserted := 0
	for _, key := range keys {
		p := defaultFishEcology(key)
		res, ierr := s.DB.SQL.ExecContext(ctx, `
			INSERT INTO fish_ecology(
				fish_key, layer_min, layer_pref, layer_max,
				temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve,
				season_mask, diet_json, weather_json, action_json, substrate_json,
				color_json, current_pref, activity, light_pref, clarity_pref,
				aggression, enabled, profile_version, source, updated_ts
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(fish_key) DO UPDATE SET
				layer_min=excluded.layer_min, layer_pref=excluded.layer_pref,
				layer_max=excluded.layer_max, temp_min_c=excluded.temp_min_c,
				temp_pref_c=excluded.temp_pref_c, temp_max_c=excluded.temp_max_c,
				diel_mask=excluded.diel_mask, diel_curve=excluded.diel_curve,
				season_mask=excluded.season_mask,
				diet_json=excluded.diet_json, weather_json=excluded.weather_json,
				action_json=excluded.action_json, substrate_json=excluded.substrate_json,
				color_json=excluded.color_json, current_pref=excluded.current_pref,
				activity=excluded.activity, light_pref=excluded.light_pref,
				clarity_pref=excluded.clarity_pref, aggression=excluded.aggression,
				profile_version=excluded.profile_version, source=excluded.source,
				updated_ts=excluded.updated_ts
			WHERE fish_ecology.profile_version < excluded.profile_version`,
			p.FishKey, p.LayerMin, p.LayerPref, p.LayerMax,
			p.TempMinC, p.TempPrefC, p.TempMaxC, p.DielMask, p.DielCurve,
			p.SeasonMask, p.DietJSON, p.WeatherJSON, p.ActionJSON, p.SubstrateJSON,
			p.ColorJSON, p.CurrentPref, p.Activity, p.LightPref, p.ClarityPref,
			p.Aggression, boolInt(p.Enabled), p.ProfileVersion, p.Source, now)
		if ierr != nil {
			return inserted, ierr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	return inserted, nil
}

func (s *Server) reloadFishEcologyCache(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT fish_key, layer_min, layer_pref, layer_max,
		       temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve,
		       season_mask, diet_json, weather_json, action_json, substrate_json,
		       color_json, current_pref, activity, light_pref, clarity_pref,
		       aggression, rarity_tier, enabled, profile_version, source
		FROM fish_ecology`)
	if err != nil {
		return err
	}
	defer rows.Close()
	cache := make(map[string]fishEcologyProfile, 252)
	for rows.Next() {
		var p fishEcologyProfile
		var enabled int
		if err := rows.Scan(
			&p.FishKey, &p.LayerMin, &p.LayerPref, &p.LayerMax,
			&p.TempMinC, &p.TempPrefC, &p.TempMaxC, &p.DielMask, &p.DielCurve,
			&p.SeasonMask, &p.DietJSON, &p.WeatherJSON, &p.ActionJSON, &p.SubstrateJSON,
			&p.ColorJSON, &p.CurrentPref, &p.Activity, &p.LightPref, &p.ClarityPref,
			&p.Aggression, &p.RarityTier, &enabled, &p.ProfileVersion, &p.Source,
		); err != nil {
			return err
		}
		p.Enabled = enabled != 0
		cache[p.FishKey] = p
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.fishEcologyMu.Lock()
	s.fishEcologyCache = cache
	s.fishEcologyCacheLoaded = true
	s.fishEcologyMu.Unlock()
	// 水域覆盖与全局档案同一次刷新：只刷新一半会出现「习性旧、覆盖新」的错配。
	if err := s.reloadFishEcologySpeciesCache(ctx); err != nil {
		return err
	}
	return s.reloadFishEcologyLocCache(ctx)
}

// fishEcologyLocOverride 是水域覆盖的内存形态：nil 字段继承全局档案。
// 只覆盖「习性」字段（水层/温度/昼夜/季节/食性/天气/活跃度/启停），
// 不覆盖体长体重与稀有度——那些属于鱼种本身，不是水域属性。
type fishEcologyLocOverride struct {
	layerMin, layerPref, layerMax *float64
	tempMinC, tempPrefC, tempMaxC *float64
	dielMask, seasonMask          *int64
	// dielCurve 非 nil 时接管昼夜曲线（空串表示该水域回退到位图判断）。
	dielCurve             *string
	dietJSON, weatherJSON *string
	activity              *float64
	enabled               *bool
}

// fishEcologySpeciesOverride 是鱼种级覆盖的内存形态：nil 字段继承资产基线。
// 字段比水域覆盖宽——鱼种档案维护的是「这个物种本身的习性」，
// 所以底质/水色/水流/光照/攻击性/拟饵动作这些不随水域变的部分都放在这一层。
type fishEcologySpeciesOverride struct {
	layerMin, layerPref, layerMax *float64
	tempMinC, tempPrefC, tempMaxC *float64
	dielMask, seasonMask          *int64
	dielCurve                     *string
	dietJSON, weatherJSON         *string
	actionJSON                    *string
	substrateJSON, colorJSON      *string
	currentPref, activity         *float64
	lightPref, clarityPref        *float64
	aggression                    *float64
	enabled                       *bool
}

// reloadFishEcologySpeciesCache 全量载入鱼种级覆盖。
// 与水域覆盖一并刷新，避免层级之间出现半新半旧。
func (s *Server) reloadFishEcologySpeciesCache(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	rows, err := s.DB.AllFishEcologySpecies(ctx)
	if err != nil {
		return err
	}
	cache := make(map[string]fishEcologySpeciesOverride, len(rows))
	for _, o := range rows {
		ov := fishEcologySpeciesOverride{
			layerMin: o.LayerMin, layerPref: o.LayerPref, layerMax: o.LayerMax,
			tempMinC: o.TempMinC, tempPrefC: o.TempPrefC, tempMaxC: o.TempMaxC,
			dielMask: o.DielMask, dielCurve: o.DielCurve, seasonMask: o.SeasonMask,
			dietJSON: o.DietJSON, weatherJSON: o.WeatherJSON, actionJSON: o.ActionJSON,
			substrateJSON: o.SubstrateJSON, colorJSON: o.ColorJSON,
			currentPref: o.CurrentPref, activity: o.Activity,
			lightPref: o.LightPref, clarityPref: o.ClarityPref,
			aggression: o.Aggression,
		}
		if o.Enabled != nil {
			enabled := *o.Enabled != 0
			ov.enabled = &enabled
		}
		cache[o.FishKey] = ov
	}
	s.fishEcologyMu.Lock()
	s.fishEcologySpeciesCache = cache
	s.fishEcologyMu.Unlock()
	return nil
}

// reloadFishEcologyLocCache 全量载入水域覆盖。与全局档案同一次刷新，
// 避免「刷新了习性却漏了覆盖」的半新半旧状态。
func (s *Server) reloadFishEcologyLocCache(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	rows, err := s.DB.AllFishEcologyLoc(ctx)
	if err != nil {
		return err
	}
	cache := make(map[string]map[string]fishEcologyLocOverride, 16)
	for _, o := range rows {
		enabled := o.Enabled != nil && *o.Enabled != 0
		ov := fishEcologyLocOverride{
			layerMin: o.LayerMin, layerPref: o.LayerPref, layerMax: o.LayerMax,
			tempMinC: o.TempMinC, tempPrefC: o.TempPrefC, tempMaxC: o.TempMaxC,
			dielMask: o.DielMask, dielCurve: o.DielCurve, seasonMask: o.SeasonMask,
			dietJSON: o.DietJSON, weatherJSON: o.WeatherJSON,
			activity: o.Activity,
		}
		if o.Enabled != nil {
			ov.enabled = &enabled
		}
		byKey := cache[o.StatLoc]
		if byKey == nil {
			byKey = map[string]fishEcologyLocOverride{}
			cache[o.StatLoc] = byKey
		}
		byKey[o.FishKey] = ov
	}
	s.fishEcologyMu.Lock()
	s.fishEcologyLocCache = cache
	s.fishEcologyMu.Unlock()
	// 生态全量重载后，节奏通路的名册食性聚合必须同步作废，否则旧聚合
	// 活过 TTL（PR #531 审查 P2-4）。
	s.invalidatePaceDietCache()
	return nil
}

// mergeFishEcology 把水域覆盖叠到全局档案上：只改有值的字段。
func mergeFishEcology(p fishEcologyProfile, ov *fishEcologyLocOverride) fishEcologyProfile {
	if ov == nil {
		return p
	}
	if ov.layerMin != nil {
		p.LayerMin = *ov.layerMin
	}
	if ov.layerPref != nil {
		p.LayerPref = *ov.layerPref
	}
	if ov.layerMax != nil {
		p.LayerMax = *ov.layerMax
	}
	if ov.tempMinC != nil {
		p.TempMinC = *ov.tempMinC
	}
	if ov.tempPrefC != nil {
		p.TempPrefC = *ov.tempPrefC
	}
	if ov.tempMaxC != nil {
		p.TempMaxC = *ov.tempMaxC
	}
	if ov.dielMask != nil {
		p.DielMask = *ov.dielMask
	}
	// 曲线与掩码分别覆盖：只改掩码时曲线仍是全局的，会被 fishDielWeight 优先采用，
	// 视觉上就是「改了掩码没生效」。所以显式给曲线留了覆盖位（空串=压回掩码判断）。
	if ov.dielCurve != nil {
		p.DielCurve = *ov.dielCurve
	}
	if ov.seasonMask != nil {
		p.SeasonMask = *ov.seasonMask
	}
	if ov.dietJSON != nil {
		p.DietJSON = *ov.dietJSON
	}
	if ov.weatherJSON != nil {
		p.WeatherJSON = *ov.weatherJSON
	}
	if ov.activity != nil {
		p.Activity = *ov.activity
	}
	if ov.enabled != nil {
		p.Enabled = *ov.enabled
	}
	return p
}

// mergeFishEcologySpecies 把鱼种级覆盖叠到资产基线上：只改有值的字段。
// 字段比水域覆盖宽（多了底质/水色/动作/水流/光照/清澈度/攻击性）。
func mergeFishEcologySpecies(p fishEcologyProfile, ov *fishEcologySpeciesOverride) fishEcologyProfile {
	if ov == nil {
		return p
	}
	if ov.layerMin != nil {
		p.LayerMin = *ov.layerMin
	}
	if ov.layerPref != nil {
		p.LayerPref = *ov.layerPref
	}
	if ov.layerMax != nil {
		p.LayerMax = *ov.layerMax
	}
	if ov.tempMinC != nil {
		p.TempMinC = *ov.tempMinC
	}
	if ov.tempPrefC != nil {
		p.TempPrefC = *ov.tempPrefC
	}
	if ov.tempMaxC != nil {
		p.TempMaxC = *ov.tempMaxC
	}
	if ov.dielMask != nil {
		p.DielMask = *ov.dielMask
	}
	if ov.dielCurve != nil {
		p.DielCurve = *ov.dielCurve
	}
	if ov.seasonMask != nil {
		p.SeasonMask = *ov.seasonMask
	}
	if ov.dietJSON != nil {
		p.DietJSON = *ov.dietJSON
	}
	if ov.weatherJSON != nil {
		p.WeatherJSON = *ov.weatherJSON
	}
	if ov.actionJSON != nil {
		p.ActionJSON = *ov.actionJSON
	}
	if ov.substrateJSON != nil {
		p.SubstrateJSON = *ov.substrateJSON
	}
	if ov.colorJSON != nil {
		p.ColorJSON = *ov.colorJSON
	}
	if ov.currentPref != nil {
		p.CurrentPref = *ov.currentPref
	}
	if ov.activity != nil {
		p.Activity = *ov.activity
	}
	if ov.lightPref != nil {
		p.LightPref = *ov.lightPref
	}
	if ov.clarityPref != nil {
		p.ClarityPref = *ov.clarityPref
	}
	if ov.aggression != nil {
		p.Aggression = *ov.aggression
	}
	if ov.enabled != nil {
		p.Enabled = *ov.enabled
	}
	return p
}

// fishEcologyFor 是判鱼侧唯一入口：资产基线 → 鱼种覆盖 → 该水域覆盖。
//
// statLoc 为空（位置未知/静态库不可用）时只跳过水域层，鱼种层照常合并——
// 水温窗口这类「属于物种本身」的参数不该因为定位缺失而退回合成基线。
func (s *Server) fishEcologyFor(ctx context.Context, statLoc, key string) fishEcologyProfile {
	p := s.fishEcology(ctx, key)
	if s == nil {
		return p
	}
	s.fishEcologyMu.RLock()
	var sp *fishEcologySpeciesOverride
	if v, ok := s.fishEcologySpeciesCache[key]; ok {
		sp = &v
	}
	var ov *fishEcologyLocOverride
	if statLoc != "" {
		if byKey := s.fishEcologyLocCache[statLoc]; byKey != nil {
			if v, ok := byKey[key]; ok {
				ov = &v
			}
		}
	}
	s.fishEcologyMu.RUnlock()
	p = mergeFishEcologySpecies(p, sp)
	return mergeFishEcology(p, ov)
}

func (s *Server) invalidateFishEcologyCache() {
	if s == nil {
		return
	}
	s.fishEcologyMu.Lock()
	s.fishEcologyCache = nil
	s.fishEcologyCacheLoaded = false
	s.fishEcologyLocCache = nil
	s.fishEcologySpeciesCache = nil
	s.fishEcologyMu.Unlock()
	s.invalidatePaceDietCache()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func defaultFishEcology(key string) fishEcologyProfile {
	fishEcologyDefsOnce.Do(func() {
		fishEcologyDefs = map[string]fishEcologyProfile{}
		var profiles []fishEcologyProfile
		if json.Unmarshal(fishEcologyDefsJSON, &profiles) != nil {
			return
		}
		for _, profile := range profiles {
			fishEcologyDefs[profile.FishKey] = profile
		}
	})
	if profile, ok := fishEcologyDefs[key]; ok {
		if profile.Source == "" {
			profile.Source = "local-sim:deterministic-v3"
		}
		// 版本号一律以代码常量为准：资产里的 profile_version 只是生成时的标记，
		// 若让它盖住常量，抬高常量也不会触发重灌（00078 加 diel_curve 后就是这种坑）。
		profile.ProfileVersion = fishEcologyProfileVersion
		return profile
	}
	return fishEcologyProfile{
		FishKey: key, LayerPref: .55, LayerMax: 1,
		TempMinC: 5, TempPrefC: 17, TempMaxC: 27, DielMask: 0b1111,
		SeasonMask: 0b1111,
		// 没有档案的鱼不放默认食谱：判鱼层用 len(diet)==0 识别"无食性资料"，
		// 退回旧的证据判定，而不是拿一份假食谱（worm/plant/fish 全 1）把
		// 玉米判成所有未知鱼都能吃。
		DietJSON:    `{}`,
		WeatherJSON: `{"clear":1,"cloudy":1,"rain":1}`,
		ActionJSON:  `{"steady":1,"sinking":1}`, SubstrateJSON: `{"sand":1,"mud":1}`,
		ColorJSON: `{"silver":1,"natural":1}`, CurrentPref: .5, Activity: 1,
		LightPref: .5, ClarityPref: .5, Aggression: 1, Enabled: true,
		ProfileVersion: fishEcologyProfileVersion, Source: "local-sim:fallback",
	}
}

func (s *Server) fishEcology(ctx context.Context, key string) fishEcologyProfile {
	if s == nil || s.DB == nil {
		return defaultFishEcology(key)
	}
	s.fishEcologyMu.RLock()
	if s.fishEcologyCacheLoaded {
		profile, ok := s.fishEcologyCache[key]
		s.fishEcologyMu.RUnlock()
		if !ok {
			return defaultFishEcology(key)
		}
		return profile
	}
	s.fishEcologyMu.RUnlock()
	var p fishEcologyProfile
	var enabled, profileVersion int
	err := s.DB.SQL.QueryRowContext(ctx, `
		SELECT fish_key, layer_min, layer_pref, layer_max,
		       temp_min_c, temp_pref_c, temp_max_c, diel_mask, diel_curve,
		       season_mask, diet_json, weather_json, action_json, substrate_json,
		       color_json, current_pref, activity, light_pref, clarity_pref,
		       aggression, rarity_tier, enabled, profile_version, source
		FROM fish_ecology WHERE fish_key=?`, key).Scan(
		&p.FishKey, &p.LayerMin, &p.LayerPref, &p.LayerMax,
		&p.TempMinC, &p.TempPrefC, &p.TempMaxC, &p.DielMask, &p.DielCurve,
		&p.SeasonMask, &p.DietJSON, &p.WeatherJSON, &p.ActionJSON, &p.SubstrateJSON,
		&p.ColorJSON, &p.CurrentPref, &p.Activity, &p.LightPref, &p.ClarityPref,
		&p.Aggression, &p.RarityTier, &enabled, &profileVersion, &p.Source)
	if err != nil {
		return defaultFishEcology(key)
	}
	p.Enabled = enabled != 0
	p.ProfileVersion = profileVersion
	return p
}

func normalizeWaterLayer(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// effectiveLureLayer 返回判鱼用的路亚实时水层（评审 P3 统一口径）：
// 14/7 运动证据优先；无证据按 runtime 拟饵名义泳层兜底；再兜底中层 .5。
// 三处消费者（rigWaterLayer / effectiveBaitDepthCMLocked / fishLureWeight）
// 必须同口径，避免 LureLayer=0 的"未知"态被各处解释成不同泳层。
func effectiveLureLayer(c fishRollContext) float64 {
	if c.lureLayer > 0 {
		return normalizeWaterLayer(c.lureLayer)
	}
	if c.runtime != nil && c.runtime.lureKnown && c.runtime.lure.DiveDepth > 0 {
		return normalizeWaterLayer(c.runtime.lure.DiveDepth)
	}
	return .5
}

func (s *Server) rigWaterLayer(c fishRollContext) float64 {
	if bottomRigTypes[c.rigType] {
		return .93
	}
	if rigTypeLureFamily[c.rigType] {
		// 无运动证据时按饵的名义泳层兜底，而不是一律中层 .5——水面系饵
		// 刚入水还没上报动作时按 .5 判鱼会错过表层掠食鱼。
		return effectiveLureLayer(c)
	}
	if c.depthCM > 0 {
		// 浮钓保持旧的 5m 满刻度：区域查询的批量/逐点天气权重必须有
		// 与点位无关的水层口径；按 spotEnv 实际水深归一会让同一张图的
		// 不同落点算出不同水温/光照，批量捕获无法与逐点生产权重对上
		// （TestFishQueryRegionCaptureMatchesPointCalculation）。评审 P2-2 回退。
		return normalizeWaterLayer(float64(c.depthCM) / 500)
	}
	return .45
}

// fishLayerWeightProfile 按当前钓法水层与鱼种区间计算软匹配权重：
// 区间（±0.08 容差）内保留 pref 高斯峰（精确命中偏好层 ≈1.0）与 0.75 地板，
// 区间外对称指数衰减（k=2），下限 0.02 保留杂口。旧版只罚"底钓打表层鱼"
// 与"路亚比鱼深"两个方向，水面饵打底层鱼只走高斯尾巴（v4 区间收窄后
// 仍有 ~0.27），是水面系上底鱼的根因之一；对称化后衰减与方向无关。
func (s *Server) fishLayerWeightProfile(p fishEcologyProfile, c fishRollContext) float64 {
	target := s.rigWaterLayer(c)
	lo, hi := p.LayerMin, p.LayerMax
	if target >= lo-.08 && target <= hi+.08 {
		half := math.Max(.08, (hi-lo)/2)
		z := (target - p.LayerPref) / half
		return math.Max(.75, .12+.88*math.Exp(-.5*z*z))
	}
	half := math.Max(.08, (hi-lo)/2)
	out := math.Max(lo-.08-target, target-hi-.08) / half
	return math.Max(.02, .75*math.Exp(-2*out))
}

func (s *Server) fishBiteEnvironmentWeight(ctx context.Context, key string, c fishRollContext) float64 {
	if c.rigType == 0 && c.depthCM == 0 && c.lureLayer == 0 {
		return 1
	}
	// 昼夜层与生态总开关解耦：整套生态（水温/水层/天气/清澈度）默认关，
	// 但「这种鱼这个时段活不活跃」是玩家能直接感知的手感，默认就参与判鱼。
	ecologyOn := s.tunableF("fish_ecology_enabled", 0) == 1
	dielOn := s.tunableF("fish_diel_weight_enabled", 1) == 1
	if !ecologyOn && !dielOn {
		return 1
	}
	p := s.cachedFishEcology(ctx, c, key)
	// 「全局不出鱼」只属于生态总开关：昼夜层只是乘子，不该让鱼凭空消失。
	if ecologyOn && !p.Enabled {
		return 0
	}
	_, hour := worldDayAndHour(c.now.Unix())
	// 四段曲线只做乘性压制（峰值 1.0 / 最低 0.08），不再有「专性鱼翻倍」那套加成：
	// 鱼种之间的差异交给曲线本身的分段形状表达。
	diel := fishDielWeight(p, hour)
	// 非活跃时段禁口（fish_diel_inactive_block，默认开）：位图 diel_mask 由曲线 ≥0.5
	// 派生，所以「曲线值 <0.5」就是玩家在鱼种卡上看到的活跃时段之外。此时这条鱼直接
	// 不参与抽签（权重 0），而不是只乘一个 0.3~0.4 的系数——曲线边缘段的平滑值压不住，
	// 会出现「卡片写夜行、凌晨照样连竿」。
	if dielOn && s.tunableF(tunableFishDielInactiveBlock, 1) >= 0.5 && diel < dielActiveThreshold {
		return 0
	}
	if !ecologyOn {
		// 只开昼夜层时直接返回该系数：不牵连水层、水温、天气、清澈度。
		return clampFloat(diel, fishDielCurveMin, 1)
	}
	type weightedFactor struct {
		value     float64
		tolerance float64
	}
	factors := []weightedFactor{{
		value: s.fishLayerWeightProfile(p, c), tolerance: c.skill.LayerAccuracy,
	}}
	if dielOn {
		factors = append(factors, weightedFactor{diel, c.skill.EcologyInterest})
	}
	water := fishWaterConditionsFor(p, c)
	factors = append(factors,
		weightedFactor{fishSeasonWeight(p, water.Season), c.skill.WeatherAdaptation},
		weightedFactor{fishWeatherKindWeight(p, c.weather.kind), c.skill.WeatherAdaptation},
		weightedFactor{fishWeatherExtraWeight(p, c.weather), c.skill.WeatherAdaptation},
	)
	tolerance := math.Max(2.2, (p.TempMaxC-p.TempMinC)/2)
	z := (water.WaterTempC - p.TempPrefC) / tolerance
	factors = append(factors,
		weightedFactor{.35 + .65*math.Exp(-.5*z*z), c.skill.WeatherAdaptation},
		weightedFactor{1 - math.Abs(p.ClarityPref-water.Clarity)*.28, c.skill.EcologyInterest},
		weightedFactor{1 - math.Abs(p.LightPref-water.Light)*.18, c.skill.EcologyInterest},
		weightedFactor{fishSpotBiteWeight(p, c.spotEnv), c.skill.EcologyInterest},
	)
	if c.lureAction != "" {
		if weights := fishEcologyJSONMap(p.ActionJSON); len(weights) > 0 {
			if weight, ok := fishEcologyActionWeight(weights, c.lureAction); ok {
				factors = append(factors, weightedFactor{weight, c.skill.LureTolerance})
			}
		}
	}
	factors = append(factors,
		weightedFactor{fishMotionConditionWeight(p, c, water), c.skill.LureTolerance},
	)
	factors = append(factors,
		weightedFactor{1 + c.skill.RareFish*float64(p.RarityTier)*.12, 0},
		weightedFactor{s.fishLureWeight(p, c, water), c.skill.LureTolerance},
		weightedFactor{fishSkillEnvironmentWeight(key, c), 0},
	)
	factor := 1.0
	for i, raw := range factors {
		if raw.value <= 0 || math.IsNaN(raw.value) || math.IsInf(raw.value, 0) {
			continue
		}
		value := raw.value
		if value < 1 && raw.tolerance > 0 {
			value += (1 - value) * clampFloat(raw.tolerance, 0, .45)
		}
		floor, exponent := .25, .35
		if i == 0 {
			floor, exponent = .05, .60
		}
		factor *= math.Pow(clampFloat(value, floor, 2.0), exponent)
	}
	return clampFloat(factor, .08, 2.5)
}

// fishActivityForHour 把游戏小时映射到昼夜掩码的**位序**（不是时段编号）：
// 0=夜 1=晨 2=昼 3=昏，与「1 夜 2 晨 4 昼 8 昏」的位定义、以及 diel_curve
// 的 [夜,晨,昼,昏] 四段顺序严格一一对应。
//
// 分段规则本身在 internal/worldclock —— 后台也要按同一套算术算「现在哪个时段」，
// 两边各写一份迟早会漂。2026-11-17 曾修正过一次位映射错位（原返回时段编号再被
// 当位序用，整体错一格）；2026-11-20 又把位序改成与上游一致的「夜在前」，
// 目的是让上游的生态资产与曲线能整份直接使用。
func fishActivityForHour(hour int64) uint {
	return uint(worldclock.SegmentOf(hour))
}

// dielCurveWeights 解析四段权重（夜、晨、昼、昏）。返回 nil 表示该鱼没有曲线
// （空串、非四段、非法 JSON 都算），调用方回退到位图判断。
func dielCurveWeights(raw string) []float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var w []float64
	if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w) != 4 {
		return nil
	}
	for i := range w {
		w[i] = clampFloat(w[i], 0, 1)
	}
	return w
}

// fishDielWeight 返回当前时段对吃口的影响系数。
//
// 有曲线时按窗口期加权：峰值段 1.0、次优段按观测比例衰减、非活跃段最低 0.08
// （接近不出但不置零）。曲线是**乘性**权重，只会压低不会抬高——想突出某条鱼，
// 应该调它的基础/覆盖权重，而不是把昼夜系数写到 1 以上。
//
// 没有曲线时回退旧位图：命中 1.0、未命中 0.35（与上游同口径，供旧档案、
// 手工清空曲线或回滚使用）。
// tunableFishDielInactiveBlock 是「非活跃时段禁口」总开关（默认开）。
const tunableFishDielInactiveBlock = "fish_diel_inactive_block"

// dielActiveThreshold 是「活跃段」判定线：diel_mask 由 tools/genfishdata 按曲线 ≥0.5
// 置位，这里必须用同一个阈值，否则卡片上的活跃时段和判鱼口径会打架。
const dielActiveThreshold = 0.5

func fishDielWeight(profile fishEcologyProfile, hour int64) float64 {
	bit := int(fishActivityForHour(hour))
	if curve := dielCurveWeights(profile.DielCurve); curve != nil {
		return clampFloat(curve[bit], fishDielCurveMin, 1)
	}
	if profile.DielMask&(1<<uint(bit)) != 0 {
		return 1
	}
	return .35
}

func fishEcologyJSONMap(raw string) map[string]float64 {
	var m map[string]float64
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

// lureMotionStatusMask 覆盖客户端 Gmkegncajem 已知的 0..22 位动作状态。
// 0xD0000000/0xD1000000 的高位是钓组阶段，不属于拟饵动作。
const lureMotionStatusMask uint32 = 0x007FFFFF

// 客户端 4.0.25034 把钓组动作定义成 23 个状态：托管元数据里的动作枚举
// （fhiclnhgoge）取值 0x1..0x800000 共 23 个单位。resources.assets 偏移 0x12722da1
// 起内嵌 kRigAnimationType_* 本地化表，每条 16 个语言列、第 15 列是简体，23 个显示名
// 已全部提出：Hooking 鱼咬钩时抖竿、UniformMotion 收线、StopAndGo 搅动及静止、
// SpeedUp 加收线速度、Hanging 大动作扯动、BottomTouch 接触底部、Twitch 搐动、Splash
// 激溅、StreamMoving 顺水漂流、JigStep Jig梯阶、WalkDog 犬步、LittleJerk 轻微的抽停、
// MediumJerk 强烈的抽动、FreshWaterLittleJerk 轻度抽停、FreshWaterMediumJerk 中度抽停、
// MotionlessInWaterColumn 悬挂在水底深处、MotionlessOnBottom 在水底无移动、
// MotionlessOnWaterSurface 水面上无移动、MotionOnBottom 在水底动、VerticalDive 下沉、
// VerticalAscend 垂直上升、Trolling 拖钓、MotionInBottomLayer 底层的运动。
// 本地化表按名称排列不带位序，键名仍需实机 14/7 上报对位。
//
// 已对位的位（实机 14/7 与客户端弹字互证，客户端 4.0.25034）：
// 0x8=BottomTouch、0x10=Twitch、0x20=Splash、0x40=StreamMoving、0x80=JigStep、
// 0x100=WalkDog、0x200=MotionlessInWaterColumn、0x400=UniformMotion、0x4000=Trolling、
// 0x80000=VerticalDive；0x20000=MediumJerk、0x40000=LittleJerk，两者只在海图出现
// （loc12_27 铁板钓组 rig=50 分别弹“强烈的抽动”“轻微抽停了”，铁板垂直抖法），
// 官方英文原文是 Strong pirking / Pirking，中文名里的"抽停"与淡水抽停饵无关；
// 0x4=Hanging，旧奥挂底时独亮且不伴 0x400，官方简体名“大动作扯动”、英俄德等语言
// 均为“带停顿的收线”，按“收线中带停顿”理解，仍属于饵在水中运动。
//
// 未对位的位：0x1（权重表占位名 stir）、0x8000、0x100000（占位名 motion_15/motion_20）。
// 2026-11-06 用抽停竿（竿型 12）加抽停饵（拟饵类型 7 jerkbait）在旧奥实测得到 rig=22，
// 客户端只上报 0x400 收线、0x20 激溅和 0x100 犬步，淡水未触发 FreshWaterLittleJerk/
// FreshWaterMediumJerk；两次刺鱼也只有 14/9 抬杆，未见 14/7 新位。这三位保持中性，
// 不进攻略前置条件。
//
// 攻略前置条件要用的三个客户端动作位（kRigAnimationType_*）：
// 顺流移动、jig 阶梯，以及“饵在水中运动”对应的动到位集合。
const (
	lureMotionDriftBit uint32 = 0x40
	lureMotionJigBit   uint32 = 0x80
)

// lureMotionInWaterMask 排除浮漂/水层/底部静置和触底姿态，剩下的都是饵在
// 水里移动的状态位。0x4 Hanging 是“带停顿的收线”、0x1000 水面静置不在其中，
// 0x1/0x8000/0x100000 三个未对位位仍留在集合里，宁可多判在动也不误判静止。
const lureMotionInWaterMask uint32 = lureMotionStatusMask &^ (0x1000 | 0x200 | 0x800 | 0x8)

// lureMotionEvidence 判断本次判鱼是否真的拿到动作上报。没有上报时掩码为 0，
// 不能当成“玩家没做这个动作”。
func lureMotionEvidence(mask uint32) bool {
	return mask&lureMotionStatusMask != 0
}

// classifyLureMotion 映射客户端 kRigAnimationType_* 状态位。优先级按
// “瞬时主动作 > 水层姿态 > 收线兜底”排列，避免 0x400 收线盖住抽、跳等动作。
func classifyLureMotion(mask uint32) string {
	mask &= lureMotionStatusMask
	switch {
	case mask&0x40000 != 0:
		return "little_jerk"
	case mask&0x20000 != 0:
		return "medium_jerk"
	case mask&0x10000 != 0:
		return "stop_go"
	case mask&0x10 != 0:
		return "twitch"
	case mask&0x80 != 0:
		return "jig"
	case mask&0x100 != 0:
		return "walk_dog"
	case mask&0x400000 != 0:
		return "bottom_layer_move"
	case mask&0x4000 != 0:
		return "trolling"
	case mask&0x2000 != 0:
		return "bottom_move"
	case mask&0x80000 != 0:
		return "vertical_dive"
	case mask&0x20 != 0:
		return "splash"
	case mask&0x800 != 0:
		return "bottom_motionless"
	case mask&0x200 != 0:
		return "water_column_motionless"
	case mask&0x1000 != 0:
		return "surface_motionless"
	case mask&0x40 != 0:
		return "drift"
	case mask&0x8 != 0:
		return "bottom_contact"
	case mask&0x4 != 0:
		return "retrieval_pause"
	case mask&0x2 != 0:
		return "speed_up"
	case mask&0x200000 != 0:
		return "vertical_ascend"
	case mask&0x400 != 0:
		return "steady"
	case mask&0x1 != 0:
		return "stir"
	default:
		return ""
	}
}

// fishEcologyActionWeight 兼容旧 fish_ecology.action_json 键。已有数据库行
// 不会被 INSERT OR IGNORE 更新，因此新细分动作必须能回退到旧粗分类。
func fishEcologyActionWeight(weights map[string]float64, action string) (float64, bool) {
	if weight, ok := weights[action]; ok {
		return weight, true
	}
	aliases := map[string][]string{
		"bottom_motionless":       {"bottom", "bottom_hang"},
		"little_jerk":             {"jig", "hard_twitch", "motion_18"},
		"medium_jerk":             {"hard_twitch", "jig", "motion_18"},
		"retrieval_pause":         {"stop_go", "moving"},
		"surface_motionless":      {"surface"},
		"vertical_dive":           {"sinking", "vertical_rise"},
		"water_column_motionless": {"sinking", "bottom_hang"},
		"marine_jig":              {"jig"},
	}
	for _, candidate := range aliases[action] {
		if weight, ok := weights[candidate]; ok {
			return weight, true
		}
	}
	var candidates []string
	switch action {
	case "steady", "speed_up", "trolling", "drift", "twitch", "jerk", "hard_twitch", "stop_go",
		"little_jerk", "medium_jerk", "retrieval_pause":
		candidates = []string{"moving"}
	case "bottom_motionless", "bottom_contact", "water_column_motionless":
		candidates = []string{"bottom_motionless"}
	case "bottom_move", "bottom_layer_move":
		candidates = []string{"bottom_motionless", "moving"}
	case "jig", "walk_dog":
		candidates = []string{"moving", "bottom_motionless"}
	case "surface", "surface_motionless", "splash":
		candidates = []string{"surface_motionless", "moving"}
	case "sinking", "vertical_rise", "vertical_dive", "vertical_ascend":
		candidates = []string{"water_column_motionless", "moving"}
	case "stir":
		candidates = []string{"moving", "water_column_motionless"}
	}
	for _, candidate := range candidates {
		if weight, ok := weights[candidate]; ok {
			return weight, true
		}
	}
	return 0, false
}

// lureLayerFromMotion 由 14/7 运动位推导实时水层。dive 是当前拟饵的名义泳层
// （loadLureAttributes 的 DiveDepth），>0 时把结果钳进 dive+0.15 以内：运动位
// 是玩家操作意图，但饵的物理泳层有上限——水面系饵（dive≈0.10）的匀速收线
// 旧版会按 +0.03 无界漂到中层甚至底层，深潜铁板的跳底位也会越过物理深度。
// 水面动作位（0x1000/0x100/0x20）不受钳制：把深潜饵快收到水面是真实操作。
func lureLayerFromMotion(mask uint32, previous, dive float64) float64 {
	if previous <= 0 {
		previous = .5
	}
	mask &= lureMotionStatusMask
	layer := previous
	switch {
	case mask&0x1000 != 0:
		layer = .08
	// 犬步（WalkDog）是水面系的收线动作：没有这个分支时会落到 default，
	// 水面拟饵被当成中层（previous≈.5）判鱼，只能抽到中层鱼，客户端
	// 表现为"鱼来了但不咬"。激溅（0x20）同理，只是可能同时带收线位。
	case mask&0x100 != 0:
		layer = .06
	case mask&0x80000 != 0:
		layer = normalizeWaterLayer(previous + .08)
	case mask&0x20 != 0:
		// 激溅是主动作，可能同时带收线位 0x400 或其它水层位；必须优先于
		// 收线/静止分支，保持与 classifyLureMotion 一致。
		layer = .08
	case mask&0x200000 != 0:
		layer = normalizeWaterLayer(previous - .08)
	case mask&0x2000 != 0, mask&0x400000 != 0:
		layer = .94
	case mask&0x800 != 0:
		layer = .92
	case mask&0x200 != 0:
		layer = .55
	case mask&0x400 != 0:
		layer = normalizeWaterLayer(previous + .03)
	}
	if dive > 0 {
		layer = math.Min(layer, dive+.15)
	}
	return normalizeWaterLayer(layer)
}
