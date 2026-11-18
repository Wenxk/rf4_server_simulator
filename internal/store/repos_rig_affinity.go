package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 钓法倾向矩阵：钓法族 × 鱼种水层档 → 判鱼乘子。
//
// 存在的理由：水层匹配原来只有一条几何公式（钓组折算成一个水层点，与鱼种区间比），
// 方向对但不可解释、不可调。本表把「哪种钓法更该钓到哪一层鱼」变成 5×4 的显式配置，
// 判鱼链在几何层之后乘以它，后台改多少就生效多少。
//
// 维度只有两列是为了能配：钓组类型 24 种、鱼种 253 种，逐对配置没人配得完；
// 钓法族按"饵怎么呈现"归并，水层档按 fish_ecology.layer_pref 分四段。

// 钓法族（后台矩阵的行）。
const (
	RigFamilyFloat  = "float"  // 浮钓：漂/波莱尼亚/滑漂/远投漂
	RigFamilyBottom = "bottom" // 底钓：沉底族（喂食器/发丝/方法饵/铅坠）
	RigFamilyLure   = "lure"   // 路亚：硬饵/软饵/德州/卡罗莱纳
	RigFamilyMarine = "marine" // 海钓：铁板/沉底海钓（钓组 50~60）
	RigFamilyFly    = "fly"    // 飞钓：毛钩
)

// RigFamilyOrder 后台矩阵的行顺序。
var RigFamilyOrder = []string{RigFamilyFloat, RigFamilyBottom, RigFamilyLure, RigFamilyMarine, RigFamilyFly}

// RigFamilyLabels 钓法族中文名。
var RigFamilyLabels = map[string]string{
	RigFamilyFloat:  "浮钓",
	RigFamilyBottom: "底钓",
	RigFamilyLure:   "路亚",
	RigFamilyMarine: "海钓",
	RigFamilyFly:    "飞钓",
}

// 鱼种水层档（后台矩阵的列），按 fish_ecology.layer_pref 切分。
const (
	LayerBandSurface  = "surface"   // < 0.30 表层
	LayerBandMidUpper = "mid_upper" // 0.30 ~ 0.50 中上层
	LayerBandMidLower = "mid_lower" // 0.50 ~ 0.72 中下层
	LayerBandBottom   = "bottom"    // >= 0.72 底层
)

// LayerBandOrder 后台矩阵的列顺序（由浅到深）。
var LayerBandOrder = []string{LayerBandSurface, LayerBandMidUpper, LayerBandMidLower, LayerBandBottom}

// LayerBandLabels 水层档中文名与区间说明。
var LayerBandLabels = map[string]string{
	LayerBandSurface:  "表层",
	LayerBandMidUpper: "中上层",
	LayerBandMidLower: "中下层",
	LayerBandBottom:   "底层",
}

// LayerBandHint 档位区间提示，后台表头用。
var LayerBandHint = map[string]string{
	LayerBandSurface:  "< 0.30",
	LayerBandMidUpper: "0.30~0.50",
	LayerBandMidLower: "0.50~0.72",
	LayerBandBottom:   "≥ 0.72",
}

// RigAffinityWeight 一行矩阵值。Key 形如 "float/bottom"，供判鱼侧直接查表。
type RigAffinityWeight struct {
	RigFamily string  `json:"rig_family"`
	LayerBand string  `json:"layer_band"`
	Weight    float64 `json:"weight"`
}

// Key 矩阵键：钓法族/水层档。
func (r RigAffinityWeight) Key() string { return r.RigFamily + "/" + r.LayerBand }

// RigAffinityDefaults 出厂矩阵。语义：同一族在"主场"那档 >1，跨两档以上 <1。
// 与几何层是乘法关系，所以单行不设到 3 以上——要彻底封某族请用生态 enabled=0。
func RigAffinityDefaults() []RigAffinityWeight {
	return []RigAffinityWeight{
		{RigFamilyFloat, LayerBandSurface, 1.6},
		{RigFamilyFloat, LayerBandMidUpper, 1.3},
		{RigFamilyFloat, LayerBandMidLower, 0.8},
		{RigFamilyFloat, LayerBandBottom, 0.6},

		{RigFamilyBottom, LayerBandSurface, 0.35},
		{RigFamilyBottom, LayerBandMidUpper, 0.6},
		{RigFamilyBottom, LayerBandMidLower, 0.9},
		{RigFamilyBottom, LayerBandBottom, 1.35},

		{RigFamilyLure, LayerBandSurface, 1.4},
		{RigFamilyLure, LayerBandMidUpper, 1.2},
		{RigFamilyLure, LayerBandMidLower, 0.9},
		{RigFamilyLure, LayerBandBottom, 0.7},

		{RigFamilyMarine, LayerBandSurface, 0.8},
		{RigFamilyMarine, LayerBandMidUpper, 1.0},
		{RigFamilyMarine, LayerBandMidLower, 1.1},
		{RigFamilyMarine, LayerBandBottom, 1.1},

		{RigFamilyFly, LayerBandSurface, 1.8},
		{RigFamilyFly, LayerBandMidUpper, 1.3},
		{RigFamilyFly, LayerBandMidLower, 0.7},
		{RigFamilyFly, LayerBandBottom, 0.4},
	}
}

// ValidRigFamily / ValidLayerBand 校验矩阵键，保存时用。
func ValidRigFamily(v string) bool { _, ok := RigFamilyLabels[v]; return ok }
func ValidLayerBand(v string) bool { _, ok := LayerBandLabels[v]; return ok }

// RigAffinityWeightRange 允许的乘子范围。上限 5 是为了留出手工强偏的余地，
// 下限 0.05 是「几乎不在这一层」但不等于 0（等于 0 请用 enabled=0）。
const (
	RigAffinityMinWeight = 0.05
	RigAffinityMaxWeight = 5.0
)

// LoadRigAffinity 读全量矩阵，返回 "钓法族/水层档" → 乘子。
// 表为空时返回 defaults=true 与出厂矩阵（迁移已写入，正常不会走到这里）。
func (db *DB) LoadRigAffinity(ctx context.Context) (map[string]float64, bool, error) {
	out := map[string]float64{}
	if db == nil || db.SQL == nil {
		return RigAffinityDefaultMap(), true, nil
	}
	rows, err := db.SQL.QueryContext(ctx, `SELECT rig_family, layer_band, weight FROM rig_affinity_weight`)
	if err != nil {
		return RigAffinityDefaultMap(), true, err
	}
	defer rows.Close()
	for rows.Next() {
		var fam, band string
		var w float64
		if err := rows.Scan(&fam, &band, &w); err != nil {
			return RigAffinityDefaultMap(), true, err
		}
		out[fam+"/"+band] = w
	}
	if err := rows.Err(); err != nil {
		return RigAffinityDefaultMap(), true, err
	}
	if len(out) == 0 {
		return RigAffinityDefaultMap(), true, nil
	}
	return out, false, nil
}

// RigAffinityDefaultMap 出厂矩阵的 map 形式。
func RigAffinityDefaultMap() map[string]float64 {
	out := make(map[string]float64, 20)
	for _, d := range RigAffinityDefaults() {
		out[d.Key()] = d.Weight
	}
	return out
}

// SaveRigAffinity 覆盖写入给定行（未给的行保持原值）。校验族/档名与乘子区间。
func (db *DB) SaveRigAffinity(ctx context.Context, rows []RigAffinityWeight) (int64, error) {
	if db == nil || db.SQL == nil || len(rows) == 0 {
		return 0, nil
	}
	now := float64(time.Now().UnixNano()) / 1e9
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int64
	for _, r := range rows {
		if !ValidRigFamily(r.RigFamily) {
			return n, fmt.Errorf("未知钓法族：%s", r.RigFamily)
		}
		if !ValidLayerBand(r.LayerBand) {
			return n, fmt.Errorf("未知水层档：%s", r.LayerBand)
		}
		if r.Weight < RigAffinityMinWeight || r.Weight > RigAffinityMaxWeight {
			return n, fmt.Errorf("%s·%s 乘子超出 %.2f~%.1f：%g",
				RigFamilyLabels[r.RigFamily], LayerBandLabels[r.LayerBand],
				RigAffinityMinWeight, RigAffinityMaxWeight, r.Weight)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rig_affinity_weight(rig_family, layer_band, weight, updated_ts) VALUES(?,?,?,?)
			ON CONFLICT(rig_family, layer_band) DO UPDATE SET weight=excluded.weight, updated_ts=excluded.updated_ts`,
			r.RigFamily, r.LayerBand, r.Weight, now); err != nil {
			return n, fmt.Errorf("写入 %s·%s: %w", RigFamilyLabels[r.RigFamily], LayerBandLabels[r.LayerBand], err)
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return n, err
	}
	return n, nil
}

// RestoreRigAffinity 把 20 行恢复为出厂矩阵，返回写入行数。
func (db *DB) RestoreRigAffinity(ctx context.Context) (int64, error) {
	if db == nil || db.SQL == nil {
		return 0, nil
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM rig_affinity_weight`); err != nil {
		return 0, err
	}
	return db.SaveRigAffinity(ctx, RigAffinityDefaults())
}

// RigAffinityMatrixView 后台展示用：补齐缺失行（读不到的行按出厂值显示）。
func RigAffinityMatrixView(loaded map[string]float64) []RigAffinityWeight {
	def := RigAffinityDefaultMap()
	out := make([]RigAffinityWeight, 0, len(RigFamilyOrder)*len(LayerBandOrder))
	for _, fam := range RigFamilyOrder {
		for _, band := range LayerBandOrder {
			key := fam + "/" + band
			w, ok := loaded[key]
			if !ok {
				w = def[key]
			}
			out = append(out, RigAffinityWeight{RigFamily: fam, LayerBand: band, Weight: w})
		}
	}
	return out
}

// RigAffinityKey 判鱼侧拼键，避免两边各写一遍分隔符。
func RigAffinityKey(family, band string) string {
	return strings.Join([]string{family, band}, "/")
}
