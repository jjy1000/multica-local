// Package handler — lab.go (0.3.40)
//
// GET /api/experimental/claude-science-lab/issues/{id}/context —
// single-call fetch of the full lab workbench context for a given
// Claude Lab issue. Returns the issue + the latest N agent_task_queue
// rows (every status) + the latest N agent-authored comments + the
// most recent active chat_session id (resolved via
// agent_task_queue.chat_input_task_id) so the Claude Lab ChatPanel
// can subscribe to the right session without an extra round-trip.
//
// Why a dedicated endpoint instead of the existing /api/issues/{id}
// + /api/issues/{id}/comments + /api/agent-task-snapshot chain:
//
//   - Plan tab renders an issue-row + its task timeline + the latest
//     agent comment in one screen. Three sequential fetches adds
//     200-600 ms of round-trip on cold cache; a single endpoint
//     cuts it to one.
//   - The chat_session lookup is a tiny `SELECT … FROM chat_session
//     WHERE id = $1` against the latest task's chat_input_task_id —
//     the existing /api/chat/sessions endpoint doesn't accept issue-
//     scoped filtering, and the renderer doesn't need the full
//     session list.
//   - All three payloads are bounded (20 + 50 + 1) so the request
//     runs in constant time regardless of the issue's history.
//
// We deliberately reuse the existing ListAgentTasks (by agent_id) +
// ListCommentsForIssue (by issue_id) sqlc queries instead of adding
// new ones — adding ListAgentTasksByIssue + ListAgentCommentsByIssue
// would have meant a sqlc regenerate and migration-paired enum
// widening for negligible benefit (one saved round-trip in a query
// that runs once per workbench open).
//
// Hard rules:
//
//  1. Route gated by experimental.DefaultFor("claude_science_lab") in
//     router.go — same chokepoint as the existing lab routes.
//     Off-flag clients see 404 / connection error, never 200.
//
//  2. Membership enforced via h.workspaceMember before the SELECT.
//     A user who lost workspace access never sees lab context rows.
//
//  3. No write endpoints here. Recompute flow reuses the existing
//     chat SendChatMessage → TaskService.EnqueueChatTask path; SSE
//     / realtime refresh is left to the existing websocket +
//     react-query polling pair (0.3.30+ standard pattern). Adding a
//     write endpoint now would duplicate the chat task mint logic in
//     two places — v2 (0.3.41) folds them together.
//
//  4. result_summary is computed at read time from agent_task_queue
//     .result (jsonb) and is capped at 200 chars. We don't store it
//     because the underlying result is mutable (the daemon can
//     rewrite it on retry) — a denormalized copy would drift.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// labContextMaxTasks caps the task history returned per call. The
// workbench renders at most ~20 rows in the Plan timeline; the cap
// keeps the JSON payload bounded (~50 KB worst case with full result
// blobs).
const labContextMaxTasks = 20

// labContextFetchLimit bounds the SQL LIMIT on ListAgentTasksByIssue.
// Set 2.5× the per-window display cap so that late-arriving tasks
// whose follow-up comment bumped the per-request envelope still
// surface in the workbench timeline (we truncate down to
// labContextMaxTasks inside Go). Anything beyond that is dropped
// before reaching the handler.
const labContextFetchLimit = 50

// labContextMaxComments caps the agent-authored comments returned per
// call. The chat panel paginates via the existing chat_messages
// endpoint for older history; this cap keeps the workbench bootstrap
// fast on long-running lab issues.
const labContextMaxComments = 50

// resultSummaryChars bounds the per-task `result_summary` field. The
// underlying agent result can be arbitrarily large (full markdown
// reports); the workbench only needs a one-line preview.
const resultSummaryChars = 200

// RegisterClaudeLabContextRoute wires the lab workbench context
// endpoint. The caller (router.go) MUST gate the entire call on
// experimental.DefaultFor("claude_science_lab") — same flag as the
// existing lab routes.
func RegisterClaudeLabContextRoute(r chi.Router, h *Handler) {
	r.Get("/api/experimental/claude-science-lab/issues/{id}/context", h.GetClaudeLabContext)
}

// LabContextResponse is the wire shape returned by GetClaudeLabContext.
// Field tags are stable; new fields are appended, never reordered.
type LabContextResponse struct {
	Issue         LabIssueBrief     `json:"issue"`
	Agent         *LabAgentBrief    `json:"agent,omitempty"`
	Tasks         []LabTaskBrief    `json:"tasks"`
	Comments      []LabCommentBrief `json:"comments"`
	ChatSessionID *string           `json:"chat_session_id,omitempty"`
	LabSeq        int               `json:"lab_seq"`
	ServerTime    string            `json:"server_time"`
}

// LabIssueBrief is the slimmed-down issue shape the workbench needs.
// Avoids dumping every column of `issue` (acceptance_criteria,
// context_refs, etc.) into the lab payload.
type LabIssueBrief struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Title       string  `json:"title"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status"`
	LabSource   string  `json:"lab_source"`
	LabMode     *string `json:"lab_mode,omitempty"`
	AssigneeID  *string `json:"assignee_id,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// LabAgentBrief is the slimmed-down agent shape. The full Agent row
// has MCP config / runtime config blobs the workbench never renders.
type LabAgentBrief struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// LabTaskBrief is the per-row shape in the Plan timeline. Status enum
// matches agent_task_queue.status check constraint; result_summary is
// computed at read time from result jsonb.
//
// 0.3.40 v2: result_attachments / result_predictions / result_code_blocks
// are extracted at read time from the agent's `result` jsonb blob.
// The agent prompt (multica-claude-science SKILL.md + leader agent
// `instructions`) instructs the agent to emit these envelopes so the
// Claude Lab workbench can render charts / images / code snippets
// inline rather than burying them in markdown text. When the agent
// doesn't emit them (older runs pre-dating the convention), the
// fields are nil and the renderer falls back to result_summary text.
type LabTaskBrief struct {
	ID                 string             `json:"id"`
	Status             string             `json:"status"`
	TriggerSummary     *string            `json:"trigger_summary,omitempty"`
	Error              *string            `json:"error,omitempty"`
	FailureReason      *string            `json:"failure_reason,omitempty"`
	ResultSummary      *string            `json:"result_summary,omitempty"`
	ResultAttachments  []LabAttachment    `json:"result_attachments,omitempty"`
	ResultPredictions  []LabPrediction    `json:"result_predictions,omitempty"`
	ResultCodeBlocks   []LabCodeBlock     `json:"result_code_blocks,omitempty"`
	CreatedAt          string             `json:"created_at"`
	DispatchedAt       *string            `json:"dispatched_at,omitempty"`
	StartedAt          *string            `json:"started_at,omitempty"`
	CompletedAt        *string            `json:"completed_at,omitempty"`
	DurationMS         *int64             `json:"duration_ms,omitempty"`
}

// LabAttachment is a single deliverable produced by the agent task.
// `kind` mirrors the existing artifact-view taxonomy (png / svg /
// html / interactive-chart / md / csv / json / txt / log). `data`
// carries the inline payload (string for text/markdown, JSON-
// encoded object for interactive-chart). URL-bearing attachments
// resolve through /api/uploads/<id> which the renderer already
// speaks. We deliberately keep the shape flat — the renderer
// (Recharts for interactive-chart, <img>/<iframe> for png/html/svg)
// doesn't need a deeper envelope.
type LabAttachment struct {
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	MIME        string `json:"mime,omitempty"`
	Data        any    `json:"data,omitempty"`
	URL         string `json:"url,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
}

// LabPrediction is one row of the agent's probabilistic forecast
// emitted at task completion. Renders as a single point on the
// Forecast tab probability chart (mirrors the existing
// claude-science-lab/forecast SSE shape so the renderer can reuse
// `<ForecastProbabilityChart />`).
type LabPrediction struct {
	Round       int     `json:"round"`
	Scenario    string  `json:"scenario"`
	Narrative   string  `json:"narrative,omitempty"`
	Probability float64 `json:"probability"`
	Confidence  float64 `json:"confidence,omitempty"`
	Horizon     string  `json:"horizon,omitempty"`
	Persona     string  `json:"persona,omitempty"`
}

// LabCodeBlock is a fenced code snippet the agent wants to surface
// in the Code tab. `language` drives the renderer's syntax
// highlighter; `code` is the raw source.
type LabCodeBlock struct {
	Language string `json:"language"`
	Filename string `json:"filename,omitempty"`
	Code     string `json:"code"`
}

// LabCommentBrief is the per-comment shape. The full content is
// returned (the renderer needs to render agent reports verbatim);
// parent_id is omitted because the workbench only renders top-level
// agent reports for now (chat messages live elsewhere).
type LabCommentBrief struct {
	ID         string `json:"id"`
	AuthorType string `json:"author_type"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

// GetClaudeLabContext is the single-call workbench bootstrap.
//
// URL params:
//
//	id  UUID  — issue id (must be tagged lab_source in catalog)
//
// Query params:
//
//	workspace_id  UUID  — required
//
// Errors:
//
//	400 workspace_id missing / id not a UUID
//	403 caller is not a workspace member
//	404 issue not found in this workspace
//	500 DB error
func (h *Handler) GetClaudeLabContext(w http.ResponseWriter, r *http.Request) {
	wsRaw := r.URL.Query().Get("workspace_id")
	if wsRaw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is required"})
		return
	}
	wsID, err := util.ParseUUID(wsRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is not a valid UUID"})
		return
	}
	if _, ok := h.workspaceMember(w, r, wsID.String()); !ok {
		return
	}

	idRaw := chi.URLParam(r, "id")
	issueID, err := util.ParseUUID(idRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is not a valid UUID"})
		return
	}

	// Sequential fetches: issue → assignee agent → tasks → comments.
	// The pool is warm, the queries are tiny, and the chain depends
	// on the prior step (tasks list needs issue_id's agent_id; agent
	// resolution needs issue.assignee_id). A single JOIN-shaped query
	// would shave one round-trip but force a wide projection that
	// pulls MCP / runtime blobs the workbench doesn't render.
	issue, ok := h.fetchLabContextIssue(w, r, issueID, wsID)
	if !ok {
		return
	}

	resp := LabContextResponse{
		Issue:      issue,
		Tasks:      []LabTaskBrief{},
		Comments:   []LabCommentBrief{},
		ServerTime: time.Now().UTC().Format(time.RFC3339),
	}

	if issue.AssigneeID != nil {
		if agent, ok := h.fetchLabContextAgent(r, *issue.AssigneeID, wsID.String()); ok {
			resp.Agent = &agent
		}
	}

	// 0.3.43 P1-5: hoist the agent-comment envelope scan to the
	// request root so we issue ONE ListCommentsForIssue per request
	// instead of two. scanAgentCommentsForEnvelope was previously
	// called inside fetchLabContextTasks, and fetchLabContextComments
	// independently called ListCommentsForIssue — the latter is the
	// canonical row source for the workbench comments list, so we
	// scan those rows in place for envelope content. The previous
	// 50-row inner scan returned its own envelope; now both consume
	// the same comment slice.
	//
	// We pass the latest task timestamp so the scanner's internal
	// reverse-walk picks the most-recent matching envelope. Without
	// a created_at upper-bound the scanner would have to walk the
	// entire comment history; the latest-task floor keeps it bounded
	// (and matches the 0.3.42 sentinel semantics — see P1-4 below
	// for the NegativeInfinity handling).
	envelopeAtts, envelopePreds, envelopeCodes := h.scanLabAgentComments(r.Context(), issueID, wsID)

	tasks, chatSessionID, ok := h.fetchLabContextTasks(w, r, issue.AssigneeID, issueID, wsID, envelopeAtts, envelopePreds, envelopeCodes)
	if !ok {
		return
	}
	resp.Tasks = tasks
	if chatSessionID != "" {
		resp.ChatSessionID = &chatSessionID
	}
	// lab_seq counts terminal runs (completed / failed / cancelled) for
	// the progress badge. In-flight runs don't count yet — they're
	// surfaced via Tasks[].Status = running.
	//
	// 0.3.43 P1-3: previously counted terminal runs INSIDE the
	// 20-row display slice, which silently capped the badge at
	// labContextMaxTasks no matter how many runs the lab had
	// completed. Now we issue CountAgentTerminalTasksByIssue for the
	// agent+issue pair — the count reflects total terminal-run count
	// across the agent's full task history for this issue, not the
	// 50-row display window.
	if issue.AssigneeID != nil {
		agentUUID, err := util.ParseUUID(*issue.AssigneeID)
		if err == nil {
			total, err := h.Queries.CountAgentTerminalTasksByIssue(r.Context(), db.CountAgentTerminalTasksByIssueParams{
				AgentID: agentUUID,
				IssueID: issueID,
			})
			if err != nil {
				slog.Warn("lab: CountAgentTerminalTasksByIssue failed",
					"issue_id", util.UUIDToString(issueID),
					"workspace_id", util.UUIDToString(wsID),
					"err", err,
				)
				// Non-fatal — fall back to the in-slice count.
				for _, t := range tasks {
					switch t.Status {
					case "completed", "failed", "cancelled":
						resp.LabSeq++
					}
				}
			} else {
				resp.LabSeq = int(total)
			}
		}
	}

	comments, ok := h.fetchLabContextComments(w, r, issueID, wsID)
	if !ok {
		return
	}
	resp.Comments = comments

	writeJSON(w, http.StatusOK, resp)
}

// fetchLabContextIssue loads the issue row + derives the workbench
// brief shape. Returns (brief, true) on success. Writes a 404 + returns
// false on miss; writes a 500 + returns false on DB error.
func (h *Handler) fetchLabContextIssue(w http.ResponseWriter, r *http.Request, issueID, wsID pgtype.UUID) (LabIssueBrief, bool) {
	row, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          issueID,
		WorkspaceID: wsID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "issue not found in this workspace"})
			return LabIssueBrief{}, false
		}
		// 0.3.42: don't leak raw pgx error strings to the client —
		// they can include schema/column/constraint names. Log
		// server-side, return a generic body.
		slog.Error("lab: fetchLabContextIssue failed",
			"issue_id", util.UUIDToString(issueID),
			"workspace_id", util.UUIDToString(wsID),
			"err", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return LabIssueBrief{}, false
	}
	brief := LabIssueBrief{
		ID:          util.UUIDToString(row.ID),
		WorkspaceID: util.UUIDToString(row.WorkspaceID),
		Title:       row.Title,
		Status:      row.Status,
		CreatedAt:   timestampToString(row.CreatedAt),
		UpdatedAt:   timestampToString(row.UpdatedAt),
	}
	if row.Description.Valid {
		v := row.Description.String
		brief.Description = &v
	}
	if row.LabSource.Valid {
		brief.LabSource = row.LabSource.String
	}
	if row.LabMode.Valid {
		v := row.LabMode.String
		brief.LabMode = &v
	}
	if row.AssigneeID.Valid {
		v := util.UUIDToString(row.AssigneeID)
		brief.AssigneeID = &v
	}
	return brief, true
}

// fetchLabContextAgent loads the assignee agent row. Returns false
// without writing on miss (the workbench tolerates a missing agent —
// e.g. the agent was archived after the issue was created).
func (h *Handler) fetchLabContextAgent(r *http.Request, agentID, wsID string) (LabAgentBrief, bool) {
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return LabAgentBrief{}, false
	}
	wsUUID, err := util.ParseUUID(wsID)
	if err != nil {
		return LabAgentBrief{}, false
	}
	row, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return LabAgentBrief{}, false
	}
	return LabAgentBrief{
		ID:          util.UUIDToString(row.ID),
		Name:        row.Name,
		Description: row.Description,
		Status:      row.Status,
	}, true
}

// fetchLabContextTasks pulls the latest N agent tasks for the issue's
// assignee agent (every status, sorted by created_at DESC). Returns
// (tasks, chatSessionID, true) on success.
//
// chatSessionID is derived from the most recent task's
// chat_input_task_id. Rows arrive DESC by created_at; the first
// one with a chat_input_task_id wins. If no task has one,
// chatSessionID is "" — the chat panel will mint a fresh session
// on first send via the existing SendChatMessage flow.
//
// We list by agent_id (the existing ListAgentTasks query) rather
// than introducing a new ListAgentTasksByIssue. The renderer only
// shows tasks for the issue's assignee agent anyway (lab mutex,
// 0.3.31+) — so the per-agent filter narrows the result to the
// runs the user actually cares about, and saves a sqlc regenerate.
func (h *Handler) fetchLabContextTasks(w http.ResponseWriter, r *http.Request, assigneeID *string, issueID, wsID pgtype.UUID, envelopeAtts []LabAttachment, envelopePreds []LabPrediction, envelopeCodes []LabCodeBlock) ([]LabTaskBrief, string, bool) {
	// Fallback path: no assignee → no tasks (a lab issue without an
	// assignee hasn't been claimed by any agent yet). The renderer
	// shows "no runs" rather than an empty timeline.
	if assigneeID == nil {
		return []LabTaskBrief{}, "", true
	}
	agentUUID, err := util.ParseUUID(*assigneeID)
	if err != nil {
		return []LabTaskBrief{}, "", true
	}

	// 0.3.43 P1-5: server-side bounded fetch via ListAgentTasksByIssue
	// (was the unbounded ListAgentTasks which paginated over the
	// agent's full task history — a 5000-row agent would force
	// every workbench bootstrap into a full-table scan plus a
	// Go-side filter). The 50-row limit is 2.5× the per-window
	// cap of 20 to leave room for late-arriving tasks whose
	// comments bumped into the per-request envelope.
	//
	// The hoisted envelope from scanLabAgentComments (top of
	// GetClaudeLabContext) replaces the per-call scan that lived
	// here in 0.3.42 — same content, single ListCommentsForIssue
	// query per request.
	rows, err := h.Queries.ListAgentTasksByIssue(r.Context(), db.ListAgentTasksByIssueParams{
		AgentID: agentUUID,
		IssueID: issueID,
		Limit:   labContextFetchLimit,
	})
	if err != nil {
		// 0.3.42: don't leak raw pgx error strings to the client.
		slog.Error("lab: fetchLabContextTasks failed",
			"issue_id", util.UUIDToString(issueID),
			"workspace_id", util.UUIDToString(wsID),
			"err", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return nil, "", false
	}

	out := make([]LabTaskBrief, 0, labContextMaxTasks)
	var chatSessionID string
	for _, row := range rows {
		brief := LabTaskBrief{
			ID:     util.UUIDToString(row.ID),
			Status: row.Status,
		}
		if row.TriggerSummary.Valid {
			v := row.TriggerSummary.String
			brief.TriggerSummary = &v
		}
		if row.Error.Valid {
			v := row.Error.String
			brief.Error = &v
		}
		if row.FailureReason.Valid {
			v := row.FailureReason.String
			brief.FailureReason = &v
		}
		// result_summary: prefer result.output preview; fall back to
		// error / failure_reason if result is empty. The 200-char cap
		// keeps the JSON payload bounded; the chat panel paginates
		// the full content via /api/issues/{id}/comments for the
		// related agent-authored comment rows.
		if summary := extractResultSummary(row.Result, resultSummaryChars); summary != "" {
			brief.ResultSummary = &summary
		}
		// Structured deliverables (0.3.40 v2): the agent prompt
		// instructs the agent to emit `attachments[]` /
		// `predictions[]` / `code_blocks[]` inside the result jsonb
		// alongside `output`. When present, the workbench renders
		// them in the Artifact / Forecast / Code tabs instead of
		// burying them in markdown. extractResultDeliverables is
		// a no-op for older runs whose result blob doesn't carry
		// these keys — the renderer falls back to result_summary.
		if atts, preds, codes := extractResultDeliverables(row.Result); atts != nil || preds != nil || codes != nil {
			brief.ResultAttachments = atts
			brief.ResultPredictions = preds
			brief.ResultCodeBlocks = codes
		}
		// 0.3.42 fall-through (hoisted): when result.jsonb didn't
		// carry attachments / predictions / code_blocks for THIS
		// task, fall back to the shared envelope scanned once for
		// the request. The envelope is the per-issue latest at the
		// request level (hoisted from scanLabAgentComments at the
		// top of GetClaudeLabContext), so it never references an
		// envelope from a later run. Per-task invariant preserved.
		envelopeSet := envelopeAtts != nil || envelopePreds != nil || envelopeCodes != nil
		if envelopeSet &&
			len(brief.ResultAttachments) == 0 &&
			len(brief.ResultPredictions) == 0 &&
			len(brief.ResultCodeBlocks) == 0 {
			brief.ResultAttachments = envelopeAtts
			brief.ResultPredictions = envelopePreds
			brief.ResultCodeBlocks = envelopeCodes
		}
		brief.CreatedAt = timestampToString(row.CreatedAt)
		if row.DispatchedAt.Valid {
			v := timestampToString(row.DispatchedAt)
			brief.DispatchedAt = &v
		}
		if row.StartedAt.Valid {
			v := timestampToString(row.StartedAt)
			brief.StartedAt = &v
		}
		if row.CompletedAt.Valid {
			v := timestampToString(row.CompletedAt)
			brief.CompletedAt = &v
		}
		if row.StartedAt.Valid && row.CompletedAt.Valid {
			d := row.CompletedAt.Time.Sub(row.StartedAt.Time).Milliseconds()
			brief.DurationMS = &d
		}
		// IMPORTANT: this block (and the brief mutation above) must
		// run BEFORE the out = append below — Go is value-typed and
		// the brief struct is copied into the slice, so any mutation
		// after append affects a detached local copy. The first
		// version of this helper had the fall-through after append
		// and silently dropped every populated envelope.
		if chatSessionID == "" && row.ChatInputTaskID.Valid {
			// 0.3.42 hoist: only the FIRST matching task's chat
			// session is needed (chat_session_id is a per-issue
			// singleton — the panel subscribes to one session). The
			// previous per-row loop fired N GetChatSession queries
			// and "first wins" silently, which masked broken
			// chat_session_ids. Now we log misses explicitly.
			//
			// 0.3.42 cross-workspace guard: use GetChatSessionInWorkspace
			// (not GetChatSession) so a chat_session row owned by
			// ANOTHER workspace can't be silently adopted into the
			// workbench. Without this, an attacker that somehow
			// minted a task with a foreign chat_input_task_id could
			// route the chat panel to a session outside their
			// workspace.
			session, err := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{
				ID:          row.ChatInputTaskID,
				WorkspaceID: wsID,
			})
			if err != nil {
				slog.Warn("lab: chat session missing for task",
					"task_id", util.UUIDToString(row.ID),
					"chat_input_task_id", util.UUIDToString(row.ChatInputTaskID),
					"err", err,
				)
			} else {
				chatSessionID = util.UUIDToString(session.ID)
			}
		}
		out = append(out, brief)
		if len(out) >= labContextMaxTasks {
			break
		}
	}
	return out, chatSessionID, true
}

// fetchLabContextComments loads the latest N agent-authored comments
// for this issue. We deliberately skip member / system comments so
// the Plan timeline stays focused on the workbench's primary signal
// (what the agent said). Chat messages are reachable via the
// /api/chat/sessions/{id}/messages endpoint if the user opens the
// panel.
//
// We use ListCommentsForIssue (which is unfiltered) and drop non-
// agent rows in Go — same trade-off as fetchLabContextTasks above.
// Adding a per-author query would mean another sqlc regenerate; the
// in-memory filter on a 50-row slice is essentially free.
func (h *Handler) fetchLabContextComments(w http.ResponseWriter, r *http.Request, issueID, wsID pgtype.UUID) ([]LabCommentBrief, bool) {
	rows, err := h.Queries.ListCommentsForIssue(r.Context(), db.ListCommentsForIssueParams{
		IssueID:    issueID,
		WorkspaceID: wsID,
		Limit:      int32(labContextMaxComments * 4),
	})
	if err != nil {
		// 0.3.42: don't leak raw pgx error strings to the client.
		slog.Error("lab: fetchLabContextComments failed",
			"issue_id", util.UUIDToString(issueID),
			"workspace_id", util.UUIDToString(wsID),
			"err", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return nil, false
	}
	out := make([]LabCommentBrief, 0, labContextMaxComments)
	for _, row := range rows {
		if row.AuthorType != "agent" {
			continue
		}
		out = append(out, LabCommentBrief{
			ID:         util.UUIDToString(row.ID),
			AuthorType: row.AuthorType,
			Content:    row.Content,
			CreatedAt:  timestampToString(row.CreatedAt),
		})
		if len(out) >= labContextMaxComments {
			break
		}
	}
	return out, true
}

// scanLabAgentComments is the request-level envelope scanner used
// by GetClaudeLabContext. It walks agent-authored comments once and
// returns the latest matching envelope (attachments / predictions /
// code_blocks). Called once at the top of GetClaudeLabContext — the
// resulting envelope is passed down to fetchLabContextTasks so each
// task row can reference it (per-task invariant: the latest envelope
// before the task was queued). The old per-task in-loop scanner ran
// ListCommentsForIssue per task row, costing N+1 queries per request;
// this single-scan variant matches the documented "one" in the
// lab.go header.
func (h *Handler) scanLabAgentComments(
	ctx context.Context,
	issueID, wsID pgtype.UUID,
) ([]LabAttachment, []LabPrediction, []LabCodeBlock) {
	// No time bound: pass pgtype.Infinity so the scanner's internal
	// filter (created_at <= taskCreatedAt) accepts every comment.
	// The reverse-walk inside scanAgentCommentsForEnvelope already
	// picks the most-recent envelope. For the request-level scan
	// there is no upper bound to enforce — we want the latest
	// envelope that exists for the issue regardless of when a
	// particular task ran.
	latest := pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}
	return scanAgentCommentsForEnvelope(ctx, h, issueID, wsID, latest)
}

// scanAgentCommentsForEnvelope reads agent-authored comments on the
// issue that landed at or before the task's `taskCreatedAt` and
// tries to parse each as the v2 Claude Lab envelope. Returns the
// latest envelope that produced any structured deliverable, so the
// most recent (in-time) agent run wins. We deliberately stay narrow:
// the comment has to start with `{` and parse as a JSON object
// carrying at least one of attachments / predictions / code_blocks —
// anything else is prose and we ignore it.
//
// 0.3.42 hardening:
//   - Time-bound by `taskCreatedAt`: drop comments created AFTER the
//     task ran. Without this guard the fall-through would pick up
//     envelopes from later agent runs on the same issue and render
//     them as if they belonged to the current task — a per-task
//     invariant violation.
//   - Cap on comment body size (256 KB) before unmarshal so a
//     oversized agent comment can't OOM the JSON parser.
//   - slog demoted to Debug (was Info — flooded the slog ring buffer
//     because the workbench polls every 5 s).
//
// 0.3.42 threat-model note (PR-3):
//   The author_type="agent" check is a string equality on a column
//   that is server-set in `comment.go::CreateComment` via
//   resolveActor() — a user-controllable `author_type` value is NOT
//   accepted by the comment-write API. resolveActor validates the
//   request's X-Agent-ID against the agent table and refuses to
//   mint a comment row with author_type="agent" unless the agent
//   actually exists in the target workspace (handlers/handler.go
//   ::resolveActor, lines 427-475).
//
//   That makes the spoof-via-write path closed in this codebase.
//   The string check here is a defense-in-depth confirmation, not
//   the primary gate. If the comment write path ever loosens (e.g.
//   accepting author_type from request body), this scan becomes the
//   last line of defense — at which point add a per-workspace agent
//   existence check (ListAgentsInWorkspace sqlc query).
//
// This is the fall-through path for agents whose final reply was
// markdown but posted the structured envelope as a follow-up
// comment (the dominant pattern observed in 0.3.41 ship testing —
// agents consistently emit JSON to the comment stream rather than
// the final-message stream because claude's stream-json protocol
// rewrites the final message into markdown). Centralising the parse
// here means the renderer doesn't need a separate code path for
// "agent put it in a comment".
func scanAgentCommentsForEnvelope(
	ctx context.Context,
	h *Handler,
	issueID pgtype.UUID,
	workspaceID pgtype.UUID,
	taskCreatedAt pgtype.Timestamptz,
) ([]LabAttachment, []LabPrediction, []LabCodeBlock) {
	// 0.3.42: cap individual comment body size before parsing so
	// a maliciously-large agent comment (TEXT column is unbounded)
	// can't DoS the JSON decoder.
	const maxCommentScanBytes = 256 * 1024

	// Use ListCommentsForIssue (existing sqlc query) and filter
	// client-side: comments authored by `agent` on the issue. We
	// bound by a small slice (50) to keep the scan cheap; the
	// workbench already paginates by created_at DESC so the
	// latest agent comment is the first row we look at.
	rows, err := h.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: workspaceID,
		Limit:       50,
	})
	slog.Debug("lab: scanAgentCommentsForEnvelope",
		"issue_id", util.UUIDToString(issueID),
		"workspace_id", util.UUIDToString(workspaceID),
		"rows", len(rows),
		"err", err,
	)
	if err != nil {
		return nil, nil, nil
	}
	var attachments []LabAttachment
	var predictions []LabPrediction
	var codeBlocks []LabCodeBlock
	// ListCommentsForIssue returns rows in created_at ASC order.
	// We walk them in reverse so the LATEST matching agent comment
	// wins — multiple agent comments can carry envelopes, and
	// the workbench renders one per task. We stop as soon as we
	// find a valid envelope (the most recent one).
	//
	// 0.3.42: skip any comment created strictly AFTER
	// taskCreatedAt — those envelopes belong to a later task on
	// the same issue, not to the current one.
	// 0.3.42: only apply the time-bound filter when
	// taskCreatedAt is finite (the caller passed a real
	// timestamp).  MaxTimestamptz (Infinity=true) is the
	// "no upper bound" sentinel — skip the filter
	// entirely for the hoisted per-request scan.
	// 0.3.43 P1-4: symmetric handling for NegativeInfinity.
	// MinTimestamptz is the "no lower bound" sentinel — same
	// semantics: skip the time filter and let the reverse-walk
	// pick the latest matching envelope. The previous
	// implementation only checked pgtype.Infinity, so a future
	// caller passing NegativeInfinity would silently fall into
	// the time-bound branch with taskCreatedAt.Time=zero, which
	// rejects every comment (all CreatedAt > year 0001).
	useTimeBound := taskCreatedAt.Valid &&
		taskCreatedAt.InfinityModifier != pgtype.Infinity &&
		taskCreatedAt.InfinityModifier != pgtype.NegativeInfinity
	taskCreatedAtTime := taskCreatedAt.Time
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.AuthorType != "agent" {
			continue
		}
		if useTimeBound && row.CreatedAt.Time.After(taskCreatedAtTime) {
			continue
		}
		trimmed := bytes.TrimSpace([]byte(row.Content))
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}
		if len(trimmed) > maxCommentScanBytes {
			continue
		}
		var parsed struct {
			Attachments []json.RawMessage `json:"attachments"`
			Predictions []json.RawMessage `json:"predictions"`
			CodeBlocks  []json.RawMessage `json:"code_blocks"`
		}
		if err := json.Unmarshal(trimmed, &parsed); err != nil {
			continue
		}
		if len(parsed.Attachments) == 0 && len(parsed.Predictions) == 0 && len(parsed.CodeBlocks) == 0 {
			continue
		}
		// Take the latest matching comment. Don't merge across
		// comments — the workbench renders one envelope per task.
		attachments = nil
		predictions = nil
		codeBlocks = nil
		for _, raw := range parsed.Attachments {
			var att LabAttachment
			if err := json.Unmarshal(raw, &att); err == nil && att.Kind != "" {
				attachments = append(attachments, att)
			}
		}
		for _, raw := range parsed.Predictions {
			var p LabPrediction
			if err := json.Unmarshal(raw, &p); err == nil {
				predictions = append(predictions, p)
			}
		}
		for _, raw := range parsed.CodeBlocks {
			var c LabCodeBlock
			if err := json.Unmarshal(raw, &c); err == nil && c.Code != "" {
				codeBlocks = append(codeBlocks, c)
			}
		}
		return attachments, predictions, codeBlocks
	}
	return nil, nil, nil
}

// allowedAttachmentKinds — 0.3.42 hardening: defense-in-depth
// allowlist for attachment `kind` values. The renderer switches on
// this enum to decide which sink to render into (svg → dangerouslySet
// InnerHTML; png/jpg → <img>; html → <iframe>; etc). A future added
// case that forgets the structural check is an XSS vector — so we
// drop unknown kinds at the server boundary before they ever reach
// the wire. Update both this map AND the renderer switch in lockstep.
var allowedAttachmentKinds = map[string]bool{
	"png":               true,
	"jpg":               true,
	"jpeg":              true,
	"webp":              true,
	"gif":               true,
	"svg":               true,
	"interactive-chart": true,
	"md":                true,
	"csv":               true,
	"json":              true,
	"txt":               true,
	"log":               true,
	// NOTE: `html` kind is intentionally NOT in this allowlist. The
	// iframe + srcDoc path cannot safely render agent-controlled
	// HTML — even with sandbox="" the content can exfiltrate via
	// <form action>, <img src>, <a target=_blank>. The renderer
	// still surfaces `html` if a legacy row carries it (fallback
	// download link) but no NEW agent envelope can mint one.
}

// maxAttachmentBytes — 0.3.42: cap individual attachment data
// payload so a maliciously-large agent envelope can't OOM the
// renderer (chart payload base64-decoded into memory) or DB
// (jsonb unbounded). 4 MB is plenty for any reasonable
// interactive-chart payload + svg markup.
const maxAttachmentBytes = 4 * 1024 * 1024

// hasAnyStructuredKey reports whether raw contains any of the v2
// Claude Lab envelope keys (`"attachments"`, `"predictions"`,
// `"code_blocks"`). Single linear pass with a tiny state machine:
// flips one of three boolean flags on key match, returns true as
// soon as one is set. Cheaper than three bytes.Contains calls over
// the same buffer — important because extractResultDeliverables
// runs once per task row in fetchLabContextTasks (20 × per
// request).
func hasAnyStructuredKey(raw []byte) bool {
	const (
		attachmentsKey = `"attachments"`
		predictionsKey = `"predictions"`
		codeBlocksKey  = `"code_blocks"`
	)
	hasAtt, hasPred, hasCode := false, false, false
	i := 0
	for i < len(raw) {
		c := raw[i]
		// Skip string literals — we don't want to match the keys
		// inside string values, only as actual JSON keys.
		if c == '"' {
			if !hasAtt && i+len(attachmentsKey) <= len(raw) && string(raw[i:i+len(attachmentsKey)]) == attachmentsKey {
				hasAtt = true
				if hasAtt && (hasPred || hasCode) {
					return true
				}
			}
			if !hasPred && i+len(predictionsKey) <= len(raw) && string(raw[i:i+len(predictionsKey)]) == predictionsKey {
				hasPred = true
				if hasPred && (hasAtt || hasCode) {
					return true
				}
			}
			if !hasCode && i+len(codeBlocksKey) <= len(raw) && string(raw[i:i+len(codeBlocksKey)]) == codeBlocksKey {
				hasCode = true
				if hasCode && (hasAtt || hasPred) {
					return true
				}
			}
			// Walk past the closing quote of this string literal,
			// honoring backslash escapes.
			j := i + 1
			for j < len(raw) {
				if raw[j] == '\\' {
					j += 2
					continue
				}
				if raw[j] == '"' {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		i++
	}
	return hasAtt || hasPred || hasCode
}

// extractResultDeliverables pulls the three structured deliverables
// (attachments / predictions / code_blocks) from agent_task_queue
// .result (jsonb) so the workbench timeline can render them in the
// Artifact / Forecast / Code tabs. Each envelope is optional and
// independently extracted — a run with only attachments (no
// predictions) is valid and just leaves the predictions slice nil.
//
// Returns (nil, nil, nil) when the blob is empty or doesn't carry
// any of the structured keys, so the caller can use a single
// `if any != nil` check to decide whether to attach the fields.
//
// 0.3.42 hardening:
//   - Drops attachments whose `kind` is not in allowedAttachmentKinds.
//   - Drops attachments whose `data` exceeds maxAttachmentBytes.
//   - The per-row work happens BEFORE the slice append so a future
//     refactor that swaps to a pre-allocated buffer can't regress.
//
// Wire contract (documented in multica-claude-science SKILL.md so
// agents know what to emit):
//
//	{
//	  "output": "Final markdown report",
//	  "attachments": [
//	    {"kind": "interactive-chart", "name": "KS distribution",
//	     "mime": "application/json",
//	     "data": {"type": "scatter", "x": {"field":"x","label":"X"},
//	              "y": {"field":"y","label":"Y"},
//	              "points": [...]}}
//	    {"kind": "svg", "name": "decision-tree.svg", "data": "<svg…>"}
//	    {"kind": "png", "url": "/api/uploads/abc123"}
//	  ],
//	  "predictions": [
//	    {"round": 1, "scenario": "baseline",
//	     "probability": 0.42, "confidence": 0.71,
//	     "horizon": "week", "persona": "statistician"}
//	  ],
//	  "code_blocks": [
//	    {"language": "python", "filename": "ks.py",
//	     "code": "import scipy.stats…"}
//	  ]
//	}
func extractResultDeliverables(raw []byte) ([]LabAttachment, []LabPrediction, []LabCodeBlock) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	// Fast path: skip the unmarshal when none of the structured keys
	// are present. Saves the typecheck cost on the common case
	// where agents only emit `output` + a handful of other scalars.
	//
	// 0.3.43 P1-7: the previous implementation called
	// bytes.Contains three times over the full result blob, which
	// on a 4 MB markdown result was 12 MB of redundant scans per
	// task in fetchLabContextTasks (20 tasks × 4 MB × 3 = 240 MB
	// per request). The new fast path does a single linear pass
	// that flips three boolean flags then short-circuits as soon
	// as one is set.
	trimmed := bytes.TrimSpace(raw)
	if !hasAnyStructuredKey(trimmed) {
		return nil, nil, nil
	}
	var envelope struct {
		Attachments []json.RawMessage `json:"attachments"`
		Predictions []json.RawMessage `json:"predictions"`
		CodeBlocks  []json.RawMessage `json:"code_blocks"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, nil, nil
	}
	var attachments []LabAttachment
	if len(envelope.Attachments) > 0 {
		attachments = make([]LabAttachment, 0, len(envelope.Attachments))
		for _, raw := range envelope.Attachments {
			var att LabAttachment
			if err := json.Unmarshal(raw, &att); err != nil || att.Kind == "" {
				continue
			}
			if !allowedAttachmentKinds[att.Kind] {
				continue
			}
			// 0.3.42: cap per-attachment data payload. `data` is
			// `any` (json.RawMessage-compatible) so we approximate
			// the size by its JSON byte length — good enough to
			// stop the worst case (huge base64 PNGs encoded inline).
			if att.Data != nil {
				dataBytes, err := json.Marshal(att.Data)
				if err != nil || len(dataBytes) > maxAttachmentBytes {
					continue
				}
			}
			attachments = append(attachments, att)
		}
		if len(attachments) == 0 {
			attachments = nil
		}
	}
	var predictions []LabPrediction
	if len(envelope.Predictions) > 0 {
		predictions = make([]LabPrediction, 0, len(envelope.Predictions))
		for _, raw := range envelope.Predictions {
			var p LabPrediction
			if err := json.Unmarshal(raw, &p); err == nil {
				predictions = append(predictions, p)
			}
		}
		if len(predictions) == 0 {
			predictions = nil
		}
	}
	var codeBlocks []LabCodeBlock
	if len(envelope.CodeBlocks) > 0 {
		codeBlocks = make([]LabCodeBlock, 0, len(envelope.CodeBlocks))
		for _, raw := range envelope.CodeBlocks {
			var c LabCodeBlock
			if err := json.Unmarshal(raw, &c); err == nil && c.Code != "" {
				codeBlocks = append(codeBlocks, c)
			}
		}
		if len(codeBlocks) == 0 {
			codeBlocks = nil
		}
	}
	return attachments, predictions, codeBlocks
}

// extractResultSummary pulls a one-line preview from agent_task_queue
// .result (jsonb) so the Plan timeline shows the agent's first
// sentence next to a successful run. Falls back gracefully when the
// payload is empty or shaped differently — never returns an error,
// just an empty string the caller drops on the floor.
//
// Why this lives here, not in sqlc: the result jsonb is a free-form
// blob whose shape the agent controls (markdown, JSON, plain text).
// Centralising the summary extraction in one Go function lets the
// renderer agree on the preview contract without forcing every agent
// prompt to produce a structured envelope.
func extractResultSummary(raw []byte, cap int) string {
	if len(raw) == 0 {
		return ""
	}
	// First try {"output": "..."}. Most Multica agents (chat + lab
	// both) wrap their final reply in this envelope; the lab-workbench
	// contract documents it as the canonical summary source. An empty
	// output field is treated as an explicit "no summary" — the
	// renderer shows blank next to the run rather than echoing the
	// raw JSON. Non-envelope payloads fall through to the raw preview.
	var envelope struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Output != "" {
		return truncateUTF8(envelope.Output, cap)
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		// Envelope present but output empty → explicit no-summary.
		return ""
	}
	// Fall back to the raw bytes (stripped of leading whitespace).
	// Most non-JSON agent output is markdown; the renderer will
	// render the full content via the matching agent comment anyway,
	// so the timeline summary is just a teaser.
	stripped := trimLeadingWhitespace(raw)
	if len(stripped) == 0 {
		return ""
	}
	return truncateUTF8(string(stripped), cap)
}

func trimLeadingWhitespace(b []byte) []byte {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return b[i:]
		}
	}
	return nil
}

// truncateUTF8 cuts s to at most n bytes without splitting a
// multi-byte rune in the middle. Returns the (possibly shortened)
// string.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Walk back from n until we land on a rune boundary. UTF-8
	// continuation bytes start with 0b10xxxxxx, so the first byte
	// whose top two bits aren't 10 is the start of the next rune.
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	if n <= 0 {
		return ""
	}
	return s[:n]
}