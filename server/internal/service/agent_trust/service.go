// Package agent_trust — trust scoring + self-review for the
// agent_self_optimization lab's merged loop (0.5.2).
//
// Boris Cherny's ablation principle applied to agents: the main agent must
// decide whether to trust a sub-agent's output based on its track record,
// and a low-trust sub-agent's output gets reviewed before acceptance.
//
// Scoring contract (user-approved 2026-08-01):
//
//	initial score           5.0
//	max score               10.0
//	user correction         -0.5   (ApplyCorrection — issue-detail button)
//	review pass             +0.2   (LLM self-review accepted the output)
//	review fail             -0.5   (LLM self-review rejected the output)
//	review threshold        7.0    (score < 7.0 → gate review on completion)
//
// The gate lives in ProcessTaskCompletion: the TaskService calls it after a
// task reaches a terminal state. When the agent's score is below the
// threshold the output is reviewed by an LLM (English prompt, JSON verdict);
// pass/fail/skip outcomes are appended to agent_trust_event so the
// self-optimization runner can learn from the correction history.
//
// Everything is non-blocking and fail-open for the caller: ProcessTaskCompletion
// never errors (it logs), ApplyCorrection returns the new score.
package agent_trust

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Scoring constants. Keep them here — tuning needs no migration.
const (
	InitialScore           = 5.0
	MaxScore               = 10.0
	CorrectionPenalty      = 0.5
	ReviewPassBonus        = 0.2
	ReviewFailPenalty      = 0.5
	DefaultReviewThreshold = 7.0
)

// EventType mirrors the CHECK constraint in migration 228.
type EventType string

const (
	EventCorrection      EventType = "correction"
	EventReviewRequested EventType = "review_requested"
	EventReviewPass      EventType = "review_pass"
	EventReviewFail      EventType = "review_fail"
	EventReviewSkipped   EventType = "review_skipped"
)

// ReviewVerdict is what the LLM reviewer returns.
type ReviewVerdict string

const (
	VerdictPass ReviewVerdict = "pass"
	VerdictFail ReviewVerdict = "fail"
	// VerdictSkip means the review could not run (no provider CLI, timeout,
	// malformed JSON) — the score is left untouched.
	VerdictSkip ReviewVerdict = "skip"
)

// Reviewer executes one self-review of a task output. The production
// implementation calls the provider CLI (review.go); tests inject a fake.
type Reviewer interface {
	// Review returns the verdict for the given task output. It must be
	// fast enough to be called synchronously from the completion path.
	Review(ctx context.Context, task db.AgentTaskQueue, result []byte) ReviewVerdict
}

// Service holds the score arithmetic + event ledger. One instance per
// process; methods are stateless apart from the injected Reviewer, so the
// Service itself is goroutine-safe (the Reviewer must be too).
type Service struct {
	reviewer Reviewer
}

// NewService returns a Service with the default (production) reviewer
// attached. Call SetReviewer to override in tests.
func NewService() *Service {
	return &Service{reviewer: &CLIReviewer{}}
}

// SetReviewer replaces the LLM reviewer (test seam).
func (s *Service) SetReviewer(r Reviewer) {
	if r != nil {
		s.reviewer = r
	}
}

// Querier is the minimal surface of generated.Queries the Service needs.
// Keeping it local lets tests fake the DB without dragging the full
// generated struct into the picture.
type Querier interface {
	GetAgentTrustProfile(ctx context.Context, arg db.GetAgentTrustProfileParams) (db.AgentTrustProfile, error)
	ApplyAgentTrustCorrection(ctx context.Context, arg db.ApplyAgentTrustCorrectionParams) (db.AgentTrustProfile, error)
	ApplyAgentTrustReviewRequested(ctx context.Context, arg db.ApplyAgentTrustReviewRequestedParams) (db.AgentTrustProfile, error)
	ApplyAgentTrustReviewPass(ctx context.Context, arg db.ApplyAgentTrustReviewPassParams) (db.AgentTrustProfile, error)
	ApplyAgentTrustReviewFail(ctx context.Context, arg db.ApplyAgentTrustReviewFailParams) (db.AgentTrustProfile, error)
	CreateAgentTrustEvent(ctx context.Context, arg db.CreateAgentTrustEventParams) (db.AgentTrustEvent, error)
	GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error)
}

// LoadProfile returns the trust profile for (workspace, agent). An agent
// with no row yet is treated as the initial score — no row is created until
// the first score-changing event so an untouched agent has zero footprint.
func LoadProfile(ctx context.Context, q Querier, workspaceID, agentID pgtype.UUID) (db.AgentTrustProfile, bool) {
	prof, err := q.GetAgentTrustProfile(ctx, db.GetAgentTrustProfileParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		if err != pgx.ErrNoRows {
			slog.Warn("agent-trust: load profile failed", "workspace", workspaceID, "agent", agentID, "err", err)
		}
		return db.AgentTrustProfile{}, false
	}
	return prof, true
}

// Score returns the agent's current trust score (default InitialScore).
func Score(prof db.AgentTrustProfile, ok bool) float64 {
	if !ok {
		return InitialScore
	}
	if v, valid := numericToFloat64(prof.Score); valid {
		return v
	}
	return InitialScore
}

// ReviewThreshold returns the gate threshold (default DefaultReviewThreshold).
func ReviewThreshold(prof db.AgentTrustProfile, ok bool) float64 {
	if !ok {
		return DefaultReviewThreshold
	}
	if v, valid := numericToFloat64(prof.ReviewThreshold); valid {
		return v
	}
	return DefaultReviewThreshold
}

// ApplyCorrection records a user correction: score -CorrectionPenalty,
// clamped to [0, MaxScore], plus a 'correction' event with the delta and
// an optional note. taskID/issueID may be zero values. Returns the new score.
func (s *Service) ApplyCorrection(
	ctx context.Context,
	q Querier,
	workspaceID, agentID, taskID, issueID pgtype.UUID,
	note string,
	createdBy pgtype.UUID,
) (float64, error) {
	prof, ok := LoadProfile(ctx, q, workspaceID, agentID)
	before := Score(prof, ok)
	after := clampScore(before - CorrectionPenalty)

	if _, err := q.ApplyAgentTrustCorrection(ctx, db.ApplyAgentTrustCorrectionParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Score:       floatToNumeric(after),
	}); err != nil {
		return before, fmt.Errorf("agent-trust: apply correction: %w", err)
	}
	if _, err := q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		EventType:   string(EventCorrection),
		ScoreDelta:  floatToNumeric(-CorrectionPenalty),
		ScoreBefore: floatToNumeric(before),
		ScoreAfter:  floatToNumeric(after),
		TaskID:      taskID,
		IssueID:     issueID,
		Note:        pgtype.Text{String: note, Valid: note != ""},
		CreatedBy:   createdBy,
	}); err != nil {
		slog.Warn("agent-trust: correction event write failed", "agent", agentID, "err", err)
	}
	slog.Info("agent-trust: correction recorded",
		"agent", agentID, "workspace", workspaceID,
		"before", before, "after", after)
	return after, nil
}

// ProcessTaskCompletion is the trust gate the TaskService calls after a
// task completes. It is a no-op when:
//
//   - the task has no agent or no issue (chat tasks, quick-create)
//   - the agent's score is at or above the review threshold (trust)
//   - the reviewer has no LLM to call (VerdictSkip)
//
// When the gate fires, a 'review_requested' event is recorded, the output is
// reviewed, and pass/fail moves the score + appends the outcome event.
//
// Fail-open: every failure path logs and returns without error so the task
// completion flow is never blocked or retried by trust bookkeeping.
func (s *Service) ProcessTaskCompletion(ctx context.Context, q Querier, task db.AgentTaskQueue, result []byte) {
	if !task.AgentID.Valid || !task.IssueID.Valid {
		return
	}
	issue, err := q.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("agent-trust: task completion gate could not resolve workspace",
			"task", task.ID, "issue", task.IssueID, "err", err)
		return
	}
	prof, ok := LoadProfile(ctx, q, issue.WorkspaceID, task.AgentID)
	score := Score(prof, ok)
	threshold := ReviewThreshold(prof, ok)
	if score >= threshold {
		// Trusted: accept without review.
		return
	}

	// Gate fires: record the review request, then run the reviewer.
	if _, err := q.ApplyAgentTrustReviewRequested(ctx, db.ApplyAgentTrustReviewRequestedParams{
		WorkspaceID: issue.WorkspaceID,
		AgentID:     task.AgentID,
		Score:       floatToNumeric(score),
	}); err != nil {
		slog.Warn("agent-trust: review_requested counter failed", "task", task.ID, "err", err)
	}
	if _, err := q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
		WorkspaceID: issue.WorkspaceID,
		AgentID:     task.AgentID,
		EventType:   string(EventReviewRequested),
		ScoreDelta:  floatToNumeric(0),
		TaskID:      task.ID,
		IssueID:     task.IssueID,
	}); err != nil {
		slog.Warn("agent-trust: review_requested event failed", "task", task.ID, "err", err)
	}

	verdict := s.reviewer.Review(ctx, task, result)
	switch verdict {
	case VerdictPass:
		after := clampScore(score + ReviewPassBonus)
		s.applyReviewOutcome(ctx, q, issue.WorkspaceID, task,
			score, after, ReviewPassBonus, EventReviewPass)
	case VerdictFail:
		after := clampScore(score - ReviewFailPenalty)
		s.applyReviewOutcome(ctx, q, issue.WorkspaceID, task,
			score, after, -ReviewFailPenalty, EventReviewFail)
	default: // VerdictSkip
		slog.Info("agent-trust: review skipped (no LLM)", "task", task.ID, "agent", task.AgentID)
		_, _ = q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
			WorkspaceID: issue.WorkspaceID,
			AgentID:     task.AgentID,
			EventType:   string(EventReviewSkipped),
			ScoreDelta:  floatToNumeric(0),
			TaskID:      task.ID,
			IssueID:     task.IssueID,
		})
	}
}

// ReviewTask runs a manual self-review of a specific task's output and
// records the outcome event + score delta (same semantics as the automatic
// gate). Used by POST /api/experimental/trust/{agentId}/review. The caller
// (handler) resolved the task row; the workspace comes from the task's
// issue so the score is always recorded against the right tenant.
func (s *Service) ReviewTask(ctx context.Context, q Querier, task db.AgentTaskQueue, result []byte) ReviewVerdict {
	// The handler resolved the task; if the issue is missing there is
	// nothing to attribute the review to.
	workspaceID := taskWorkspace(ctx, q, task)
	if !workspaceID.Valid {
		return VerdictSkip
	}
	verdict := s.reviewer.Review(ctx, task, result)
	prof, ok := LoadProfile(ctx, q, workspaceID, task.AgentID)
	score := Score(prof, ok)
	var delta float64
	var event EventType
	switch verdict {
	case VerdictPass:
		delta = ReviewPassBonus
		event = EventReviewPass
	case VerdictFail:
		delta = -ReviewFailPenalty
		event = EventReviewFail
	default:
		_, _ = q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
			WorkspaceID: workspaceID,
			AgentID:     task.AgentID,
			EventType:   string(EventReviewSkipped),
			ScoreDelta:  floatToNumeric(0),
			TaskID:      task.ID,
			IssueID:     task.IssueID,
		})
		return VerdictSkip
	}
	after := clampScore(score + delta)
	s.applyReviewOutcome(ctx, q, workspaceID, task, score, after, delta, event)
	return verdict
}

// taskWorkspace resolves the workspace for a task via its issue (the task
// row carries no workspace_id). Zero value when unresolvable.
func taskWorkspace(ctx context.Context, q Querier, task db.AgentTaskQueue) pgtype.UUID {
	if !task.IssueID.Valid {
		return pgtype.UUID{}
	}
	issue, err := q.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("agent-trust: task workspace resolve failed", "task", task.ID, "err", err)
		return pgtype.UUID{}
	}
	return issue.WorkspaceID
}

// applyReviewOutcome writes the profile delta + the outcome event for a
// pass/fail review verdict.
func (s *Service) applyReviewOutcome(
	ctx context.Context, q Querier, workspaceID pgtype.UUID, task db.AgentTaskQueue,
	before, after, delta float64,
	event EventType,
) {
	switch event {
	case EventReviewPass:
		if _, err := q.ApplyAgentTrustReviewPass(ctx, db.ApplyAgentTrustReviewPassParams{
			WorkspaceID: workspaceID,
			AgentID:     task.AgentID,
			Score:       floatToNumeric(after),
		}); err != nil {
			slog.Warn("agent-trust: review pass profile write failed", "task", task.ID, "err", err)
			return
		}
	case EventReviewFail:
		if _, err := q.ApplyAgentTrustReviewFail(ctx, db.ApplyAgentTrustReviewFailParams{
			WorkspaceID: workspaceID,
			AgentID:     task.AgentID,
			Score:       floatToNumeric(after),
		}); err != nil {
			slog.Warn("agent-trust: review fail profile write failed", "task", task.ID, "err", err)
			return
		}
	}
	if _, err := q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
		WorkspaceID: workspaceID,
		AgentID:     task.AgentID,
		EventType:   string(event),
		ScoreDelta:  floatToNumeric(delta),
		ScoreBefore: floatToNumeric(before),
		ScoreAfter:  floatToNumeric(after),
		TaskID:      task.ID,
		IssueID:     task.IssueID,
	}); err != nil {
		slog.Warn("agent-trust: review outcome event failed", "task", task.ID, "err", err)
	}
	slog.Info("agent-trust: review outcome",
		"task", task.ID, "agent", task.AgentID, "event", event,
		"before", before, "after", after)
}

// ---- helpers ----

// clampScore bounds a score to [0, MaxScore].
func clampScore(v float64) float64 {
	return math.Min(MaxScore, math.Max(0, v))
}

// floatToNumeric converts a float64 into pgtype.Numeric via a fixed 1-decimal
// string (scores are one-decimal by construction; string parsing avoids
// binary float drift in the DB column).
func floatToNumeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.1f", v))
	return n
}

// numericToFloat64 converts pgtype.Numeric to float64. Returns false for
// NaN / infinity / nil (callers fall back to the default).
func numericToFloat64(n pgtype.Numeric) (float64, bool) {
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

// ProfileScore is a convenience wrapper used by tests and by the HTTP
// layer: it returns the current score and whether the agent has a profile.
type ProfileScore struct {
	Score  float64
	HasRow bool
}

// LoadScore returns the agent's trust score + row presence in one call.
func LoadScore(ctx context.Context, q Querier, workspaceID, agentID pgtype.UUID) ProfileScore {
	prof, ok := LoadProfile(ctx, q, workspaceID, agentID)
	return ProfileScore{Score: Score(prof, ok), HasRow: ok}
}
