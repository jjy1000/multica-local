// Package handler — agent_trust.go (0.5.2 + 0.5.5.1).
//
// HTTP surface for the agent trust score + self-review ledger:
//
//	GET    /api/experimental/trust/profiles?workspace_id=&limit=
//	       → trust leaderboard (agent, score, counters)
//	GET    /api/experimental/trust/events?workspace_id=&limit=&offset=
//	       → timeline of corrections / reviews (newest first)
//	POST   /api/experimental/trust/{agentId}/correct
//	       {"workspace_id","task_id"?,"issue_id"?,"note"?} → -0.5 correction
//	POST   /api/experimental/trust/{agentId}/review
//	       {"workspace_id","task_id"?,"issue_id"?} → manual self-review trigger
//
// 0.5.5.1: agent_self_optimization is now product-level. The
// per-handler experimentalFlagEnabled() gate is removed — every
// endpoint is unconditionally reachable. The user-facing control
// point for self-opt is the autopilot row's `enabled` field, not
// the per-user opt-in.
//
// All endpoints are gated the same way the rest of the
// agent_self_optimization surface is: experimentalFlagEnabled() with the
// per-user experimental_pref row as source of truth; flag off → 404 so the
// surface is not even discoverable.
//
// The score arithmetic + event ledger live in
// server/internal/service/agent_trust (service.go); the handlers here are
// thin JSON adapters.
package handler

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TrustProfileDTO is one leaderboard row. Scores are floats for the
// renderer; counters are ints.
type TrustProfileDTO struct {
	AgentID              string  `json:"agent_id"`
	WorkspaceID          string  `json:"workspace_id"`
	Score                float64 `json:"score"`
	ReviewThreshold      float64 `json:"review_threshold"`
	ReviewRequestedCount int32   `json:"review_requested_count"`
	ReviewPassCount      int32   `json:"review_pass_count"`
	ReviewFailCount      int32   `json:"review_fail_count"`
	CorrectionCount      int32   `json:"correction_count"`
}

// TrustEventDTO is one timeline row.
type TrustEventDTO struct {
	ID          string  `json:"id"`
	AgentID     string  `json:"agent_id"`
	EventType   string  `json:"event_type"`
	ScoreDelta  float64 `json:"score_delta"`
	ScoreBefore float64 `json:"score_before,omitempty"`
	ScoreAfter  float64 `json:"score_after,omitempty"`
	TaskID      string  `json:"task_id,omitempty"`
	IssueID     string  `json:"issue_id,omitempty"`
	Note        string  `json:"note,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// ListTrustProfiles handles GET /api/experimental/trust/profiles.
func (h *Handler) ListTrustProfiles(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	wsID, err := parseTrustWorkspaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := h.Queries.ListAgentTrustProfilesByWorkspace(r.Context(), db.ListAgentTrustProfilesByWorkspaceParams{
		WorkspaceID: wsID,
		Limit:       int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list trust profiles: "+err.Error())
		return
	}
	out := make([]TrustProfileDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, trustProfileDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// ListTrustEvents handles GET /api/experimental/trust/events.
func (h *Handler) ListTrustEvents(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	wsID, err := parseTrustWorkspaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			offset = n
		}
	}
	rows, err := h.Queries.ListAgentTrustEventsByWorkspace(r.Context(), db.ListAgentTrustEventsByWorkspaceParams{
		WorkspaceID: wsID,
		Limit:       int32(limit),
		Offset:      int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list trust events: "+err.Error())
		return
	}
	out := make([]TrustEventDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, trustEventDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// CorrectAgentTrust handles POST /api/experimental/trust/{agentId}/correct.
// The caller is the user flagging the agent's work as wrong; the service
// applies -0.5 and appends a 'correction' event. Body:
//
//	{"workspace_id":"...","task_id"?:"...","issue_id"?:"...","note"?:"..."}
func (h *Handler) CorrectAgentTrust(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.TrustService == nil {
		writeError(w, http.StatusServiceUnavailable, "trust service not initialised")
		return
	}
	agentID, err := parseTrustAgentID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		WorkspaceID string `json:"workspace_id"`
		TaskID      string `json:"task_id,omitempty"`
		IssueID     string `json:"issue_id,omitempty"`
		Note        string `json:"note,omitempty"`
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
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	taskID, err := optionalUUID(body.TaskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "task_id: "+err.Error())
		return
	}
	issueID, err := optionalUUID(body.IssueID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "issue_id: "+err.Error())
		return
	}
	callerID, err := util.ParseUUID(requestUserID(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid caller identity")
		return
	}
	after, err := h.TrustService.ApplyCorrection(r.Context(), h.Queries, wsID, agentID, taskID, issueID, body.Note, callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apply correction: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"score": after})
}

// ReviewAgentTrust handles POST /api/experimental/trust/{agentId}/review.
// Manual self-review trigger: runs the LLM review on the task's stored
// result (or skips when the task has no result). Same verdict semantics as
// the automatic gate. Body: {"workspace_id","task_id"?,"issue_id"?}
func (h *Handler) ReviewAgentTrust(w http.ResponseWriter, r *http.Request) {
	if false /* 0.5.5.1: agent_self_optimization is product-level. The flag gate is removed. */ && !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), "agent_self_optimization") {
		http.NotFound(w, r)
		return
	}
	if h.TrustService == nil {
		writeError(w, http.StatusServiceUnavailable, "trust service not initialised")
		return
	}
	agentID, err := parseTrustAgentID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		WorkspaceID string `json:"workspace_id"`
		TaskID      string `json:"task_id,omitempty"`
		IssueID     string `json:"issue_id,omitempty"`
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
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(wsID), "workspace not found"); !ok {
		return
	}
	// Resolve the task to review. The caller supplies task_id (preferred) or
	// the most recent completed task for this agent in the workspace.
	task, ok, err := resolveTaskForReview(r, h, agentID, wsID, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no reviewable task for this agent")
		return
	}
	verdict := h.TrustService.ReviewTask(r.Context(), h.Queries, task, task.Result)
	writeJSON(w, http.StatusOK, map[string]any{"verdict": verdict})
}

// ---- helpers ----

func parseTrustWorkspaceID(r *http.Request) (pgtype.UUID, error) {
	raw := r.URL.Query().Get("workspace_id")
	if raw == "" {
		return pgtype.UUID{}, errors.New("workspace_id query param required")
	}
	return util.ParseUUID(raw)
}

func parseTrustAgentID(r *http.Request) (pgtype.UUID, error) {
	raw := chi.URLParam(r, "agentId")
	if raw == "" {
		return pgtype.UUID{}, errors.New("agent id path param required")
	}
	return util.ParseUUID(raw)
}

func optionalUUID(raw string) (pgtype.UUID, error) {
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	return util.ParseUUID(raw)
}

// resolveTaskForReview finds the task to review: the one named in the body,
// or the agent's most recent completed task in the workspace.
func resolveTaskForReview(
	r *http.Request,
	h *Handler,
	agentID, wsID pgtype.UUID,
	body struct {
		WorkspaceID string `json:"workspace_id"`
		TaskID      string `json:"task_id,omitempty"`
		IssueID     string `json:"issue_id,omitempty"`
	},
) (db.AgentTaskQueue, bool, error) {
	if body.TaskID != "" {
		taskID, err := util.ParseUUID(body.TaskID)
		if err != nil {
			return db.AgentTaskQueue{}, false, err
		}
		task, err := h.Queries.GetAgentTask(r.Context(), taskID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.AgentTaskQueue{}, false, nil
			}
			return db.AgentTaskQueue{}, false, err
		}
		return task, true, nil
	}
	task, err := h.Queries.LatestCompletedTaskForAgent(r.Context(), db.LatestCompletedTaskForAgentParams{
		AgentID:     agentID,
		WorkspaceID: wsID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AgentTaskQueue{}, false, nil
		}
		return db.AgentTaskQueue{}, false, err
	}
	return task, true, nil
}

// numericToFloat64Util converts pgtype.Numeric to float64 for the wire
// DTOs. Returns false for NaN / infinity / nil (callers leave the field 0).
func numericToFloat64Util(n pgtype.Numeric) (float64, bool) {
	if n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return 0, false
	}
	f := new(big.Float).SetInt(n.Int)
	if n.Exp != 0 {
		f.Mul(f, new(big.Float).SetFloat64(math.Pow10(int(n.Exp))))
	}
	v, _ := f.Float64()
	return v, true
}

func trustProfileDTO(row db.AgentTrustProfile) TrustProfileDTO {
	dto := TrustProfileDTO{
		AgentID:     uuid.UUID(row.AgentID.Bytes).String(),
		WorkspaceID: uuid.UUID(row.WorkspaceID.Bytes).String(),
	}
	if v, ok := numericToFloat64Util(row.Score); ok {
		dto.Score = v
	}
	if v, ok := numericToFloat64Util(row.ReviewThreshold); ok {
		dto.ReviewThreshold = v
	}
	dto.ReviewRequestedCount = row.ReviewRequestedCount
	dto.ReviewPassCount = row.ReviewPassCount
	dto.ReviewFailCount = row.ReviewFailCount
	dto.CorrectionCount = row.CorrectionCount
	return dto
}

func trustEventDTO(row db.AgentTrustEvent) TrustEventDTO {
	dto := TrustEventDTO{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		AgentID:   uuid.UUID(row.AgentID.Bytes).String(),
		EventType: row.EventType,
	}
	if v, ok := numericToFloat64Util(row.ScoreDelta); ok {
		dto.ScoreDelta = v
	}
	if row.ScoreBefore.Valid {
		if v, ok := numericToFloat64Util(row.ScoreBefore); ok {
			dto.ScoreBefore = v
		}
	}
	if row.ScoreAfter.Valid {
		if v, ok := numericToFloat64Util(row.ScoreAfter); ok {
			dto.ScoreAfter = v
		}
	}
	if row.TaskID.Valid {
		dto.TaskID = uuid.UUID(row.TaskID.Bytes).String()
	}
	if row.IssueID.Valid {
		dto.IssueID = uuid.UUID(row.IssueID.Bytes).String()
	}
	if row.Note.Valid {
		dto.Note = row.Note.String
	}
	dto.CreatedAt = row.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	return dto
}
