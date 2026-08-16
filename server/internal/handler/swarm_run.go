// Package handler — swarm_run.go (0.5.21).
//
// HTTP surface for the swarm topology feature. Mirrors the
// mythos_supervise.go pattern: thin membership-gated JSON handlers
// over the swarm service + sqlc queries.
//
//   POST   /api/experimental/swarm-topology/runs               — bootstrap
//   POST   /api/experimental/swarm-topology/runs/{id}/interrupt — user action
//   GET    /api/experimental/swarm-topology/runs/{id}/state    — live status
//   GET    /api/experimental/swarm-topology/runs?workspace_id= — past runs (panel)
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
	"strconv"

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
	// 0.5.22 audit fix (P0): IsPaused is the live pause state the
	// frontend reads to switch the SwarmInterruptBar Pause/Resume
	// label (apps/.../swarm-interrupt-bar.tsx:115). Without this
	// field the renderer always sees undefined and the label is
	// permanently wrong after the first pause.
	IsPaused       bool                `json:"is_paused"`
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
	Kind    string          `json:"kind"` // pause/resume/cancel/redirect/inject_message
	Payload json.RawMessage `json:"payload,omitempty"`
}

// swarmService returns the swarm orchestrator service wired into
// the handler at boot. Mirrors mythos_supervise.go:262 — no lazy
// init, no per-handler Service instance. The router.go:736 boot
// path assigns h.SwarmService before HTTP routes register; nil
// here means the service was not wired (older tests / a path that
// built Handler without router.go wiring).
func (h *Handler) swarmService() *swarmsvc.Service {
	return h.SwarmService
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
	//
	// 0.5.22 audit fix (P0): TopologySpec MUST be set explicitly.
	// The sqlc-generated CreateSwarmRun INSERT always sends $5
	// (topology_spec) — Go zero-value []byte(nil) maps to SQL NULL,
	// and migration 241 line 65 declares the column NOT NULL.
	// Without this, every bootstrap returns 23502.
	maxRuntime := req.MaxRuntimeHours
	if maxRuntime <= 0 {
		maxRuntime = 72
	}
	run, err := h.Queries.CreateSwarmRun(r.Context(), db.CreateSwarmRunParams{
		WorkspaceID:     issue.WorkspaceID,
		CreatorUserID:   creatorUUID,
		RootIssueID:     rootIssueUUIDFromString(req.RootIssueID),
		Problem:         req.Problem,
		TopologySpec:    []byte(`{}`),
		MaxRuntimeHours: maxRuntime,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create swarm run: "+err.Error())
		return
	}

	// Lock the resource claim (mirrors install_mythos pattern).
	if err := experimental.Claim(r.Context(), h.Queries, experimental.SourceSwarmTopology, experimental.LockSwarmRun, run.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "claim lock: "+err.Error())
		return
	}

	// FIX 1 (0.5.22): kick the orchestrator goroutine now that the run
	// row exists. install_swarm.go does not exist — PostSwarmRun is the
	// closest caller. StartOrchestrator is idempotent (running-map check
	// + terminal-status short-circuit in orchestrator.go:106-121), so a
	// re-bootstrap that already returned above (line 134-137) does not
	// need this call.
	//
	// 0.5.22 audit fix (P0): h.SwarmService nil-guard. If the boot
	// wiring in router.go hasn't run (older tests / non-default
	// Handler paths), the orchestrator cannot be started — log loud
	// and surface 503 so the client retries on the next boot. Without
	// this guard a nil deref would 500 with a panic the server
	// recovers from, but the run row would already be INSERTed —
	// leaving the workspace in a stuck-preparing state with no
	// orchestrator to advance it.
	svc := h.swarmService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "swarm orchestrator service not wired; please retry")
		return
	}
	if err := svc.StartOrchestrator(r.Context(), run.ID); err != nil {
		slog.Warn("swarm orchestrator start failed",
			"run_id", run.ID.String(), "err", err.Error())
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
		IsPaused:       run.IsPaused,
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
// sees the new state without waiting for the tick, AND drains the
// in-flight agent_task_queue rows owned by the run's role-agents so
// the daemons drop those tasks on their next claim-poll.
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
	case "pause", "resume", "cancel", "redirect", "inject_message":
	default:
		writeError(w, http.StatusBadRequest, "kind must be pause|resume|cancel|redirect|inject_message")
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

		// Sync drain: flip every in-flight agent_task_queue row owned
		// by the run's role-agents to 'cancelled'. The daemons pick
		// this up on their next claim-poll (≤ 5s) and stop dispatch.
		// Active states only — completed/failed/cancelled rows are
		// untouched so the audit trail stays intact.
		//
		// This is best-effort: if the drain fails we still return 202
		// because the run itself is already aborted (orchestrator tick
		// will retry on terminal-status detect). Log loudly so an
		// operator can chase the underlying DB error.
		if err := h.Queries.CancelAgentTasksBySwarmRun(r.Context(), runUUID); err != nil {
			slog.Warn("swarm cancel drain failed",
				"run_id", runUUID.String(), "err", err.Error())
		}
	}

	// For pause / resume: flip swarm_run.is_paused. Unlike cancel this
	// is NOT terminal — the run stays active and the orchestrator's
	// tick reads is_paused at the top, returning early while paused
	// (skips phase advance + task enqueue) until a resume flips it back
	// to false.
	switch req.Kind {
	case "pause":
		if _, err := h.Queries.SetSwarmRunPaused(r.Context(), db.SetSwarmRunPausedParams{
			ID:       runUUID,
			IsPaused: true,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "set paused: "+err.Error())
			return
		}
	case "resume":
		if _, err := h.Queries.SetSwarmRunPaused(r.Context(), db.SetSwarmRunPausedParams{
			ID:       runUUID,
			IsPaused: false,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "set resumed: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id": runUUID.String(),
		"kind":   req.Kind,
		// cancel/pause/resume are sync (state flipped now); redirect +
		// inject_message are async (picked up on the next orchestrator tick).
		"applied": req.Kind == "cancel" || req.Kind == "pause" || req.Kind == "resume",
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

// GetSwarmRunsByWorkspace returns the past swarm_run rows for a
// workspace, ordered newest-first. Powers the PastRunsPanel in the
// desktop swarm-topology view.
//
// 0.5.21 fix: previously this endpoint did not exist, so the panel
// queryFn returned [] and the user saw an empty list regardless of
// state. Mirrors the schema-side pattern at
// migrations/241_swarm_topology.up.sql:idx_swarm_run_by_workspace
// (workspace_id, started_at DESC) — the index covers both the
// filter and the order-by, so the query is O(matches-in-range) not
// O(table-scan).
//
// Membership gate: the X-Workspace-ID header in the experimental
// auth group ensures the caller has access to the workspace; we
// validate the UUID parse only. Like the mythos-supervise sibling
// at mythos_supervise.go:189, we do NOT also call loadIssueForUser
// because there is no issue in this handler's input shape.
func (h *Handler) GetSwarmRunsByWorkspace(w http.ResponseWriter, r *http.Request) {
	wsRaw := r.URL.Query().Get("workspace_id")
	if wsRaw == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	wsUUID, err := util.ParseUUID(wsRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}

	// Sanity-check the workspace exists (cheap, returns 404 cleanly
	// if the X-Workspace-ID header bypassed a typo).
	if _, err := h.Queries.GetWorkspace(r.Context(), wsUUID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Cap at 100 (mirrors autopilot.go::ListAutopilotRuns cap).
	limit := int32(100)
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = int32(v)
		}
	}

	rows, err := h.Queries.ListSwarmRunsByWorkspace(r.Context(), db.ListSwarmRunsByWorkspaceParams{
		WorkspaceID: wsUUID,
		Limit:       limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list swarm runs: "+err.Error())
		return
	}

	// Slim envelope (no role set — that's per-run state, fetched on
	// demand via GetSwarmRunState).
	resp := make([]SwarmRunResponse, 0, len(rows))
	for _, run := range rows {
		resp = append(resp, h.swarmRunToResponse(run, nil))
	}
	writeJSON(w, http.StatusOK, resp)
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