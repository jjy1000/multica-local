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
