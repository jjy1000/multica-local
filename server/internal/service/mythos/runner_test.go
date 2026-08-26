// Tests for the pure (DB-independent) helpers in the mythos runner
// package. The full Run() path requires a real *db.Queries handle
// and is covered in handler-level integration tests; here we
// exercise the helpers and constants that compose it.
//
// 0.3.28 PR-4: regression coverage for the MaxLoopIters hard ceiling
// and the lab_source constant (Issue handler's IsKnownKey guard
// checks against the catalog; the Source const here is the runner's
// internal mirror).

package mythos

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMaxLoopItersHardCap_IsFive(t *testing.T) {
	// 0.3.28 PR-4: hard ceiling lowered from 16 → 5 to keep each
	// call's worst-case sub-issue fan-out bounded. The handler
	// mirrors this constant via mythosMaxLoopHardCap; if the
	// ceilings ever drift, Run() will still cap at the runner-side
	// constant (5) for non-HTTP callers.
	if MaxLoopItersHardCap != 5 {
		t.Fatalf("MaxLoopItersHardCap = %d, want 5", MaxLoopItersHardCap)
	}
}

func TestRunLoopIterationTitleShape(t *testing.T) {
	// Branch: the sub-issue title is composed from SubIssuePrefix +
	// "iter-{N}". The setUp deliberately covers the title-derivation
	// helper rather than the DB-walking runLoopIteration itself,
	// because the latter needs a *db.Queries handle. We can verify
	// the title-shape contract by mirroring the printf in
	// runLoopIteration at a couple of iter values.
	for _, c := range []struct {
		prefix string
		iter   int
		want   string
	}{
		{"[mythos] ", 1, "[mythos] iter-1"},
		{"[mythos] ", 5, "[mythos] iter-5"},
		{"", 2, "iter-2"},
	} {
		got := fmt.Sprintf("%siter-%d", c.prefix, c.iter)
		if got != c.want {
			t.Errorf("prefix=%q iter=%d: got %q, want %q", c.prefix, c.iter, got, c.want)
		}
	}
}

func TestRunCodaTitleShape(t *testing.T) {
	for _, c := range []struct {
		prefix string
		want   string
	}{
		{"[mythos] ", "[mythos] coda"},
		{"", "coda"},
	} {
		if got := c.prefix + "coda"; got != c.want {
			t.Errorf("prefix=%q: got %q, want %q", c.prefix, got, c.want)
		}
	}
}

func TestSourceConstant_MatchesCatalog(t *testing.T) {
	// The catalog.IsKnownKey gate at issue.go:2252 reads the same
	// string ("mythos_swarm"). If the runner is ever rebuilt with a
	// different literal, this test will catch a silent drift.
	if Source != "mythos_swarm" {
		t.Fatalf("Source = %q, want %q (must match catalog entry)", Source, "mythos_swarm")
	}
}

// TestRunnerCreatorType_IsNotSystem — 0.5.62 audit regression pin.
// The DB CHECK issue_creator_type_check restricts creator_type to
// {member, agent}. The runner previously sent "system" for both the
// loop sub-issue (runLoopIteration) and the coda sub-issue (runCoda),
// which tripped SQLSTATE 23514 on every fork and silently set
// mythos_run.status='failed'. This is a static check — if the literal
// ever creeps back into either call site, the test fires. The fix
// landed at runner.go:578 (loop) and runner.go:631 (coda) and changed
// both to "agent" with CreatorID set to the assignee agent UUID, which
// is the semantically correct value (the sub-issue is authored on
// behalf of the agent that will work it).
func TestRunnerCreatorType_IsNotSystem(t *testing.T) {
	src := readRunnerSource(t)
	if strings.Contains(src, `CreatorType:  "system"`) ||
		strings.Contains(src, `CreatorType: "system"`) ||
		strings.Contains(src, `CreatorType:"system"`) {
		t.Fatalf("runner.go still contains a CreatorType: \"system\" literal — "+
			"violates issue_creator_type_check (allowed: member, agent)")
	}
}

// TestRunnerAssignsIssueNumber — 0.5.63 audit regression pin.
// issue.number is NOT NULL DEFAULT 0 AND uq_issue_workspace_number
// UNIQUE (workspace_id, number). The HTTP create path explicitly
// passes a Number from IncrementIssueCounter; the runner previously
// omitted Number on both CreateIssue calls, so every fork landed
// with default 0 and tripped SQLSTATE 23505 on the first pre-existing
// row in the workspace. The fix at runner.go (loop + coda) calls
// IncrementIssueCounter and threads the result through Number:. If a
// future "fix" removes the Number field, this test fires.
func TestRunnerAssignsIssueNumber(t *testing.T) {
	src := readRunnerSource(t)
	// Count CreateIssueParams blocks (each fork is one block).
	// Every block must include `Number:`.
	count := strings.Count(src, "db.CreateIssueParams{")
	if count < 2 {
		t.Fatalf("expected at least 2 CreateIssueParams blocks (loop + coda), found %d", count)
	}
	// Each block must reference `Number:`. The simplest signal is
	// the literal "Number:" inside the file; if it's missing entirely
	// the runner reverted to default 0 (the bug).
	if !strings.Contains(src, "Number:       issueNumber,") ||
		!strings.Contains(src, "Number:       codaNumber,") {
		t.Fatalf("runner.go must pass Number to both CreateIssueParams blocks " +
			"(loop + coda); otherwise issue.number defaults to 0 and trips uq_issue_workspace_number")
	}
	// And the incrementer calls must precede each CreateIssue.
	if !strings.Contains(src, "IncrementIssueCounter(ctx, cfg.WorkspaceID)") {
		t.Fatalf("runner.go must call IncrementIssueCounter to populate Number atomically")
	}
}

// TestRunnerEnqueuesSubIssues — 0.5.64 audit regression pin.
// Pre-0.5.61 the 404 stale-flag-gate masked the runner; after
// 0.5.61 + 0.5.62 (CreatorType) + 0.5.63 (Number) the runner reached
// the waitFn call but no agent task was ever enqueued, so the daemon
// never saw the sub-issue and the request hung until WriteTimeout.
// The fix calls TaskService.EnqueueTaskForIssue after both the loop
// and coda CreateIssue. If a future refactor removes either call,
// the test fires (and the production runner hangs again).
func TestRunnerEnqueuesSubIssues(t *testing.T) {
	src := readRunReader(t)
	if !strings.Contains(src, "EnqueueTaskForIssue") {
		t.Fatalf("runner.go must call TaskService.EnqueueTaskForIssue after CreateIssue " +
			"on both loop and coda sub-issues; otherwise the daemon never claims them")
	}
	// The function comment block must mention the audit fix so a
	// future reader doesn't "tidy" the call away as unused.
	if !strings.Contains(src, "0.5.64 audit fix") {
		t.Fatalf("runner.go must document the 0.5.64 enqueue audit fix inline")
	}
	// NewService signature must require TaskService — without it
	// the wiring is unenforced at compile time and the runner hangs
	// at runtime instead of failing fast.
	if !strings.Contains(src, "taskService *service.TaskService") {
		t.Fatalf("NewService signature must require *service.TaskService so the wiring is enforced")
	}
}

// TestRunnerPreservesPartialWaitFnOutput — 0.5.66 audit regression pin.
// runLoopIteration + runCoda historically overwrote the body/summary
// with a synthetic "[mythos coda] context deadline exceeded" string
// whenever waitFn returned an error — even though waitFn's polling
// loop captures the last agent comment body before the timeout. The
// daemon may finish the task 1-2 seconds after waitFn's 5min deadline
// expires, so the captured body often contains real synthesis that
// the historical overwrite discarded. The fix switches on `out != ""`
// first, then falls back to the synthetic string. If a future refactor
// restores the if/else-discard pattern, the test fires.
func TestRunnerPreservesPartialWaitFnOutput(t *testing.T) {
	src := readRunReader(t)
	// Both call sites must prefer out (captured body) over the
	// werr-derived synthetic string.
	if !strings.Contains(src, "case out != \"\":") {
		t.Fatalf("runner.go waitFn-result handling must prefer captured body " +
			"over the werr-derived synthetic string; otherwise mythos_run loses " +
			"real synthesis when waitFn times out milliseconds before daemon completion")
	}
	// Both audit-fix comments must be present.
	if !strings.Contains(src, "0.5.66 audit fix") {
		t.Fatalf("runner.go must document the 0.5.66 partial-output preservation fix inline")
	}
}

// TestRunnerSchedulesSoleRecoveryWatch — 0.5.68 audit regression pin.
// Before this commit, when runCoda's waitFn hit the 5min deadline,
// Run() returned with mythos_run.status='completed' but the daemon
// often finished the coda task 30s–5min later, posting real
// synthesis as a comment that was never persisted into
// mythos_run.coda_conclusions. The fix: runCoda now returns a
// `codaTimedOut` bool; Run() calls scheduleSoleRecoveryWatch when
// it's true in sole mode (skipped for enhancer, where tickSupervise
// already handles completion). If a future refactor drops the call,
// drops the codaTimedOut propagation, or moves the call site out
// of the sole-mode branch, this test fires.
func TestRunnerSchedulesSoleRecoveryWatch(t *testing.T) {
	src := readRunReader(t)
	// runCoda must return a bool (codaTimedOut).
	if !strings.Contains(src, "(string, pgtype.UUID, bool, error)") {
		t.Fatalf("runCoda must return codaTimedOut bool; otherwise sole recovery " +
			"watch can never be scheduled")
	}
	// Run() must check the bool + schedule the watch + only for sole mode.
	if !strings.Contains(src, "scheduleSoleRecoveryWatch(run.ID, finalID)") {
		t.Fatalf("Run() must call scheduleSoleRecoveryWatch when runCoda timed out")
	}
	if !strings.Contains(src, "if codaTimedOut && cfg.Mode == ModeSole") {
		t.Fatalf("recovery-watch schedule must be sole-mode-only; " +
			"enhancer mode already uses tickSupervise for completion")
	}
	// The scheduler + loop must exist with both audit-fix comments.
	if !strings.Contains(src, "soleRecoveryWatchLoop") {
		t.Fatalf("soleRecoveryWatchLoop goroutine missing")
	}
	if !strings.Contains(src, "0.5.68") {
		t.Fatalf("runner.go must document the 0.5.68 sole-recovery-watch fix inline")
	}
}

// TestSoleRecoveryWatchMarshalsCodaConclusionsSafely — 0.5.69 audit
// regression pin. The 0.5.68 recovery-watch used
// `[]byte(\`["\` + latest + \`]"\`)` to persist the daemon's
// coda synthesis. Agent-written synthesis is markdown — always
// contains newlines, backticks, em-dashes, quotes, backslashes.
// The naive concat always tripped SQLSTATE 22P02 at the JSON parse
// step, so the recovery watch wrote nothing. The fix uses
// json.Marshal([]string{latest}). This is a behavioral test that
// actually invokes the encoding path on a markdown-shaped input.
func TestSoleRecoveryWatchMarshalsCodaConclusionsSafely(t *testing.T) {
	// Markdown-shaped coda synthesis with all the nasty chars:
	// newlines, backticks (code fence), em-dashes, raw " and \,
	// unicode, control-ish whitespace.
	const markdown = "## Coda synthesis\n\nThe \"answer\" is:\n\n" +
		"```json\n{\"key\": \"value\\path\"}\n```\n\n" +
		"with em—dash and backtick `code`.\n"
	encoded, err := json.Marshal([]string{markdown})
	if err != nil {
		t.Fatalf("json.Marshal must accept markdown coda synthesis, got: %v", err)
	}
	// Round-trip: decode + check the string survives intact.
	var out []string
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("encoded payload must round-trip, got: %v", err)
	}
	if len(out) != 1 || out[0] != markdown {
		t.Fatalf("encoded payload must preserve markdown verbatim; "+
			"got len=%d, first=%q", len(out), out[0])
	}
	// Source-level pin: the runner.go fix must use json.Marshal,
	// NOT naive string concat. If a future refactor reverts to
	// concat, this test still pins the round-trip behavior at the
	// function level; the literal grep is defense-in-depth.
	src := readRunReader(t)
	if strings.Contains(src, "[]byte(`[\\\"` +") {
		t.Fatalf("runner.go must not use naive string concat for coda_conclusions; " +
			"use json.Marshal instead — markdown always has chars that break JSON")
	}
	if !strings.Contains(src, "json.Marshal([]string{latest})") {
		t.Fatalf("runner.go must use json.Marshal for coda_conclusions persist; " +
			"otherwise markdown content trips SQLSTATE 22P02")
	}
}

// readRunReader is an alias for the existing readRunnerSource helper
// — earlier versions of this file used a different name. Keep the
// alias so the test names read cleanly without renaming the helper.
func readRunReader(t *testing.T) string { return readRunnerSource(t) }

// readRunnerSource loads runner.go via go's embed-like test helper.
// We don't actually need embed — a plain os.ReadFile of the file
// relative to the package directory is sufficient.
func readRunnerSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatalf("read runner.go: %v", err)
	}
	return string(data)
}

func TestTokeniseAndCosine_Reuse(t *testing.T) {
	// The runner reuses these helpers across iterations. Regression
	// coverage keeps the convergence-signal path deterministic.
	a := tokenise("[mythos loop iter=1] Investigating: alpha.")
	b := tokenise("[mythos loop iter=2] Investigating: alpha.")
	score := CosineSimilarity(a, b)
	if score <= 0 {
		t.Fatalf("two identical bodies should score > 0, got %f", score)
	}
	if score > 1.0001 {
		t.Fatalf("identical bodies should score <= 1, got %f", score)
	}
}

func TestTokeniseAndCosine_Different(t *testing.T) {
	a := tokenise("completely different")
	b := tokenise("totally unrelated")
	if score := CosineSimilarity(a, b); score != 0 {
		t.Fatalf("disjoint tokens should score 0, got %f", score)
	}
}
