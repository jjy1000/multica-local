// Package main — decision_sync_listeners_test.go (0.5.22 Semantica × Multica Phase 2)
//
// Unit tests for the pure payload-parsing helpers, the dedup gate,
// and the sync-row predicate. No DB / no bus — extractIssueID +
// isTerminalStatus + shouldFireRecentSync + syncIssueRow are all pure
// so the test drives them directly with synthetic payloads. The
// listener integration (events.Bus → syncIfTerminal → handler.POST)
// is exercised separately at the handler package level.
package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/handler"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestExtractIssueID_AcceptsAllPayloadShapes(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{
			name:    "IssueResponse value (UpdateIssue publisher)",
			payload: map[string]any{"issue": handler.IssueResponse{ID: "11111111-1111-4111-8111-111111111111"}},
			want:    "11111111-1111-4111-8111-111111111111",
		},
		{
			name:    "IssueResponse pointer",
			payload: map[string]any{"issue": &handler.IssueResponse{ID: "22222222-2222-4222-8222-222222222222"}},
			want:    "22222222-2222-4222-8222-222222222222",
		},
		{
			name:    "map payload (other emitters)",
			payload: map[string]any{"issue": map[string]any{"id": "33333333-3333-4333-8333-333333333333"}},
			want:    "33333333-3333-4333-8333-333333333333",
		},
		{
			name:    "top-level issue_id fallback",
			payload: map[string]any{"issue_id": "44444444-4444-4444-8444-444444444444"},
			want:    "44444444-4444-4444-8444-444444444444",
		},
		{
			name:    "empty payload",
			payload: map[string]any{},
			want:    "",
		},
		{
			name:    "issue present but wrong type",
			payload: map[string]any{"issue": 42},
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractIssueID(tc.payload); got != tc.want {
				t.Fatalf("extractIssueID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractIssueID_TopLevelTakesPrecedence(t *testing.T) {
	payload := map[string]any{
		"issue_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"issue":    handler.IssueResponse{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
	}
	if got := extractIssueID(payload); got != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("extractIssueID() = %q, want top-level issue_id to win", got)
	}
}

func TestIsTerminalStatus(t *testing.T) {
	for _, s := range []string{"done", "closed", "cancelled"} {
		if !isTerminalStatus(s) {
			t.Errorf("isTerminalStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"todo", "in_progress", "in_review", "backlog", ""} {
		if isTerminalStatus(s) {
			t.Errorf("isTerminalStatus(%q) = true, want false", s)
		}
	}
}

func TestShouldFireRecentSync_DedupesByIssueID(t *testing.T) {
	// Use a UUID-shaped string so it survives the regex-shaped keys.
	issueID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

	// First call fires.
	if !shouldFireRecentSync(issueID) {
		t.Fatalf("first call should fire")
	}

	// Second call within TTL should be suppressed.
	if shouldFireRecentSync(issueID) {
		t.Fatalf("second call within TTL should be suppressed")
	}

	// Different issue_id is not affected.
	if !shouldFireRecentSync("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb") {
		t.Fatalf("different issue_id should fire independently")
	}
}

// TestShouldFireRecentSync_TTLExpiresAfterWindow shrinks the TTL to
// a few milliseconds and verifies the gate re-opens once the window
// has passed. Pins the contract that recentSyncTTL bounds the dedup,
// not just decorates it. Run with `-race` to also exercise the
// LoadOrStore path under contention.
func TestShouldFireRecentSync_TTLExpiresAfterWindow(t *testing.T) {
	origTTL := recentSyncTTL
	recentSyncTTL = 50 * time.Millisecond
	t.Cleanup(func() { recentSyncTTL = origTTL })

	issueID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

	if !shouldFireRecentSync(issueID) {
		t.Fatalf("first call should fire")
	}
	if shouldFireRecentSync(issueID) {
		t.Fatalf("second call within TTL should be suppressed")
	}
	time.Sleep(recentSyncTTL + 20*time.Millisecond)
	if !shouldFireRecentSync(issueID) {
		t.Fatalf("third call after TTL should fire")
	}
}

// TestShouldFireRecentSync_ConcurrentSafety is the `-race` companion:
// 100 goroutines hammer the same key. Atomic dedup (LoadOrStore) must
// let exactly one goroutine return true. Without LoadOrStore, two or
// more goroutines would both observe a fresh slot and both Store,
// both return true. Worst case in production is benign — Semantica
// dedupes by decision.id upstream — but the listener contract is
// "collapse 4 events to 1 POST" and we want to pin it.
func TestShouldFireRecentSync_ConcurrentSafety(t *testing.T) {
	issueID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	const N = 100

	var fired int32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if shouldFireRecentSync(issueID) {
				atomic.AddInt32(&fired, 1)
			}
		}()
	}
	wg.Wait()
	if fired != 1 {
		t.Fatalf("fired count = %d, want exactly 1 (atomic dedup)", fired)
	}
}

// TestShouldFireRecentSync_StaleEntryOverwrite covers the edge where
// the prior slot's value is older than recentSyncTTL but the key is
// still present. The gate should re-open AND overwrite the stale
// timestamp so subsequent calls within the new TTL are deduped.
func TestShouldFireRecentSync_StaleEntryOverwrite(t *testing.T) {
	origTTL := recentSyncTTL
	recentSyncTTL = 30 * time.Millisecond
	t.Cleanup(func() { recentSyncTTL = origTTL })

	issueID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	if !shouldFireRecentSync(issueID) {
		t.Fatalf("first call should fire")
	}
	time.Sleep(recentSyncTTL + 10*time.Millisecond)
	// Stale — should overwrite and fire again.
	if !shouldFireRecentSync(issueID) {
		t.Fatalf("second call after TTL should overwrite and fire")
	}
	// And the new slot should be fresh — immediate third call is suppressed.
	if shouldFireRecentSync(issueID) {
		t.Fatalf("third call within the new TTL should be suppressed")
	}
}

// TestEvictStaleSyncEntries is a direct test of the janitor body: it
// pre-seeds recentSyncDedup with one fresh + one stale entry and
// asserts that the stale entry is deleted while the fresh entry
// survives. Pins the bounded-memory contract — without eviction, a
// long-running desktop install would accumulate one entry per
// terminal issue forever (~25 MB/year at 1k/day).
func TestEvictStaleSyncEntries(t *testing.T) {
	origTTL := recentSyncTTL
	recentSyncTTL = 50 * time.Millisecond
	t.Cleanup(func() { recentSyncTTL = origTTL })

	freshKey := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	staleKey := "99999999-9999-4999-8999-999999999999"

	// Seed: fresh = now, stale = now - 2*TTL (definitely past cutoff).
	recentSyncDedup.Store(freshKey, time.Now())
	recentSyncDedup.Store(staleKey, time.Now().Add(-2*recentSyncTTL))

	evictStaleSyncEntries()

	if _, ok := recentSyncDedup.Load(freshKey); !ok {
		t.Errorf("fresh entry was evicted (should survive)")
	}
	if _, ok := recentSyncDedup.Load(staleKey); ok {
		t.Errorf("stale entry was not evicted (memory leak)")
	}
}

// syncIssueRow pins the four-case contract for the pure sync gate
// (the I/O-free heart of syncIfTerminal). Without these tests a future
// refactor could drift the gate semantics — most likely by silently
// dropping the lab_source check, which would let non-semantica labs
// flood the Semantica corpus.
func TestSyncIssueRow_AcceptsTerminalSemantica(t *testing.T) {
	issueID := "11111111-1111-4111-8111-111111111111"
	row := db.Issue{
		Status:    "done",
		LabSource: pgtype.Text{String: "semantica", Valid: true},
	}
	if !syncIssueRow(issueID, row) {
		t.Fatalf("done+semantica should sync")
	}
}

func TestSyncIssueRow_RejectsNonTerminal(t *testing.T) {
	issueID := "22222222-2222-4222-8222-222222222222"
	for _, status := range []string{"todo", "in_progress", "in_review", "backlog", ""} {
		row := db.Issue{
			Status:    status,
			LabSource: pgtype.Text{String: "semantica", Valid: true},
		}
		if syncIssueRow(issueID, row) {
			t.Errorf("status=%q should be rejected (not terminal)", status)
		}
	}
}

func TestSyncIssueRow_RejectsNonSemanticaLab(t *testing.T) {
	issueID := "33333333-3333-4333-8333-333333333333"
	for _, lab := range []string{"pythia_oracle", "claude_science_lab", "mythos_swarm", ""} {
		row := db.Issue{
			Status:    "done",
			LabSource: pgtype.Text{String: lab, Valid: lab != ""},
		}
		if syncIssueRow(issueID, row) {
			t.Errorf("lab=%q should be rejected (semantica only)", lab)
		}
	}
}

// TestSyncIssueRow_Deduped pins that the dedup gate fires inside
// syncIssueRow (the same path syncIfTerminal uses). Two rows for the
// same issue_id within TTL: first fires, second is deduped.
func TestSyncIssueRow_Deduped(t *testing.T) {
	issueID := "44444444-4444-4444-8444-444444444444"
	row := db.Issue{
		Status:    "done",
		LabSource: pgtype.Text{String: "semantica", Valid: true},
	}
	if !syncIssueRow(issueID, row) {
		t.Fatalf("first call should sync")
	}
	if syncIssueRow(issueID, row) {
		t.Fatalf("second call within TTL should be deduped")
	}
}

// TestSyncIssueRow_NonTerminalDoesNotConsumeDedupSlot pins the
// gate-ordering fix: a non-terminal status_changed event (e.g.
// todo→in_progress at dispatch) must NOT consume the dedup slot, or
// the real terminal transition a few minutes later would be
// suppressed. This test fails on the pre-fix ordering (dedup first,
// filters second) because the non-terminal row would stamp the slot
// and the subsequent terminal row would be deduped away.
func TestSyncIssueRow_NonTerminalDoesNotConsumeDedupSlot(t *testing.T) {
	issueID := "55555555-5555-4555-8555-555555555555"

	nonTerminal := db.Issue{
		Status:    "in_progress",
		LabSource: pgtype.Text{String: "semantica", Valid: true},
	}
	if syncIssueRow(issueID, nonTerminal) {
		t.Fatalf("non-terminal row should not sync")
	}

	// The same issue transitions to terminal shortly after — it MUST
	// still sync because the non-terminal event did not stamp the dedup
	// slot.
	terminal := db.Issue{
		Status:    "done",
		LabSource: pgtype.Text{String: "semantica", Valid: true},
	}
	if !syncIssueRow(issueID, terminal) {
		t.Fatalf("terminal after non-terminal should sync (dedup slot must not be consumed by non-firing events)")
	}
}

// TestExtractIssueID_NilIssueResponsePointer covers the realistic
// panic vector where a publisher serializes via `&resp` after an
// error path. Field access on a nil pointer to a struct PANICS in Go
// (dereferencing nil), so extractIssueID must guard `iss == nil`
// before reading `iss.ID` — we want it to return "" instead of
// crashing the publish-site goroutine.
func TestExtractIssueID_NilIssueResponsePointer(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil pointer panicked: %v", r)
		}
	}()
	payload := map[string]any{"issue": (*handler.IssueResponse)(nil)}
	if got := extractIssueID(payload); got != "" {
		t.Errorf("nil pointer should return %q, got %q", "", got)
	}
}
