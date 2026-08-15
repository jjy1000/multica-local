// Package handler — swarm_run.go (0.5.21).
//
// HTTP surface for the swarm topology feature. Mirrors the
// mythos_supervise.go pattern: thin membership-gated JSON handlers
// over the swarm service + sqlc queries.
//
//   POST   /api/experimental/swarm-topology/runs               — bootstrap
//   POST   /api/experimental/swarm-topology/runs/{id}/interrupt — user action
//   GET    /api/experimental/swarm-topology/runs/{id}/state    — live status
//   GET    /api/issues/{id}/swarm-runs                          — reverse lookup
//
// Like mythos_supervise, none of these touch the LLM dispatch
// path; the orchestrator service owns the per-run goroutine.
// Flag-off (no swarm_topology catalog entry) returns 404 at the
// gate middleware, with zero side effects on already-running
// orchestrators (Service.Stop cancels them at daemon shutdown,
// not at flag toggle).

package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	swarmsvc "github.com/multica-ai/multica/server/internal/service/swarm"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SwarmRoleResponse is the JSON envelope for a swarm_role row.
type SwarmRoleResponse struct {
	ID               string `json:"id"`
	RoleName         string `json:"role_name"`
	RoleInstructions string `json:"role_instructions"`
	ParentRoleID     string `json:"parent_role_id,omitempty"`
	Status           string `json:"status"`
	CurrentStep      string `json:"current_step,omitempty"`
	LastHeartbeatAt  string `json:"last_heartbeat_at,omitempty"`
}

// SwarmRunResponse is the JSON envelope for a swarm_run row.
type SwarmRunResponse struct {
	ID              string              `json:"id"`
	RootIssueID     string              `json:"root_issue_id"`
	Status          string              `json:"status"`
	CurrentPhase    string              `json:"current_phase"`
	MaxRuntimeHours int32               `json:"max_runtime_hours"`
	StartedAt       string              `json:"started_at"`
	CompletedAt     string              `json:"completed_at,omitempty"`
	InterruptedAt   string              `json:"interrupted_at,omitempty"`
	InterruptReason string              `json:"interrupt_reason,omitempty"`
	Roles           []SwarmRoleResponse `json:"roles,omitempty"`
}

// SwarmStateResponse is the live state envelope for the orchestrator
// ticker. Returned by GET /runs/{id}/state.
type SwarmStateResponse struct {
	RunID          string              `json:"run_id"`
	Status         string              `json:"status"`
	CurrentPhase   string              `json:"current_phase"`
	Roles          []SwarmRoleResponse `json:"roles"`
	ActiveCount    int64               `json:"active_role_count"`
	CompletedCount int64               `json:"completed_role_count"`
}

// PostSwarmRunRequest is the body for POST /runs (bootstrap).
type PostSwarmRunRequest struct {
	RootIssueID     string          `json:"root_issue_id"`
	Problem         string          `json:"problem"`
	MaxRuntimeHours int32           `json:"max_runtime_hours,omitempty"`
	TopologySpec    json.RawMessage `json:"topology_spec,omitempty"`
}

// PostSwarmInterruptRequest is the body for POST /runs/{id}/interrupt.
type PostSwarmInterruptRequest struct {
	Kind    string          `json:"kind"` // pause/cancel/redirect/inject_message
	Payload json.RawMessage `json:"payload,omitempty"`
}

// swarmSvc is the per-handler swarm service. Constructed lazily on
// first call (mirrors mythosService() at mythos_supervise.go:262 —
// keeps the swarm service out of Handler's constructor signature so
// existing tests don't have to stub it).
var swarmSvcOnce = struct {
	svc *swarmsvc.Service
}{}

// swarmService returns the swarm orchestrator service wired into the
// handler. Lazy-init pattern.
func (h *Handler) swarmService() *swarmsvc.Service {
	if swarmSvcOnce.svc == nil {
		swarmSvcOnce.svc = swarmsvc.NewService(h.Queries, slog.Default())
	}
	return swarmSvcOnce.svc
}

// PostSwarmRun bootstraps a swarm for an issue. Idempotent on
// root_issue_id (UNIQUE index enforces one-swarm-per-issue): re-calling
// with the same root_issue_id returns the existing run. Membership
// gate is the workspace-side issue loader.
func (h *Handler) PostSwarmRun(w http.ResponseWriter, r *http.Request) {
	var req PostSwarmRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RootIssueID == "" {
		writeError(w, http.StatusBadRequest, "root_issue_id required")
		return
	}

	// Load the issue for membership gating. Mirrors loadIssueForUser.
	issue, ok := h.loadIssueForUser(w, r, req.RootIssueID)
	if !ok {
		return
	}
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var creatorUUID pgtype.UUID
	if err := creatorUUID.Scan(creatorID); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid creator uuid")
		return
	}

	// Idempotency: check for an existing run on this root_issue_id.
	existing, err := h.Queries.GetSwarmRunByRootIssue(r.Context(), rootIssueUUIDFromString(req.RootIssueID))
	if err == nil && existing.ID.Valid {
		writeJSON(w, http.StatusOK, h.swarmRunToResponse(existing, nil))
		return
	}

	// Bootstrap: insert the swarm_run row with status='preparing'.
	// The leader agent (multica-creating-swarms skill) fills in
	// topology_spec during the planning phase.
	maxRuntime := req.MaxRuntimeHours
	if maxRuntime <= 0 {
		maxRuntime = 72
	}
	run, err := h.Queries.CreateSwarmRun(r.Context(), db.CreateSwarmRunParams{
		WorkspaceID:     issue.WorkspaceID,
		CreatorUserID:   creatorUUID,
		RootIssueID:     rootIssueUUIDFromString(req.RootIssueID),
		Problem:         req.Problem,
		MaxRuntimeHours: maxRuntime,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create swarm run: "+err.Error())
		return
	}

	// Lock the resource claim (mirrors install_mythos pattern).
	if err := experimental.Claim(r.Context(), h.Queries, experimental.SourceSwarmTopology, experimental.ResourceType("swarm_run"), run.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "claim lock: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, h.swarmRunToResponse(run, nil))
}

// GetSwarmRunState returns the live state envelope (status + phase +
// role set + counters) for one swarm_run. Membership-gated via the
// root_issue_id loader.
func (h *Handler) GetSwarmRunState(w http.ResponseWriter, r *http.Request) {
	runUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}

	run, err := h.Queries.GetSwarmRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "swarm run not found")
		return
	}

	// Membership gate via root_issue loader.
	if _, ok := h.loadIssueForUser(w, r, run.RootIssueID.String()); !ok {
		return
	}

	roles, err := h.Queries.ListSwarmRolesByRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list roles: "+err.Error())
		return
	}
	activeCount, err := h.Queries.CountActiveRolesByRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count active: "+err.Error())
		return
	}
	completedCount, err := h.Queries.CountCompletedRolesByRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count completed: "+err.Error())
		return
	}

	resp := SwarmStateResponse{
		RunID:          run.ID.String(),
		Status:         run.Status,
		CurrentPhase:   run.CurrentPhase,
		Roles:          h.swarmRolesToResponse(roles),
		ActiveCount:    activeCount,
		CompletedCount: completedCount,
	}
	writeJSON(w, http.StatusOK, resp)
}

// PostSwarmInterrupt applies a user-initiated interrupt (pause /
// cancel / redirect / inject_message). The orchestrator's tick loop
// picks up the interrupt on its next 30s cycle. For cancel, the
// handler also flips status to 'aborted' immediately so the user
// sees the new state without waiting for the tick.
func (h *Handler) PostSwarmInterrupt(w http.ResponseWriter, r *http.Request) {
	runUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}

	var req PostSwarmInterruptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Kind {
	case "pause", "cancel", "redirect", "inject_message":
	default:
		writeError(w, http.StatusBadRequest, "kind must be pause|cancel|redirect|inject_message")
		return
	}

	run, err := h.Queries.GetSwarmRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "swarm run not found")
		return
	}

	// Membership gate.
	if _, ok := h.loadIssueForUser(w, r, run.RootIssueID.String()); !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var userUUID pgtype.UUID
	if err := userUUID.Scan(userID); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid user uuid")
		return
	}

	// Audit row.
	if _, err := h.Queries.CreateSwarmInterrupt(r.Context(), db.CreateSwarmInterruptParams{
		SwarmRunID: runUUID,
		UserID:     userUUID,
		Kind:       req.Kind,
		Payload:    req.Payload,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "create interrupt: "+err.Error())
		return
	}

	// For cancel: flip status to 'aborted' immediately so the user
	// sees the new state without waiting for the orchestrator tick.
	// The orchestrator's tick detects the terminal status and exits
	// via the errTerminalStatus branch (orchestrator.go:171).
	if req.Kind == "cancel" {
		if _, err := h.Queries.SetSwarmRunStatus(r.Context(), db.SetSwarmRunStatusParams{
			ID:     runUUID,
			Status: string(swarmsvc.StatusAborted),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "set aborted: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id":  runUUID.String(),
		"kind":    req.Kind,
		"applied": req.Kind == "cancel", // cancel is sync; others async via tick
	})
}

// GetSwarmRunsByIssue returns the (single) swarm_run for an issue,
// or 404 if none. The root_issue_id column has a UNIQUE index, so
// there is at most one row per issue.
func (h *Handler) GetSwarmRunsByIssue(w http.ResponseWriter, r *http.Request) {
	issueUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}

	// Membership gate.
	if _, ok := h.loadIssueForUser(w, r, issueUUID.String()); !ok {
		return
	}

	run, err := h.Queries.GetSwarmRunByRootIssue(r.Context(), issueUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no swarm run for this issue")
		return
	}
	roles, err := h.Queries.ListSwarmRolesByRun(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list roles: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.swarmRunToResponse(run, roles))
}

// swarmRunToResponse is the JSON converter for a swarm_run row +
// optional role set.
func (h *Handler) swarmRunToResponse(run db.SwarmRun, roles []db.SwarmRole) SwarmRunResponse {
	resp := SwarmRunResponse{
		ID:              run.ID.String(),
		RootIssueID:     run.RootIssueID.String(),
		Status:          run.Status,
		CurrentPhase:    run.CurrentPhase,
		MaxRuntimeHours: run.MaxRuntimeHours,
		Roles:           h.swarmRolesToResponse(roles),
	}
	if run.StartedAt.Valid {
		resp.StartedAt = run.StartedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if run.CompletedAt.Valid {
		resp.CompletedAt = run.CompletedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if run.InterruptedAt.Valid {
		resp.InterruptedAt = run.InterruptedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if run.InterruptReason.Valid {
		resp.InterruptReason = run.InterruptReason.String
	}
	return resp
}

// swarmRolesToResponse converts a []db.SwarmRole to the JSON envelope.
func (h *Handler) swarmRolesToResponse(roles []db.SwarmRole) []SwarmRoleResponse {
	out := make([]SwarmRoleResponse, 0, len(roles))
	for _, role := range roles {
		r := SwarmRoleResponse{
			ID:               role.ID.String(),
			RoleName:         role.RoleName,
			RoleInstructions: role.RoleInstructions,
			Status:           role.Status,
		}
		if role.ParentRoleID.Valid {
			r.ParentRoleID = role.ParentRoleID.String()
		}
		if role.CurrentStep.Valid {
			r.CurrentStep = role.CurrentStep.String
		}
		if role.LastHeartbeatAt.Valid {
			r.LastHeartbeatAt = role.LastHeartbeatAt.Time.Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, r)
	}
	return out
}

// rootIssueUUIDFromString converts a UUID string to pgtype.UUID via
// util.ParseUUID. Returns zero pgtype.UUID on parse error; call sites
// must gate via loadIssueForUser (which validates first).
func rootIssueUUIDFromString(s string) pgtype.UUID {
	u, err := util.ParseUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return u
}