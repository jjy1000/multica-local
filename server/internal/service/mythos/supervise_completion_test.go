// Package mythos — supervise_completion_test.go (0.3.64).
//
// Regression coverage for the final-issue terminal-status branch in
// tickSupervision. Pre-0.3.64 the supervise goroutine read
// final_issue_id but never wrote SubTasksDone, so every supervised
// run polled forever (until the 24h max-lifetime cap). The fix:
// when final_issue_id reaches a terminal issue status (done /
// closed / cancelled), tickSupervision flips phase to PhaseDone
// AND snaps SubTasksDone = SubTasksTotal so the next state serialise
// reflects a clean completion.
//
// These tests pin both halves of the fix:
//
//   - positive: any of the three terminal statuses triggers PhaseDone
//   - negative: a non-terminal status keeps the phase in supervising
//     and does NOT touch SubTasksDone
//   - guardrail: SubTasksDone MUST equal SubTasksTotal (exact) when
//     the final issue lands a terminal status. A regression to the
//     pre-fix shape (snap to 0, or leave it unset) fails this
//     assertion. This is the contract that 0.3.46 P0#4 / 0.3.64
//     audit restored.
//
// The tests use a fake tickSupervisionQuerier injected via the
// Service.tickQ seam added in 0.3.64. No real DB.

package mythos

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeTickQuerier is a hand-rolled stub for tickSupervisionQuerier.
// Only GetMythosRun and GetIssue are needed by the completion path;
// every other call panics so an accidental widening of the seam
// surfaces as a test failure rather than a silent nil deref.
type fakeTickQuerier struct {
	mu sync.Mutex

	runRow  db.MythosRun
	runErr  error
	issue   db.Issue
	issueOK bool
	issueErr error
}

func (f *fakeTickQuerier) GetMythosRun(_ context.Context, _ pgtype.UUID) (db.MythosRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runRow, f.runErr
}

func (f *fakeTickQuerier) GetIssue(_ context.Context, _ pgtype.UUID) (db.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.issueOK {
		// Surface a clear "miss" to the test if the production code
		// ever calls GetIssue on a row the fixture didn't set.
		panic("fakeTickQuerier.GetIssue called but issueOK is false")
	}
	return f.issue, f.issueErr
}

// makeTestUUID returns a valid pgtype.UUID for fixture row IDs.
func makeTestUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

// makeServiceWithFake wires a Service with the fake querier. The
// production *db.Queries field is left nil because tickSupervision
// only touches s.tickQ when set. The fakeEffectiveQuerier covers the
// custom-status subtests ("closed", empty) that issuestatus.Effective
// walks via s.effectiveQ.
func makeServiceWithFake(fake tickSupervisionQuerier) *Service {
	return &Service{
		queries:      nil,
		tickQ:        fake,
		effectiveQ:   &fakeEffectiveQuerier{},
		superviseSet: make(map[pgtype.UUID]context.CancelFunc),
	}
}

// fakeEffectiveQuerier implements issuestatus.Querier with the minimum
// surface that TestTickSupervision_CompletionByFinalIssueStatus needs:
//   - "closed" → category "done" (terminal; triggers PhaseDone)
//   - ""      → no row; Effective falls through to the original status
// All other methods panic (intentional) so a future widening of the
// seam surfaces as a test failure rather than a silent nil deref.
type fakeEffectiveQuerier struct{}

func (*fakeEffectiveQuerier) GetIssueStatusEntryByKey(_ context.Context, arg db.GetIssueStatusEntryByKeyParams) (db.IssueStatus, error) {
	if arg.Key == "closed" {
		return db.IssueStatus{Key: "closed", Category: "done"}, nil
	}
	// Empty + unknown custom statuses: simulate "no row" so Effective
	// leaves the original status unchanged. Return errNoRows so
	// Effective's existing `if err != nil { return status }` branch
	// handles it.
	return db.IssueStatus{}, pgx.ErrNoRows
}

func (*fakeEffectiveQuerier) ListIssueStatusEntries(context.Context, db.ListIssueStatusEntriesParams) ([]db.IssueStatus, error) {
	panic("fakeEffectiveQuerier.ListIssueStatusEntries not implemented")
}

func (*fakeEffectiveQuerier) SeedIssueStatusEntries(context.Context, pgtype.UUID) error {
	panic("fakeEffectiveQuerier.SeedIssueStatusEntries not implemented")
}

func (*fakeEffectiveQuerier) ListIssueStatusKeysByCategories(context.Context, db.ListIssueStatusKeysByCategoriesParams) ([]string, error) {
	panic("fakeEffectiveQuerier.ListIssueStatusKeysByCategories not implemented")
}

// TestTickSupervision_CompletionByFinalIssueStatus covers the four
// 0.3.64 cases for the final_issue_id completion branch.
//
//   - positive: status=done / closed / cancelled → PhaseDone,
//     SubTasksDone = SubTasksTotal
//   - negative: status=todo / in_progress / empty → Phase stays
//     out of Done, SubTasksDone untouched
//   - guardrail: positive cases assert SubTasksDone == SubTasksTotal
//     (NOT <, NOT == 0). This is the regression line: pre-0.3.64
//     SubTasksDone was never written, so the comparison would have
//     found 0 vs total and the run polled forever.
func TestTickSupervision_CompletionByFinalIssueStatus(t *testing.T) {
	const total = 4

	tests := []struct {
		name         string
		issueStatus  string
		issueOK      bool
		issueErr     error
		wantPhase    SupervisionPhase
		wantDoneSnap bool // when true, expect SubTasksDone == total
	}{
		{
			name:         "final_issue_done_flips_to_done_and_snaps",
			issueStatus:  "done",
			issueOK:      true,
			wantPhase:    PhaseDone,
			wantDoneSnap: true,
		},
		{
			name:         "final_issue_closed_flips_to_done_and_snaps",
			issueStatus:  "closed",
			issueOK:      true,
			wantPhase:    PhaseDone,
			wantDoneSnap: true,
		},
		{
			name:         "final_issue_cancelled_flips_to_done_and_snaps",
			issueStatus:  "cancelled",
			issueOK:      true,
			wantPhase:    PhaseDone,
			wantDoneSnap: true,
		},
		{
			name:        "final_issue_todo_stays_supervising",
			issueStatus: "todo",
			issueOK:     true,
			wantPhase:   PhaseSupervising,
		},
		{
			name:        "final_issue_in_progress_stays_supervising",
			issueStatus: "in_progress",
			issueOK:     true,
			wantPhase:   PhaseSupervising,
		},
		{
			name:        "final_issue_empty_status_stays_supervising",
			issueStatus: "",
			issueOK:     true,
			wantPhase:   PhaseSupervising,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			finalIssueID := makeTestUUID(t)
			rootIssueID := makeTestUUID(t)

			// coda_conclusions: an array of N strings. The supervise
			// path uses len(conclusions) as SubTasksTotal. We use 4
			// so the "snap" assertion is unambiguous (any non-zero,
			// non-total value would be wrong).
			conclusions, err := json.Marshal([]string{"a", "b", "c", "d"})
			if err != nil {
				t.Fatalf("marshal conclusions: %v", err)
			}

			fake := &fakeTickQuerier{
				runRow: db.MythosRun{
					ID:              makeTestUUID(t),
					FinalIssueID:    finalIssueID,
					CodaConclusions: conclusions,
				},
				issue: db.Issue{
					ID:     finalIssueID,
					Status: tc.issueStatus,
				},
				issueOK: tc.issueOK,
			}

			svc := makeServiceWithFake(fake)

			prev := SupervisionState{
				Phase:         PhaseSupervising,
				SubTasksTotal: total,
				// Intentionally pre-set SubTasksDone to a non-zero,
				// non-total value (total - 1) so the test catches
				// BOTH regressions:
				//   - pre-fix path: never assigns, SubTasksDone stays
				//     at total-1 → PhaseDone but SubTasksDone != total
				//   - half-fix path: assigns 0 → SubTasksDone < total
				SubTasksDone: total - 1,
			}

			got, err := svc.tickSupervision(context.Background(), fake.runRow.ID, rootIssueID, prev)
			if err != nil {
				t.Fatalf("tickSupervision returned error: %v", err)
			}

			if got.Phase != tc.wantPhase {
				t.Errorf("Phase = %q, want %q", got.Phase, tc.wantPhase)
			}

			if tc.wantDoneSnap {
				// Guardrail: the regression line. Pre-0.3.64
				// SubTasksDone was never assigned and stayed at the
				// pre-set value. We assert *exact equality* with
				// SubTasksTotal — not just non-zero, not just > 0.
				if got.SubTasksDone != got.SubTasksTotal {
					t.Errorf("SubTasksDone = %d, want %d (snap-to-total guardrail)",
						got.SubTasksDone, got.SubTasksTotal)
				}
				if got.SubTasksTotal != total {
					t.Errorf("SubTasksTotal = %d, want %d (fixture drift)", got.SubTasksTotal, total)
				}
			} else {
				// Negative cases must not touch SubTasksDone at all
				// — the pre-set value survives the call.
				if got.SubTasksDone != total-1 {
					t.Errorf("SubTasksDone = %d, want unchanged %d (negative case must not write)",
						got.SubTasksDone, total-1)
				}
				if got.Phase == PhaseDone {
					t.Errorf("Phase = PhaseDone but issue status %q is not terminal", tc.issueStatus)
				}
			}
		})
	}
}

// TestTickSupervision_SnapToTotalGuardrail is the dedicated
// regression line for the 0.3.46 / 0.3.64 audit. It runs the
// terminal-status path with a deliberately non-zero pre-set
// SubTasksDone and asserts the snap-to-total invariant holds. If a
// future refactor "forgets" the snap and the field stays at its
// pre-set value (the pre-fix shape), this test fails immediately.
//
// The assertion is: SubTasksDone MUST equal SubTasksTotal after a
// terminal issue status — exact equality, not just "non-zero" or
// "greater than zero". A snap to 0 (another plausible wrong fix)
// also fails this test.
func TestTickSupervision_SnapToTotalGuardrail(t *testing.T) {
	const total = 7

	finalIssueID := makeTestUUID(t)
	rootIssueID := makeTestUUID(t)

	conclusions, err := json.Marshal([]string{"a", "b", "c", "d", "e", "f", "g"})
	if err != nil {
		t.Fatalf("marshal conclusions: %v", err)
	}

	fake := &fakeTickQuerier{
		runRow: db.MythosRun{
			ID:              makeTestUUID(t),
			FinalIssueID:    finalIssueID,
			CodaConclusions: conclusions,
		},
		issue: db.Issue{
			ID:     finalIssueID,
			Status: "done",
		},
		issueOK: true,
	}

	svc := makeServiceWithFake(fake)

	// pre-set SubTasksDone to (total - 2) so the test distinguishes
	// between three buggy shapes:
	//   - pre-fix:       SubTasksDone stays at total-2 (never written)
	//   - half-fix:      SubTasksDone snaps to 0
	//   - correct fix:   SubTasksDone snaps to total
	prev := SupervisionState{
		Phase:         PhaseSupervising,
		SubTasksTotal: total,
		SubTasksDone:  total - 2,
	}

	got, err := svc.tickSupervision(context.Background(), fake.runRow.ID, rootIssueID, prev)
	if err != nil {
		t.Fatalf("tickSupervision returned error: %v", err)
	}

	if got.Phase != PhaseDone {
		t.Fatalf("Phase = %q, want PhaseDone", got.Phase)
	}
	if got.SubTasksDone != total {
		t.Fatalf("SubTasksDone = %d, want %d (snap-to-total invariant; pre-fix leaves it at %d)",
			got.SubTasksDone, total, total-2)
	}
	if got.SubTasksTotal != total {
		t.Fatalf("SubTasksTotal = %d, want %d (must not be mutated by tick)", got.SubTasksTotal, total)
	}
}
