package handler

// 0.5.86 issue-delivery batch: lab run reports land IN the issue.
//
// Problem (0.5.83 post-ship audit + user report 2026-08-28): Pythia and
// TimesFM forecast runs persisted their rows but the text report lived
// ONLY in the lab view — the issue had no deliverable. Fix: the run
// handlers post the report as the lab leader agent's comment
// (AuthorType="agent") on the bound issue and record the comment id on
// the run row (migration 282, report_comment_id — the idempotency
// marker). Auxiliary labs (causal_graph, llm_wiki_bridge) never reach
// this path — they are trace/visualize-only by classification.
//
// The write mirrors TaskService.createAgentComment (service/task.go)
// shape-for-shape — same AuthorType semantics, same
// EventCommentCreated payload — so the renderer's timeline, mentions,
// and realtime invalidation treat the report exactly like any agent
// reply. It lives in the handler package because both call sites
// (forecast_issue.go, timesfm_forecast.go) are SSE/HTTP handlers that
// hold *Handler, not a TaskService.

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// postLabRunReportComment creates `content` as an issue comment authored
// by the named lab leader agent and returns the comment id for the
// run's report_comment_id marker. Best-effort by contract: every error
// path logs a WRN and returns the zero UUID — a failed writeback must
// never fail (or duplicate) the run itself, and the caller skips the
// marker update when the id is zero so a retry can re-attempt.
//
// When the leader agent row is missing (flag enabled but install never
// ran) the report still lands, authored by the canonical system author
// (AuthorType="system", all-zero UUID with Valid:true — mig 107
// convention, same as the swarm coda comment).
func postLabRunReportComment(
	ctx context.Context,
	h *Handler,
	issueID pgtype.UUID,
	workspaceID pgtype.UUID,
	leaderName string,
	content string,
) pgtype.UUID {
	zero := pgtype.UUID{}
	if h == nil || h.Queries == nil || content == "" || !issueID.Valid || !workspaceID.Valid {
		return zero
	}

	issue, err := h.Queries.GetIssue(ctx, issueID)
	if err != nil {
		slog.Warn("lab report writeback: issue lookup failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
		return zero
	}

	authorType := "system"
	authorID := pgtype.UUID{Valid: true} // all-zero canonical system author
	if leaderName != "" {
		if agent, agentErr := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        leaderName,
		}); agentErr == nil {
			authorType = "agent"
			authorID = agent.ID
		} else {
			slog.Warn("lab report writeback: leader agent missing — falling back to system author",
				"leader", leaderName, "issue_id", util.UUIDToString(issueID))
		}
	}

	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  authorType,
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
	})
	if err != nil {
		slog.Warn("lab report writeback: CreateComment failed",
			"issue_id", util.UUIDToString(issueID), "author_type", authorType, "error", err)
		return zero
	}

	if h.Bus != nil {
		h.Bus.Publish(events.Event{
			Type:        protocol.EventCommentCreated,
			WorkspaceID: util.UUIDToString(issue.WorkspaceID),
			ActorType:   authorType,
			ActorID:     util.UUIDToString(authorID),
			Payload: map[string]any{
				"comment": map[string]any{
					"id":             util.UUIDToString(comment.ID),
					"issue_id":       util.UUIDToString(comment.IssueID),
					"author_type":    comment.AuthorType,
					"author_id":      util.UUIDToString(comment.AuthorID),
					"content":        comment.Content,
					"type":           comment.Type,
					"parent_id":      util.UUIDToPtr(comment.ParentID),
					"source_task_id": util.UUIDToPtr(comment.SourceTaskID),
					"created_at":     comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
				},
				"issue_title":  issue.Title,
				"issue_status": issue.Status,
			},
		})
	}

	slog.Info("lab report writeback: comment posted",
		"issue_id", util.UUIDToString(issueID),
		"author_type", authorType,
		"comment_id", util.UUIDToString(comment.ID))
	return comment.ID
}
