// Package handler — claude_science_run.go (0.5.22)
//
// POST /api/experimental/claude-science/issues/{issueID}/run — manual
// "Run research" trigger for issue-bound labs whose catalog opts out of
// the service-layer auto-dispatch path (currently only claude_science_lab).
// The endpoint is the ONLY path that can start an agent task for that
// lab, so the workbench's "Run research" button gets a single source of
// truth.
//
// Hard rules:
//
//  1. Route is mounted inside the existing
//     `RequireExperimentalFlag("claude_science_lab")` chi group in
//     router.go — same chokepoint as the runtime + forecast + context
//     routes. Off-flag callers see 404, indistinguishable from a
//     nonexistent route.
//  2. The issue MUST have lab_source == "claude_science_lab" AND an
//     assignee of type "agent" — no squad, no member, no empty assignee.
//     Anything else returns 400 (the workbench header keeps the
//     assignee visible; the user can fix it from issue-detail first).
//  3. HasPendingTaskForIssueAndAgent dedups rapid double-clicks at 409;
//     same gate as the comment-trigger path (handler/comment.go:1626).
//  4. Calls TaskService.EnqueueTaskForIssue directly — bypasses the
//     service-layer gates (maybeEnqueueOnAssign + WillEnqueueRun), which
//     is the whole point: the user has manually opted in for this run.

package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RegisterClaudeScienceRunRoute wires the manual-run endpoint onto the
// supplied router. The caller (router.go) MUST gate the entire call on
// `RequireExperimentalFlag("claude_science_lab")` so off-flag clients
// get a uniform 404.
func RegisterClaudeScienceRunRoute(r chi.Router, h *Handler) {
	r.Post("/api/experimental/claude-science/issues/{issueID}/run", h.PostClaudeScienceRun)
}

// PostClaudeScienceRun enqueues an agent_task_queue row for the given
// issue. Returns 202 Accepted with the new task_id on success.
//
// Path params: issueID (UUID)
//
// Headers:    X-Workspace-ID (required for membership check),
//
//	X-User-ID     (set by middleware.Auth)
//
// Responses:
//
//	202 — { task_id }
//	400 — invalid issueID, lab_source ≠ claude_science_lab,
//	      missing / wrong assignee
//	403 — not a workspace member
//	404 — issue not found in workspace
//	409 — pending task already exists for (issue, agent)
//	500 — enqueue failure
func (h *Handler) PostClaudeScienceRun(w http.ResponseWriter, r *http.Request) {
	issueIDStr := chi.URLParam(r, "issueID")
	issueID, ok := parseUUIDOrBadRequest(w, issueIDStr, "issueID")
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, uuidToString(issueID))
	if !ok {
		return
	}

	// 1. Lab source must be claude_science_lab. The endpoint is
	// registered inside the RequireExperimentalFlag("claude_science_lab")
	// group, but a future caller could bind it to a different lab — the
	// explicit check keeps the contract self-contained.
	if !issue.LabSource.Valid || issue.LabSource.String != "claude_science_lab" {
		writeError(w, http.StatusBadRequest, "issue is not bound to claude_science_lab")
		return
	}

	// 2. Assignee must be an agent. The workbench header stays visible
	// for a squad/member assignee (so the user can fix it) but we refuse
	// to start a run for one — the skill contract assumes the research
	// leader.
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" ||
		!issue.AssigneeID.Valid {
		writeError(w, http.StatusBadRequest,
			"issue must have an agent assignee to manually run research")
		return
	}

	// 3. Dedup — match the existing HasPendingTaskForIssueAndAgent path
	// used by the comment trigger (handler/comment.go:1626). Fail-closed
	// on DB error.
	hasPending, dbErr := h.Queries.HasPendingTaskForIssueAndAgent(r.Context(),
		db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: issue.AssigneeID,
		})
	if dbErr != nil {
		slog.Warn("PostClaudeScienceRun: pending check failed",
			"issue_id", uuidToString(issue.ID), "error", dbErr)
		writeError(w, http.StatusInternalServerError, "failed to check pending tasks")
		return
	}
	if hasPending {
		writeError(w, http.StatusConflict,
			"a task is already in flight for this issue")
		return
	}

	// 4. Enqueue. Direct call — bypasses the service-layer auto-dispatch
	// gates because this endpoint IS the manual opt-in.
	task, enqErr := h.TaskService.EnqueueTaskForIssue(r.Context(), issue, pgtype.UUID{})
	if enqErr != nil {
		slog.Warn("PostClaudeScienceRun: enqueue failed",
			"issue_id", uuidToString(issue.ID), "error", enqErr)
		writeError(w, http.StatusInternalServerError, "failed to enqueue research task")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id": uuidToString(task.ID),
	})
}
