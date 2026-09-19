package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Ported from upstream MUL-7449 / MUL-7485 (#8495 / #8540), minus the batch
// paths (notifyParentsOfBatchChildDone / highestClosedBatchStage /
// batchClosedScopeHasCancelled do not exist in this fork) and minus the
// "Completing → Closing" verb alignment (this fork's final-stage branch reads
// "This was the final stage. Wrap up the parent", which asserts no verb to
// contradict).

func TestStageProgressSummarySeparatesCancelledFromDone(t *testing.T) {
	children := []db.Issue{
		child(1, "done"),
		child(2, "cancelled"), child(2, "cancelled"),
		child(3, "backlog"),
	}

	summary, next := stageProgressSummary(children, 2, literalChildStatus)
	want := "Stage 1: 1/1 done; Stage 2: 0/2 done, 2 cancelled; Stage 3: 0/1 done (next)"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
	if next != 3 {
		t.Fatalf("nextStage = %d, want 3", next)
	}
}

func TestResolvedChildStatusesKeepCanonicalCancelled(t *testing.T) {
	doneID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	cancelledID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	children := []db.Issue{
		{ID: doneID, Status: "approved", Stage: pgtype.Int4{Int32: 1, Valid: true}},
		{ID: cancelledID, Status: "wont_do", Stage: pgtype.Int4{Int32: 1, Valid: true}},
	}

	statuses := resolveChildStatuses(children, func(c db.Issue) string {
		switch c.Status {
		case "approved":
			return "done"
		case "wont_do":
			return "cancelled"
		default:
			return c.Status
		}
	})
	if got := statuses.status(children[1]); got != "cancelled" {
		t.Fatalf("canonical status = %q, want cancelled", got)
	}
	if !statuses.isTerminal(children[1]) {
		t.Fatal("canonical cancelled status must still close the barrier")
	}

	summary, _ := stageProgressSummary(children, 1, statuses.status)
	if want := "Stage 1: 1/2 done, 1 cancelled"; summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
}

func TestCountStageCancelled(t *testing.T) {
	children := []db.Issue{
		child(1, "done"),
		child(2, "cancelled"), child(2, "cancelled"),
		child(3, "cancelled"),
	}
	if got := countStageCancelled(children, 2, literalChildStatus); got != 2 {
		t.Fatalf("countStageCancelled(stage 2) = %d, want 2", got)
	}
	if got := countStageCancelled(children, 1, literalChildStatus); got != 0 {
		t.Fatalf("countStageCancelled(stage 1) = %d, want 0", got)
	}
	// An unstaged cancelled child belongs to no stage and must not leak into
	// any stage's count.
	if got := countStageCancelled(append(children, child(0, "cancelled")), 2, literalChildStatus); got != 2 {
		t.Fatalf("countStageCancelled(stage 2) with unstaged sibling = %d, want 2", got)
	}
}

func TestAnyCancelledChildren(t *testing.T) {
	if !anyCancelledChildren([]db.Issue{child(1, "done"), child(2, "cancelled")}, literalChildStatus) {
		t.Fatal("a cancelled sibling anywhere in the set must be detected")
	}
	if anyCancelledChildren([]db.Issue{child(1, "done"), child(2, "backlog")}, literalChildStatus) {
		t.Fatal("no cancelled sibling must not warn")
	}
}

// When the cancellations are in the stage this comment is about, the agent
// deciding whether to promote gets the two facts it can act on: how many
// sub-issues were cancelled, and which stage has to not depend on them.
func TestStageAdvanceInstructionNamesCountAndDependentStage(t *testing.T) {
	got := stageAdvanceInstruction(3, "parent-id", 2)
	for _, want := range []string{
		"Stage 3 is next",
		"has 2 sub-issues cancelled",
		"not something Stage 3 depends on",
		"post a comment to confirm first",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "includes cancelled items") {
		t.Fatalf("named stage must not fall back to the general warning: %s", got)
	}
}

func TestStageAdvanceInstructionSingularCancellation(t *testing.T) {
	got := stageAdvanceInstruction(3, "parent-id", 1)
	if !strings.Contains(got, "has 1 sub-issue cancelled") {
		t.Fatalf("want singular sub-issue, got %q", got)
	}
	if strings.Contains(got, "1 sub-issues") {
		t.Fatalf("plural leaked into the singular case: %s", got)
	}
}

// With no next stage the counted warning still lands, keyed to whatever comes
// next rather than a promotable stage number.
func TestStageAdvanceInstructionCancelledWithoutNextStage(t *testing.T) {
	got := stageAdvanceInstruction(0, "parent-id", 2)
	for _, want := range []string{
		"This was the final stage",
		"has 2 sub-issues cancelled",
		"not a dependency of whatever comes next",
		"do not create the next stage yet",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction missing %q: %s", want, got)
		}
	}
}
