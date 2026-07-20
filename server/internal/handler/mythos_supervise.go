// Package handler — mythos_supervise.go (0.3.31).
//
// HTTP surface for the enhancer-mode supervise loop. The runner
// itself manages the long-lived goroutine via Service.startSupervise;
// this file is the read path (current state) and the manual tick
// trigger the user can fire from the IssueLabsSection "立即检查"
// button.
//
// No LLM dispatch hooks live here — the supervise loop reads
// issue / comment rows via the standard list endpoints and writes
// mythos_run.supervision_state via the same Service the runner
// uses. This keeps the "flag-off bypasses experimental code"
// contract: turning mythos_swarm off causes the gate middleware
// below to return 404, with zero side effects on the running
// supervise goroutines (Service.Stop cancels them at daemon
// shutdown, not at flag toggle).

package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	mythossvc "github.com/multica-ai/multica/server/internal/service/mythos"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MythosSuperviseStateResponse is the JSON envelope for the
// supervision_state column. Mirrors the SupervisionState struct
// in server/internal/service/mythos/supervise.go so the renderer
// can read phase / sub-task progress / latest reflection without
// re-deriving them.
type MythosSuperviseStateResponse struct {
	RunID                string `json:"run_id"`
	Phase                string `json:"phase"`
	StartedAt            string `json:"started_at,omitempty"`
	LastCheckAt          string `json:"last_check_at,omitempty"`
	LastTickDurationMs   int64  `json:"last_tick_duration_ms"`
	TotalTicks           int    `json:"total_ticks"`
	SubTasksTotal        int    `json:"sub_tasks_total"`
	SubTasksDone         int    `json:"sub_tasks_done"`
	LatestReflection     string `json:"latest_reflection,omitempty"`
	LatestReflectionIter int    `json:"latest_reflection_iter,omitempty"`
	AbortReason          string `json:"abort_reason,omitempty"`
}

// GetMythosSuperviseState returns the current supervision_state for
// a given run. The renderer polls this when the IssueLabsSection
// supervise panel is open. Mounted at GET /api/experimental/mythos-swarm/supervise/{runID}.
//
// Gated by RequireExperimentalFlag("mythos_swarm") at the router —
// the function itself does not double-check.
func (h *Handler) GetMythosSuperviseState(w http.ResponseWriter, r *http.Request) {
	runIDStr := chi.URLParam(r, "runID")
	runID, err := utilParseUUID(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	raw, err := h.Queries.GetMythosRunSupervisionState(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "supervision state not found")
		return
	}
	if len(raw) == 0 {
		writeJSON(w, http.StatusOK, MythosSuperviseStateResponse{
			RunID: uuid.UUID(runID.Bytes).String(),
			Phase: "preparing",
		})
		return
	}
	var state struct {
		Phase                string `json:"phase"`
		StartedAt            string `json:"started_at"`
		LastCheckAt          string `json:"last_check_at"`
		LastTickDurationMs   int64  `json:"last_tick_duration_ms"`
		TotalTicks           int    `json:"total_ticks"`
		SubTasksTotal        int    `json:"sub_tasks_total"`
		SubTasksDone         int    `json:"sub_tasks_done"`
		LatestReflection     string `json:"latest_reflection,omitempty"`
		LatestReflectionIter int    `json:"latest_reflection_iter,omitempty"`
		AbortReason          string `json:"abort_reason,omitempty"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		writeError(w, http.StatusInternalServerError, "supervision_state decode failed")
		return
	}
	writeJSON(w, http.StatusOK, MythosSuperviseStateResponse{
		RunID:                uuid.UUID(runID.Bytes).String(),
		Phase:                state.Phase,
		StartedAt:            state.StartedAt,
		LastCheckAt:          state.LastCheckAt,
		LastTickDurationMs:   state.LastTickDurationMs,
		TotalTicks:           state.TotalTicks,
		SubTasksTotal:        state.SubTasksTotal,
		SubTasksDone:         state.SubTasksDone,
		LatestReflection:     state.LatestReflection,
		LatestReflectionIter: state.LatestReflectionIter,
		AbortReason:          state.AbortReason,
	})
}

// PostMythosSuperviseTick triggers an immediate supervise pass.
// Mounted at POST /api/experimental/mythos-swarm/supervise/{runID}/tick.
//
// Semantics: the handler synchronously runs one tick (the same code
// path the 30s ticker uses) and returns the resulting state. This
// is best-effort — a manual tick does NOT bypass the natural
// termination conditions (done/aborted). It only forces an
// immediate re-evaluation.
//
// Side effects: writes supervision_state once. Does not cancel
// the running ticker; the next 30s tick will fire as usual.
func (h *Handler) PostMythosSuperviseTick(w http.ResponseWriter, r *http.Request) {
	runIDStr := chi.URLParam(r, "runID")
	runID, err := utilParseUUID(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	run, err := h.Queries.GetMythosRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Mode != "enhancer" {
		writeError(w, http.StatusBadRequest, "run is not in enhancer mode")
		return
	}
	if run.Status != "supervising" {
		writeError(w, http.StatusConflict,
			"run is not currently supervising (status="+run.Status+")")
		return
	}

	// The Service exposed by Handler is stored as a non-exported
	// field. If the field is missing or nil (older builds), fall
	// back to a no-op 503 so the route is at least discoverable.
	svc := h.mythosService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable,
			"mythos supervise service unavailable in this build")
		return
	}

	state, err := svc.TickSupervisionOnce(r.Context(), run.ID, run.RootIssueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"manual tick failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, MythosSuperviseStateResponse{
		RunID:                uuid.UUID(run.ID.Bytes).String(),
		Phase:                string(state.Phase),
		StartedAt:            state.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		LastCheckAt:          state.LastCheckAt.Format("2006-01-02T15:04:05Z07:00"),
		LastTickDurationMs:   state.LastTickDurationMs,
		TotalTicks:           state.TotalTicks,
		SubTasksTotal:        state.SubTasksTotal,
		SubTasksDone:         state.SubTasksDone,
		LatestReflection:     state.LatestReflection,
		LatestReflectionIter: state.LatestReflectionIter,
		AbortReason:          state.AbortReason,
	})
}

// GetMythosRunsByIssue returns the most recent enhancer-mode mythos
// run rows for a given issue, scoped to the active workspace. The
// renderer uses this to discover the run id for the supervise panel;
// the panel then polls /supervise/{runID} for live state.
//
// Mounted at GET /api/issues/{issueID}/mythos-runs?workspace_id=...
//
// Returns a slice of {run_id} envelopes ordered by started_at DESC
// (LIMIT 5). Empty array means "no run yet" — the panel renders a
// friendly empty state instead of erroring.
func (h *Handler) GetMythosRunsByIssue(w http.ResponseWriter, r *http.Request) {
	issueIDStr := chi.URLParam(r, "issueID")
	issueID, err := utilParseUUID(issueIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	workspaceIDStr := r.URL.Query().Get("workspace_id")
	workspaceID, err := utilParseUUID(workspaceIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}
	rows, err := h.Queries.ListMythosRunsByIssueAndWorkspace(r.Context(),
		db.ListMythosRunsByIssueAndWorkspaceParams{
			RootIssueID: issueID,
			WorkspaceID: workspaceID,
		})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list mythos runs: "+err.Error())
		return
	}
	out := make([]MythosRunSummary, 0, len(rows))
	for _, row := range rows {
		summary := MythosRunSummary{
			RunID:      uuid.UUID(row.ID.Bytes).String(),
			Status:     row.Status,
			Mode:       row.Mode,
			StartedAt:  row.StartedAt.Time.Format(time.RFC3339),
			Problem:    row.Problem,
			Iterations: row.CurrentLoop,
		}
		if row.CompletedAt.Valid {
			summary.CompletedAt = row.CompletedAt.Time.Format(time.RFC3339)
		}
		if row.FinalIssueID.Valid {
			summary.FinalIssueID = uuid.UUID(row.FinalIssueID.Bytes).String()
		}
		if len(row.CodaConclusions) > 0 {
			summary.CodaConclusions = json.RawMessage(row.CodaConclusions)
		}
		out = append(out, summary)
	}
	writeJSON(w, http.StatusOK, out)
}

// MythosRunSummary is the JSON envelope for the runs-by-issue list.
// The supervise panel reads only run_id/status/mode; the 0.3.55
// finished-result fields (problem / iterations / completed_at /
// final_issue_id / coda_conclusions) let the Mythos lab view render a
// COMPLETED run's outcome instead of only the in-session POST /run
// response — pre-0.3.55 a finished run was invisible the moment the
// user left the page. Additive fields; existing consumers ignore them.
type MythosRunSummary struct {
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Mode      string `json:"mode"`
	StartedAt string `json:"started_at"`
	// 0.3.55 finished-result surface.
	Problem         string          `json:"problem"`
	Iterations      int32           `json:"iterations"`
	CompletedAt     string          `json:"completed_at,omitempty"`
	FinalIssueID    string          `json:"final_issue_id,omitempty"`
	CodaConclusions json.RawMessage `json:"coda_conclusions,omitempty"`
}

// utilParseUUID is a tiny shim so this file does not have to import
// the util package just for one function. Mirrors util.ParseUUID's
// "trusted" semantics (panics on invalid bytes via uuid.Parse).
func utilParseUUID(s string) (pgtype.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// mythosService returns the Mythos service stored on the Handler, or
// nil if it has not been wired (older builds / tests). Used by the
// supervise HTTP handlers to call TickSupervisionOnce.
func (h *Handler) mythosService() *mythossvc.Service {
	return h.MythosService
}
