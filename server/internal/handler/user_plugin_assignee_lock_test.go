package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// 0.5.88 P4 — user plugins join the 0.5.86 interaction-model taxonomy.
// A manifest declaring interaction_model="assignee" + leader_agent="X"
// makes the plugin's flag an assignee-model lab: binding it locks the
// issue assignee to the agent row named X (same semantics as the
// built-ins), while absent/auxiliary manifests never lock
// (behavior-preserving). The contract is validated at plugin
// create/update time (assignee WITHOUT leader_agent → 400).
//
// Pinned here at the DB-backed handler level because the lock gate
// spans CreateUserPlugin → registry stamping → assigneeLabLockError →
// service-side leader-rewrite. Global state discipline (shared dev DB):
// every step that touches user_plugin rows or the in-memory registry
// key-targets its cleanup — hard-deletes by slug + UnregisterUserPlugin
// by flag key, so no leftover rows or registrations leak into other
// tests.
func TestUserPluginInteractionModelContract(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	ctx := context.Background()
	const (
		slug       = "p4-lock-contract"
		flagKey    = "user_p4-lock-contract"
		leaderName = "p4_lock_leader"
		otherName  = "p4_lock_other"
	)

	// Purge any leftover from a crashed earlier run before asserting
	// anything (user_plugin rows are server-global, the registry is
	// package-global). Registered as cleanup LAST → runs LAST, after
	// the per-subtest issue deletes.
	purgeGlobals := func() {
		testPool.Exec(ctx, `DELETE FROM user_plugin WHERE slug = $1`, slug)
		testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name IN ($2, $3)`,
			testWorkspaceID, leaderName, otherName)
		experimental.UnregisterUserPlugin(flagKey)
	}
	purgeGlobals()
	t.Cleanup(purgeGlobals)

	// createPluginBody POSTs a plugin create and returns the recorder.
	createPluginBody := func(manifest string) *httptest.ResponseRecorder {
		body := map[string]any{
			"slug":         slug,
			"title":        map[string]any{"en": "P4 Lock Contract", "zh": "P4 锁定合约"},
			"description":  map[string]any{"en": "0.5.88 interaction-model fixture", "zh": "0.5.88 协作模式夹具"},
			"trigger_mode": "issue_select",
			"runtime_kind": "inline",
		}
		if manifest != "" {
			body["manifest"] = json.RawMessage(manifest)
		}
		w := httptest.NewRecorder()
		testHandler.CreateUserPlugin(w, newRequest(http.MethodPost, "/api/user-plugins", body))
		return w
	}

	updateManifest := func(t *testing.T, manifest string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.UpdateUserPlugin(w, withURLParam(
			newRequest(http.MethodPut, "/api/user-plugins/"+slug, map[string]any{
				"manifest": json.RawMessage(manifest),
			}),
			"slug", slug,
		))
		return w
	}

	t.Run("create: assignee without leader_agent is rejected", func(t *testing.T) {
		w := createPluginBody(`{"interaction_model":"assignee"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "leader_agent is required") {
			t.Errorf("expected leader-required error, got: %s", w.Body.String())
		}
	})

	t.Run("create: invalid interaction_model literal is rejected", func(t *testing.T) {
		w := createPluginBody(`{"interaction_model":"owner"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		// The wire body JSON-escapes the quotes in the message.
		if !strings.Contains(w.Body.String(), `interaction_model must be`) {
			t.Errorf("expected model-literal error, got: %s", w.Body.String())
		}
	})

	// Seed the plugin with a minimal (default-auxiliary) manifest so the
	// update-path validation and the rest of the flow have a row.
	t.Run("create: auxiliary default seeds without a manifest contract", func(t *testing.T) {
		w := createPluginBody("")
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("update: assignee without leader_agent is rejected", func(t *testing.T) {
		w := updateManifest(t, `{"interaction_model":"assignee"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "leader_agent is required") {
			t.Errorf("expected leader-required error, got: %s", w.Body.String())
		}
	})

	// Promote the seeded plugin to the full assignee contract.
	w := updateManifest(t, `{"interaction_model":"assignee","leader_agent":"`+leaderName+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("contract update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !experimental.IsAssigneeModelLab(flagKey) {
		t.Fatalf("re-registered flag must resolve assignee-model; InteractionModelOf=%q",
			experimental.InteractionModelOf(flagKey))
	}

	t.Run("flags payload surfaces the interaction model + leader", func(t *testing.T) {
		w := httptest.NewRecorder()
		testHandler.ListExperimentalFlags(w, newRequest(http.MethodGet, "/api/experimental-flags", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var list struct {
			Flags []struct {
				Key              string `json:"key"`
				InteractionModel string `json:"interaction_model"`
				LeaderAgent      string `json:"leader_agent"`
				IsUserPlugin     bool   `json:"is_user_plugin"`
			} `json:"flags"`
		}
		if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
			t.Fatalf("decode flags payload: %v", err)
		}
		for _, f := range list.Flags {
			if f.Key != flagKey {
				continue
			}
			if !f.IsUserPlugin {
				t.Errorf("flag %q must carry is_user_plugin", flagKey)
			}
			if f.InteractionModel != experimental.InteractionModelAssignee {
				t.Errorf("payload interaction_model = %q, want %q", f.InteractionModel, experimental.InteractionModelAssignee)
			}
			if f.LeaderAgent != leaderName {
				t.Errorf("payload leader_agent = %q, want %q", f.LeaderAgent, leaderName)
			}
			return
		}
		t.Fatalf("flag %q missing from /api/experimental-flags payload", flagKey)
	})

	// Provision agent rows in the fixture workspace (purgeGlobals
	// already removed any stale rows).
	agentID := func(t *testing.T, name string) string {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
			"name":                 name,
			"description":          "0.5.88 user-plugin lock fixture",
			"runtime_id":           testRuntimeID,
			"visibility":           "private",
			"max_concurrent_tasks": 1,
		}))
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateAgent(%s): expected 201, got %d: %s", name, w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode CreateAgent response: %v", err)
		}
		id, _ := resp["id"].(string)
		if id == "" {
			t.Fatalf("CreateAgent(%s): no id in response", name)
		}
		return id
	}
	leaderID := agentID(t, leaderName)
	otherID := agentID(t, otherName)

	// createIssue returns (status, issue id, body) and registers a
	// cleanup delete for every successfully created issue. The body is
	// snapshotted before the throwaway decode drains the recorder
	// buffer.
	createIssue := func(t *testing.T, body map[string]any) (int, string, string) {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.CreateIssue(w, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, body))
		bodyStr := w.Body.String()
		var resp map[string]any
		_ = json.NewDecoder(strings.NewReader(bodyStr)).Decode(&resp)
		id, _ := resp["id"].(string)
		if id != "" {
			created := id
			t.Cleanup(func() {
				r := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created, nil), "id", created)
				testHandler.DeleteIssue(httptest.NewRecorder(), r)
			})
		}
		return w.Code, id, bodyStr
	}

	t.Run("create: empty assignee passes and leader-rewrite fills the leader", func(t *testing.T) {
		code, id, body := createIssue(t, map[string]any{
			"title":      "p4-lock-empty-assignee",
			"lab_source": flagKey,
		})
		if code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", code, body)
		}
		var assigneeID string
		if err := testPool.QueryRow(ctx,
			`SELECT COALESCE(assignee_id::text, '') FROM issue WHERE id = $1`, id,
		).Scan(&assigneeID); err != nil {
			t.Fatalf("load issue assignee: %v", err)
		}
		if assigneeID != leaderID {
			t.Fatalf("leader-rewrite assignee = %q, want leader %q", assigneeID, leaderID)
		}
	})

	t.Run("create: manual non-leader agent assignee is rejected", func(t *testing.T) {
		code, _, body := createIssue(t, map[string]any{
			"title":         "p4-lock-reject-other",
			"lab_source":    flagKey,
			"assignee_type": "agent",
			"assignee_id":   otherID,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", code, body)
		}
		if !strings.Contains(body, "locks the assignee to the lab agent ("+leaderName+")") {
			t.Errorf("expected leader-naming error, got: %s", body)
		}
	})

	// Remove the leader row to exercise the install-first guard. The
	// earlier subtests' issues are already deleted by their cleanups, so
	// no row references the leader id here.
	if _, err := testPool.Exec(ctx,
		`DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, leaderName,
	); err != nil {
		t.Fatalf("delete leader row: %v", err)
	}

	t.Run("create: leader row absent yields the install-first 400", func(t *testing.T) {
		code, _, body := createIssue(t, map[string]any{
			"title":         "p4-lock-leader-absent",
			"lab_source":    flagKey,
			"assignee_type": "agent",
			"assignee_id":   otherID,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", code, body)
		}
		if !strings.Contains(body, "not installed yet") {
			t.Errorf("expected install-first error, got: %s", body)
		}
	})

	t.Run("update: leader assignee allowed, reassigning away is rejected", func(t *testing.T) {
		// Restore the leader row (fresh id — resolution is name-based).
		newLeaderID := agentID(t, leaderName)
		code, id, body := createIssue(t, map[string]any{
			"title":         "p4-lock-upd-bind",
			"lab_source":    flagKey,
			"assignee_type": "agent",
			"assignee_id":   newLeaderID,
		})
		if code != http.StatusCreated {
			t.Fatalf("leader assignee must be allowed: expected 201, got %d: %s", code, body)
		}
		w := httptest.NewRecorder()
		testHandler.UpdateIssue(w, withURLParam(
			newRequest("PUT", "/api/issues/"+id, map[string]any{
				"assignee_type": "member",
				"assignee_id":   testUserID,
			}),
			"id", id,
		))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Auxiliary downgrade: the same plugin flips to auxiliary → the lock
	// must release (uniform taxonomy: auxiliary never locks).
	t.Run("downgrade to auxiliary releases the lock", func(t *testing.T) {
		w := updateManifest(t, `{"interaction_model":"auxiliary","leader_agent":"`+leaderName+`"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("auxiliary downgrade: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		if experimental.IsAssigneeModelLab(flagKey) {
			t.Fatalf("auxiliary plugin must not resolve as assignee-model")
		}
		code, _, body := createIssue(t, map[string]any{
			"title":         "p4-lock-aux-free",
			"lab_source":    flagKey,
			"assignee_type": "agent",
			"assignee_id":   otherID,
		})
		if code != http.StatusCreated {
			t.Fatalf("auxiliary plugin must never lock: expected 201, got %d: %s", code, body)
		}
	})
}
