// Package handler — self_opt_edits.go (0.5.2).
//
// HTTP surface for the agent_opt_edit ledger's human-confirm tier:
//
//	GET  /api/experimental/self-opt/edits?workspace_id=&limit=&offset=
//	     → pending-confirmation ('suggested') edits, sorted by score
//	POST /api/experimental/self-opt/edits/{id}/apply
//	     {"workspace_id"} → apply a suggested/rejected edit (server re-checks gates)
//	POST /api/experimental/self-opt/edits/{id}/reject
//	     {"workspace_id","reason"?} → soft reject
//	POST /api/experimental/self-opt/edits/{id}/ignore
//	     {"workspace_id"} → soft archive (NOT in rejection buffer)
//	POST /api/experimental/self-opt/edits/{id}/revert
//	     {"workspace_id"} → roll back an applied edit to its snapshot
//
// All gated like the rest of the self-opt surface (flag off → 404).
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SelfOptEditDTO is the wire shape for one ledger row (the 待确认建议 list
// and the audit trail). Scores are floats for the renderer.
type SelfOptEditDTO struct {
	ID               string  `json:"id"`
	AgentID          string  `json:"agent_id,omitempty"`
	AgentName        string  `json:"agent_name,omitempty"`
	// TargetType + TargetID (0.5.3): the optimizable subject this edit
	// targets — agent | skill | squad | autopilot. AgentName is the
	// subject's display name for all four kinds.
	TargetType       string  `json:"target_type"`
	TargetID         string  `json:"target_id,omitempty"`
	EditType         string  `json:"edit_type"`
	BeforeText       string  `json:"before_text"`
	AfterText        string  `json:"after_text"`
	Rationale        string  `json:"rationale,omitempty"`
	Application      string  `json:"application"`
	ValidationScore  float64 `json:"validation_score,omitempty"`
	ValidationReason string  `json:"validation_reason,omitempty"`
	CreatedAt        string  `json:"created_at"`
	AppliedBy        string  `json:"applied_by,omitempty"`
	CorrectedTaskID  string  `json:"corrected_task_id,omitempty"`
}

func toSelfOptEditDTO(row db.AgentOptEdit) SelfOptEditDTO {
	dto := SelfOptEditDTO{
		ID:          uuid.UUID(row.ID.Bytes).String(),
		EditType:    row.EditType,
		BeforeText:  row.BeforeText,
		AfterText:   row.AfterText,
		Application: row.Application,
	}
	dto.TargetType = row.TargetType
	if row.TargetID.Valid {
		dto.TargetID = uuid.UUID(row.TargetID.Bytes).String()
	}
	if row.AgentID.Valid {
		dto.AgentID = uuid.UUID(row.AgentID.Bytes).String()
	}
	if row.AppliedBy.Valid {
		dto.AppliedBy = row.AppliedBy.String
	}
	if row.CorrectedTaskID.Valid {
		dto.CorrectedTaskID = uuid.UUID(row.CorrectedTaskID.Bytes).String()
	}
	if row.Rationale.Valid {
		dto.Rationale = row.Rationale.String
	}
	if row.ValidationReason.Valid {
		dto.ValidationReason = row.ValidationReason.String
	}
	if row.ValidationScore.Valid {
		if v, ok := numericToFloat64Util(row.ValidationScore); ok {
			dto.ValidationScore = v
		}
	}
	dto.CreatedAt = row.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	return dto
}

// ListSelfOptEdits handles GET /api/experimental/self-opt/edits.
func (h *Handler) ListSelfOptEdits(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	wsID, err := parseSelfOptWorkspaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 0.5.2 adversarial review s2 residual: the read surface must be
	// membership-gated like the writes — a flag-opted-in user must not be
	// able to list another workspace's pending suggestions by guessing the
	// workspace uuid.
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	if h.SelfOptService == nil {
		writeError(w, http.StatusServiceUnavailable, "self-opt service not initialised")
		return
	}
	limit := int32(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	offset := int32(0)
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			offset = int32(n)
		}
	}
	rows, err := h.SelfOptService.ListSuggestedEdits(r.Context(), wsID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list suggested edits: "+err.Error())
		return
	}
	out := make([]SelfOptEditDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSelfOptEditDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"edits": out})
}

// editActionRequest is the shared body for the four edit mutations.
type editActionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Reason      string `json:"reason,omitempty"`
}

func (h *Handler) parseEditAction(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, editActionRequest, bool) {
	editID, err := parseSelfOptRunID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return pgtype.UUID{}, pgtype.UUID{}, editActionRequest{}, false
	}
	var body editActionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return pgtype.UUID{}, pgtype.UUID{}, editActionRequest{}, false
	}
	wsID, err := util.ParseUUID(body.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace_id: "+err.Error())
		return pgtype.UUID{}, pgtype.UUID{}, editActionRequest{}, false
	}
	return editID, wsID, body, true
}

// ApplySelfOptEdit handles POST /api/experimental/self-opt/edits/{id}/apply.
func (h *Handler) ApplySelfOptEdit(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.SelfOptService == nil {
		writeError(w, http.StatusServiceUnavailable, "self-opt service not initialised")
		return
	}
	editID, wsID, _, ok := h.parseEditAction(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	if _, err := h.SelfOptService.ApplyEdit(r.Context(), wsID, editID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

// RejectSelfOptEdit handles POST /api/experimental/self-opt/edits/{id}/reject.
func (h *Handler) RejectSelfOptEdit(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.SelfOptService == nil {
		writeError(w, http.StatusServiceUnavailable, "self-opt service not initialised")
		return
	}
	editID, wsID, body, ok := h.parseEditAction(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	if err := h.SelfOptService.RejectEdit(r.Context(), wsID, editID, body.Reason); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// IgnoreSelfOptEdit handles POST /api/experimental/self-opt/edits/{id}/ignore.
func (h *Handler) IgnoreSelfOptEdit(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.SelfOptService == nil {
		writeError(w, http.StatusServiceUnavailable, "self-opt service not initialised")
		return
	}
	editID, wsID, _, ok := h.parseEditAction(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	if err := h.SelfOptService.IgnoreEdit(r.Context(), wsID, editID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

// RevertSelfOptEdit handles POST /api/experimental/self-opt/edits/{id}/revert.
func (h *Handler) RevertSelfOptEdit(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.SelfOptService == nil {
		writeError(w, http.StatusServiceUnavailable, "self-opt service not initialised")
		return
	}
	editID, wsID, _, ok := h.parseEditAction(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	if err := h.SelfOptService.RevertEdit(r.Context(), wsID, editID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reverted"})
}
