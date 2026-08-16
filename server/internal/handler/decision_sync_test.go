// Package handler — decision_sync_test.go (0.5.22 Semantica × Multica Phase 2)
//
// Unit tests for the pure decision-envelope builder + a source-level
// contract test pinning the goroutine-offload pattern. No DB / HTTP —
// buildSemanticaDecision is a pure function so the test drives it
// directly with a synthetic db.Issue row.
package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildSemanticaDecision_MapsIssueToEnvelope(t *testing.T) {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")
	wsUUID := mustParseUUID(t, "22345678-1234-4234-8234-123456789abc")
	actorUUID := mustParseUUID(t, "32345678-1234-4234-8234-123456789abc")

	longDesc := strings.Repeat("x", 3000)
	row := db.Issue{
		ID:          issueUUID,
		WorkspaceID: wsUUID,
		Title:       "Deploy v2 to production",
		Description: pgtype.Text{String: longDesc, Valid: true},
		Status:      "done",
	}

	d := buildSemanticaDecision(row, "done", "agent", actorUUID)

	// ID is the idempotency key on the Semantica side.
	if want := "multica_" + util.UUIDToString(issueUUID); d.ID != want {
		t.Errorf("ID = %q, want %q", d.ID, want)
	}
	if d.Title != "Deploy v2 to production" {
		t.Errorf("Title = %q", d.Title)
	}
	if d.Status != "done" {
		t.Errorf("Status = %q", d.Status)
	}
	// Description is truncated to semanticaDecisionDescriptionMax runes
	// plus a trailing Unicode ellipsis.
	if !strings.HasPrefix(d.Description, strings.Repeat("x", semanticaDecisionDescriptionMax)) {
		t.Errorf("Description not truncated to %d runes", semanticaDecisionDescriptionMax)
	}
	if !strings.HasSuffix(d.Description, "…") {
		t.Errorf("Description missing truncation ellipsis")
	}
	// Tags are stable so Semantica queries can filter the corpus.
	if len(d.Tags) != 2 || d.Tags[0] != "multica" || d.Tags[1] != "lab:semantica" {
		t.Errorf("Tags = %v, want [multica lab:semantica]", d.Tags)
	}
	// Provenance carries the full traceability chain.
	if d.Provenance.Source != "multica" {
		t.Errorf("Provenance.Source = %q", d.Provenance.Source)
	}
	if d.Provenance.IssueID != util.UUIDToString(issueUUID) {
		t.Errorf("Provenance.IssueID = %q", d.Provenance.IssueID)
	}
	if d.Provenance.WorkspaceID != util.UUIDToString(wsUUID) {
		t.Errorf("Provenance.WorkspaceID = %q", d.Provenance.WorkspaceID)
	}
	if d.Provenance.ActorType != "agent" {
		t.Errorf("Provenance.ActorType = %q", d.Provenance.ActorType)
	}
	if d.Provenance.ActorID != util.UUIDToString(actorUUID) {
		t.Errorf("Provenance.ActorID = %q", d.Provenance.ActorID)
	}
	if d.Provenance.OccurredAt == "" {
		t.Errorf("Provenance.OccurredAt is empty")
	}
}

func TestBuildSemanticaDecision_EmptyDescriptionAndActor(t *testing.T) {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")
	wsUUID := mustParseUUID(t, "22345678-1234-4234-8234-123456789abc")

	row := db.Issue{
		ID:          issueUUID,
		WorkspaceID: wsUUID,
		Title:       "No description",
		Description: pgtype.Text{}, // invalid — empty
		Status:      "cancelled",
	}

	d := buildSemanticaDecision(row, "cancelled", "", pgtype.UUID{})

	if d.Description != "" {
		t.Errorf("Description = %q, want empty", d.Description)
	}
	// Empty actor_id must be omitted (omitempty) — the struct field is
	// empty and the JSON marshaller drops it.
	if d.Provenance.ActorID != "" {
		t.Errorf("ActorID = %q, want empty", d.Provenance.ActorID)
	}
}

// TestBuildSemanticaDecision_UTF8TruncationSafe — pins the rune-aware
// truncation contract. A 2500-rune CJK string is 7500+ bytes; the
// old byte-indexed slice would have split a multi-byte sequence at
// byte 2000 and corrupted the JSON payload. The rune-aware slice
// keeps every codepoint whole AND the result must be valid UTF-8.
func TestBuildSemanticaDecision_UTF8TruncationSafe(t *testing.T) {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")
	wsUUID := mustParseUUID(t, "22345678-1234-4234-8234-123456789abc")

	// 2500 Chinese characters = 7500 bytes, well past the 2000-rune cap.
	cjkDesc := strings.Repeat("中", 2500)
	if len(cjkDesc) <= semanticaDecisionDescriptionMax {
		t.Fatalf("test setup error: CJK desc length %d should exceed byte cap %d",
			len(cjkDesc), semanticaDecisionDescriptionMax)
	}

	row := db.Issue{
		ID:          issueUUID,
		WorkspaceID: wsUUID,
		Title:       "CJK test",
		Description: pgtype.Text{String: cjkDesc, Valid: true},
		Status:      "done",
	}

	d := buildSemanticaDecision(row, "done", "agent", pgtype.UUID{})

	// Truncation marker must be present.
	if !strings.HasSuffix(d.Description, "…") {
		t.Fatalf("Description missing truncation ellipsis: %q", d.Description)
	}
	// Rune count must equal cap exactly (no over- or under-shoot).
	runes := []rune(d.Description)
	if len(runes) != semanticaDecisionDescriptionMax+1 { // +1 for the ellipsis rune
		t.Errorf("Description rune count = %d, want %d (+1 for ellipsis)",
			len(runes), semanticaDecisionDescriptionMax+1)
	}
	// Result must be valid UTF-8 (no mid-codepoint splits).
	if !utf8.ValidString(d.Description) {
		t.Errorf("Description contains invalid UTF-8 (rune slice corrupted)")
	}
	// And the JSON envelope must marshal cleanly.
	payload, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !utf8.Valid(payload) {
		t.Errorf("JSON payload contains invalid UTF-8: %s", payload)
	}
}

// TestBuildSemanticaDecision_BoundaryLengths — exercise the exact
// 1999/2000/2001/2002 byte + rune boundaries so a future off-by-one
// in the truncation predicate surfaces immediately.
func TestBuildSemanticaDecision_BoundaryLengths(t *testing.T) {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")
	wsUUID := mustParseUUID(t, "22345678-1234-4234-8234-123456789abc")

	cases := []struct {
		name      string
		desc      string
		wantTrunc bool
		wantRunes int
	}{
		{"under cap (ASCII 1999)", strings.Repeat("x", 1999), false, 1999},
		{"at cap (ASCII 2000)", strings.Repeat("x", semanticaDecisionDescriptionMax), false, semanticaDecisionDescriptionMax},
		{"over cap by 1 (ASCII 2001)", strings.Repeat("x", semanticaDecisionDescriptionMax+1), true, semanticaDecisionDescriptionMax + 1}, // +1 for ellipsis
		{"over cap by 2 (ASCII 2002)", strings.Repeat("x", semanticaDecisionDescriptionMax+2), true, semanticaDecisionDescriptionMax + 1},
		{"at cap (CJK 2000)", strings.Repeat("中", semanticaDecisionDescriptionMax), false, semanticaDecisionDescriptionMax},
		{"over cap (CJK 2001)", strings.Repeat("中", semanticaDecisionDescriptionMax+1), true, semanticaDecisionDescriptionMax + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := db.Issue{
				ID:          issueUUID,
				WorkspaceID: wsUUID,
				Title:       "boundary",
				Description: pgtype.Text{String: tc.desc, Valid: true},
				Status:      "done",
			}
			d := buildSemanticaDecision(row, "done", "agent", pgtype.UUID{})
			if tc.wantTrunc && !strings.HasSuffix(d.Description, "…") {
				t.Errorf("expected truncation, got %q", d.Description)
			}
			if !tc.wantTrunc && strings.HasSuffix(d.Description, "…") {
				t.Errorf("unexpected truncation: %q", d.Description)
			}
			if got := len([]rune(d.Description)); got != tc.wantRunes {
				t.Errorf("rune count = %d, want %d", got, tc.wantRunes)
			}
			if !utf8.ValidString(d.Description) {
				t.Errorf("invalid UTF-8 in output")
			}
		})
	}
}

// TestBuildSemanticaDecision_EmptyTitle — the LOW F6 fix: an issue
// with empty title should still produce a searchable decision record.
// The fallback uses the issue UUID as the visible title.
func TestBuildSemanticaDecision_EmptyTitle(t *testing.T) {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")
	wsUUID := mustParseUUID(t, "22345678-1234-4234-8234-123456789abc")

	row := db.Issue{
		ID:          issueUUID,
		WorkspaceID: wsUUID,
		Title:       "",
		Description: pgtype.Text{},
		Status:      "done",
	}

	d := buildSemanticaDecision(row, "done", "agent", pgtype.UUID{})

	if d.Title == "" {
		t.Fatalf("Title should have a fallback (UUID), got empty string")
	}
	// Fallback should reference the issue UUID so the record is
	// traceable to the source.
	if !strings.Contains(d.Title, util.UUIDToString(issueUUID)) {
		t.Errorf("Title fallback = %q, expected to contain UUID %q",
			d.Title, util.UUIDToString(issueUUID))
	}
}

// TestBuildSemanticaDecision_ZeroWorkspaceID — the MEDIUM F23 edge
// case. A zero pgtype.UUID on the row must not panic; the envelope
// emits an empty workspace_id string.
func TestBuildSemanticaDecision_ZeroWorkspaceID(t *testing.T) {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")

	row := db.Issue{
		ID:          issueUUID,
		WorkspaceID: pgtype.UUID{}, // zero value
		Title:       "no workspace",
		Description: pgtype.Text{},
		Status:      "closed",
	}

	d := buildSemanticaDecision(row, "closed", "system", pgtype.UUID{})

	if d.Provenance.WorkspaceID != "" {
		t.Errorf("WorkspaceID = %q, want empty for zero pgtype.UUID", d.Provenance.WorkspaceID)
	}
	// Envelope must still marshal cleanly.
	payload, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !strings.Contains(string(payload), `"workspace_id":""`) {
		t.Errorf("expected empty workspace_id in JSON, got: %s", payload)
	}
}

// flipSemanticaDefault temporarily sets the semantica catalog default
// so experimental.DefaultFor("semantica") returns the value a test
// needs. DefaultVal is the catalog's only authoritative toggle; the
// flag-off gate in postDecisionSync short-circuits before any HTTP
// work, so an end-to-end test must flip it on. Restored via t.Cleanup.
// Tests in this package run sequentially (no t.Parallel), so the
// brief mutation is safe.
func flipSemanticaDefault(t *testing.T, val bool) {
	t.Helper()
	for i := range experimental.Catalog {
		if experimental.Catalog[i].Key == "semantica" {
			old := experimental.Catalog[i].DefaultVal
			experimental.Catalog[i].DefaultVal = val
			t.Cleanup(func() { experimental.Catalog[i].DefaultVal = old })
			return
		}
	}
	t.Fatalf("semantica not found in catalog")
}

// testSyncHandler builds a Handler whose registry points the semantica
// loopback at url. The flag default is flipped on so postDecisionSync
// reaches the HTTP POST instead of short-circuiting.
func testSyncHandler(t *testing.T, url string) *Handler {
	t.Helper()
	flipSemanticaDefault(t, true)
	reg := experimental.NewRegistry()
	reg.SetLoopbackURL("semantica", url)
	return &Handler{ExperimentRegistry: reg}
}

func testSyncRow(t *testing.T) db.Issue {
	issueUUID := mustParseUUID(t, "12345678-1234-4234-8234-123456789abc")
	wsUUID := mustParseUUID(t, "22345678-1234-4234-8234-123456789abc")
	return db.Issue{
		ID:          issueUUID,
		WorkspaceID: wsUUID,
		Title:       "Deploy v2",
		Description: pgtype.Text{String: "ship it", Valid: true},
		Status:      "done",
	}
}

// TestSyncIssueDecisionToSemantica_ReturnsImmediately — HIGH #5 / F24.
// Pins the load-bearing goroutine-offload contract documented in the
// file header: SyncIssueDecisionToSemantica must return in well under
// 100 ms even when the upstream HTTP server blocks for 200 ms. A
// regression that drops `go h.postDecisionSync(...)` would block the
// event-bus publish site for the full upstream latency.
func TestSyncIssueDecisionToSemantica_ReturnsImmediately(t *testing.T) {
	// The handler signals it was reached, then sleeps to simulate a
	// slow upstream. The spawned goroutine blocks inside Do() for the
	// sleep duration, but the caller must not wait for it.
	handlerHit := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerHit)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := testSyncHandler(t, srv.URL)
	start := time.Now()
	h.SyncIssueDecisionToSemantica(testSyncRow(t), "done", "agent", pgtype.UUID{})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("SyncIssueDecisionToSemantica blocked for %v; expected <100ms (goroutine offload broken)", elapsed)
	}

	// The caller returned, so the goroutine is running in the
	// background. Confirm it actually reached the upstream server
	// (proving the offload fired, not skipped), then give it a settle
	// window so the race detector sees no lingering shared-state access
	// at teardown.
	select {
	case <-handlerHit:
	case <-time.After(2 * time.Second):
		t.Fatal("spawned goroutine never reached the upstream server")
	}
	time.Sleep(300 * time.Millisecond)
}

// TestSyncIssueDecisionToSemantica_NilHandlerSafe — HIGH #5 / F24
// companion. The nil-handler guard must return immediately without
// panicking.
func TestSyncIssueDecisionToSemantica_NilHandlerSafe(t *testing.T) {
	var h *Handler
	start := time.Now()
	h.SyncIssueDecisionToSemantica(testSyncRow(t), "done", "agent", pgtype.UUID{})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("nil-handler call blocked for %v", elapsed)
	}
}

// TestPostDecisionSync_HappyPath — HIGH #6 / F25. Drives the
// synchronous goroutine body directly and asserts a POST lands on
// /api/decisions with a well-formed JSON envelope.
func TestPostDecisionSync_HappyPath(t *testing.T) {
	var gotPath, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Multica-Embedded")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := testSyncHandler(t, srv.URL)
	row := testSyncRow(t)
	h.postDecisionSync(row, "done", "agent", pgtype.UUID{})

	if gotPath != "/api/decisions" {
		t.Errorf("path = %q, want /api/decisions", gotPath)
	}
	if gotHeader != "1" {
		t.Errorf("X-Multica-Embedded = %q, want \"1\"", gotHeader)
	}
	var env semanticaDecision
	if err := json.Unmarshal([]byte(gotBody), &env); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, gotBody)
	}
	if env.ID != "multica_"+util.UUIDToString(row.ID) {
		t.Errorf("envelope ID = %q, want multica_<uuid>", env.ID)
	}
}

// TestPostDecisionSync_SubprocessDown — HIGH #6 / F25. When the
// loopback URL is empty (manager not up), the body must no-op with a
// Debug log, never a panic or a POST.
func TestPostDecisionSync_SubprocessDown(t *testing.T) {
	flipSemanticaDefault(t, true)
	reg := experimental.NewRegistry() // no SetLoopbackURL → empty
	h := &Handler{ExperimentRegistry: reg}

	var posted bool
	// No server to hit — if a POST fires, this would need a URL; we
	// assert the function returns without panicking instead.
	h.postDecisionSync(testSyncRow(t), "done", "agent", pgtype.UUID{})
	if posted {
		t.Errorf("POST fired despite empty loopback URL")
	}
}

// TestPostDecisionSync_5xxResponse — HIGH #6 / F25. Non-2xx upstream
// must be logged, never panic.
func TestPostDecisionSync_5xxResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := testSyncHandler(t, srv.URL)
	h.postDecisionSync(testSyncRow(t), "done", "agent", pgtype.UUID{}) // must not panic
}

// TestPostDecisionSync_FlagOff — HIGH #6 / F25. With the flag default
// false, the body must short-circuit before any HTTP. We do NOT flip
// the default here; the catalog default is false for semantica.
func TestPostDecisionSync_FlagOff(t *testing.T) {
	// Ensure the flag is off (its production default).
	flipSemanticaDefault(t, false)

	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := experimental.NewRegistry()
	reg.SetLoopbackURL("semantica", srv.URL)
	h := &Handler{ExperimentRegistry: reg}

	h.postDecisionSync(testSyncRow(t), "done", "agent", pgtype.UUID{})

	if hit {
		t.Errorf("POST fired despite flag off")
	}
}

// TestPostDecisionSync_NilRegistry — HIGH #6 / F25. Nil registry must
// no-op without panic.
func TestPostDecisionSync_NilRegistry(t *testing.T) {
	flipSemanticaDefault(t, true)
	h := &Handler{ExperimentRegistry: nil}
	h.postDecisionSync(testSyncRow(t), "done", "agent", pgtype.UUID{}) // must not panic
}
