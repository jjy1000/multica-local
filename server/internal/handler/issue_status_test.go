package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// Issue status catalog tests (MUL-6243).

// seedTestCatalog makes the shared test workspace's catalog present. The
// fixture creates its workspace with raw SQL, so it has no catalog rows —
// which is itself the unseeded case covered by TestUnseededWorkspaceStillAccepts.
func seedTestCatalog(t *testing.T) {
	t.Helper()
	if err := issuestatus.Ensure(context.Background(), testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
}

// createTestCustomStatus inserts a custom status directly and removes it after
// the test, so catalog state cannot leak between tests in the shared workspace.
func createTestCustomStatus(t *testing.T, key, category string) db.IssueStatus {
	t.Helper()
	seedTestCatalog(t)
	entry, err := testHandler.Queries.CreateIssueStatusEntry(context.Background(), db.CreateIssueStatusEntryParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Key:         key,
		Name:        key,
		Description: "",
		Category:    category,
		Color:       "#123456",
	})
	if err != nil {
		t.Fatalf("create custom status %q: %v", key, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, entry.ID)
	})
	return entry
}

// withCustomIssueStatusesFlag flips h.FeatureFlags to a static-provider
// service with the MUL-6243 rollout key set, restoring the prior flags on
// cleanup. Fork adaptation: upstream keeps this helper in
// internal/handler/featureflag_test.go next to its internal/featureflags
// package; this fork has no such file, and the key lives in
// pkg/featureflag.CustomIssueStatuses.
func withCustomIssueStatusesFlag(t *testing.T, h *Handler, enabled bool) {
	t.Helper()
	provider := featureflag.NewStaticProvider()
	provider.Set(featureflag.CustomIssueStatuses, featureflag.Rule{Default: enabled})
	flags := featureflag.NewService(provider)

	origHandlerFlags := h.FeatureFlags
	h.FeatureFlags = flags
	t.Cleanup(func() {
		h.FeatureFlags = origHandlerFlags
	})
}

// TestEnsureIsIdempotent covers the rolling-deploy case: two pods can seed the

func TestEnsureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	seedTestCatalog(t)
	if err := issuestatus.Ensure(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("second Ensure should be a no-op, got: %v", err)
	}

	entries, err := testHandler.Queries.ListIssueStatusEntries(ctx, db.ListIssueStatusEntriesParams{
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("list catalog: %v", err)
	}

	systemByKey := map[string]db.IssueStatus{}
	for _, e := range entries {
		if e.IsSystem {
			systemByKey[e.Key] = e
		}
	}
	if len(systemByKey) != 7 {
		t.Fatalf("expected exactly 7 built-in rows after double seeding, got %d", len(systemByKey))
	}
	// Every built-in must be its own category's canonical — the invariant that
	// makes Effective an identity function on built-in keys.
	for key, entry := range systemByKey {
		if entry.Category != key {
			t.Errorf("built-in %q has category %q; a built-in must be its own category's canonical", key, entry.Category)
		}
	}
}

// TestCatalogOrderMatchesHistoricalStatusOrder pins the default board order.
// A workspace with no custom statuses must list exactly as it did before this

func TestCatalogOrderMatchesHistoricalStatusOrder(t *testing.T) {
	seedTestCatalog(t)
	entries, err := testHandler.Queries.ListIssueStatusEntries(context.Background(), db.ListIssueStatusEntriesParams{
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("list catalog: %v", err)
	}

	var gotSystem []string
	for _, e := range entries {
		if e.IsSystem {
			gotSystem = append(gotSystem, e.Key)
		}
	}
	want := []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"}
	for i := range want {
		if i >= len(gotSystem) || gotSystem[i] != want[i] {
			t.Fatalf("built-in order = %v, want %v (frontend STATUS_ORDER)", gotSystem, want)
		}
	}
}

// TestUnseededWorkspaceStillAcceptsBuiltInStatuses is the regression guard for
// the failure mode found while wiring this up: requiring a catalog row to
// validate a status made every issue write fail in a workspace whose seed had

func TestUnseededWorkspaceStillAcceptsBuiltInStatuses(t *testing.T) {
	ctx := context.Background()
	// Strip the catalog to simulate a workspace created by a pod that predates
	// the feature, or one the seed migration has not reached yet.
	if _, err := testPool.Exec(ctx, `DELETE FROM issue_status WHERE workspace_id = $1`, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("clear catalog: %v", err)
	}
	t.Cleanup(func() { seedTestCatalog(t) })

	for _, key := range issuestatus.Canonical() {
		if _, err := issuestatus.Resolve(ctx, testHandler.Queries, parseUUID(testWorkspaceID), key); err != nil {
			t.Errorf("built-in %q must resolve in an unseeded workspace, got: %v", key, err)
		}
		if got := issuestatus.Effective(ctx, testHandler.Queries, parseUUID(testWorkspaceID), key); got != key {
			t.Errorf("Effective(%q) = %q in an unseeded workspace, want the key unchanged", key, got)
		}
	}

	// A non-built-in key still needs a catalog row, so failing open is scoped
	// exactly to the set that was valid before this feature.
	if _, err := issuestatus.Resolve(ctx, testHandler.Queries, parseUUID(testWorkspaceID), "human_review"); err == nil {
		t.Error("a custom key with no catalog row must not resolve")
	}

	// The error message must never omit a key that Resolve accepts.
	keys, err := issuestatus.ActiveKeys(ctx, testHandler.Queries, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("ActiveKeys: %v", err)
	}
	if len(keys) != 7 {
		t.Errorf("ActiveKeys on an unseeded workspace = %v, want the 7 built-ins", keys)
	}
}

// TestCustomStatusInheritsItsCategoryBehavior is the core promise of the

func TestCustomStatusInheritsItsCategoryBehavior(t *testing.T) {
	ctx := context.Background()
	cases := []struct{ key, category string }{
		{"human_review_t", issuestatus.InReview},
		{"rework_t", issuestatus.Todo},
		{"gate_approved_t", issuestatus.Done},
		{"waiting_customer_t", issuestatus.Blocked},
		{"triage_later_t", issuestatus.Backlog},
	}
	for _, tc := range cases {
		createTestCustomStatus(t, tc.key, tc.category)
		got := issuestatus.Effective(ctx, testHandler.Queries, parseUUID(testWorkspaceID), tc.key)
		if got != tc.category {
			t.Errorf("Effective(%q) = %q, want %q", tc.key, got, tc.category)
		}
	}
}

func TestBuiltInStatusesAreImmutable(t *testing.T) {
	ctx := context.Background()
	seedTestCatalog(t)
	builtIn, err := testHandler.Queries.GetIssueStatusEntryByKey(ctx, db.GetIssueStatusEntryByKeyParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Key:         "in_review",
	})
	if err != nil {
		t.Fatalf("load built-in: %v", err)
	}

	t.Run("rename is refused at the API", func(t *testing.T) {
		req := withURLParam(
			newRequest(http.MethodPatch, "/api/issue-statuses/"+uuidToString(builtIn.ID), map[string]any{"name": "Renamed"}),
			"id", uuidToString(builtIn.ID))
		rec := httptest.NewRecorder()
		testHandler.UpdateIssueStatus(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 renaming a built-in, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("archive is refused at the API", func(t *testing.T) {
		req := withURLParam(
			newRequest(http.MethodDelete, "/api/issue-statuses/"+uuidToString(builtIn.ID), nil),
			"id", uuidToString(builtIn.ID))
		rec := httptest.NewRecorder()
		testHandler.ArchiveIssueStatus(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 archiving a built-in, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	// Defense in depth: even a direct query cannot rename or archive a
	// built-in, because the statements carry an is_system guard.
	t.Run("storage layer refuses too", func(t *testing.T) {
		if _, err := testHandler.Queries.UpdateIssueStatusEntry(ctx, db.UpdateIssueStatusEntryParams{
			ID:          builtIn.ID,
			WorkspaceID: parseUUID(testWorkspaceID),
			Name:        pgtype.Text{String: "Renamed", Valid: true},
		}); err == nil {
			t.Error("UpdateIssueStatusEntry must not touch a built-in row")
		}
		if _, err := testHandler.Queries.ArchiveIssueStatusEntry(ctx, db.ArchiveIssueStatusEntryParams{
			ID:          builtIn.ID,
			WorkspaceID: parseUUID(testWorkspaceID),
		}); err == nil {
			t.Error("ArchiveIssueStatusEntry must not touch a built-in row")
		}
	})
}

// TestArchiveRefusesWhileIssuesStillUseTheStatus is the decision recorded on

func TestCreateIssueStatusValidation(t *testing.T) {
	seedTestCatalog(t)
	withCustomIssueStatusesFlag(t, testHandler, true)

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"reserved built-in key", map[string]any{"name": "Mine", "key": "in_review", "category": "in_review", "color": "#123456"}, http.StatusBadRequest},
		{"name slugifying onto a built-in", map[string]any{"name": "In Review", "category": "in_review", "color": "#123456"}, http.StatusBadRequest},
		{"unknown category", map[string]any{"name": "Weird", "category": "started", "color": "#123456"}, http.StatusBadRequest},
		{"missing category", map[string]any{"name": "Weird2", "color": "#123456"}, http.StatusBadRequest},
		{"bad color", map[string]any{"name": "Weird3", "category": "todo", "color": "red"}, http.StatusBadRequest},
		{"empty name", map[string]any{"name": "  ", "category": "todo", "color": "#123456"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			testHandler.CreateIssueStatus(rec, newRequest(http.MethodPost, "/api/issue-statuses", tc.body))
			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCreateIssueStatusIsGatedOnRollout pins the rollout gate. Creating the
// first custom status mints a value older pods cannot interpret, so it must be

func TestCreateIssueStatusIsGatedOnRollout(t *testing.T) {
	seedTestCatalog(t)
	withCustomIssueStatusesFlag(t, testHandler, false)

	rec := httptest.NewRecorder()
	testHandler.CreateIssueStatus(rec, newRequest(http.MethodPost, "/api/issue-statuses", map[string]any{
		"name": "Human Review", "category": "in_review", "color": "#123456",
	}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 while the rollout gate is closed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Reading the catalog is never gated — clients need it to render statuses.
	listRec := httptest.NewRecorder()
	testHandler.ListIssueStatuses(listRec, newRequest(http.MethodGet, "/api/issue-statuses", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("listing must stay open while the gate is closed, got %d: %s", listRec.Code, listRec.Body.String())
	}

	// Fork-local extension: with the gate open the same request must create.
	// This is the 403 -> 201 wiring proof (testHandler.FeatureFlags is nil
	// until withCustomIssueStatusesFlag assigns it, so the closed side above
	// also proves nil reads as off).
	withCustomIssueStatusesFlag(t, testHandler, true)
	openRec := httptest.NewRecorder()
	testHandler.CreateIssueStatus(openRec, newRequest(http.MethodPost, "/api/issue-statuses", map[string]any{
		"name": "Human Review On", "category": "in_review", "color": "#123456",
	}))
	if openRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 while the rollout gate is open, got %d: %s", openRec.Code, openRec.Body.String())
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE key = 'human_review_on'`)
	})
}

// TestIssueWriteStoresCanonicalStatusKey guards the 500 found in review:
// resolution is case- and whitespace-insensitive, so writing the caller's raw
