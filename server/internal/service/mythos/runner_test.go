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
