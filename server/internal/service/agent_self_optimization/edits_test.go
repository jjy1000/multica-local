// Package agent_self_optimization — edits_test.go (0.5.18 hygiene, F-007).
//
// Static-invariant regression pin for F-007 (triage.md §F-007). The audit
// flagged "self-opt auto-apply replaces ENTIRE instructions" at runner.go:594
// but L594 is a `go runSubject(...)` goroutine spawn, not the apply path.
// Reading `edits.go::ApplyEdit` (the actual write site, called from both
// user-confirm and auto-apply paths) shows three branches:
//
//   - add    : cur += edit.AfterText + "\n"        (L167-171, true append)
//   - delete : strings.Replace(cur, BeforeText, "", 1)         (L172-176)
//   - replace: strings.Replace(cur, BeforeText, AfterText, 1)  (L177-181)
//
// NONE of them assign `cur = edit.AfterText` (the "replace ENTIRE
// instructions" pattern the audit feared). This test pins that absence so
// a future refactor cannot silently re-introduce the destructive path
// without breaking the test.
//
// Method: read edits.go as text (the file is in the same package, so the
// test always sees the current source) and grep for the dangerous
// assignment. We do NOT use go/parser because (i) the audit cite was
// a textual grep at runner.go:594, mirroring it keeps the surface
// trivially auditable; (ii) edits.go has no go:generate directives
// that would break a string read.
//
// Companion invariant: `delete` and `replace` are explicitly excluded
// from auto-apply per optimizer.go:429 ("Design verdict §5a: delete /
// replace NEVER auto-apply"). That gate is enforced by Optimize() which
// we do not pin here (already covered by optimizer_test.go TestApplyGates).
package agent_self_optimization

import (
	"os"
	"strings"
	"testing"
)

// TestApplyEditNeverReplacesEntireInstructions pins the F-007 audit
// invariant: edits.go::ApplyEdit must not contain a `cur = ...AfterText`
// assignment that would clobber the entire instruction set. Only the
// three surgical branches (add / delete / replace) are permitted.
func TestApplyEditNeverReplacesEntireInstructions(t *testing.T) {
	src, err := os.ReadFile("edits.go")
	if err != nil {
		t.Fatalf("read edits.go: %v", err)
	}
	body := string(src)

	// Forbidden: any line that reassigns cur to the edit's AfterText
	// wholesale. Allow the surgical `strings.Replace(cur, ...)` calls in
	// the `delete` / `replace` branches, but block the clobber pattern.
	//
	// Pattern deliberately lenient (allows whitespace, allows `cur :=`)
	// so a stylistic edit does not break the pin.
	forbidden := []string{
		"cur = edit.AfterText",
		"cur=edit.AfterText",
		"cur = AfterText",
		"cur=AfterText",
		"\tcur = edit.AfterText",
	}
	for _, pat := range forbidden {
		if strings.Contains(body, pat) {
			t.Fatalf("F-007 regression: edits.go contains %q — ApplyEdit would replace ENTIRE instructions", pat)
		}
	}

	// Positive pin: the three branches must still be present, in order.
	// If a future refactor collapses them into a single path (e.g. a
	// switch-less if/else), this assertion fails to flag the loss of
	// audit clarity, not the loss of correctness. That is acceptable —
	// the existing optimizer_test.go TestApplyGates covers correctness.
	wantMarkers := []string{
		`case "add":`,
		`case "delete":`,
		`case "replace":`,
	}
	for _, marker := range wantMarkers {
		if !strings.Contains(body, marker) {
			t.Fatalf("F-007 invariant drift: edits.go missing branch %q — auto-apply behaviour may have changed silently", marker)
		}
	}
}

// TestApplyEditSnapshotBeforeMutation pins the rollback-point invariant:
// `preEdit := cur` must be captured BEFORE the switch mutates cur. This
// is the single line that makes RevertEdit (edits.go:255) work — without
// it, a user-revert would write the post-edit text back, locking in the
// change rather than undoing it. F-007 audit did not flag this directly,
// but the same "audit false positive" class of bug (mutate first, then
// snapshot) would be caught here.
func TestApplyEditSnapshotBeforeMutation(t *testing.T) {
	src, err := os.ReadFile("edits.go")
	if err != nil {
		t.Fatalf("read edits.go: %v", err)
	}
	body := string(src)

	// preEdit capture must appear at least once.
	if !strings.Contains(body, "preEdit := cur") {
		t.Fatal("F-007 invariant drift: edits.go missing 'preEdit := cur' — RevertEdit cannot restore the pre-edit state")
	}

	// The capture must precede the switch on EditType. Cheap proxy: the
	// `preEdit := cur` line must appear earlier in the file than the
	// `switch edit.EditType` line. If a future refactor moves the
	// switch above the capture, RevertEdit would write the mutated text
	// back to the subject, silently locking in changes.
	preIdx := strings.Index(body, "preEdit := cur")
	switchIdx := strings.Index(body, "switch edit.EditType")
	if preIdx < 0 || switchIdx < 0 {
		t.Fatalf("F-007 invariant drift: cannot locate preEdit/switch markers (preIdx=%d, switchIdx=%d)", preIdx, switchIdx)
	}
	if preIdx >= switchIdx {
		t.Fatalf("F-007 invariant drift: 'preEdit := cur' (offset %d) appears AFTER 'switch edit.EditType' (offset %d) — snapshot would be the mutated state", preIdx, switchIdx)
	}
}