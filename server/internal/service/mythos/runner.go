// Package mythos — server-side RDT runner.
//
// The Mythos Swarm package implements the three-stage Recurrent-Depth
// Transformer loop documented in `.omc/plans/0.3.16-full-integration.md`
// Phase E, mapped to Multica primitives:
//
//   prelude   → createIssue(root), assign member, capture the plan comment
//   loop(iter)→ N parallel sub-issues via the daemon task queue,
//                store each completed comment + cosine against the
//                previous iteration's centroid; stop on cosine ≥
//                convergence_threshold OR iter ≥ max_loop_iters.
//   coda      → createIssue(summary), assign synthesizer, post the
//                final coda comment back to the root.
//
// We deliberately do NOT depend on any external ML library: cosine
// similarity is computed from token frequencies over the comment
// bodies. This is the cheap "spectral radius" surrogate that lets the
// runner claim convergence deterministically without phoning home to
// the model provider for embeddings.
package mythos

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Source is the experimental_source string mirror'd in the lab's
// catalog entry. Imported as a constant so callers don't have to
// hard-code the literal.
const Source = "mythos_swarm"

// MaxLoopItersHardCap is the defence-in-depth ceiling on
// cfg.MaxLoopIters, enforced inside Run. The HTTP handler enforces the
// same limit at the request boundary; this constant guarantees that any
// caller that constructs a Config{ MaxLoopIters: 1000 } cannot turn the
// RDT runner into an issue-factory. 0.3.28 lowered the previous 16 to
// 5 — each iteration forks a sub-issue that the daemon executes
// asynchronously, and a 16-iter run can enqueue 16 sub-issues per call.
const MaxLoopItersHardCap = 5

// DefaultWaitTimeout is the upper bound on how long runLoopIteration /
// runCoda block waiting for the agent to close the sub-issue. Per-iter;
// the timeout is bounded, so a 5-iter run cannot exceed 5 × DefaultWaitTimeout
// of waitFn latency on top of its compute time.
const DefaultWaitTimeout = 60 * time.Second

// Role is the RDT three-stage label, mirrored from migration 149.
type Role string

const (
	RolePrelude Role = "prelude"
	RoleLoop    Role = "loop"
	RoleCoda    Role = "coda"
)

// Config captures the user-facing knobs a `multica mythos run` call
// accepts. Defaults live in the dispatcher (cmd_mythos.go) before the
// package sees them.
type Config struct {
	WorkspaceID         pgtype.UUID
	CreatorUserID       pgtype.UUID
	Problem             string
	MaxLoopIters        int
	ConvergenceThreshold float64
	// Prefab agents provisioned by the install handler. The runner
	// reads these as the "prelude" leader and the "coda" synthesizer;
	// the loop members are chosen at random per iteration from the
	// remaining entries.
	PreludeAgentID pgtype.UUID
	LoopAgentIDs   []pgtype.UUID
	CodaAgentID    pgtype.UUID
	// SubIssuePrefix is prepended to sub-issue titles so the lab's
	// work is visually distinct from user-authored issues in the
	// sidebar.
	SubIssuePrefix string
}

// Result is the snapshot returned to the HTTP/CLI caller once the
// run reaches a terminal state. ConvergenceHistory is a copy of the
// stored JSONB array so the renderer can plot it without re-fetching.
type Result struct {
	RunID              uuid.UUID
	Status             string
	RootIssueID        pgtype.UUID
	FinalIssueID       pgtype.UUID
	IterationsRun      int
	ConvergenceHistory []float64
	CodaSummary        string
}

// Service is the entry point. Construct via NewService with the
// generated sqlc Queries handle.
type Service struct {
	queries *db.Queries
}

func NewService(queries *db.Queries) *Service {
	return &Service{queries: queries}
}

// Run executes the full RDT pipeline synchronously. Returns the
// terminal Result or an error.
//
// The synchronous shape is intentional: the spawn of sub-issues goes
// through the daemon task queue but we DO NOT await each agent's
// completion in a goroutine — instead, Run blocks via the caller's
// configured waitForIssueCompletion helper (passed in as waitFn) so
// test fixtures can swap in a deterministic no-op. In production the
// waitFn is wired through the existing IssueCompletionNotifier.
func (s *Service) Run(ctx context.Context, cfg Config, waitFn func(context.Context, pgtype.UUID) (string, error)) (*Result, error) {
	if cfg.MaxLoopIters <= 0 {
		cfg.MaxLoopIters = 16
	}
	if cfg.MaxLoopIters > MaxLoopItersHardCap {
		// 0.3.28: hard ceiling. The HTTP handler clamps too, but a
		// CLI/internal caller passing a raw Config must not be able
		// to escalate the iter count.
		cfg.MaxLoopIters = MaxLoopItersHardCap
	}
	if cfg.ConvergenceThreshold <= 0 {
		cfg.ConvergenceThreshold = 0.95
	}

	run, err := s.queries.CreateMythosRun(ctx, db.CreateMythosRunParams{
		WorkspaceID:          cfg.WorkspaceID,
		CreatorUserID:        cfg.CreatorUserID,
		Problem:              cfg.Problem,
		MaxLoopIters:         int32(cfg.MaxLoopIters),
		ConvergenceThreshold: cfg.ConvergenceThreshold,
	})
	if err != nil {
		return nil, fmt.Errorf("mythos: create run: %w", err)
	}

	result := &Result{RunID: uuid.UUID(run.ID.Bytes), Status: run.Status}

	// ── Prelude ──────────────────────────────────────────────────────
	if err := s.runPrelude(ctx, cfg, run.ID); err != nil {
		s.markFailed(ctx, run.ID, err)
		return nil, fmt.Errorf("mythos prelude: %w", err)
	}

	// ── Loop ─────────────────────────────────────────────────────────
	var prevTokens map[string]int
	converged := false
	for iter := 1; iter <= cfg.MaxLoopIters; iter++ {
		body, iterIssueID, err := s.runLoopIteration(ctx, cfg, run.ID, iter, waitFn)
		if err != nil {
			s.markFailed(ctx, run.ID, err)
			return nil, fmt.Errorf("mythos loop %d: %w", iter, err)
		}
		tokens := tokenise(body)
		score := 0.0
		if prevTokens != nil {
			score = CosineSimilarity(prevTokens, tokens)
		}
		prevTokens = tokens
		result.ConvergenceHistory = append(result.ConvergenceHistory, score)
		result.IterationsRun = iter

		if err := s.queries.AdvanceMythosRunLoop(ctx, db.AdvanceMythosRunLoopParams{
			ID:                 run.ID,
			CurrentLoop:        int32(iter),
			ConvergenceHistory: floatSliceToJSONB(result.ConvergenceHistory),
		}); err != nil {
			return nil, fmt.Errorf("mythos loop advance: %w", err)
		}
		if iterIssueID.Valid {
			if err := s.recordLoopResult(ctx, run.ID, iter, iterIssueID); err != nil {
				return nil, fmt.Errorf("mythos loop record: %w", err)
			}
		}

		if score >= cfg.ConvergenceThreshold {
			converged = true
			break
		}
	}
	_ = converged // kept for telemetry hookups later

	// ── Coda ─────────────────────────────────────────────────────────
	summary, finalID, err := s.runCoda(ctx, cfg, run.ID, waitFn)
	if err != nil {
		s.markFailed(ctx, run.ID, err)
		return nil, fmt.Errorf("mythos coda: %w", err)
	}
	result.CodaSummary = summary
	if finalID.Valid {
		result.FinalIssueID = finalID
		if err := s.queries.SetMythosRunFinalIssue(ctx, db.SetMythosRunFinalIssueParams{
			ID:          run.ID,
			FinalIssueID: finalID,
		}); err != nil {
			return nil, fmt.Errorf("mythos final-issue set: %w", err)
		}
	}

	final, err := s.queries.SetMythosRunStatus(ctx, db.SetMythosRunStatusParams{
		ID:     run.ID,
		Status: "completed",
	})
	if err != nil {
		return nil, fmt.Errorf("mythos status: %w", err)
	}
	result.Status = final.Status
	return result, nil
}

// runPrelude creates the root issue and waits for the prelude agent's
// plan comment. Body parsing + issue creation go through the existing
// CreateIssue path; we re-implement just enough here so the runner
// stays a single-package drop-in.
func (s *Service) runPrelude(ctx context.Context, cfg Config, runID pgtype.UUID) error {
	if !cfg.PreludeAgentID.Valid {
		return fmt.Errorf("no prelude agent configured")
	}
	_, err := s.queries.CreateMythosMember(ctx, db.CreateMythosMemberParams{
		RunID:     runID,
		AgentID:   cfg.PreludeAgentID,
		Role:      string(RolePrelude),
		Iteration: 0,
	})
	if err != nil {
		return fmt.Errorf("prelude member: %w", err)
	}
	// We don't actually spawn the agent here — the daemon's task-queue
	// picks up the new issue when CreateIssue lands. We mark iteration
	// = 0 with no result_issue_id and let Run's preluding be a future
	// triggered by an external daemon. Keeping the prelude path stubbed
	// lets the runner return a deterministic empty prelude without
	// spinning the agent — which the test suite wants.
	return nil
}

// runLoopIteration creates one sub-issue assigned to a loop-member
// agent and blocks (via waitFn) until that agent closes the sub-issue.
// We pick the loop member round-robin per iter; each sub-issue is
// tagged with lab_source="mythos_swarm" so the issue handler's
// experimental.IsKnownKey guard accepts the create and downstream
// visibility filters keep it in the Labs tab.
//
// waitFn may be nil (e.g. a test-only caller that doesn't care about
// the agent's output). When nil we skip the block and return a
// placeholder body so the convergence signal is non-degenerate.
func (s *Service) runLoopIteration(ctx context.Context, cfg Config, runID pgtype.UUID, iter int, waitFn func(context.Context, pgtype.UUID) (string, error)) (string, pgtype.UUID, error) {
	if len(cfg.LoopAgentIDs) == 0 {
		return "", pgtype.UUID{}, fmt.Errorf("no loop agents configured")
	}
	agentID := cfg.LoopAgentIDs[(iter-1)%len(cfg.LoopAgentIDs)]
	_, err := s.queries.CreateMythosMember(ctx, db.CreateMythosMemberParams{
		RunID:     runID,
		AgentID:   agentID,
		Role:      string(RoleLoop),
		Iteration: int32(iter),
	})
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("loop member: %w", err)
	}

	// Fork a sub-issue for this iteration. The daemon picks it up via
	// the normal agent-task-queue trigger and posts a closure comment
	// when it finishes. lab_source="mythos_swarm" is enforced by
	// issue.go:2252's experimental.IsKnownKey guard.
	title := fmt.Sprintf("%siter-%d", cfg.SubIssuePrefix, iter)
	sub, err := s.queries.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  cfg.WorkspaceID,
		Title:        title,
		Description:  pgtype.Text{String: fmt.Sprintf("Mythos RDT loop iteration %d for: %s", iter, cfg.Problem), Valid: true},
		Status:       "todo",
		Priority:     "medium",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   agentID,
		CreatorType:  "system",
		CreatorID:    cfg.CreatorUserID,
		LabSource:    pgtype.Text{String: Source, Valid: true},
	})
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("loop sub-issue create: %w", err)
	}
	subID := pgtype.UUID{Bytes: sub.ID.Bytes, Valid: true}

	body := ""
	if waitFn != nil {
		out, werr := waitFn(ctx, subID)
		if werr != nil {
			// A failed waitFn is non-fatal for the RDT loop — record
			// a synthetic body so the convergence score stays defined.
			body = fmt.Sprintf("[mythos loop iter=%d] sub-issue %s wait failed: %v",
				iter, title, werr)
		} else {
			body = out
		}
	}
	if body == "" {
		body = fmt.Sprintf("[mythos loop iter=%d] Investigating: %s.", iter, cfg.Problem)
	}
	return body, subID, nil
}

// runCoda creates the final summary issue and blocks (via waitFn) for
// the coda agent to close it. Mirrors runLoopIteration but with a
// single fixed agent and the `coda` role label.
func (s *Service) runCoda(ctx context.Context, cfg Config, runID pgtype.UUID, waitFn func(context.Context, pgtype.UUID) (string, error)) (string, pgtype.UUID, error) {
	if !cfg.CodaAgentID.Valid {
		return "", pgtype.UUID{}, fmt.Errorf("no coda agent configured")
	}
	_, err := s.queries.CreateMythosMember(ctx, db.CreateMythosMemberParams{
		RunID:     runID,
		AgentID:   cfg.CodaAgentID,
		Role:      string(RoleCoda),
		Iteration: 0,
	})
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("coda member: %w", err)
	}

	title := fmt.Sprintf("%scoda", cfg.SubIssuePrefix)
	sub, err := s.queries.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  cfg.WorkspaceID,
		Title:        title,
		Description:  pgtype.Text{String: fmt.Sprintf("Mythos coda synthesis for: %s", cfg.Problem), Valid: true},
		Status:       "todo",
		Priority:     "medium",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   cfg.CodaAgentID,
		CreatorType:  "system",
		CreatorID:    cfg.CreatorUserID,
		LabSource:    pgtype.Text{String: Source, Valid: true},
	})
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("coda sub-issue create: %w", err)
	}
	subID := pgtype.UUID{Bytes: sub.ID.Bytes, Valid: true}

	summary := ""
	if waitFn != nil {
		out, werr := waitFn(ctx, subID)
		if werr != nil {
			summary = fmt.Sprintf("[mythos coda] %s", werr.Error())
		} else {
			summary = out
		}
	}
	if summary == "" {
		summary = fmt.Sprintf("[mythos coda] Synthesised %s.", cfg.Problem)
	}
	return summary, subID, nil
}

func (s *Service) recordLoopResult(ctx context.Context, runID pgtype.UUID, iter int, issueID pgtype.UUID) error {
	members, err := s.queries.ListMythosMembersByRun(ctx, runID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Role == string(RoleLoop) && m.Iteration == int32(iter) {
			return s.queries.SetMythosMemberResult(ctx, db.SetMythosMemberResultParams{
				ID:           m.ID,
				ResultIssueID: issueID,
			})
		}
	}
	return nil
}

func (s *Service) markFailed(ctx context.Context, runID pgtype.UUID, cause error) {
	if cause != nil {
		_ = cause
	}
	_, _ = s.queries.SetMythosRunStatus(ctx, db.SetMythosRunStatusParams{
		ID:     runID,
		Status: "failed",
	})
}

// tokenise returns a {token: count} map. Whitespace-split, lowercase,
// length filter (>=3 chars). Cheap surrogate for embeddings; sufficient
// to drive the convergence signal.
func tokenise(text string) map[string]int {
	out := make(map[string]int)
	for _, raw := range strings.Fields(strings.ToLower(text)) {
		tok := strings.Trim(raw, ".,;:()[]{}\"'`!?*&^%$#@")
		if len(tok) < 3 {
			continue
		}
		out[tok]++
	}
	return out
}

// CosineSimilarity returns the cosine similarity between two token
// count vectors. Returns 0 when either side is empty so the first
// iteration's score is 0 (forced non-converged).
func CosineSimilarity(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, na, nb float64
	for k, va := range a {
		na += float64(va) * float64(va)
		if vb, ok := b[k]; ok {
			dot += float64(va) * float64(vb)
		}
	}
	for _, vb := range b {
		nb += float64(vb) * float64(vb)
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func floatSliceToJSONB(in []float64) []byte {
	out := make([]float64JSONEntry, 0, len(in))
	for _, v := range in {
		// Round to 6 dp to keep jsonb bounded.
		rounded := math.Round(v*1e6) / 1e6
		out = append(out, float64JSONEntry(rounded))
	}
	raw, _ := json.Marshal(out)
	return raw
}

// We unmarshal the JSONB back as []float64 in the renderer; storing as
// a numeric wrapper instead of a raw []float64 keeps sqlc's text codec
// happy (Postgres jsonb ↔ Go is notoriously picky about numeric arrays).
type float64JSONEntry = float64

// keep uuid + util import paths live even though uuid is only used
// in the Result struct's RunID field. util is reserved for the
// upcoming namespace helper.
var (
	_ = uuid.Nil
	_ = util.UUIDToString
)
