package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"rf4server/internal/store"
)

// 钓法倾向矩阵的后台读写：钓法族 × 鱼种水层档 → 判鱼乘子（20 行）。
//
// 为什么要有这一层：判鱼里的"钓法水层"原本只有一条几何公式，底钓恒 0.93、
// 浮钓按有效饵深，玩家看不见也调不了。实机抽样（琥珀湖、玉米、正午）：
// 底钓 100% 出底层鱼、浮钓仍有 64% 是底层鱼——"不同钓法钓不同鱼"只有一半成立。
// 这层把"哪种钓法更该钓到哪一层鱼"变成可读可改的矩阵。
//
// 乘子原样生效（不走判鱼链的幂压缩），保存后判鱼侧 30 秒内生效（gameserver 侧缓存 TTL）。

// handleRigAffinityGet GET：当前矩阵 + 出厂矩阵 + 维度定义 + 开关状态。
func (s *Server) handleRigAffinityGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	loaded, defaultsOnly, err := s.db.LoadRigAffinity(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	enabled := true
	if v, ok, err := s.db.TunableGet(ctx, "rig_affinity_enabled"); err == nil && ok {
		enabled = v != "0"
	}
	writeJSON(w, map[string]any{
		"enabled":       enabled,
		"defaults_only": defaultsOnly, // true = 表为空，当前显示的是出厂值
		"families":      rigAffinityFamilies(),
		"bands":         rigAffinityBands(),
		"rows":          store.RigAffinityMatrixView(loaded),
		"defaults":      store.RigAffinityDefaults(),
		"min_weight":    store.RigAffinityMinWeight,
		"max_weight":    store.RigAffinityMaxWeight,
	})
}

func rigAffinityFamilies() []map[string]string {
	out := make([]map[string]string, 0, len(store.RigFamilyOrder))
	for _, k := range store.RigFamilyOrder {
		out = append(out, map[string]string{"key": k, "label": store.RigFamilyLabels[k]})
	}
	return out
}

func rigAffinityBands() []map[string]string {
	out := make([]map[string]string, 0, len(store.LayerBandOrder))
	for _, k := range store.LayerBandOrder {
		out = append(out, map[string]string{
			"key": k, "label": store.LayerBandLabels[k], "hint": store.LayerBandHint[k],
		})
	}
	return out
}

// handleRigAffinitySave POST：只更新请求里给出的格子，未给的行保持原值。
func (s *Server) handleRigAffinitySave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rows []store.RigAffinityWeight `json:"rows"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	if len(req.Rows) == 0 {
		httpError(w, 400, "没有要保存的格子")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, err := s.db.SaveRigAffinity(ctx, req.Rows)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "rig_affinity", "all", "rows="+strconv.FormatInt(n, 10))
	writeJSON(w, map[string]any{"ok": true, "updated": n})
}

// handleRigAffinityRestore POST：恢复出厂矩阵。
func (s *Server) handleRigAffinityRestore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, err := s.db.RestoreRigAffinity(ctx)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(ctx, adminOf(r), "rig_affinity", "all", "restore rows="+strconv.FormatInt(n, 10))
	writeJSON(w, map[string]any{"ok": true, "updated": n})
}
