// Package agent_self_optimization — optimizer.go (0.5.2, generalized 0.5.3).
//
// SkillOpt-style instruction optimizer for agents. Inspired by
// microsoft/SkillOpt (text-space optimizer: the skill document is the
// trainable state of a frozen agent; a separate optimizer model turns
// scored rollouts into bounded add/delete/replace edits, and a candidate
// edit is accepted only when validation improves) and by the OpenMythos
// convergence intuition (loop until no further improvement — spectral
// radius < 1 analog).
//
// 0.5.3 generalization: the optimizable subject is polymorphic.
//   - agent     : agent.instructions      (trainable text, full machinery)
//   - skill     : skill.content           (SKILL.md body)
//   - squad     : squad.instructions      (migration 088)
//   - autopilot : autopilot.issue_title_template
//
// Trust semantics (user spec, 2026-08-02): a correction drops trust by
// 0.5-1; LOW-trust subjects are the ones that need optimization (the
// system fixes what the user corrected), and trust ≥ 8 parks the subject
// in RETENTION — no more proposals ("当信用到8以上，视为可以不用再进化的
// 保留机制"). This inverts the 0.5.2 gate, which required HIGH trust for
// auto-apply; 0.5.3 auto-apply requires a correction anchor instead
// (the fix derives from a documented failure), plus validation.
//
// The loop:
//
//  1. Gather evidence per subject: done issues it worked on in the window
//     + trust ledger (corrections / review outcomes) + its current
//     instruction text.
//  2. An optimizer LLM proposes ≤ MaxEditsPerRun bounded edits
//     (add / delete / replace) as structured JSON, each with a rationale
//     tied to concrete evidence.
//  3. Rejection buffer: any proposal whose (before, after) pair matches a
//     previously REJECTED edit is skipped — the loop must not re-propose
//     negative experience.
//  4. Validation gate: a second LLM call scores the DELTA (the proposed
//     change, not the whole document) 0-100. Only accepted edits are
//     written back to the subject's text.
//  5. Every proposal (accepted or rejected) lands in agent_opt_edit so
//     the history is the experience base for the next run.
//
// Degradation: when no provider CLI is available the optimizer returns
// ErrNoLLM and the runner falls back to the existing heuristic
// suggestions — the lab still works without an LLM, it just cannot edit
// instruction text.
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

// MaxEditsPerRun caps the optimizer proposals per subject per run. Small
// and bounded — a text-space optimizer must not rewrite an instruction
// set wholesale in one pass.
const MaxEditsPerRun = 3

// RejectionBufferSize is how many past rejected edits per subject are fed
// to the optimizer as negative experience.
const RejectionBufferSize = 10

// ErrNoLLM is returned when no provider CLI is available; callers fall
// back to the heuristic path.
var ErrNoLLM = fmt.Errorf("optimizer: no provider CLI available")

// ---- Subject types (0.5.3) ----
const (
	// SubjectAgent is the classic 0.5.2 subject: agent.instructions.
	SubjectAgent = "agent"
	// SubjectSkill optimizes skill.content (the SKILL.md body).
	SubjectSkill = "skill"
	// SubjectSquad optimizes squad.instructions (migration 088).
	SubjectSquad = "squad"
	// SubjectAutopilot optimizes autopilot.issue_title_template.
	SubjectAutopilot = "autopilot"
)

// ---- Optimization scopes (0.5.3) ----
const (
	// ScopeEnroll: the subject opted into auto-apply (agent marker, or a
	// non-agent subject proposed under the lab's general consent). The
	// classic 0.5.2 gate.
	ScopeEnroll = "enroll"
	// ScopeTrust: trust-driven optimization. The subject has negative
	// evidence (corrections / failed reviews) and a score below
	// OptimizeTrustThreshold — the system is mandated to fix it. Auto-apply
	// is allowed without the enrollment marker, but still requires a
	// correction anchor + validation.
	ScopeTrust = "trust"
	// ScopeRetain: retention mode — trust ≥ RetainTrustScore. The subject
	// is parked; NO proposals are made. (Ledger rows under this scope are
	// not written; the report marks the park decision.)
	ScopeRetain = "retain"
)

// RetainTrustScore is the retention threshold: a subject at trust ≥ 8 is
// considered "no longer needs evolution" and receives NO proposals
// (user spec: "当信用到8以上，视为可以不用再进化的保留机制").
const RetainTrustScore = 8.0

// OptimizeTrustThreshold: trust < 7 with negative evidence → the subject
// enters trust-scope optimization (system mandate, marker not required).
const OptimizeTrustThreshold = 7.0

// Application is the lifecycle state of an instruction edit.
type Application string

const (
	// ApplicationApplied — auto-applied (add-only, correction-backed,
	// gates passed) or manually applied; written back to the subject's text.
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
// auto-applied, on top of the hard gates (scope + correction-backed +
// not lab-managed/hard-blocked + rate cap 1/run + snapshot/rollback
// committed).
const AutoApplyGate = 90.0

// SuggestedOrderingCredit is the +5 cap on ordering INSIDE the 'suggested'
// queue for a correction-backed candidate. It can never lift anything
// across the auto-apply line — the auto-apply gate reads the raw score.
const SuggestedOrderingCredit = 5.0

// SuggestionExpiryRuns is how many weekly runs (~21 days) a suggestion
// may wait before it expires to 'ignored' (soft archive, NOT rejected).
const SuggestionExpiryRuns = 3

// MaxAutoAppliesPerAgentPerRun caps auto-applied edits per subject per run
// (rate / amplitude cap). After a post-hoc validation failure or a user
// rejection of that subject's edit, auto-apply halts for the window.
const MaxAutoAppliesPerAgentPerRun = 1

// HardBlockedSafetyTokens are substrings that mark instruction text as a
// safety/constraint rule. Any edit touching text containing these tokens
// is excluded from auto-apply and must go through human confirmation —
// regardless of score or edit type.
//
// 0.5.3 narrowing: 必须 / 不得 were removed — they are high-frequency
// instruction verbs in Chinese agent prompts (e.g. "必须完成任务"), so
// keeping them blocked every add edit that happens to restate a
// requirement. The remaining tokens are the genuinely safety-sensitive
// ones (deny/never/confirm/secret + prohibitions).
var HardBlockedSafetyTokens = []string{
	"deny", "never", "confirm", "secret",
	"禁止", "不可",
}

// InstructionEdit is one proposed / recorded instruction edit.
type InstructionEdit struct {
	// TargetType identifies the subject kind (agent | skill | squad |
	// autopilot). TargetID is the subject row id (for agents it is the
	// agent id).
	TargetType string
	TargetID   pgtype.UUID
	// Scope records which optimization scope proposed this edit (enroll |
	// trust). Persisted to agent_opt_edit.subject_scope.
	Scope string
	// TargetName is the human-readable subject name (report / ledger).
	TargetName string
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
	// edit (the DELTA, 0.5.3 — not the whole instruction set).
	ValidationScore float64
	// ValidationReason is the validator's one-line justification.
	ValidationReason string
	// CorrectedTaskID, when set, marks the edit as correction-backed: the
	// candidate is derivable from a task whose output the user corrected.
	// Auto-apply requires this.
	CorrectedTaskID pgtype.UUID
	// Snapshot is the full instruction text BEFORE this edit was applied
	// (the rollback point). Set only when the edit is applied.
	Snapshot string
	// AgentID is kept for backward compat (agent edits only); new code
	// reads TargetID.
	AgentID pgtype.UUID
}

// EditProposal is the wire shape the optimizer LLM returns.
type EditProposal struct {
	EditType  string `json:"edit_type"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Rationale string `json:"rationale"`
}

// OptimizeEvidence is what the optimizer needs per subject.
type OptimizeEvidence struct {
	// TargetType + TargetID identify the subject (agent | skill | squad |
	// autopilot). For agents, TargetID is the agent id.
	TargetType string
	TargetID   pgtype.UUID
	TargetName string
	// CurrentText is the subject's trainable instruction text.
	CurrentText string
	// Issues are done issues assigned to this subject (agents only).
	Issues []db.ListDoneIssuesForSelfOptRow
	// TrustEvents (agents only).
	TrustEvents []db.AgentTrustEvent
	// RejectedEdits — past rejected proposals (buffer).
	RejectedEdits []db.AgentOptEdit
	// AutoApplyEnrolled is the per-subject opt-in for auto-apply (agent
	// enrollment marker). Trust-scope subjects ignore this (system
	// mandate).
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
	LabManagedFunc func(ctx context.Context, q *db.Queries, targetType string, targetID pgtype.UUID) (bool, error)
}

// NewOptimizer returns an Optimizer wired to the real provider CLIs.
func NewOptimizer() *Optimizer {
	o := &Optimizer{
		ProviderLLM:  agent_trust.RunProviderLLM,
		ValidatorLLM: agent_trust.RunProviderLLM,
	}
	o.LabManagedFunc = func(ctx context.Context, q *db.Queries, targetType string, targetID pgtype.UUID) (bool, error) {
		return isLabManagedSubject(ctx, q, targetType, targetID)
	}
	return o
}

// subjectTrust returns (currentTrustScore, hasNegativeEvidence).
// hasNegativeEvidence is true when the ledger contains a correction or a
// failed review — the trigger for trust-scope optimization.
func subjectTrust(events []db.AgentTrustEvent) (float64, bool) {
	var last float64
	for _, evt := range events {
		if evt.ScoreAfter.Valid {
			if v, ok := numericToFloat(evt.ScoreAfter); ok {
				last = v
				break // newest first
			}
		}
	}
	neg := false
	for _, evt := range events {
		if evt.EventType == "correction" || evt.EventType == "review_fail" {
			neg = true
			break
		}
	}
	return last, neg
}

// OptimizeSubject produces bounded instruction edits for one subject and
// classifies each by the design-review verdict:
//
//   - applied: ADD-only, correction-backed, gates passed, score ≥ 90.
//     The caller writes them back (cumulative finalText) and snapshots the
//     pre-edit set. Trust-scope subjects (low trust + negative evidence)
//     skip the enrollment marker requirement; non-agent subjects are
//     NEVER auto-applied (suggested-only — their edit has no trust
//     ledger to anchor a system mandate).
//   - suggested: score ≥ 60 but not auto-applied (delete/replace by
//     construction, or gate miss, or score < 90). Parked for human
//     confirmation.
//   - rejected: score < 60, or matching a prior rejection (negative
//     experience).
//
// Retention: subjects at trust ≥ RetainTrustScore are parked — no
// proposals at all.
//
// finalText is the cumulative state after all APPLIED edits only;
// suggested edits are NOT included (they wait for human approval).
func (o *Optimizer) OptimizeSubject(
	ctx context.Context,
	q *db.Queries,
	ev OptimizeEvidence,
) (applied, suggested, rejected []InstructionEdit, finalText string, err error) {
	// Evidence gate: agents need issues/trust events; non-agent subjects
	// are always eligible (their evidence is the current text itself).
	if ev.TargetType == SubjectAgent && len(ev.Issues) == 0 && len(ev.TrustEvents) == 0 {
		// No evidence → no edits. The runner's deferral gate usually
		// catches this earlier, but a per-subject gap is fine.
		return nil, nil, nil, ev.CurrentText, nil
	}

	// Retention gate (0.5.3): trust ≥ 8 → parked, no proposals.
	if ev.TargetType == SubjectAgent {
		trustScore, _ := subjectTrust(ev.TrustEvents)
		if trustScore >= RetainTrustScore {
			slog.Info("agent-self-opt: subject in retention (trust ≥ 8); skipping proposals",
				"subject", ev.TargetName, "type", ev.TargetType, "trust", trustScore)
			return nil, nil, nil, ev.CurrentText, nil
		}
	}

	proposals, err := o.proposeEdits(ctx, ev)
	if err != nil {
		return nil, nil, nil, ev.CurrentText, err
	}
	if len(proposals) == 0 {
		return nil, nil, nil, ev.CurrentText, nil
	}

	// Scope resolution (0.5.3): trust < OptimizeTrustThreshold with
	// negative evidence → trust scope (system mandate, marker not
	// required). Otherwise enroll scope (marker-gated).
	scope := ScopeEnroll
	if ev.TargetType == SubjectAgent {
		trustScore, neg := subjectTrust(ev.TrustEvents)
		if neg && trustScore < OptimizeTrustThreshold {
			scope = ScopeTrust
		}
	}

	// Auto-apply eligibility gates. Lab-managed exclusion: a subject
	// hidden by any lab's experimental_resource_visibility is part of a
	// manifest/install/dispatch contract — never auto-edit it.
	labManaged, lerr := o.LabManagedFunc(ctx, q, ev.TargetType, ev.TargetID)
	if lerr != nil {
		slog.Warn("agent-self-opt: lab-managed check failed; defaulting to not-auto-apply",
			"subject", ev.TargetName, "err", lerr)
		labManaged = true // fail closed
	}
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

	candidate := ev.CurrentText
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
		edit.TargetType = ev.TargetType
		edit.TargetID = ev.TargetID
		edit.TargetName = ev.TargetName
		edit.Scope = scope
		if ev.TargetType == SubjectAgent {
			edit.AgentID = ev.TargetID
		}

		// Validation: score the DELTA (the proposed change) vs the current
		// text, 0-100.
		score, reason, verr := o.validate(ctx, ev, candidate, newState, p)
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

		// Hard gates: lab-managed + correction-backed + no safety-token
		// content. Enrollment applies ONLY to the enroll scope (trust
		// scope is a system mandate; the user's own spec directs the
		// system to fix low-trust subjects).
		canAutoApply = canAutoApply &&
			!labManaged &&
			correctionBacked &&
			!touchesSafetyToken(p.Before) &&
			!touchesSafetyToken(p.After)
		if scope == ScopeEnroll {
			canAutoApply = canAutoApply && enrolled
		}

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

// OptimizeAgent is the backward-compat alias for the classic agent path
// (0.5.2 callers). It builds an agent-shaped OptimizeEvidence and
// delegates to OptimizeSubject.
func (o *Optimizer) OptimizeAgent(
	ctx context.Context,
	q *db.Queries,
	ev OptimizeEvidence,
) (applied, suggested, rejected []InstructionEdit, finalText string, err error) {
	ev.TargetType = SubjectAgent
	return o.OptimizeSubject(ctx, q, ev)
}

// isLabManagedSubject reports whether the subject is hidden by any lab's
// experimental_resource_visibility row (i.e. it is lab infrastructure and
// must not be auto-edited).
func isLabManagedSubject(ctx context.Context, q *db.Queries, targetType string, targetID pgtype.UUID) (bool, error) {
	kind, ok := map[string]experimental.HideableResource{
		SubjectAgent:     experimental.HideAgent,
		SubjectSkill:     experimental.HideSkill,
		SubjectSquad:     experimental.HideSquad,
		SubjectAutopilot: experimental.HideAutopilot,
	}[targetType]
	if !ok {
		return false, nil
	}
	for _, flagKey := range experimental.AllFlagKeys() {
		ids, err := experimental.HiddenResourceIDsByFlag(ctx, q, flagKey, kind)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			if id == uuid.UUID(targetID.Bytes) {
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
			"subject", ev.TargetName, "err", perr)
		return nil, nil
	}
	// Bound the proposals.
	if len(parsed) > MaxEditsPerRun {
		parsed = parsed[:MaxEditsPerRun]
	}
	return parsed, nil
}

// validate scores the candidate edit (the DELTA) against the current text
// on a 0-100 scale. The validator LLM returns a JSON object:
//
//	{"score": 0-100, "reason": "<one line>"}
//
// 0.5.3 incremental scoring: the rubric targets the CHANGE (added /
// removed / replaced lines), not the whole instruction set. Scoring the
// whole document made auto-apply unreachable in practice — a well-grounded
// two-line add could never lift a 2000-character instruction set to 90+.
// Higher score = the delta is strongly grounded and cannot regress other
// behavior; a score near 0 means the change is speculative or harmful.
func (o *Optimizer) validate(
	ctx context.Context,
	ev OptimizeEvidence,
	current, candidate string,
	p EditProposal,
) (float64, string, error) {
	prompt := fmt.Sprintf(`Subject "%s" (%s) currently has the following instruction text:

--- current text start ---
%s
--- current text end ---

A proposed change (the DELTA) is:

--- proposed change start ---
%s
--- proposed change end ---

Context: the subject produced %d completed issues in the scan window and %d trust events (corrections / review failures are signals of mistakes).

Score how strongly THIS DELTA improves the current text, on a 0-100 scale. Score the delta only — a change that fixes a documented failure mode scores high even if the surrounding text is long; a change that is speculative, contradicts existing instructions, or could regress established behavior scores low.
Scoring rubric:
- 85-100: the change removes or mitigates a DOCUMENTED failure mode (a user correction or review failure), is precisely scoped, and cannot regress other behavior.
- 65-84: the change is clearly grounded in the evidence and improves clarity / correctness, with minor residual risk.
- 40-64: the change is plausibly useful but partially speculative or redundant.
- 0-39: the change is speculative, contradicts existing instructions, or could regress established behavior.
Reply with ONLY a JSON object: {"score": <int 0-100>, "reason": "<one line, English>"}. No prose.`,
		ev.TargetName, ev.TargetType, current, deltaDescription(p), len(ev.Issues), len(ev.TrustEvents))

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

// deltaDescription renders the proposed change as a compact text block for
// the validation prompt (the "DELTA").
func deltaDescription(p EditProposal) string {
	switch p.EditType {
	case "add":
		return "ADD:\n" + strings.TrimSpace(p.After)
	case "delete":
		return "DELETE:\n" + strings.TrimSpace(p.Before)
	case "replace":
		return "REPLACE:\n" + strings.TrimSpace(p.Before) + "\n--- with ---\n" + strings.TrimSpace(p.After)
	default:
		return "UNKNOWN EDIT TYPE"
	}
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
// For each applied edit of the subject (newest first, rate-capped at 1/run so
// at most one needs re-scoring), it:
//
//   - re-fetches the subject's CURRENT instruction text,
//   - if the edit's AfterText is no longer present (a later manual edit
//     removed it) the edit is considered already superseded — no re-score,
//   - otherwise re-scores CURRENT text vs the SNAPSHOT (pre-edit): a low
//     score means the applied edit made things measurably worse, so the
//     edit is reverted to its snapshot and the rollback is recorded as
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
	targetType string,
	targetID pgtype.UUID,
	targetName, currentText string,
	workspaceID pgtype.UUID,
) error {
	if !targetID.Valid {
		return nil
	}
	applied, err := q.ListAppliedAgentOptEdits(ctx, db.ListAppliedAgentOptEditsParams{
		TargetType:  targetType,
		TargetID:    targetID,
		WorkspaceID: workspaceID,
		Limit:       5,
	})
	if err != nil {
		return fmt.Errorf("revalidate applied edits: list: %w", err)
	}
	cur := currentText
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
			TargetName:  targetName,
			TargetType:  targetType,
			CurrentText: snapshot,
		}, snapshot, cur, EditProposal{})
		if verr != nil {
			// Gate failure (no LLM / timeout) — skip; the next run re-tries.
			continue
		}
		if score >= RevalidateRetainFloor {
			continue
		}
		if err := o.rollbackAppliedEdit(ctx, q, targetType, targetID, targetName, workspaceID, e, score); err != nil {
			slog.Warn("agent-self-opt: post-hoc rollback failed",
				"subject", targetName, "edit", e.ID, "err", err)
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
// self-review loop's punishment half. For non-agent subjects the trust
// event is skipped (no trust ledger exists for them).
func (o *Optimizer) rollbackAppliedEdit(
	ctx context.Context,
	q *db.Queries,
	targetType string,
	targetID pgtype.UUID,
	targetName string,
	workspaceID pgtype.UUID,
	e db.AgentOptEdit,
	score float64,
) error {
	if !e.InstructionsSnapshot.Valid {
		return fmt.Errorf("applied edit %s has no snapshot; cannot roll back", uuid.UUID(e.ID.Bytes).String())
	}
	if err := writeBackText(ctx, q, targetType, targetID, e.InstructionsSnapshot.String); err != nil {
		return fmt.Errorf("restore snapshot: %w", err)
	}
	if _, err := q.UpdateAgentOptEditApplication(ctx, db.UpdateAgentOptEditApplicationParams{
		ID:          e.ID,
		Application: string(ApplicationReverted),
		WorkspaceID: workspaceID,
	}); err != nil {
		return fmt.Errorf("mark reverted: %w", err)
	}
	if targetType == SubjectAgent {
		// Trust event: the self-review found the applied edit regressive →
		// -0.5 (same punishment as a user correction / failed review).
		prof, ok := agent_trust.LoadProfile(ctx, q, workspaceID, targetID)
		before := agent_trust.Score(prof, ok)
		after := math.Min(agent_trust.MaxScore, math.Max(0, before-agent_trust.ReviewFailPenalty))
		if _, err := q.ApplyAgentTrustReviewFail(ctx, db.ApplyAgentTrustReviewFailParams{
			WorkspaceID: workspaceID,
			AgentID:     targetID,
			Score:       floatToNumericLocal(after),
		}); err != nil {
			slog.Warn("agent-self-opt: post-hoc review_fail trust event failed",
				"subject", targetName, "err", err)
		}
		// Ledger event row (self-contained timeline entry).
		_, _ = q.CreateAgentTrustEvent(ctx, db.CreateAgentTrustEventParams{
			WorkspaceID: workspaceID,
			AgentID:     targetID,
			EventType:   "review_fail",
			ScoreDelta:  floatToNumericLocal(-agent_trust.ReviewFailPenalty),
			ScoreBefore: floatToNumericLocal(before),
			ScoreAfter:  floatToNumericLocal(after),
			TaskID:      e.CorrectedTaskID,
			IssueID:     pgtype.UUID{},
			Note:        pgtype.Text{String: fmt.Sprintf("post-hoc revalidation: applied edit rolled back (score %.0f)", score), Valid: true},
		})
	}
	slog.Warn("agent-self-opt: post-hoc revalidation rolled back applied edit",
		"subject", targetName, "edit", e.ID, "score", score)
	return nil
}

// writeBackText persists the new instruction text onto the subject row.
// Autopilot writes only the issue_title_template (description untouched).
func writeBackText(ctx context.Context, q *db.Queries, targetType string, targetID pgtype.UUID, text string) error {
	switch targetType {
	case SubjectAgent:
		_, err := q.UpdateAgent(ctx, db.UpdateAgentParams{
			ID:           targetID,
			Instructions: pgtype.Text{String: text, Valid: true},
		})
		return err
	case SubjectSkill:
		// Workspace is not available here; the runner passes it via the
		// caller's queries. Skills/squads/autopilots are never auto-applied
		// (suggested-only), so write-back only happens on manual apply /
		// revert paths, which resolve the workspace separately.
		return fmt.Errorf("writeBackText: %s requires workspace (use edits.go ApplyEdit)", targetType)
	case SubjectSquad:
		return fmt.Errorf("writeBackText: %s requires workspace (use edits.go ApplyEdit)", targetType)
	case SubjectAutopilot:
		return fmt.Errorf("writeBackText: %s requires workspace (use edits.go ApplyEdit)", targetType)
	default:
		return fmt.Errorf("writeBackText: unknown subject type %q", targetType)
	}
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

// buildProposalPrompt renders the evidence for one subject. The prompt is
// per-target-type: agents get issue + trust evidence; skills / squads /
// autopilots get the text itself (editorial improvements, human-confirmed).
func buildProposalPrompt(ev OptimizeEvidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Subject: %s (type %s, id %s)
Current instruction text:
--- start ---
%s
--- end ---
`, ev.TargetName, ev.TargetType, util.UUIDToString(ev.TargetID), ev.CurrentText)

	switch ev.TargetType {
	case SubjectAgent:
		fmt.Fprintf(&b, `
Evidence from the last scan window:
- Completed issues assigned to this subject: %d
- Trust ledger events (corrections / review failures / review passes): %d
`, len(ev.Issues), len(ev.TrustEvents))

		// Design verdict §5a: auto-apply requires the candidate to trace to a
		// CONFIRMED correction. Anchoring the corrected task id in the prompt
		// lets the optimizer derive the fix from that specific task instead of
		// a window-wide signal.
		if ev.CorrectedTaskID.Valid {
			fmt.Fprintf(&b, "\nAnchoring correction: the user corrected the output of task %s for this subject. Derive any auto-appliable fix from that task's failure.\n",
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
	default:
		// skill / squad / autopilot: editorial improvement target. The
		// optimizer improves clarity / structure / completeness of the
		// instruction text itself; every edit lands in the human-confirm
		// tier (no trust ledger exists to mandate auto-apply).
		b.WriteString(`
This subject's instruction text is the trainable state. Propose edits that
improve its clarity, structure, and actionable guidance — as if a senior
engineer were reviewing the text. Do NOT invent external facts; base edits
only on what the text itself implies.
`)
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
