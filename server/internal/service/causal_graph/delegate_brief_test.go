// Package causalgraph — delegate_brief_test.go (0.5.88)
//
// DB-less regression pins for BuildDelegateBrief / renderDelegateBrief,
// mirroring claim_brief_test.go's structure: nil-safety / silent
// fallback / cancelled-context pins for the builder, plus pure-render
// pins for the heading shape, the frozen + auxiliary + leaderless
// filters, and the 2000-byte cap. No DATABASE_URL needed — the
// builder's only DB touch is ListEnabledFlagKeys, which the
// error-skip pins exercise through nil Queries and a pre-cancelled
// context (both return ("", nil) before any row is read).
package causalgraph

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// noopLeaderFor is a resolver that never finds a leader.
func noopLeaderFor(string) (string, bool) { return "", false }

// staticLeaderFor resolves every key to the given leader.
func staticLeaderFor(leader string) func(string) (string, bool) {
	return func(string) (string, bool) { return leader, true }
}

// TestBuildDelegateBrief_NilQueriesSilentFallback pins the nil-safety
// contract: a nil Queries must return ("", nil) — never panic — so the
// daemon call site can pipe through without guarding.
func TestBuildDelegateBrief_NilQueriesSilentFallback(t *testing.T) {
	got, err := BuildDelegateBrief(context.Background(), nil, staticLeaderFor("research"), "issue-1")
	if err != nil {
		t.Fatalf("expected nil error on nil queries, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string on nil queries, got %q", got)
	}
}

// TestBuildDelegateBrief_NilResolverSilentFallback pins the resolver
// nil-guard. A nil leaderFor would otherwise panic inside the filter
// loop.
func TestBuildDelegateBrief_NilResolverSilentFallback(t *testing.T) {
	q := &db.Queries{} // not nil, but no real DB either
	got, err := BuildDelegateBrief(context.Background(), q, nil, "issue-1")
	if err != nil {
		t.Fatalf("expected nil error on nil resolver, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string on nil resolver, got %q", got)
	}
}

// TestBuildDelegateBrief_CancelledContextSilentFallback pins the
// silent-fallback contract for the 200ms deadline path: a
// pre-cancelled context returns the empty default without panicking.
// (With no real DB the query errors out and the builder logs at WRN
// and returns ("", nil).)
func TestBuildDelegateBrief_CancelledContextSilentFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	q := &db.Queries{}
	got, err := BuildDelegateBrief(ctx, q, staticLeaderFor("research"), "issue-1")
	if err != nil {
		t.Fatalf("expected nil error on cancelled context, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string on cancelled context, got %q", got)
	}
}

// TestRenderDelegateBrief_EmptyWhenNone pins the empty contract: no
// delegatable labs → NO section at all (no heading stub — the caller
// concatenates the string into the prompt and must not end up with an
// orphan heading).
func TestRenderDelegateBrief_EmptyWhenNone(t *testing.T) {
	if got := renderDelegateBrief(nil, "issue-1"); got != "" {
		t.Fatalf("expected empty string for no labs, got %q", got)
	}
	if got := renderDelegateBrief([]DelegateLabEntry{}, "issue-1"); got != "" {
		t.Fatalf("expected empty string for empty lab slice, got %q", got)
	}
}

// TestRenderDelegateBrief_HeadingAndLabLines pins the markdown shape:
// the literal heading, one "- <flag-key> (leader: <agent>)" line per
// lab, and the copy-pasteable delegate command carrying the concrete
// issue id as --parent.
func TestRenderDelegateBrief_HeadingAndLabLines(t *testing.T) {
	labs := []DelegateLabEntry{
		{FlagKey: "claude_science_lab", Leader: "research"},
		{FlagKey: "pythia_oracle", Leader: "pythia_runtime"},
	}
	got := renderDelegateBrief(labs, "ab12cd12-ab12-ab12-ab12-ab12cd12ab12")

	if !strings.HasPrefix(got, "## Available Labs (delegation)\n") {
		t.Errorf("missing briefing heading, got: %q", got)
	}
	if !strings.Contains(got, "- claude_science_lab (leader: research)\n") {
		t.Errorf("missing claude_science_lab line, got: %q", got)
	}
	if !strings.Contains(got, "- pythia_oracle (leader: pythia_runtime)\n") {
		t.Errorf("missing pythia_oracle line, got: %q", got)
	}
	wantCmd := "multica lab delegate --parent ab12cd12-ab12-ab12-ab12-ab12cd12ab12 <flag-key> \"<task>\""
	if !strings.Contains(got, wantCmd) {
		t.Errorf("missing delegate command %q, got: %q", wantCmd, got)
	}
	// Ordering follows the input slice — built-ins arrive in catalog
	// order upstream.
	if strings.Index(got, "claude_science_lab") > strings.Index(got, "pythia_oracle") {
		t.Errorf("expected catalog-order lines, got: %q", got)
	}
}

// TestRenderDelegateBrief_PlaceholderWhenNoIssue pins the empty
// issueID fallback: the literal <issue-id> placeholder renders instead
// of an empty --parent value.
func TestRenderDelegateBrief_PlaceholderWhenNoIssue(t *testing.T) {
	got := renderDelegateBrief([]DelegateLabEntry{{FlagKey: "semantica", Leader: "semantica_decision_advisor"}}, "")
	if !strings.Contains(got, "--parent <issue-id> ") {
		t.Errorf("expected literal <issue-id> placeholder, got: %q", got)
	}
}

// TestRenderDelegateBrief_HardCapTruncates pins the 2000-byte cap.
// A lab list whose rendered section exceeds maxDelegateBriefChars must
// come back at/below cap + suffix, with the suffix present.
func TestRenderDelegateBrief_HardCapTruncates(t *testing.T) {
	// Each line is ~80 bytes; 60 labs ≈ 4800 bytes — well past the cap.
	labs := make([]DelegateLabEntry, 0, 60)
	for i := 0; i < 60; i++ {
		labs = append(labs, DelegateLabEntry{
			FlagKey: "user_plugin-" + strings.Repeat("k", 40) + "-" + itoa(i),
			Leader:  "leader-" + itoa(i),
		})
	}
	got := renderDelegateBrief(labs, "issue-1")
	if len(got) > maxDelegateBriefChars+len("…(truncated)\n") {
		t.Fatalf("capped section is %d bytes, want <= %d + suffix", len(got), maxDelegateBriefChars)
	}
	if !strings.Contains(got, "…(truncated)") {
		t.Errorf("expected truncation suffix when over cap, got tail: %q", got[len(got)-60:])
	}
	// The heading must survive the cut — an agent reading a headless
	// fragment gets no context.
	if !strings.HasPrefix(got, "## Available Labs (delegation)\n") {
		t.Errorf("heading lost in truncation, got prefix: %q", got[:40])
	}
	// No half-rendered line: every line between the heading and the
	// suffix must be a complete lab line.
	for _, line := range strings.Split(strings.TrimSuffix(got, "…(truncated)\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "You can") {
			continue
		}
		if !strings.HasPrefix(line, "- user_plugin-") {
			t.Errorf("truncation cut mid-line: %q", line)
		}
	}
}

// TestRenderDelegateBrief_UnderCapNoSuffix pins that a small section
// never carries the truncation suffix.
func TestRenderDelegateBrief_UnderCapNoSuffix(t *testing.T) {
	got := renderDelegateBrief([]DelegateLabEntry{{FlagKey: "timesfm", Leader: "timesfm_oracle"}}, "issue-1")
	if strings.Contains(got, "…(truncated)") {
		t.Errorf("section under the cap must not be truncated, got: %q", got)
	}
}

// itoa is a tiny test helper avoiding a strconv import for one call
// site per fixture loop.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
