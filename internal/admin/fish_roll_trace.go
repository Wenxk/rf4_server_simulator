package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 判鱼决策追踪的只读查询。写侧在 gameserver 的真实咬钩路径（fish_roll_trace.go），
// 后台只负责展示；只读接口不写 audit_log。

const maxFishRollTraceLimit = 200

// handleFishRollTrace GET：?loc=&limit=
// 返回最近若干条判定快照，含 payload（候选与各层权重）原文，由前端解析展示。
func (s *Server) handleFishRollTrace(w http.ResponseWriter, r *http.Request) {
	loc := strings.TrimSpace(r.URL.Query().Get("loc"))
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxFishRollTraceLimit {
		limit = maxFishRollTraceLimit
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := s.db.ListFishRollTrace(ctx, loc, limit)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"traces": rows})
}
