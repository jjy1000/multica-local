// Package agent_self_optimization — optimizer.go (0.5.2).
//
// SkillOpt-style instruction optimizer for agents. Inspired by
// microsoft/SkillOpt (text-space optimizer: the skill document is the
// trainable state of a frozen agent; a separate optimizer model turns
// scored rollouts into bounded add/delete/replace edits, and a candidate
// edit is accepted only when validation improves) and by the OpenMythos
// convergence intuition (loop until no further improvement — spectral
// radius < 1 analog).
//
// The loop:
//
//  1. Gather evidence per agent: done issues it worked on in the window
//     + trust ledger (corrections / review outcomes) + its current
//     agent.instructions.
//  2. An optimizer LLM proposes ≤ MaxEditsPerRun bounded edits
//     (add / delete / replace) as structured JSON, each with a rationale
//     tied to concrete evidence.
//  3. Rejection buffer: any proposal whose (before, after) pair matches a
//     previously REJECTED edit is skipped — the loop must not re-propose
//     negative experience.
//  4. Validation gate: a second LLM call judges the candidate
//     instructions (current + proposed deltas) and returns pass/fail.
//     Only accepted edits are written back to agent.instructions via
//     UpdateAgent.
//  5. Every proposal (accepted or rejected) lands in agent_opt_edit so
//     the history is the experience base for the next run.
//
// Degradation: when no provider CLI is available the optimizer returns
// ErrNoLLM and the runner falls back to the existing heuristic
// suggestions — the lab still works without an LLM, it just cannot edit
// instructions.
package agent_self_optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/service/agent_trust"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MaxEditsPerRun caps the optimizer proposals per agent per run. Small
// and bounded — a text-space optimizer must not rewrite an instruction
// set wholesale in one pass.
const MaxEditsPerRun = 3

// RejectionBufferSize is how many past rejected edits per agent are fed
// to the optimizer as negative experience.
const RejectionBufferSize = 10

// ErrNoLLM is returned when no provider CLI is available; callers fall
// back to the heuristic path.
var ErrNoLLM = fmt.Errorf("optimizer: no provider CLI available")

// Application is the lifecycle state of an instruction edit.
type Application string

const (
	// ApplicationApplied — auto-applied (add-only, correction-backed,
	// trust+enrollment gates passed) or manually applied; written back to
	// agent.instructions.
	ApplicationApplied Application = "applied"
	// ApplicationSuggested — validated but not auto-applied; parked for
	// human confirmation (the "待确认建议" tier).
	ApplicationSuggested Application = "suggested"
	// ApplicationRejected — explicit user reject; soft evidence, never
	// re-proposed identically (re-proposable only with materially stronger
	// backing).
	ApplicationRejected Application = "rejected"
	// ApplicationIgnored — soft archive: a suggestion nobody acted on
	// within the expiry window. NOT in the rejection buffer; re-proposable
	// with fresh validation.
	ApplicationIgnored Application = "ignored"
	// ApplicationReverted — an applied edit was rolled back to its
	// snapshot (the "回退到上一版本" action). Content-hash keyed into the
	// rejected buffer so it cannot be re-applied identically.
	ApplicationReverted Application = "reverted"
)

// ---- Design-review thresholds (synthesis verdict, 2026-08-01) ----
//
// Destructiveness is decided by CONSTRUCTION, not by score: delete and
// replace are NEVER auto-applied — they always land in 'suggested'. The
// per-edit_type 60/75/85 threshold table is retired; a single unified
// gate applies to add-only edits. See the design-review workflow
// (wf_ada77213-8dd) for the full rationale.

// ProposeFloor is the minimum validation score for an edit to become a
// 'suggested' candidate. Below it the edit is archived (rejected buffer).
const ProposeFloor = 60.0

// AutoApplyGate is the minimum validation score for an ADD edit to be
// auto-applied, on top of the hard gates (enrolled + trust≥8 + not
// lab-managed/hard-blocked + correction-backed + rate cap 1/run +
// snapshot/rollback committed).
const AutoApplyGate = 90.0

// SuggestedOrderingCredit is the +5 cap on ordering INSIDE the 'suggested'
// queue for a correction-backed candidate. It can never lift anything
// across the auto-apply line — the auto-apply gate reads the raw score.
const SuggestedOrderingCredit = 5.0

// SuggestionExpiryRuns is how many weekly runs (~21 days) a suggestion
// may wait before it expires to 'ignored' (soft archive, NOT rejected).
const SuggestionExpiryRuns = 3

// MaxAutoAppliesPerAgentPerRun caps auto-applied edits per agent per run
// (rate / amplitude cap). After a post-hoc validation failure or a user
// rejection of that agent's edit, auto-apply halts for the window.
const MaxAutoAppliesPerAgentPerRun = 1

// HardBlockedSafetyTokens are substrings that mark an instruction as a
// safety/constraint rule. Any edit touching text containing these tokens
// is excluded from auto-apply and must go through human confirmation —
// regardless of score or edit type.
var HardBlockedSafetyTokens = []string{
	"deny", "never", "confirm", "secret",
	"禁止", "不可", "必须", "不得",
}

// InstructionEdit is one proposed / recorded instruction edit.
type InstructionEdit struct {
	AgentID    pgtype.UUID
	AgentName  string
	EditType   string // add | delete | replace
	BeforeText string
	AfterText  string
	Rationale  string
	// Application is the lifecycle state (applied / suggested / rejected /
	// ignored / reverted).
	Application Application
	// Accepted is kept for backward compat: true == ApplicationApplied.
	Accepted bool
	// ValidationScore is the validator LLM's 0-100 score of the candidate
	// instruction set vs the current one.
	ValidationScore float64
	// ValidationReason is the validator's one-line justification.
	ValidationReason string
	// CorrectedTaskID, when set, marks the edit as correction-backed: the
	// candidate is derivable from a task whose output the user corrected.
	// Auto-apply requires this.
	CorrectedTaskID pgtype.UUID
	// Snapshot is the full instruction set BEFORE this edit was applied
	// (the rollback point). Set only when the edit is applied.
	Snapshot string
}

// EditProposal is the wire shape the optimizer LLM returns.
type EditProposal struct {
	EditType  string `json:"edit_type"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Rationale string `json:"rationale"`
}

// OptimizeEvidence is what the optimizer needs per agent.
type OptimizeEvidence struct {
	AgentID             pgtype.UUID
	AgentName           string
	CurrentInstructions string
	Issues              []db.ListDoneIssuesForSelfOptRow // done issues assigned to this agent
	TrustEvents         []db.AgentTrustEvent             // corrections / reviews
	RejectedEdits       []db.AgentOptEdit                // past rejected proposals (buffer)
	// AutoApplyEnrolled is the per-agent opt-in for auto-apply. Default
	// OFF (design-review verdict): auto-apply never fires for a
	// non-enrolled agent; the primary product agent is never enrolled.
	AutoApplyEnrolled bool
	// CorrectedTaskID is the task whose output the user corrected (the
	// confirmed-correction anchor for auto-apply). Set by the optimizer
	// from the trust ledger; fed to the proposal prompt so the LLM derives
	// the fix from that specific task.
	CorrectedTaskID pgtype.UUID
}

// Optimizer runs the SkillOpt-style loop. Stateless: each run builds its
// own LLM calls. The LLM seam is a package var so tests can inject a fake
// without touching the production path.
type Optimizer struct {
	// ProviderLLM is the single-turn provider call. Defaults to
	// agent_trust.RunProviderLLM; tests swap it.
	ProviderLLM func(ctx context.Context, system, prompt string) (string, error)
	// ValidatorLLM is the validation-gate call. Defaults to ProviderLLM.
	ValidatorLLM func(ctx context.Context, system, prompt string) (string, error)
	// LabManagedFunc is the lab-managed check. Defaults to the DB-backed
	// experimental_resource_visibility lookup; tests inject a stub so a
	// nil *db.Queries is never dereferenced.
	LabManagedFunc func(ctx context.Context, q *db.Queries, agentID pgtype.UUID) (bool, error)
}

// NewOptimizer returns an Optimizer wired to the real provider CLIs.
func NewOptimizer() *Optimizer {
	o := &Optimizer{
		ProviderLLM:  agent_trust.RunProviderLLM,
		ValidatorLLM: agent_trust.RunProviderLLM,
	}
	o.LabManagedFunc = func(ctx context.Context, q *db.Queries, agentID pgtype.UUID) (bool, error) {
		return isLabManagedAgent(ctx, q, agentID)
	}
	return o
}

// OptimizeAgent produces bounded instruction edits for one agent and
// classifies each by the design-review verdict:
//
//   - applied: ADD-only, correction-backed, gates passed, score ≥ 90.
//     The caller writes them back (cumulative finalInstructions) and
//     snapshots the pre-edit set.
//   - suggested: score ≥ 60 but not auto-applied (delete/replace by
//     construction, or gate miss, or score < 90). Parked for human
//     confirmation.
//   - rejected: score < 60, or matching a prior rejection (negative
//     experience).
//
// finalInstructions is the cumulative state after all APPLIED edits only;
// suggested edits are NOT included (they wait for human approval).
func (o *Optimizer) OptimizeAgent(
	ctx context.Context,
	q *db.Queries,
	ev OptimizeEvidence,
) (applied, suggested, rejected []InstructionEdit, finalInstructions string, err error) {
	if len(ev.Issues) == 0 && len(ev.TrustEvents) == 0 {
		// No evidence → no edits. The runner's deferral gate usually
		// catches this earlier, but a per-agent gap is fine.
		return nil, nil, nil, ev.CurrentInstructions, nil
	}
	proposals, err := o.proposeEdits(ctx, ev)
	if err != nil {
		return nil, nil, nil, ev.CurrentInstructions, err
	}
	if len(proposals) == 0 {
		return nil, nil, nil, ev.CurrentInstructions, nil
	}

	// Auto-apply eligibility gates (synthesis §5). Lab-managed exclusion:
	// an agent hidden by any lab's experimental_resource_visibility is
	// part of a manifest/install/dispatch contract — never auto-edit it.
	labManaged, lerr := o.LabManagedFunc(ctx, q, ev.AgentID)
	if lerr != nil {
		slog.Warn("agent-self-opt: lab-managed check failed; defaulting to not-auto-apply",
			"agent", ev.AgentName, "err", lerr)
		labManaged = true // fail closed
	}
	trustScore := trustScoreOf(ev.TrustEvents)
	enrolled := ev.AutoApplyEnrolled
	// correctionTaskID is the task the user corrected (design verdict §5a:
	// auto-apply requires the candidate to trace to a CONFIRMED correction,
	// not merely a correction existing in the window). A correction event
	// without a task id cannot back an auto-apply.
	correctionTaskID := pgtype.UUID{}
	for _, evt := range ev.TrustEvents {
		if evt.EventType == "correction" && evt.TaskID.Valid {
			correctionTaskID = evt.TaskID
			break
		}
	}
	correctionBacked := correctionTaskID.Valid
	// The proposal prompt must know WHICH task was corrected so the LLM
	// can derive the fix from that task (not a window-wide boolean).
	ev.CorrectedTaskID = correctionTaskID

	candidate := ev.CurrentInstructions
	appliedCount := 0
	for _, p := range proposals {
		if o.isRejected(p, ev.RejectedEdits) {
			// Negative experience: do not re-propose.
			continue
		}
		edit, newState, ok := applyProposal(candidate, p)
		if !ok {
			continue
		}
		edit.AgentID = ev.AgentID
		edit.AgentName = ev.AgentName

		// Validation: numeric score of candidate vs current.
		score, reason, verr := o.validate(ctx, ev, candidate, newState)
		if verr != nil {
			// Gate failure (no LLM / timeout) → skip.
			continue
		}
		edit.ValidationScore = score
		edit.ValidationReason = reason

		// Destructiveness-by-construction: delete / replace NEVER
		// auto-apply — they always go to 'suggested' (human confirms any
		// overwrite/removal of instruction content). The user's own
		// constitution requires explicit confirmation for overwrites.
		canAutoApply := edit.EditType == "add" && score >= AutoApplyGate &&
			appliedCount < MaxAutoAppliesPerAgentPerRun

		// Hard gates (synthesis §5): enrollment + trust + lab-managed +
		// correction-backed + no safety-token content.
		canAutoApply = canAutoApply &&
			enrolled &&
			trustScore >= MinAutoApplyTrustScore &&
			!labManaged &&
			correctionBacked &&
			!touchesSafetyToken(p.Before) &&
			!touchesSafetyToken(p.After)

		switch {
		case canAutoApply:
			edit.Accepted = true
			edit.Application = ApplicationApplied
			edit.Snapshot = candidate // pre-edit set for rollback
			edit.CorrectedTaskID = correctionTaskID
			candidate = newState
			applied = append(applied, edit)
			appliedCount++
		case score >= ProposeFloor:
			edit.Accepted = false
			edit.Application = ApplicationSuggested
			suggested = append(suggested, edit)
		default:
			edit.Accepted = false
			edit.Application = ApplicationRejected
			rejected = append(rejected, edit)
		}
	}
	return applied, suggested, rejected, candidate, nil
}

// MinAutoApplyTrustScore is the hard trust gate for auto-apply: an agent
// with trust < 8 (or no trust history) never auto-applies — its edits all
// route to 'suggested'.
const MinAutoApplyTrustScore = 8.0

// trustScoreOf returns the agent's current trust score from its event
// ledger. The event list arrives newest-first (ORDER BY created_at DESC in
// ListAgentTrustEventsByAgent), so the FIRST valid ScoreAfter is the
// current score. Falls back to the initial 5.0 default when none exists
// (which is < MinAutoApplyTrustScore, so auto-apply stays off for agents
// with no trust history — safe by construction).
func trustScoreOf(events []db.AgentTrustEvent) float64 {
	for _, evt := range events {
		if evt.ScoreAfter.Valid {
			if v, ok := numericToFloat(evt.ScoreAfter); ok {
				return v
			}
		}
	}
	return 0
}

// numericToFloat converts pgtype.Numeric to float64 (best-effort).
func numericToFloat(n pgtype.Numeric) (float64, bool) {
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

// isLabManagedAgent reports whether the agent is hidden by any lab's
// experimental_resource_visibility row (i.e. it is lab infrastructure and
// must not be auto-edited).
func isLabManagedAgent(ctx context.Context, q *db.Queries, agentID pgtype.UUID) (bool, error) {
	for _, flagKey := range experimental.AllFlagKeys() {
		ids, err := experimental.HiddenResourceIDsByFlag(ctx, q, flagKey, experimental.HideAgent)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			if id == uuid.UUID(agentID.Bytes) {
				return true, nil
			}
		}
	}
	return false, nil
}

// touchesSafetyToken reports whether text contains any hard-blocked
// safety token. Auto-apply is excluded for any edit touching these.
func touchesSafetyToken(text string) bool {
	lower := strings.ToLower(text)
	for _, tok := range HardBlockedSafetyTokens {
		if strings.Contains(lower, strings.ToLower(tok)) {
			return true
		}
	}
	return false
}

// proposeEdits calls the optimizer LLM for bounded add/delete/replace
// proposals, parsed from the JSON response.
func (o *Optimizer) proposeEdits(ctx context.Context, ev OptimizeEvidence) ([]EditProposal, error) {
	prompt := buildProposalPrompt(ev)
	system := "You are a rigorous skill optimizer for an AI task-management system. " +
		"Propose small, evidence-grounded edits to an agent's instruction text. " +
		"Reply with ONLY a JSON array, no prose."

	text, err := o.ProviderLLM(ctx, system, prompt)
	if err != nil {
		return nil, err
	}
	parsed, perr := parseProposals(text)
	if perr != nil {
		slog.Warn("agent-self-opt: optimizer proposal parse failed",
			"agent", ev.AgentName, "err", perr)
		return nil, nil
	}
	// Bound the proposals.
	if len(parsed) > MaxEditsPerRun {
		parsed = parsed[:MaxEditsPerRun]
	}
	return parsed, nil
}

// validate scores the candidate instruction set vs the current one on a
// 0-100 scale. The validator LLM returns a JSON object:
//
//	{"score": 0-100, "reason": "<one line>"}
//
// Higher score = stronger evidence that the candidate strictly improves
// on the current instructions. A score near 0 means the change is
// speculative or harmful; near 100 means it fixes a documented failure
// mode with direct evidence.
func (o *Optimizer) validate(
	ctx context.Context,
	ev OptimizeEvidence,
	current, candidate string,
) (float64, string, error) {
	prompt := fmt.Sprintf(`Agent "%s" currently has the following instructions:

--- current instructions start ---
%s
--- current instructions end ---

A proposed change produces the following candidate instructions:

--- candidate instructions start ---
%s
--- candidate instructions end ---

Context: the agent produced %d completed issues in the scan window and %d trust events (corrections / review failures are signals of mistakes).

Score how strongly the candidate instruction set improves on the current one, on a 0-100 scale.
Scoring rubric:
- 85-100: the change removes or mitigates a DOCUMENTED failure mode (a user correction or review failure), is precisely scoped, and cannot regress other behavior.
- 65-84: the change is clearly grounded in the evidence and improves clarity / correctness, with minor residual risk.
- 40-64: the change is plausibly useful but partially speculative or redundant.
- 0-39: the change is speculative, contradicts existing instructions, or could regress established behavior.
Reply with ONLY a JSON object: {"score": <int 0-100>, "reason": "<one line, English>"}. No prose.`,
		ev.AgentName, current, candidate, len(ev.Issues), len(ev.TrustEvents))

	system := "You are a conservative validation gate for an AI skill optimizer. " +
		"Score candidate instruction edits 0-100; be strict — ungrounded changes score low."
	text, err := o.ValidatorLLM(ctx, system, prompt)
	if err != nil {
		return 0, "", err
	}

	var parsed struct {
		Score  int    `json:"score"`
		Reason string `json:"reason"`
	}
	if jerr := json.Unmarshal([]byte(text), &parsed); jerr != nil {
		// Fence / prose fallback: extract the first '{' … '}' span.
		if start := strings.Index(text, "{"); start >= 0 {
			if end := strings.LastIndex(text, "}"); end > start {
				if jerr2 := json.Unmarshal([]byte(text[start:end+1]), &parsed); jerr2 == nil {
					return clampScore(parsed.Score), parsed.Reason, nil
				}
			}
		}
		// Last resort: extract a number near "score".
		if s := extractScore(text); s >= 0 {
			return s, "", nil
		}
		return 0, "", fmt.Errorf("unparseable validation JSON")
	}
	return clampScore(parsed.Score), parsed.Reason, nil
}

// clampScore bounds a validation score to [0, 100].
func clampScore(v int) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return float64(v)
}

// Post-hoc commit-gate revalidation (0.5.2 adversarial review d1 — CONFIRMED).
//
// The design verdict's commit gate has two halves: (1) snapshot + manual
// rollback before auto-apply (implemented in edits.go::ApplyEdit /
// RevertEdit), and (2) the NEXT run re-validates previously auto-applied
// edits and rolls back on measured regression. Half (2) is implemented here.
//
// RevalidateAppliedEdits is called by the runner before proposing new edits.
// For each applied edit of the agent (newest first, rate-capped at 1/run so
// at most one needs re-scoring), it:
//
//   - re-fetches the agent's CURRENT instructions,
//   - if the edit's AfterText is no longer present (a later manual edit
//     removed it) the edit is considered already superseded — no re-score,
//   - otherwise re-scores CURRENT instructions vs the SNAPSHOT (pre-edit):
//     a low score means the applied edit made things measurably worse, so
//     the edit is reverted to its snapshot and the rollback is recorded as
//     a 'review_fail' trust event (score -0.5) + the ledger row flips to
//     'reverted'.
//
// The revalidation uses the same LLM seam + rubric as the auto-apply
// validation gate (Optimizer.validate), so it costs one LLM call per applied
// edit and is inherently conservative (the validator is instructed to score
// low on speculative/harmful changes).
func (o *Optimizer) RevalidateAppliedEdits(
	ctx context.Context,
	q *db.Queries,
	agent db.Agent,
	workspaceID pgtype.UUID,
) error {
	if !agent.ID.Valid {
		return nil
	}
	applied, err := q.ListAppliedAgentOptEdits(ctx, db.ListAppliedAgentOptEditsParams{
		AgentID:     agent.ID,
		WorkspaceID: workspaceID,
		Limit:       5,
	})
	if err != nil {
		return fmt.Errorf("revalidate applied edits: list: %w", err)
	}
	cur := instructionsOf(agent)
	for _, e := range applied {
		if cur == "" || e.AfterText == "" || !strings.Contains(cur, e.AfterText) {
			// The applied text is no longer present (later manual edit or a
			// prior reversion) — nothing to re-validate.
			continue
		}
		snapshot := ""
		if e.InstructionsSnapshot.Valid {
			snapshot = e.InstructionsSnapshot.String
		}
		// Re-score CURRENT (post-edit) vs SNAPSHOT (pre-edit). A low score =
		// the applied edit regressed the instruction set → roll back.
		score, _, verr := o.validate(ctx, OptimizeEvidence{
			AgentName:           agent.Name,
			CurrentInstructions: snapshot,
		}, snapshot, cur)
		if verr != nil {
			// Gate failure (no LLM / timeout) — skip; the next run re-tries.
			continue
		}
		if score >= RevalidateRetainFloor {
			continue
		}
		if err := o.rollbackAppliedEdit(ctx, q, agent, workspaceID, e, score); err != nil {
			slog.Warn("agent-self-opt: post-hoc rollback failed",
				"agent", agent.Name, "edit", e.ID, "err", err)
		}
	}
	return nil
}

// RevalidateRetainFloor is the minimum post-hoc revalidation score for an
// applied edit to STAY applied. Below it the edit is rolled back to its
// snapshot. Generous on purpose (60): the validator is strict by default
// (unscored changes rank low), so only a measurably regressive edit falls
// under it. Mirrors ProposeFloor — an applied edit that would not even make
// the 'suggested' tier today is not worth keeping.
const RevalidateRetainFloor = 60.0

// rollbackAppliedEdit reverts one applied edit to its pre-edit snapshot,
// flips the ledger row to 'reverted' (so it enters the negative-experience
// buffer and cannot be re-proposed / re-applied), and records a
// 'review_fail' trust event (-0.5) so the agent's trust score drops — the
// self-review loop's punishment half.
func (o *Optimizer) rollbackAppliedEdit(
	ctx context.Context,
	q *db.Queries,
	agent db.Agent,
	workspaceID pgtype.UUID,
	e db.AgentOptEdit,
	score float64,
) error {
	if !e.InstructionsSnapshot.Valid {
		return fmt.Errorf("applied edit %s has no snapshot; cannot roll back", uuid.UUID(e.ID.Bytes).String())
	}
	if _, err := q.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:           agent.ID,
		Instructions: pgtype.Text{String: e.InstructionsSnapshot.String, Valid: true},
	}); err != nil {
		return fmt.Errorf("restore snapshot: %w", err)
	}
	if _, err := q.UpdateAgentOptEditApplication(ctx, db.UpdateAgentOptEditApplicationParams{
		ID:          e.ID,
		Application: string(ApplicationReverted),
		WorkspaceID: workspaceID,
	}); err != nil {
		return fmt.Errorf("mark reverted: %w", err)
	}
	// Trust event: the self-review found the applied edit regressive → -0.5
	// (same punishment as a user correction / failed review). Compute the
	// profile's current score so the delta is accurate (ReviewFailPenalty =
	// 0.5), then persist score + a self-contained timeline event.
	prof, ok := agent_trust.LoadProfile(ctx, q, workspaceID, agent.ID)
	before := agent_trust.Score(prof, ok)
	after := math.Min(agent_trust.MaxScore, math.Max(0, before-agent_trust.ReviewFailPenalty))
	if _, err := q.ApplyAgentTrustReviewFail(ctx, db.ApplyAgentTrustReviewFailParams{
		WorkspaceID: workspaceID,
		AgentID:     agent.ID,
		Score:       floatToNumericLocal(after),
	}); err != nil {
		slog.Warn("agent-self-opt: post-hoc review_fail trust event failed",
			"agent", agent.Name, "err", err)
	}
	// Ledger event row (self-contained timeline entry).
	_, _ = q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
		WorkspaceID: workspaceID,
		AgentID:     agent.ID,
		EventType:   "review_fail",
		ScoreDelta:  floatToNumericLocal(-agent_trust.ReviewFailPenalty),
		ScoreBefore: floatToNumericLocal(before),
		ScoreAfter:  floatToNumericLocal(after),
		TaskID:      e.CorrectedTaskID,
		IssueID:     pgtype.UUID{},
		Note:        pgtype.Text{String: fmt.Sprintf("post-hoc revalidation: applied edit rolled back (score %.0f)", score), Valid: true},
	})
	slog.Warn("agent-self-opt: post-hoc revalidation rolled back applied edit",
		"agent", agent.Name, "edit", e.ID, "score", score)
	return nil
}

// floatToNumericLocal converts a float64 into pgtype.Numeric via a fixed
// 1-decimal string scan (mirrors agent_trust.floatToNumeric). Local copy so
// the optimizer does not reach into the unexported helper.
func floatToNumericLocal(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.1f", v))
	return n
}

// extractScore pulls the first integer out of a JSON-ish response near the
// "score" key. Returns -1 when none found.
func extractScore(text string) float64 {
	idx := strings.Index(text, "score")
	if idx < 0 {
		return -1
	}
	// Look at the following ~40 chars for a number.
	after := text[idx : idx+40]
	start := -1
	for i, r := range after {
		if r >= '0' && r <= '9' {
			start = i
			break
		}
	}
	if start < 0 {
		return -1
	}
	end := start
	for i := start; i < len(after); i++ {
		if after[i] >= '0' && after[i] <= '9' {
			end = i + 1
		} else {
			break
		}
	}
	v := 0
	for _, r := range after[start:end] {
		v = v*10 + int(r-'0')
	}
	return clampScore(v)
}

// isRejected checks the rejection buffer: has this exact (before, after)
// pair already been rejected OR reverted? Both are hard negative experience
// (0.5.2 adversarial review d6): a user-reverted edit must never be
// re-proposed / re-applied identically — it would undo the explicit rollback.
func (o *Optimizer) isRejected(p EditProposal, rejected []db.AgentOptEdit) bool {
	for _, r := range rejected {
		// Only REJECTED + REVERTED edits are negative experience. Applied
		// edits were accepted (already in the instructions — re-proposing a
		// live edit is harmless), and suggested edits are still awaiting
		// judgment.
		switch r.Application {
		case string(ApplicationRejected), string(ApplicationReverted):
		default:
			continue
		}
		if strings.TrimSpace(r.BeforeText) == strings.TrimSpace(p.Before) &&
			strings.TrimSpace(r.AfterText) == strings.TrimSpace(p.After) {
			return true
		}
	}
	return false
}

// applyProposal applies one proposal to the current instruction text and
// returns the resulting edit record + new state. ok=false when the
// proposal is malformed for the current state (e.g. delete/replace
// targets text that is not present).
func applyProposal(current string, p EditProposal) (InstructionEdit, string, bool) {
	edit := InstructionEdit{
		EditType:   p.EditType,
		BeforeText: strings.TrimSpace(p.Before),
		AfterText:  strings.TrimSpace(p.After),
		Rationale:  strings.TrimSpace(p.Rationale),
	}
	switch p.EditType {
	case "add":
		if edit.AfterText == "" {
			return edit, current, false
		}
		if current != "" && !strings.HasSuffix(current, "\n") {
			current += "\n"
		}
		return edit, current + edit.AfterText + "\n", true
	case "delete":
		if edit.BeforeText == "" || !strings.Contains(current, edit.BeforeText) {
			return edit, current, false
		}
		remaining := strings.Replace(current, edit.BeforeText, "", 1)
		return edit, strings.TrimSpace(remaining), true
	case "replace":
		if edit.BeforeText == "" || !strings.Contains(current, edit.BeforeText) {
			return edit, current, false
		}
		remaining := strings.Replace(current, edit.BeforeText, edit.AfterText, 1)
		return edit, remaining, true
	default:
		return edit, current, false
	}
}

// buildProposalPrompt renders the evidence for one agent.
func buildProposalPrompt(ev OptimizeEvidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Agent: %s (id %s)
Current instructions:
--- start ---
%s
--- end ---

Evidence from the last scan window:
- Completed issues assigned to this agent: %d
- Trust ledger events (corrections / review failures / review passes): %d
`, ev.AgentName, util.UUIDToString(ev.AgentID),
		ev.CurrentInstructions, len(ev.Issues), len(ev.TrustEvents))

	// Design verdict §5a: auto-apply requires the candidate to trace to a
	// CONFIRMED correction. Anchoring the corrected task id in the prompt
	// lets the optimizer derive the fix from that specific task instead of
	// a window-wide signal.
	if ev.CorrectedTaskID.Valid {
		fmt.Fprintf(&b, "\nAnchoring correction: the user corrected the output of task %s for this agent. Derive any auto-appliable fix from that task's failure.\n",
			util.UUIDToString(ev.CorrectedTaskID))
	}

	if len(ev.Issues) > 0 {
		b.WriteString("\nRecent completed issue topics (sanitized, keyword-only):\n")
		for i, iss := range ev.Issues {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "- %s\n", sanitizeForPrompt(iss.Title))
		}
	}
	if len(ev.TrustEvents) > 0 {
		b.WriteString("\nTrust ledger (newest first):\n")
		for i, evt := range ev.TrustEvents {
			if i >= 10 {
				break
			}
			note := ""
			if evt.Note.Valid {
				// Correction notes are user-authored but may contain
				// arbitrary text — strip control chars + truncate (the
				// design verdict: feed signals, never verbatim untrusted
				// payloads that could inject instructions).
				note = " — " + sanitizeForPrompt(evt.Note.String)
			}
			fmt.Fprintf(&b, "- [%s] delta=%s%s\n", evt.EventType, numericString(evt.ScoreDelta), note)
		}
	}
	if len(ev.RejectedEdits) > 0 {
		b.WriteString("\nPreviously REJECTED edits (do NOT re-propose these):\n")
		for i, r := range ev.RejectedEdits {
			if i >= RejectionBufferSize {
				break
			}
			fmt.Fprintf(&b, "- [%s] before=%q after=%q\n", r.EditType, r.BeforeText, r.AfterText)
		}
	}

	b.WriteString(`
Propose up to 3 bounded instruction edits that address documented failure modes.
Each edit must be: add (append a new instruction line), delete (remove an existing line), or replace (swap one line for another).
The "before" text must match the current instructions EXACTLY for delete/replace.
Reply with ONLY a JSON array, e.g. [{"edit_type":"add","after":"...","rationale":"..."}].`)
	return b.String()
}

// parseProposals parses the optimizer JSON response into proposals.
// Tolerant: accepts a bare array, {"proposals":[...]}, or any response
// that embeds a JSON array inside markdown fences / prose (provider CLIs
// frequently wrap the payload in ```json fences).
func parseProposals(text string) ([]EditProposal, error) {
	text = strings.TrimSpace(text)
	var arr []EditProposal
	if err := json.Unmarshal([]byte(text), &arr); err == nil {
		return arr, nil
	}
	var wrapped struct {
		Proposals []EditProposal `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(text), &wrapped); err == nil {
		return wrapped.Proposals, nil
	}
	// Fence / prose fallback: extract the first '[' … ']' span.
	if start := strings.Index(text, "["); start >= 0 {
		if end := strings.LastIndex(text, "]"); end > start {
			frag := text[start : end+1]
			var inner []EditProposal
			if err := json.Unmarshal([]byte(frag), &inner); err == nil {
				return inner, nil
			}
		}
	}
	return nil, fmt.Errorf("unparseable proposal JSON")
}

// numericString renders a pgtype.Numeric compactly (for prompt text).
func numericString(n pgtype.Numeric) string {
	if n.Int == nil {
		return "0"
	}
	return n.Int.String()
}

// sanitizeForPrompt neutralizes untrusted user/DB text before it enters an
// LLM prompt (design-review security finding s1: raw issue titles / notes
// could carry a prompt-injection payload that steers the optimizer into
// emitting a malicious instruction edit). It strips control characters,
// collapses whitespace, and truncates to a short window — the optimizer
// gets the *signal*, never a verbatim payload.
func sanitizeForPrompt(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' {
			continue // drop control chars (the injection surface)
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	const max = 80
	if r := []rune(out); len(r) > max {
		out = string(r[:max]) + "…"
	}
	return out
}
