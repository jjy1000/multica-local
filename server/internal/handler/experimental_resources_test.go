package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// testManifestPath is the dir PR 6's installer reads from. The test
// sets MULTICA_RESOURCES_DIR to it and the installer reads
// <dir>/claude-science/manifest.json.
//
// 0.3.52: the fixture used to live at /tmp/multica-test-fixtures and was
// a hand-maintained /tmp tree with the manifest missing — every CI run
// would fail-loud with ErrManifestUnavailable. Replaced with a small
// repo-tracked fixture (testdata/claude-science-fixture/) + a TestMain
// step that symlinks the production asset trees (skills/, agents/)
// from apps/desktop/resources/claude-science/ on top so the install
// path can actually read real SKILL.md + .txt bodies. The manifest
// itself is a 1-skill / 1-agent / 1-squad minimal copy so the install
// round-trip runs in <1 s instead of inserting all 291 skills.
//
// Empty = test will skip the manifest-dependent paths.
//
// Production sets MULTICA_RESOURCES_DIR to the bundled DMG resource
// dir; the test fixture keeps the install deterministic.
func testManifestPath(t *testing.T) string {
	t.Helper()
	// Resolve relative to the package directory so the path works
	// regardless of where `go test` was invoked from.
	fixtureRoot, err := filepath.Abs(filepath.Join("testdata", "claude-science-fixture"))
	if err != nil {
		t.Fatalf("abs fixture root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixtureRoot, "claude-science", "manifest.json")); err != nil {
		t.Skipf("fixture manifest not present at %s: %v", fixtureRoot, err)
	}
	return fixtureRoot
}

// ensureFixtureSymlinks materialises skills/ + agents/ inside the
// fixture by symlinking the production asset trees on top of the
// tracked manifest.json. Called from TestMain (idempotent).
//
// The fixture itself only carries a minimal 1/1/1 manifest so the
// install path walks the contract quickly. Symlinking (not copying)
// keeps the repo lean — the full 291-skill + 5-agent + 5-squad trees
// live only in apps/desktop/resources/claude-science/. Without the
// symlinks, loadManifestAsset would return "" for every body / prompt
// (silent degradation path — install still succeeds) but the test
// would be less meaningful because no real SKILL.md body ever reaches
// the DB.
//
// 0.3.52: returns an error rather than calling t.Fatalf because this
// helper is also invoked from TestMain, where the supplied *testing.T
// is a no-op stub — t.Fatalf on a non-running test deadlocks. The
// TestMain call site logs and skips the suite; per-test call sites
// still get the t.Fatalf behaviour via t.Skipf below.
func ensureFixtureSymlinks(t *testing.T) (ok bool) {
	t.Helper()
	fixRoot, err := filepath.Abs(filepath.Join("testdata", "claude-science-fixture"))
	if err != nil {
		t.Logf("abs fixture root: %v", err)
		return false
	}
	// Production asset root — derived relative to the server package.
// server/internal/handler/testdata/claude-science-fixture →
//   up 5 = repo root (multica-main) →
//   apps/desktop/resources/claude-science
prodRoot, err := filepath.Abs(filepath.Join(fixRoot, "..", "..", "..", "..", "..", "apps", "desktop", "resources", "claude-science"))
	if err != nil {
		t.Logf("abs prod root: %v", err)
		return false
	}
	if _, err := os.Stat(prodRoot); err != nil {
		t.Logf("production asset root not present at %s: %v", prodRoot, err)
		return false
	}

	for _, name := range []string{"skills", "agents"} {
		linkPath := filepath.Join(fixRoot, "claude-science", name)
		target := filepath.Join(prodRoot, name)
		// If linkPath exists and already RESOLVES to the right target,
		// skip — repeated TestMain invocations would otherwise error.
		// Resolve-compare (not raw-string compare) so the repo-tracked
		// relative symlinks survive every checkout: a raw comparison
		// against this run's absolute target rewrote them as absolute
		// paths on each test run, permanently dirtying other worktrees.
		if _, lerr := os.Lstat(linkPath); lerr == nil {
			if resolved, rerr := filepath.EvalSymlinks(linkPath); rerr == nil && resolved == target {
				continue
			}
		}
		// Remove any stale file / dir / broken symlink before creating.
		_ = os.Remove(linkPath)
		if err := os.Symlink(target, linkPath); err != nil {
			t.Logf("symlink %s → %s: %v", linkPath, target, err)
			return false
		}
	}
	return true
}

// withTestManifestEnv sets MULTICA_RESOURCES_DIR for the duration of
// the test. Returns a cleanup function the caller must defer.
//
// 0.3.52: derives the path from a small repo-tracked fixture instead
// of a hard-coded /tmp path. See testManifestPath for why.
func withTestManifestEnv(t *testing.T) func() {
	t.Helper()
	prev := os.Getenv(ManifestResourceDirEnv)
	path := testManifestPath(t)
	if err := os.Setenv(ManifestResourceDirEnv, path); err != nil {
		t.Fatalf("setenv %s: %v", ManifestResourceDirEnv, err)
	}
	return func() {
		if prev == "" {
			_ = os.Unsetenv(ManifestResourceDirEnv)
		} else {
			_ = os.Setenv(ManifestResourceDirEnv, prev)
		}
	}
}

// resolveLabOwnerID looks up the first user row's id at call time so
// the install path can satisfy the workspace-owner NOT NULL constraint.
// We do NOT mutate the package-level testUserID var (set by
// TestMain's setupHandlerTestFixture and read by every other test
// in this package as their authenticated caller) — that would leak
// across tests.
var resolveLabOwnerIDOnce sync.Once
var resolveLabOwnerIDValue string

func resolveLabOwnerID(t *testing.T) string {
	t.Helper()
	resolveLabOwnerIDOnce.Do(func() {
		if testPool == nil {
			return
		}
		var id string
		if err := testPool.QueryRow(t.Context(),
			`SELECT id::text FROM "user" ORDER BY created_at ASC LIMIT 1`).Scan(&id); err != nil {
			t.Logf("resolveLabOwnerID: %v (using empty fallback)", err)
			return
		}
		resolveLabOwnerIDValue = id
	})
	return resolveLabOwnerIDValue
}

// mountExperimentalResourceRoutes is a tiny test helper that wires the
// three endpoints onto a chi router. Lives in the test file so the
// production router stays untouched for PR 3; PR 4 will replace it
// with a real router mount and delete this helper.
func mountExperimentalResourceRoutes(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/{key}/status", testHandler.GetExperimentalResourcesStatus)
	r.Post("/{key}/install", testHandler.PostExperimentalResourcesInstall)
	r.Post("/{key}/rollback", testHandler.PostExperimentalResourcesRollback)
	return r
}

// TestExperimentalResourcesStatus_BeforeInstall asserts the
// pre-install status response shape: Installed=false, Hidden=false,
// no rows attached to a source that has never been touched.
//
// We clean the lock-table state first so the test does not depend on
// test-run order — the marker row from a prior round trip would
// otherwise leave Installed=true here.
func TestExperimentalResourcesStatus_BeforeInstall(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	resetLabLockTable(t)

	r := mountExperimentalResourceRoutes(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/"+string(experimental.SourceClaudeScience)+"/status", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp ExperimentalResourcesManifest
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Source != string(experimental.SourceClaudeScience) {
		t.Fatalf("source mismatch: %q", resp.Source)
	}
	if resp.Installed {
		t.Fatal("fresh lab should report Installed=false")
	}
	if resp.Hidden {
		t.Fatal("fresh lab should report Hidden=false")
	}
}

// TestExperimentalResourcesUnknownKey_404 verifies that a request
// for a Source not in installableSources returns 404. The handler
// rejects both install and rollback for unknown sources; we exercise
// rollback here as a representative case (the install endpoint has
// the same gate).
func TestExperimentalResourcesUnknownKey_404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	r := mountExperimentalResourceRoutes(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/not-a-lab/install", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestExperimentalResourcesManifestUnavailable_503 asserts the
// fail-loud path: when MULTICA_RESOURCES_DIR is unset (or the
// manifest file is missing) the install endpoint returns 503 with a
// clear hint, instead of silently succeeding or panicking.
//
// PR 6 added this contract; without it the renderer would see a
// generic 500 and the user could not tell whether the install failed
// because of a DB error or because the bundle was missing.
func TestExperimentalResourcesManifestUnavailable_503(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prev := os.Getenv(ManifestResourceDirEnv)
	_ = os.Unsetenv(ManifestResourceDirEnv)
	defer func() {
		if prev != "" {
			_ = os.Setenv(ManifestResourceDirEnv, prev)
		}
	}()

	r := mountExperimentalResourceRoutes(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/"+string(experimental.SourceClaudeScience)+"/install", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestExperimentalResourcesRoundTrip_InstalledThenHidden walks the
// full PR 6 contract end to end against the fixture manifest:
//
//   1. install → status reports Installed=true, Hidden=false, AND
//      a workspace + skill + agent + squad actually exist in the DB;
//   2. rollback → status reports Hidden=true (rows still in DB);
//   3. install again → status reports Installed=true, Hidden=false.
//
// This is the contract the renderer relies on for the badge text
// transitions ("未启用" → "已装载 N" → "已隐藏 N" → "已装载 N").
func TestExperimentalResourcesRoundTrip_InstalledThenHidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	defer withTestManifestEnv(t)()
	resetLabLockTable(t)
	// Defer cleanup so the workspace + skills + agents + squads +
	// runtime we create during install do not leak into subsequent
	// tests that count workspaces / agents / squads globally.
	defer resetLabLockTable(t)

	r := mountExperimentalResourceRoutes(t)
	key := string(experimental.SourceClaudeScience)

	install := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/"+key+"/install", nil)
		if uid := resolveLabOwnerID(t); uid != "" {
			req.Header.Set("X-User-ID", uid)
		}
		r.ServeHTTP(w, req)
		return w
	}
	rollback := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/"+key+"/rollback", nil)
		r.ServeHTTP(w, req)
		return w
	}
	status := func() ExperimentalResourcesManifest {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+key+"/status", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d %s", w.Code, w.Body.String())
		}
		var resp ExperimentalResourcesManifest
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	// 1. Install
	if w := install(); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	first := status()
	if !first.Installed {
		t.Fatal("after install: Installed should be true")
	}
	if first.Hidden {
		t.Fatal("after install: Hidden should be false")
	}
	// After install with the fixture, we expect at least 4 lock rows
	// (1 workspace + 1 skill + 1 agent + 1 squad).
	total := 0
	for _, c := range first.Counts {
		total += c.Total
	}
	if total < 4 {
		t.Fatalf("after install: expected ≥4 lock rows, got %d (%+v)", total, first.Counts)
	}

	// 2. Rollback
	if w := rollback(); w.Code != http.StatusNoContent {
		t.Fatalf("rollback: %d %s", w.Code, w.Body.String())
	}
	second := status()
	if !second.Installed {
		t.Fatal("after rollback: Installed should remain true")
	}
	if !second.Hidden {
		t.Fatal("after rollback: Hidden should be true")
	}

	// 3. Install again restores visibility.
	if w := install(); w.Code != http.StatusOK {
		t.Fatalf("install second: %d %s", w.Code, w.Body.String())
	}
	third := status()
	if !third.Installed || third.Hidden {
		t.Fatalf("after re-install: %+v", third)
	}
}

// resetLabLockTable deletes every row attached to the claude_science
// source so the before-install assertion holds regardless of test
// ordering. This is a test-only utility; production never wipes the
// lock table because soft-hide is the contract.
//
// We also delete the lab workspace itself (CASCADE wipes skills,
// agents, squads, squad_members, runtime rows attached to it) so a
// finished round-trip does not pollute subsequent tests that count
// workspaces / agents / squads globally.
func resetLabLockTable(t *testing.T) {
	t.Helper()
	if testPool == nil {
		return
	}
	if _, err := testPool.Exec(t.Context(),
		"DELETE FROM experimental_resource_lock WHERE experimental_source = $1",
		string(experimental.SourceClaudeScience)); err != nil {
		t.Fatalf("reset lock table: %v", err)
	}
	if _, err := testPool.Exec(t.Context(),
		"DELETE FROM workspace WHERE slug = $1",
		"claude-science"); err != nil {
		t.Fatalf("reset lab workspace: %v", err)
	}
}

// writeFixtureManifest writes a custom manifest to a fresh temp dir,
// points MULTICA_RESOURCES_DIR at it for the duration of the test, and
// returns the temp dir path. Callers must defer the returned cleanup
// before defer resetLabLockTable so the manifest path reverts first.
func writeFixtureManifest(t *testing.T, payload string) (dir string, cleanup func()) {
	t.Helper()
	dir = t.TempDir()
	sub := dir + "/claude-science"
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(sub+"/manifest.json", []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture manifest: %v", err)
	}
	prev := os.Getenv(ManifestResourceDirEnv)
	if err := os.Setenv(ManifestResourceDirEnv, dir); err != nil {
		t.Fatalf("setenv %s: %v", ManifestResourceDirEnv, err)
	}
	return dir, func() {
		if prev == "" {
			_ = os.Unsetenv(ManifestResourceDirEnv)
		} else {
			_ = os.Setenv(ManifestResourceDirEnv, prev)
		}
	}
}

// TestExperimentalResourcesInstall_SkipsUnknownSquadMembers asserts the
// 0.3.15 robustness fix: when the manifest's squad members / leader
// reference agent names not in this build's `agents` list (e.g.
// OpenScience dropped a prompt file but the squad JSON still lists
// it), the install path MUST skip those references and complete
// instead of bubbling up "agent name X not found in install set".
//
// The squad row itself still gets created (with the subset of agents
// that did port over), so the lab is usable rather than dead-locked.
func TestExperimentalResourcesInstall_SkipsUnknownSquadMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	defer resetLabLockTable(t)
	resetLabLockTable(t)

	dir, restore := writeFixtureManifest(t, `{
		"schema_version": 1,
		"experimental_source": "claude_science_lab",
		"workspace": {"name": "Claude 科研实验室", "slug": "claude-science", "description": "skipped-agent fixture"},
		"skills": [],
		"agents": [
			{"name": "research", "prompt_path": "agents/research.txt", "category": "primary"}
		],
		"squads": [
			{
				"name": "Claude Science 联合体",
				"description": "leader + members reference agents that we did NOT port — install must skip and complete.",
				"members": ["research", "plan", "write"],
				"leader_agent": "plan"
			}
		],
		"installed_at_build": true
	}`)
	t.Cleanup(restore)
	_ = dir

	r := mountExperimentalResourceRoutes(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/"+string(experimental.SourceClaudeScience)+"/install", nil)
	if uid := resolveLabOwnerID(t); uid != "" {
		req.Header.Set("X-User-ID", uid)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("install should succeed despite unknown squad members; got %d body=%s", w.Code, w.Body.String())
	}
}
