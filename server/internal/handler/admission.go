package handler

import (
	"net/http"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Unified execution-admission contract (MUL-4525).
//
// Every synchronous enqueue entry point (comment mention, autopilot manual
// "run now", issue assign / promotion / batch, manual rerun, direct chat)
// answers the SAME question in the SAME shape: given a user who explicitly
// named an execution target, did the run get `queued`, `coalesced` onto an
// existing task, `deferred`, or `blocked`? A silent no-op is never
// acceptable.
//
// Two invariants this contract exists to protect:
//
//  1. Whether the business object was written (comment saved, issue updated)
//     and whether the agent was actually triggered are DIFFERENT facts. A
//     comment can persist while one of its mentions is blocked — callers
//     must be able to express partial success.
//  2. The reason a target was blocked is exposed ONLY as a stable,
//     localizable, enumeration-safe code. It must never leak whether a
//     private agent exists, its name, or its owner to a caller who cannot
//     see the target. The precise cause goes to restricted server logs, not
//     the wire.
//
// Local adaptation: the upstream extract puts DispatchStatus /
// DispatchReasonCode behind a server/internal/dispatch/ package and aliases
// the ReasonCode via dispatch.ReasonCode. The local fork keeps the types in
// handler/ (no separate package) because (a) the existing call sites already
// live in handler/, and (b) introducing a new internal package is more
// invasive than warranted for the single-user / self-hosted scope.
//
// The ReasonCode is still type-safe and the wire format matches the
// upstream wire shape verbatim — old clients ignore the field, new clients
// localize against it.

// DispatchStatus is the domain-level outcome of one admission / enqueue attempt.
type DispatchStatus string

const (
	// DispatchQueued: a new run was enqueued.
	DispatchQueued DispatchStatus = "queued"
	// DispatchCoalesced: the trigger merged into an already-pending task for
	// the same target instead of creating a duplicate run.
	DispatchCoalesced DispatchStatus = "coalesced"
	// DispatchDeferred: admitted but intentionally not started yet (e.g. a
	// backlog issue parked until promotion, or suppress_run).
	DispatchDeferred DispatchStatus = "deferred"
	// DispatchBlocked: the run was refused. ReasonCode carries why.
	DispatchBlocked DispatchStatus = "blocked"
)

// DispatchReasonCode is the wire-facing admission reason. New codes may be
// added; clients must treat an unknown code as a generic failure (they
// switch with a default branch). A code NEVER encodes the existence, name,
// or owner of a target the caller is not allowed to see.
type DispatchReasonCode string

const (
	ReasonQueued                DispatchReasonCode = "queued"
	ReasonCoalesced             DispatchReasonCode = "coalesced"
	ReasonDeferred              DispatchReasonCode = "deferred"
	ReasonInvocationNotAllowed  DispatchReasonCode = "invocation_not_allowed"
	ReasonTargetUnavailable     DispatchReasonCode = "target_unavailable"
	ReasonRuntimeOffline        DispatchReasonCode = "runtime_offline"
	ReasonAttributionBlocked    DispatchReasonCode = "attribution_blocked"
	ReasonAlreadyActive         DispatchReasonCode = "already_active"
	ReasonSelfTriggerSuppressed DispatchReasonCode = "self_trigger_suppressed"
	// ReasonAlreadyHandled: the target was intentionally not (re-)triggered
	// because the acting agent's own prior activity already covered it and no
	// active run remains — e.g. a squad leader's self-@mention that the
	// self-trigger guard suppresses with no active task left (MUL-4525 §2
	// round-3). Not a permission block, but NOT success: nothing new runs.
	ReasonAlreadyHandled DispatchReasonCode = "already_handled"
	ReasonInternalError  DispatchReasonCode = "internal_error"
)

// DispatchTarget is the caller-visible reference to an execution target.
// Name is populated ONLY when the caller is allowed to see the target; a
// blocked private-agent invoke returns Type/ID (already known to the caller
// from their own request) but never a Name they were not otherwise entitled
// to.
type DispatchTarget struct {
	Type string `json:"type"` // "agent" | "squad"
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// DispatchOutcome is the unified per-target result returned by every sync
// enqueue entry point. It is additive on the wire: old clients that ignore
// it keep working. TaskID / RunID are set when a run / task was produced.
type DispatchOutcome struct {
	Status     DispatchStatus     `json:"status"`
	ReasonCode DispatchReasonCode `json:"reason_code"`
	Target     *DispatchTarget    `json:"target,omitempty"`
	TaskID     *string            `json:"task_id,omitempty"`
	RunID      *string            `json:"run_id,omitempty"`
}

// dispatchBlockedResponse is the structured body of a blocked synchronous
// enqueue (403 / 409). `error` is a generic, non-enumerating English
// fallback for old clients that only read the legacy field; `reason_code`
// is the stable machine-readable code new clients localize. Neither field
// leaks private target details.
type dispatchBlockedResponse struct {
	Error      string             `json:"error"`
	ReasonCode DispatchReasonCode `json:"reason_code"`
}

// writeDispatchBlocked writes a structured blocked-admission error. The HTTP
// status conveys the class (403 permission, 409 conflict); reason_code
// conveys the stable cause.
//
// NOT YET WIRED INTO CreateComment / UpdateComment / autopilot run-now /
// claude_science_lab dispatch. This commit ships the SHAPE only — the next
// batch wires the call sites one at a time so each step is independently
// typecheck-green + tests-green. See CLAUDE.md §0.3.44 integration log.
func (h *Handler) writeDispatchBlocked(w http.ResponseWriter, status int, code DispatchReasonCode) {
	writeJSON(w, status, dispatchBlockedResponse{
		Error:      dispatchBlockedFallbackMessage(code),
		ReasonCode: code,
	})
}

// dispatchBlockedFallbackMessage is the legacy `error` string paired with a
// reason code. It is intentionally generic and non-enumerating: it must be
// safe to show to a caller who is not allowed to know whether the target
// exists.
func dispatchBlockedFallbackMessage(code DispatchReasonCode) string {
	switch code {
	case ReasonInvocationNotAllowed:
		return "you don't have permission to use this target"
	case ReasonTargetUnavailable:
		return "the target is unavailable"
	case ReasonRuntimeOffline:
		return "the target's runtime is offline"
	case ReasonAttributionBlocked:
		return "the run couldn't be attributed to a responsible member"
	case ReasonAlreadyActive:
		return "a run is already active for this target"
	case ReasonSelfTriggerSuppressed:
		return "the target's own trigger was suppressed"
	case ReasonAlreadyHandled:
		// MUL-4525 §2 round-3: a self-trigger was suppressed because the
		// acting agent's own prior task already covered it; nothing new runs.
		return "the target's prior activity already handled this trigger"
	case ReasonInternalError:
		return "the run failed an internal check"
	default:
		return "the run was blocked"
	}
}

// decidePostMergeMiss is the post-merge-miss decision rule extracted from
// comment_reconcile_test.go's TestDecidePostMergeMiss (upstream). Three
// inputs (hadActiveTask bool, err error), one output triple.
//
//   - err != nil                 → fail closed: blocked / internal_error, no
//                                  fresh enqueue (a duplicate concurrent run
//                                  is worse than a missed one).
//   - err == nil && activeTask   → defer / deferred, no fresh enqueue (an
//                                  existing run already covers the merge).
//   - err == nil && !activeTask  → enqueue a fresh follow-up run.
//
// This decision (NOT the query call) governs the branch, hence the
// extraction. Pure function: trivial to unit-test, no DB or service
// dependencies.
func decidePostMergeMiss(hadActiveTask bool, err error) (DispatchStatus, DispatchReasonCode, bool) {
	if err != nil {
		// Query failure dominates any stale active=true flag — fail closed.
		return DispatchBlocked, ReasonInternalError, false
	}
	if hadActiveTask {
		return DispatchDeferred, ReasonDeferred, false
	}
	// No active task and no error: a fresh follow-up run is the right
	// behavior (status / reason_code are not meaningful for the "fresh
	// enqueue" branch — the caller will fill them in once the new task id
	// is known).
	return "", "", true
}

// ensure import side-effects: protocol is referenced in the comment block
// above and may be used by future wiring commits; keep the import live.
var _ = protocol.EventChatSessionUpdated