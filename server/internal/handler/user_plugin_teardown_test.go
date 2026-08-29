package handler

// user_plugin_teardown_test.go — 0.5.89 WS2 regression pins (DB-backed):
//
//  1. Provision: manifest capabilities.agents_inline / skills_inline create
//     hidden, ledgered resources at plugin create time.
//  2. Reclaim: DELETE archives provisioned agents, hard-deletes provisioned
//     skills, marks ledger rows, returns the per-resource report.
//  3. Guard: a plugin with a non-terminal bound issue is refused (409) and
//     deletable once the issue reaches a terminal status.
//  4. Declared (pre-existing) resources are ledgered origin='declared' and
//     KEPT on delete — the user's own agents must never be reclaimed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// teardownPurge removes every trace of the fixture plugin (registered as
// cleanup LAST → runs LAST, after per-test assertions).
func teardownPurge(t *testing.T, ctx context.Context, slug string) {
	t.Helper()
	testPool.Exec(ctx, `DELETE FROM user_plugin_resource WHERE plugin_slug = $1`, slug)
	testPool.Exec(ctx, `DELETE FROM experimental_pref WHERE flag_key = $1`, "user_"+slug)
	testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, slug+"%")
	testPool.Exec(ctx, `DELETE FROM skill WHERE workspace_id = $1 AND name LIKE $2`, testWorkspaceID, slug+"%")
	testPool.Exec(ctx, `DELETE FROM issue WHERE workspace_id = $1 AND lab_source = $2`, testWorkspaceID, "user_"+slug)
	testPool.Exec(ctx, `DELETE FROM user_plugin WHERE slug = $1`, slug)
	experimental.UnregisterUserPlugin("user_" + slug)
}

func TestUserPluginTeardown_ProvisionAndReclaim(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	slug := fmt.Sprintf("teardown-%d", time.Now().UnixNano())
	agentName := slug + "-agent"
	skillName := slug + "-skill"
	teardownPurge(t, ctx, slug)
	t.Cleanup(func() { teardownPurge(t, ctx, slug) })

	manifest := fmt.Sprintf(`{
		"capabilities": {
			"agents_inline": [{"name": %q, "instructions": "run the simulation"}],
			"skills_inline": [{"name": %q, "content": "# sim"}],
			"skills_visibility": "lab_scoped"
		}
	}`, agentName, skillName)

	w := httptest.NewRecorder()
	testHandler.CreateUserPlugin(w, newRequest(http.MethodPost, "/api/user-plugins", map[string]any{
		"slug":     slug,
		"title":    map[string]any{"en": "Teardown", "zh": "回收测试"},
		"manifest": json.RawMessage(manifest),
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Provisioning []struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			Action string `json:"action"`
		} `json:"provisioning"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	actions := map[string]string{}
	for _, p := range created.Provisioning {
		actions[p.Type+"-"+p.Name] = p.Action
	}
	if actions["agent-"+agentName] != "created" || actions["skill-"+skillName] != "created" {
		t.Fatalf("provisioning outcomes wrong: %+v", actions)
	}

	// Agent + skill rows exist, are hidden, and are ledgered provisioned.
	var archivedCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent WHERE workspace_id = $1 AND name = $2 AND archived_at IS NULL`,
		testWorkspaceID, agentName).Scan(&archivedCount); err != nil || archivedCount != 1 {
		t.Fatalf("provisioned agent row missing (count=%d, err=%v)", archivedCount, err)
	}
	var visRows int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM experimental_resource_visibility WHERE flag_key = $1`,
		"user_"+slug).Scan(&visRows); err != nil || visRows < 2 {
		t.Fatalf("visibility rows for provisioned resources missing (count=%d, err=%v)", visRows, err)
	}
	var skillCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM skill WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, skillName).Scan(&skillCount); err != nil || skillCount != 1 {
		t.Fatalf("provisioned skill row missing")
	}
	var ledgered int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_plugin_resource WHERE plugin_slug = $1 AND origin = 'provisioned'`,
		slug).Scan(&ledgered); err != nil || ledgered < 3 {
		t.Fatalf("ledger must carry agent + skill + env_dir provisioned rows (got %d, err=%v)", ledgered, err)
	}

	// Delete → 200 with a report that reclaims everything provisioned.
	w = httptest.NewRecorder()
	testHandler.DeleteUserPlugin(w, withURLParam(newRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil), "slug", slug))
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var report struct {
		Reclaim []struct {
			Type   string `json:"type"`
			Origin string `json:"origin"`
			Action string `json:"action"`
		} `json:"reclaim"`
	}
	json.Unmarshal(w.Body.Bytes(), &report)
	actions = map[string]string{}
	for _, r := range report.Reclaim {
		actions[r.Type] = r.Action
	}
	if actions["agent"] != "reclaimed" || actions["skill"] != "reclaimed" || actions["env_dir"] != "reclaimed" {
		t.Fatalf("reclaim actions wrong: %+v (report: %s)", actions, w.Body.String())
	}

	// Provisioned agent archived; provisioned skill hard-deleted; ledger
	// rows closed out; the flag is gone from the live catalog.
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent WHERE workspace_id = $1 AND name = $2 AND archived_at IS NULL`,
		testWorkspaceID, agentName).Scan(&archivedCount); err != nil || archivedCount != 0 {
		t.Fatalf("provisioned agent must be archived after delete (live count=%d)", archivedCount)
	}
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM skill WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, skillName).Scan(&skillCount); err != nil || skillCount != 0 {
		t.Fatalf("provisioned skill must be deleted after delete")
	}
	var openRows int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_plugin_resource WHERE plugin_slug = $1 AND reclaim_status NOT IN ('reclaimed','skipped')`,
		slug).Scan(&openRows); err != nil || openRows != 0 {
		t.Fatalf("ledger rows must be closed after reclaim (open=%d)", openRows)
	}
	if _, ok := experimental.FlagByKey("user_" + slug); ok {
		t.Fatalf("deleted plugin must be unregistered from the live catalog")
	}
}

func TestUserPluginTeardown_ActiveIssueGuard409(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	slug := fmt.Sprintf("teardown-guard-%d", time.Now().UnixNano())
	flagKey := "user_" + slug
	teardownPurge(t, ctx, slug)
	t.Cleanup(func() { teardownPurge(t, ctx, slug) })

	w := httptest.NewRecorder()
	testHandler.CreateUserPlugin(w, newRequest(http.MethodPost, "/api/user-plugins", map[string]any{
		"slug":  slug,
		"title": map[string]any{"en": "Guard", "zh": "护栏测试"},
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// A non-terminal issue bound to the plugin blocks deletion.
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, creator_type, creator_id, lab_source)
		VALUES ($1, 'guard fixture', 'todo', 'member', $2, $3)
		RETURNING id
	`, testWorkspaceID, testUserID, flagKey).Scan(&issueID); err != nil {
		t.Fatalf("insert bound issue: %v", err)
	}

	w = httptest.NewRecorder()
	testHandler.DeleteUserPlugin(w, withURLParam(newRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil), "slug", slug))
	if w.Code != http.StatusConflict {
		t.Fatalf("delete with active bound issue: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Terminal status lifts the guard.
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'done' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("close issue: %v", err)
	}
	w = httptest.NewRecorder()
	testHandler.DeleteUserPlugin(w, withURLParam(newRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil), "slug", slug))
	if w.Code != http.StatusOK {
		t.Fatalf("delete after issue terminal: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUserPluginTeardown_DeclaredResourcesKept(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	slug := fmt.Sprintf("teardown-decl-%d", time.Now().UnixNano())
	agentName := slug + "-user-agent"
	teardownPurge(t, ctx, slug)
	t.Cleanup(func() { teardownPurge(t, ctx, slug) })

	// The user's OWN agent (pre-existing, not plugin-provisioned).
	// runtime_id is NOT NULL on the live schema — bind a throwaway runtime
	// row first (mirrors user_plugins_seed_visibility_test.go's insertAgent).
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
		VALUES ($1, $2, 'local', 'test', 'offline', 'teardown fixture', '{}'::jsonb, now(), 'private', $3)
		RETURNING id
	`, testWorkspaceID, slug+"-runtime", testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, visibility, owner_id)
		VALUES ($1, $2, 'local', $3, 'private', $4)
		RETURNING id
	`, testWorkspaceID, agentName, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("insert user agent: %v", err)
	}

	manifest := fmt.Sprintf(`{"capabilities":{"agents":[%q]}}`, agentName)
	w := httptest.NewRecorder()
	testHandler.CreateUserPlugin(w, newRequest(http.MethodPost, "/api/user-plugins", map[string]any{
		"slug":     slug,
		"title":    map[string]any{"en": "Declared", "zh": "声明型"},
		"manifest": json.RawMessage(manifest),
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Declared agent is ledgered origin='declared'.
	var origin string
	if err := testPool.QueryRow(ctx,
		`SELECT origin FROM user_plugin_resource WHERE plugin_slug = $1 AND resource_type = 'agent'`,
		slug).Scan(&origin); err != nil || origin != "declared" {
		t.Fatalf("declared agent must ledger as origin=declared (got %q, err=%v)", origin, err)
	}

	w = httptest.NewRecorder()
	testHandler.DeleteUserPlugin(w, withURLParam(newRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil), "slug", slug))
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var report struct {
		Reclaim []struct {
			Type   string `json:"type"`
			Action string `json:"action"`
		} `json:"reclaim"`
	}
	json.Unmarshal(w.Body.Bytes(), &report)
	agentAction := ""
	for _, r := range report.Reclaim {
		if r.Type == "agent" {
			agentAction = r.Action
		}
	}
	if agentAction != "kept" {
		t.Fatalf("declared agent must be KEPT on delete (action=%q, report=%s)", agentAction, w.Body.String())
	}
	var archived int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent WHERE id = $1 AND archived_at IS NULL`, agentID).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("the user's own agent must survive plugin delete (live=%d, err=%v)", archived, err)
	}
}

// TestUserPluginReclaimPlanAndRetryEndpoints pins the two reclaim HTTP
// surfaces (0.5.89 gap: both shipped with zero direct coverage):
//
//   - GET  /api/user-plugins/{slug}/reclaim-plan — the read-only artifact
//     behind `lab delete --dry-run`, the UI delete dialog, and the agent's
//     confirm-before-delete protocol.
//   - POST /api/user-plugins/{slug}/reclaim — the retry for ledger rows
//     left 'failed' by the delete pass; 409 while the plugin is live.
func TestUserPluginReclaimPlanAndRetryEndpoints(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	slug := fmt.Sprintf("teardown-retry-%d", time.Now().UnixNano())
	agentName := slug + "-agent"
	skillName := slug + "-skill"
	teardownPurge(t, ctx, slug)
	t.Cleanup(func() { teardownPurge(t, ctx, slug) })

	manifest := fmt.Sprintf(`{
		"capabilities": {
			"agents_inline": [{"name": %q, "instructions": "retry fixture"}],
			"skills_inline": [{"name": %q, "content": "# retry"}]
		}
	}`, agentName, skillName)
	w := httptest.NewRecorder()
	testHandler.CreateUserPlugin(w, newRequest(http.MethodPost, "/api/user-plugins", map[string]any{
		"slug":     slug,
		"title":    map[string]any{"en": "Retry", "zh": "重试测试"},
		"manifest": json.RawMessage(manifest),
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Plan (live plugin): 200, three ledgered resources, active_issues 0.
	w = httptest.NewRecorder()
	testHandler.GetUserPluginReclaimPlan(w, withURLParam(newRequest(http.MethodGet, "/api/user-plugins/"+slug+"/reclaim-plan", nil), "slug", slug))
	if w.Code != http.StatusOK {
		t.Fatalf("reclaim-plan: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var plan struct {
		Slug         string `json:"slug"`
		ActiveIssues int64  `json:"active_issues"`
		Resources    []struct {
			Type   string `json:"type"`
			Origin string `json:"origin"`
			Status string `json:"status"`
		} `json:"resources"`
	}
	json.Unmarshal(w.Body.Bytes(), &plan)
	if plan.Slug != slug || plan.ActiveIssues != 0 || len(plan.Resources) < 3 {
		t.Fatalf("reclaim-plan shape wrong: %+v", plan)
	}
	types := map[string]string{}
	for _, r := range plan.Resources {
		types[r.Type] = r.Origin
	}
	if types["agent"] != "provisioned" || types["skill"] != "provisioned" || types["env_dir"] != "provisioned" {
		t.Fatalf("plan must list all three provisioned resources: %+v", types)
	}

	// POST while live → 409 (deletion is the entry point, not the retry).
	w = httptest.NewRecorder()
	testHandler.PostUserPluginReclaim(w, withURLParam(newRequest(http.MethodPost, "/api/user-plugins/"+slug+"/reclaim", nil), "slug", slug))
	if w.Code != http.StatusConflict {
		t.Fatalf("reclaim on live plugin: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Unknown slug → 404 on both endpoints.
	w = httptest.NewRecorder()
	testHandler.GetUserPluginReclaimPlan(w, withURLParam(newRequest(http.MethodGet, "/api/user-plugins/nope/reclaim-plan", nil), "slug", "nope"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("plan on unknown slug: expected 404, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	testHandler.PostUserPluginReclaim(w, withURLParam(newRequest(http.MethodPost, "/api/user-plugins/nope/reclaim", nil), "slug", "nope"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("reclaim on unknown slug: expected 404, got %d", w.Code)
	}

	// Delete → then the retry endpoint re-runs the reclaim idempotently:
	// every action stays "reclaimed" and no row reopens.
	w = httptest.NewRecorder()
	testHandler.DeleteUserPlugin(w, withURLParam(newRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil), "slug", slug))
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	testHandler.PostUserPluginReclaim(w, withURLParam(newRequest(http.MethodPost, "/api/user-plugins/"+slug+"/reclaim", nil), "slug", slug))
	if w.Code != http.StatusOK {
		t.Fatalf("retry reclaim: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var retry struct {
		Reclaim []struct {
			Type   string `json:"type"`
			Action string `json:"action"`
		} `json:"reclaim"`
	}
	json.Unmarshal(w.Body.Bytes(), &retry)
	retryActions := map[string]string{}
	for _, r := range retry.Reclaim {
		retryActions[r.Type] = r.Action
	}
	if retryActions["agent"] != "reclaimed" || retryActions["skill"] != "reclaimed" || retryActions["env_dir"] != "reclaimed" {
		t.Fatalf("retry must keep every resource reclaimed (idempotent): %+v (body: %s)", retryActions, w.Body.String())
	}
	var openRows int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_plugin_resource WHERE plugin_slug = $1 AND reclaim_status NOT IN ('reclaimed','skipped')`,
		slug).Scan(&openRows); err != nil || openRows != 0 {
		t.Fatalf("retry must leave no open ledger rows (open=%d, err=%v)", openRows, err)
	}
}
