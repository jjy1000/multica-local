// Package handler — agent_self_optimization.go (0.3.45.1).
//
// HTTP surface for the agent_self_optimization lab history view:
//   - GET  /api/experimental/self-opt/runs            → list runs
//   - GET  /api/experimental/self-opt/runs/{id}       → single run detail
//   - POST /api/experimental/self-opt/runs            → manual trigger
//   - POST /api/experimental/self-opt/runs/{id}/cancel → cancel a pending run
//
// All endpoints require auth + workspace membership. The flag-gate
// lives at experimental.DefaultFor("agent_self_optimization"): flag
// off → 404 for everything (the routes register, but the handler
// 404s so the existence of the flag is not leaked to clients).
//
// Why 404 (not 403): a 403 would confirm the flag exists. 404 means
// "this surface does not exist for your client" — same posture as the
// other Labs platform endpoints (see claude_lab_forecast.go,
// llm_wiki_bridge.go).

package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SelfOptRunListResponse is the wire shape for the list endpoint.
// `runs` is sorted newest-first; pagination is offset/limit.
type SelfOptRunListResponse struct {
	Runs []SelfOptRunDTO `json:"runs"`
	// HasMore is true when the query returned exactly limit rows; the
	// client can re-issue with offset+=limit to fetch the next page.
	HasMore bool `json:"has_more"`
}

// SelfOptRunDTO is the per-run payload. PromptSuggestions is
// pre-decoded JSON so the renderer can iterate without a second
// round-trip. ReportMd is the full markdown body.
type SelfOptRunDTO struct {
	ID                string                 `json:"id"`
	WorkspaceID       string                 `json:"workspace_id"`
	Status            string                 `json:"status"`
	TriggerKind       string                 `json:"trigger_kind"`
	StartedAt         string                 `json:"started_at"`
	FinishedAt        string                 `json:"finished_at,omitempty"`
	SourceIssueCount  int                    `json:"source_issue_count"`
	PromptSuggestions []any                  `json:"prompt_suggestions"`
	ReportMd          string                 `json:"report_md,omitempty"`
	KBAppendixPath    string                 `json:"kb_appendix_path,omitempty"`
	ErrorMessage      string                 `json:"error_message,omitempty"`
	CreatedIssueID    string                 `json:"created_issue_id,omitempty"`
}

// ListSelfOptRuns handles GET /api/experimental/self-opt/runs.
//
// Query params:
//
//	workspace_id  required (UUID)
//	limit         default 20, max 100
//	offset        default 0
func (h *Handler) ListSelfOptRuns(w http.ResponseWriter, r *http.Request) {
	if !experimental.DefaultFor("agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	wsID, err := parseSelfOptWorkspaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			offset = n
		}
	}

	rows, err := h.Queries.ListAgentSelfOptRunsByWorkspace(r.Context(), db.ListAgentSelfOptRunsByWorkspaceParams{
		WorkspaceID: wsID,
		Limit:       int32(limit),
		Offset:      int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list self-opt runs: "+err.Error())
		return
	}
	resp := SelfOptRunListResponse{
		Runs:    make([]SelfOptRunDTO, 0, len(rows)),
		HasMore: len(rows) == limit,
	}
	for _, row := range rows {
		resp.Runs = append(resp.Runs, toSelfOptRunDTO(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetSelfOptRun handles GET /api/experimental/self-opt/runs/{id}.
func (h *Handler) GetSelfOptRun(w http.ResponseWriter, r *http.Request) {
	if !experimental.DefaultFor("agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	runID, err := parseSelfOptRunID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.Queries.GetAgentSelfOptRun(r.Context(), runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		writeError(w, http.StatusInternalServerError, "read self-opt run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toSelfOptRunDTO(row))
}

// TriggerSelfOptRun handles POST /api/experimental/self-opt/runs.
// Body: {"workspace_id": "<uuid>"}
//
// Returns 202 Accepted with the pending run row id; the actual run
// executes asynchronously in a goroutine inside SelfOptService.
//
// 0.3.45.2: gate moved from process-level catalog default to per-user
// flagOnForUser. The 404 → 403 posture is preserved — the handler
// still hides the endpoint behind the flag, but it now ALSO checks
// that the caller is opted in (returns 403 with a clear message when
// the caller has no row in experimental_pref, instead of silently
// succeeding). This prevents a future "global opt-in by accident"
// regression: only opted-in users can trigger runs.
func (h *Handler) TriggerSelfOptRun(w http.ResponseWriter, r *http.Request) {
	if !experimental.DefaultFor("agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.SelfOptService == nil {
		writeError(w, http.StatusServiceUnavailable, "self-opt service not initialised")
		return
	}
	callerUserIDStr := requestUserID(r)
	if callerUserIDStr == "" {
		writeError(w, http.StatusUnauthorized, "missing caller identity")
		return
	}
	callerUserID, err := util.ParseUUID(callerUserIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid caller user id: "+err.Error())
		return
	}
	var body struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	wsID, err := util.ParseUUID(body.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace_id: "+err.Error())
		return
	}
	runID, err := h.SelfOptService.TriggerManualRun(r.Context(), callerUserID, wsID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"run_id": uuid.UUID(runID.Bytes).String(),
	})
}

// CancelSelfOptRun handles POST /api/experimental/self-opt/runs/{id}/cancel.
// Marks the run 'cancelled' regardless of its current status — the
// in-flight runner will see the next state transition (or the
// advisory lock will release) and exit cleanly.
func (h *Handler) CancelSelfOptRun(w http.ResponseWriter, r *http.Request) {
	if !experimental.DefaultFor("agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	runID, err := parseSelfOptRunID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err = h.Queries.UpdateAgentSelfOptRunStatus(r.Context(), db.UpdateAgentSelfOptRunStatusParams{
		ID:           runID,
		Status:       "cancelled",
		ErrorMessage: pgtype.Text{String: "user-cancelled", Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cancel self-opt run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// ---- helpers ----

func parseSelfOptWorkspaceID(r *http.Request) (pgtype.UUID, error) {
	raw := r.URL.Query().Get("workspace_id")
	if raw == "" {
		return pgtype.UUID{}, errors.New("workspace_id query param required")
	}
	return util.ParseUUID(raw)
}

func parseSelfOptRunID(r *http.Request) (pgtype.UUID, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return pgtype.UUID{}, errors.New("run id path param required")
	}
	return util.ParseUUID(raw)
}

// toSelfOptRunDTO converts the sqlc row into the wire DTO. Decodes
// the JSONB prompt_suggestions column so the renderer can iterate
// directly; if the column is malformed (legacy / pre-0.3.45.1 rows),
// the field is an empty array rather than a 500.
func toSelfOptRunDTO(row db.AgentSelfOptRun) SelfOptRunDTO {
	dto := SelfOptRunDTO{
		ID:               uuid.UUID(row.ID.Bytes).String(),
		WorkspaceID:      uuid.UUID(row.WorkspaceID.Bytes).String(),
		Status:           row.Status,
		TriggerKind:      row.TriggerKind,
		SourceIssueCount: int(row.SourceIssueCount),
	}
	if row.StartedAt.Valid {
		dto.StartedAt = row.StartedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if row.FinishedAt.Valid {
		dto.FinishedAt = row.FinishedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if row.ReportMd.Valid {
		dto.ReportMd = row.ReportMd.String
	}
	if row.KbAppendixPath.Valid {
		dto.KBAppendixPath = row.KbAppendixPath.String
	}
	if row.ErrorMessage.Valid {
		dto.ErrorMessage = row.ErrorMessage.String
	}
	if row.CreatedIssueID.Valid {
		dto.CreatedIssueID = uuid.UUID(row.CreatedIssueID.Bytes).String()
	}
	if len(row.PromptSuggestions) > 0 {
		var raw []any
		if err := json.Unmarshal(row.PromptSuggestions, &raw); err == nil {
			dto.PromptSuggestions = raw
		}
	}
	if dto.PromptSuggestions == nil {
		dto.PromptSuggestions = []any{}
	}
	return dto
}