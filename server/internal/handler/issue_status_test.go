package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
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

// ── MUL-6749: derived status keys from non-Latin display names ──────────
//
// Port of upstream d6ecf4bc8's handler tests, adapted to this fork's test
// idioms: raw httptest recorders instead of testutil.Call, and every API
// create opens the fork-local MUL-6243 rollout gate first (upstream has no
// gate; here a closed flag 403s before any key logic runs).

// createStatusThroughAPI posts to CreateIssueStatus with the rollout gate open
// and decodes the response body as IssueStatusResponse.
func createStatusThroughAPI(t *testing.T, body map[string]any) (*httptest.ResponseRecorder, IssueStatusResponse) {
	t.Helper()
	withCustomIssueStatusesFlag(t, testHandler, true)
	var created IssueStatusResponse
	rec := httptest.NewRecorder()
	testHandler.CreateIssueStatus(rec, newRequest(http.MethodPost, "/api/issue-statuses", body))
	json.Unmarshal(rec.Body.Bytes(), &created)
	return rec, created
}

// TestCreateIssueStatusAcceptsANonLatinName is the regression for MUL-6749.
// A display name written entirely in a non-Latin script has no characters in
// the key alphabet, so key derivation used to fail the create outright — and
// the settings form has no field for an explicit key, which left no way to
// create the status at all.
func TestCreateIssueStatusAcceptsANonLatinName(t *testing.T) {
	seedTestCatalog(t)

	create := func(t *testing.T, name, category string) IssueStatusResponse {
		t.Helper()
		rec, created := createStatusThroughAPI(t, map[string]any{
			"name": name, "category": category, "color": "#123456",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, parseUUID(created.ID))
		})
		return created
	}

	first := create(t, "客户确认", issuestatus.InReview)
	if first.Key != "in_review_2" {
		t.Errorf("derived key = %q, want %q", first.Key, "in_review_2")
	}
	if first.Name != "客户确认" {
		t.Errorf("display name = %q, want it stored verbatim", first.Name)
	}

	// A second one in the same category takes the next ordinal instead of
	// colliding with the first.
	second := create(t, "供应商确认", issuestatus.InReview)
	if second.Key != "in_review_3" {
		t.Errorf("second derived key = %q, want %q", second.Key, "in_review_3")
	}

	// A name that CAN be slugged is untouched by the fallback.
	english := create(t, "Human Review Zh", issuestatus.InReview)
	if english.Key != "human_review_zh" {
		t.Errorf("sluggable name derived %q, want %q", english.Key, "human_review_zh")
	}
}

// TestCreateIssueStatusDisambiguatesCollidingSlugs covers the sharper half of
// MUL-6749. The bug report's suggested workaround was to mix ASCII into the
// display name, but the non-ASCII part is dropped, so two DIFFERENT names
// collapse onto one key — and the second create used to fail with a conflict
// that blamed a display name nobody had taken.
func TestCreateIssueStatusDisambiguatesCollidingSlugs(t *testing.T) {
	seedTestCatalog(t)

	create := func(t *testing.T, name string) IssueStatusResponse {
		t.Helper()
		rec, created := createStatusThroughAPI(t, map[string]any{
			"name": name, "category": issuestatus.Todo, "color": "#123456",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, parseUUID(created.ID))
		})
		return created
	}

	if got := create(t, "待客户 Zzreview").Key; got != "zzreview" {
		t.Fatalf("first key = %q, want %q", got, "zzreview")
	}
	if got := create(t, "待供应商 Zzreview").Key; got != "zzreview_2" {
		t.Errorf("colliding key = %q, want %q", got, "zzreview_2")
	}
}

// TestDerivedKeyAvoidsAnArchivedKey pins the storage constraint that makes the
// "taken" set wider than the visible catalog: idx_issue_status_workspace_key is
// NOT partial, so an archived status still owns its key and handing it out
// again would fail on insert.
func TestDerivedKeyAvoidsAnArchivedKey(t *testing.T) {
	entry := createTestCustomStatus(t, "zzarchived", issuestatus.Todo)
	if _, err := testHandler.Queries.ArchiveIssueStatusEntry(context.Background(), db.ArchiveIssueStatusEntryParams{
		ID:          entry.ID,
		WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	rec, created := createStatusThroughAPI(t, map[string]any{
		"name": "Zzarchived", "category": issuestatus.Todo, "color": "#123456",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, parseUUID(created.ID))
	})

	if created.Key == "zzarchived" {
		t.Fatal("derivation reused an archived status's key")
	}
	if created.Key != "zzarchived_2" {
		t.Errorf("derived key = %q, want %q", created.Key, "zzarchived_2")
	}
}

// TestUnknownStatusErrorNamesCustomStatuses covers the other half of what makes
// a derived key usable: on its own `in_review_2` says nothing, so the error a
// caller gets after writing a bad status has to carry the display name or there
// is no way to find the status they were told to use. (MUL-6749)
func TestUnknownStatusErrorNamesCustomStatuses(t *testing.T) {
	seedTestCatalog(t)
	entry, err := testHandler.Queries.CreateIssueStatusEntry(context.Background(), db.CreateIssueStatusEntryParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Key:         "in_review_9",
		Name:        "客户确认",
		Description: "",
		Category:    issuestatus.InReview,
		Color:       "#123456",
	})
	if err != nil {
		t.Fatalf("create custom status: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, entry.ID)
	})

	rec := httptest.NewRecorder()
	testHandler.CreateIssue(rec, newRequest(http.MethodPost, "/api/issues", map[string]any{
		"title":  "unknown status error body",
		"status": "not_a_status",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "in_review_9 (客户确认)") {
		t.Errorf("error body does not pair the key with its name: %s", body)
	}
	// Built-ins stay bare — clients localize those from the key, so echoing the
	// seeded English name would be the one string a Chinese workspace ignores.
	if strings.Contains(body, "todo (Todo)") {
		t.Errorf("built-in statuses should be listed as bare keys: %s", body)
	}
}

// TestIssueResponseCarriesCustomStatusName pins the field an agent reads an
// issue through. `status` alone is a bare handle, and a derived key carries no
// meaning, so the display name travels beside it. (MUL-6749)
func TestIssueResponseCarriesCustomStatusName(t *testing.T) {
	seedTestCatalog(t)
	entry, err := testHandler.Queries.CreateIssueStatusEntry(context.Background(), db.CreateIssueStatusEntryParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Key:         "in_review_8",
		Name:        "客户确认",
		Description: "",
		Category:    issuestatus.InReview,
		Color:       "#123456",
	})
	if err != nil {
		t.Fatalf("create custom status: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, entry.ID)
	})

	var custom IssueResponse
	rec := httptest.NewRecorder()
	testHandler.CreateIssue(rec, newRequest(http.MethodPost, "/api/issues", map[string]any{
		"title": "custom status name", "status": "in_review_8",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &custom); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, parseUUID(custom.ID))
	})
	if custom.StatusName != "客户确认" {
		t.Errorf("status_name = %q, want %q", custom.StatusName, "客户确认")
	}
	if custom.StatusCategory != issuestatus.InReview {
		t.Errorf("status_category = %q, want %q", custom.StatusCategory, issuestatus.InReview)
	}

	// A built-in carries no name: every client renders those from the key
	// through i18n, so the seeded English one would be noise at best.
	var builtIn IssueResponse
	rec = httptest.NewRecorder()
	testHandler.CreateIssue(rec, newRequest(http.MethodPost, "/api/issues", map[string]any{
		"title": "built-in status name", "status": issuestatus.Todo,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &builtIn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, parseUUID(builtIn.ID))
	})
	if builtIn.StatusName != "" {
		t.Errorf("built-in status_name = %q, want it empty", builtIn.StatusName)
	}
}

// TestExplicitKeyCreateAlsoTakesTheCatalogLock closes the half-open race found
// in review of MUL-6749. Derivation reads the catalog and then inserts; if a
// create that supplies its OWN key were allowed to skip the lock, it could land
// on the key the derive just chose in exactly that gap. The derive would then
// fail on the unique index and return 409 to a settings form that has no key
// field — the same dead end this issue exists to remove.
//
// The assertion is that an explicit-key create PARKS while the lock is held.
// Before the fix it completed immediately.
func TestExplicitKeyCreateAlsoTakesTheCatalogLock(t *testing.T) {
	seedTestCatalog(t)
	ctx := context.Background()

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text || ':issue_status', 0))`,
		parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("take exclusive lock: %v", err)
	}

	// The rollout gate must be flipped on the test goroutine — the helper
	// registers t.Cleanup, which is not goroutine-safe.
	withCustomIssueStatusesFlag(t, testHandler, true)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		testHandler.CreateIssueStatus(rec, newRequest(http.MethodPost, "/api/issue-statuses", map[string]any{
			"name": "Zzlockprobe", "key": "zzlockprobe", "category": issuestatus.Todo, "color": "#123456",
		}))
		done <- rec
	}()

	select {
	case rec := <-done:
		t.Fatalf("an explicit-key create completed (%d) while the catalog lock was held; "+
			"it can still insert between a derived create's catalog read and its insert", rec.Code)
	case <-time.After(400 * time.Millisecond):
		// Parked on the lock, which is the point.
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	select {
	case rec := <-done:
		if rec.Code != http.StatusCreated {
			t.Fatalf("explicit-key create after the lock released: %d %s", rec.Code, rec.Body.String())
		}
		var created IssueStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, parseUUID(created.ID))
		})
	case <-time.After(10 * time.Second):
		t.Fatal("explicit-key create never completed after the lock released")
	}
}

// TestConcurrentDerivedCreatesNeverConflict is the behavioral half of the same
// fix: whatever else is writing the catalog, a create that supplied no key must
// never come back 409. A caller who typed a key CAN legitimately be told it is
// taken; a caller who typed only a display name has nothing to correct.
//
// Every result carries WHICH kind of writer produced it. Counting successes
// without that lets an explicit writer's 201 stand in for a derived writer's
// 409 and passes on exactly the regression it exists to catch.
//
// This is a probabilistic net, not a proof: the window it hunts for is the few
// microseconds between a derive's catalog read and its insert, and in-process
// it does not reliably open. TestExplicitKeyCreateAlsoTakesTheCatalogLock is
// the deterministic guard on the lock itself — this one guards the OUTCOME, so
// that any future path which lets a keyless create conflict shows up here even
// if the lock is still nominally taken. Do not delete one for the other.
func TestConcurrentDerivedCreatesNeverConflict(t *testing.T) {
	seedTestCatalog(t)

	const derivedWriters = 6
	const rounds = 4
	type result struct {
		derived bool
		label   string
		code    int
		key     string
		body    string
	}

	// Every derived name must be ALL non-ASCII, or it slugs to something and
	// never reaches the fallback the test is about: "客户确认0-1" keeps its ASCII
	// digits and derives `0_1`, which contends with nothing. Uniqueness comes
	// from a distinct base per writer and a repeated character per round, so the
	// names stay collision-free without smuggling in a digit.
	bases := []string{"客户确认", "供应商确认", "财务确认", "法务确认", "安全确认", "运营确认"}
	if len(bases) != derivedWriters {
		t.Fatalf("need one non-ASCII base per derived writer, got %d for %d", len(bases), derivedWriters)
	}

	withCustomIssueStatusesFlag(t, testHandler, true)
	for round := range rounds {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			results := make(chan result, derivedWriters+2)
			start := make(chan struct{})
			var wg sync.WaitGroup

			post := func(derived bool, label string, body map[string]any) {
				defer wg.Done()
				<-start
				rec := httptest.NewRecorder()
				testHandler.CreateIssueStatus(rec, newRequest(http.MethodPost, "/api/issue-statuses", body))
				var created IssueStatusResponse
				json.Unmarshal(rec.Body.Bytes(), &created)
				results <- result{derived, label, rec.Code, created.Key, rec.Body.String()}
			}

			// Admins naming a status in a non-Latin script, all in one category,
			// so every one of them derives from the same base.
			for i := range derivedWriters {
				name := bases[i] + strings.Repeat("确", round+1)
				wg.Add(1)
				go post(true, name, map[string]any{
					"name": name, "category": issuestatus.Blocked, "color": "#123456",
				})
			}
			// Explicit writers aiming straight at the ordinals the derives want.
			for _, key := range []string{"blocked_2", "blocked_3"} {
				wg.Add(1)
				go post(false, key, map[string]any{
					"name": fmt.Sprintf("Zz %s %d", key, round), "key": key,
					"category": issuestatus.Blocked, "color": "#123456",
				})
			}

			close(start)
			wg.Wait()
			close(results)

			t.Cleanup(func() {
				testPool.Exec(context.Background(),
					`DELETE FROM issue_status WHERE workspace_id = $1 AND category = 'blocked' AND is_system = FALSE`,
					parseUUID(testWorkspaceID))
			})

			seen := map[string]bool{}
			derivedSucceeded := 0
			for r := range results {
				switch {
				case r.derived && r.code != http.StatusCreated:
					// The regression this test exists for: a writer with no key
					// field to correct was told to correct one.
					t.Errorf("derived create %q returned %d, want 201 — a create with no key must never conflict: %s",
						r.label, r.code, r.body)
					continue
				case !r.derived && r.code != http.StatusCreated && r.code != http.StatusConflict:
					t.Errorf("explicit create %q returned %d, want 201 or 409: %s", r.label, r.code, r.body)
					continue
				case r.code == http.StatusConflict:
					// Only reachable for an explicit writer, by the branch above.
					if !strings.Contains(r.body, "already exists") {
						t.Errorf("unexpected 409 body for %q: %s", r.label, r.body)
					}
					continue
				}
				if seen[r.key] {
					t.Errorf("two statuses were created with the same key %q", r.key)
				}
				seen[r.key] = true
				if issuestatus.IsBuiltIn(r.key) {
					t.Errorf("a custom status shadowed the built-in key %q", r.key)
				}
				if r.derived {
					// Pins the PREMISE: a derived name that slugs to anything at
					// all takes the slug path and contends with nothing, which
					// would leave this test green while exercising nothing.
					if !strings.HasPrefix(r.key, issuestatus.Blocked+"_") {
						t.Errorf("derived create %q produced key %q; the name must slug to nothing "+
							"so it lands on the <category>_<n> fallback these writers contend for",
							r.label, r.key)
					}
					derivedSucceeded++
				}
			}
			if derivedSucceeded != derivedWriters {
				t.Errorf("%d of %d DERIVED creates succeeded", derivedSucceeded, derivedWriters)
			}
		})
	}
}

// TestCustomStatusPayloadsAgreeAcrossRenderings pins the contract
// TestIssueToMap_KeysMatchIssueResponse cannot reach with a built-in fixture:
// an issue on a CUSTOM status must describe itself identically whether it
// arrives over HTTP or on a background event. A field present in one and blank
// in the other reads back undefined depending on which entry point produced
// the issue. (MUL-6749)
func TestCustomStatusPayloadsAgreeAcrossRenderings(t *testing.T) {
	ctx := context.Background()
	seedTestCatalog(t)
	entry, err := testHandler.Queries.CreateIssueStatusEntry(ctx, db.CreateIssueStatusEntryParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Key:         "in_review_7",
		Name:        "客户确认",
		Description: "",
		Category:    issuestatus.InReview,
		Color:       "#123456",
	})
	if err != nil {
		t.Fatalf("create custom status: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_status WHERE id = $1`, entry.ID)
	})

	var fromHTTP IssueResponse
	rec := httptest.NewRecorder()
	testHandler.CreateIssue(rec, newRequest(http.MethodPost, "/api/issues", map[string]any{
		"title": "payload parity", "status": "in_review_7",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fromHTTP); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, parseUUID(fromHTTP.ID))
	})

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(fromHTTP.ID))
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	fromEvent := service.IssueToMapResolved(ctx, testHandler.Queries, issue, "MUL")

	for field, want := range map[string]string{
		"status":          "in_review_7",
		"status_category": issuestatus.InReview,
		"status_name":     "客户确认",
	} {
		got, ok := fromEvent[field].(string)
		if !ok {
			t.Errorf("event payload is missing %q; clients treat it as a complete issue", field)
			continue
		}
		if got != want {
			t.Errorf("event %s = %q, want %q", field, got, want)
		}
	}
	if fromHTTP.StatusName != fromEvent["status_name"] {
		t.Errorf("status_name differs by entry point: HTTP %q, event %q",
			fromHTTP.StatusName, fromEvent["status_name"])
	}
	if fromHTTP.StatusCategory != fromEvent["status_category"] {
		t.Errorf("status_category differs by entry point: HTTP %q, event %q",
			fromHTTP.StatusCategory, fromEvent["status_category"])
	}
}

// TestCustomTerminalStatusCountsAsTerminalInSQL covers the SQL-side consumers
// the Go resolver cannot reach. Fork idiom: raw SQL fixtures instead of the
// upstream dbfx builders (this fork does not carry that package). Terminal
// categories are expanded once into concrete keys so custom done statuses
// keep their behavior without a per-row issue_effective_status call.
func TestCustomTerminalStatusCountsAsTerminalInSQL(t *testing.T) {
	ctx := context.Background()
	createTestCustomStatus(t, "gate_done_s", issuestatus.Done)

	insertIssue := func(t *testing.T, title, status string) string {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
			VALUES ($1, $2, $3, 'medium', 'member', $4,
			        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
			RETURNING id
		`, testWorkspaceID, title, status, testUserID).Scan(&id); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id) })
		return id
	}

	terminalStatusKeys, err := testHandler.terminalIssueStatusKeys(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("terminalIssueStatusKeys: %v", err)
	}

	t.Run("open listing excludes a custom done status", func(t *testing.T) {
		customDone := insertIssue(t, "sql open custom done", "gate_done_s")
		open := insertIssue(t, "sql open plain", "todo")
		rows, err := testHandler.Queries.ListOpenIssues(ctx, db.ListOpenIssuesParams{
			WorkspaceID:        parseUUID(testWorkspaceID),
			TerminalStatusKeys: terminalStatusKeys,
		})
		if err != nil {
			t.Fatalf("ListOpenIssues: %v", err)
		}
		sawCustomDone, sawOpen := false, false
		for _, row := range rows {
			sawCustomDone = sawCustomDone || row.ID.String() == customDone
			sawOpen = sawOpen || row.ID.String() == open
		}
		if sawCustomDone || !sawOpen {
			t.Fatalf("open listing customDone/open = %v/%v, want false/true", sawCustomDone, sawOpen)
		}
	})

	t.Run("child progress counts it as done", func(t *testing.T) {
		parent := insertIssue(t, "sql child progress parent", "in_progress")
		customDone := insertIssue(t, "sql child custom done", "gate_done_s")
		open := insertIssue(t, "sql child plain", "todo")
		if _, err := testPool.Exec(ctx, `UPDATE issue SET parent_issue_id = $1 WHERE id = ANY($2::uuid[])`,
			parent, []string{customDone, open}); err != nil {
			t.Fatalf("attach children: %v", err)
		}
		rows, err := testHandler.Queries.ChildIssueProgress(ctx, db.ChildIssueProgressParams{
			WorkspaceID:        parseUUID(testWorkspaceID),
			TerminalStatusKeys: terminalStatusKeys,
		})
		if err != nil {
			t.Fatalf("ChildIssueProgress: %v", err)
		}
		for _, row := range rows {
			if row.ParentIssueID.String() == parent {
				if row.Total != 2 || row.Done != 1 {
					t.Errorf("child progress = %d done / %d total, want 1/2 (the custom done status must count)",
						row.Done, row.Total)
				}
				return
			}
		}
		t.Error("no progress row for the parent issue")
	})
}
