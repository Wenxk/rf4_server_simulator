package admin

import "net/http"

// 技能升级概率三档模式的后台入口。
//
// 真正的行为只由八个 tunable（skill_gain_pct_<cat>）决定，这里只是把
// 「原版 / 百分百」做成一次事务内的批量写入，避免逐个写库时中途失败留下
// 半套配置。skill_gain_mode 落库仅作为后台界面回显的标记，判鱼侧不读它。

// handleSkillGainModeSet 原子切换八类技能的升级概率模式。
func (s *Server) handleSkillGainModeSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, 400, "参数错误")
		return
	}
	switch req.Mode {
	case "native", "fixed100", "custom":
	default:
		httpError(w, 400, "技能升级概率模式只能是 native、fixed100 或 custom")
		return
	}
	if err := s.db.SkillGainModeSet(r.Context(), req.Mode, adminOf(r)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	_ = s.db.AuditLogWrite(r.Context(), adminOf(r), "skill_gain_mode", "mode", req.Mode)
	_ = s.db.QueuePush(r.Context(), mustJSON(map[string]any{"op": "reload_tunables"}))
	writeJSON(w, map[string]any{"ok": true, "mode": req.Mode})
}
