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

// 0.5.86 assignee-lock (独立工作型 hard gate). Labs classified
// InteractionModelAssignee own the bound issue's assignee slot: only
// the lab's leader agent may hold it. Pinned here at the handler level
// because the gate spans CreateIssue + UpdateIssue and consults
// defaultLabLeaderForKey → GetAgentByWorkspaceAndName.
//
// Contract table (catalog_test.go::TestCatalogInteractionModelContract
// pins the classification literals; THIS file pins the HTTP behavior):
//   - pythia_oracle → leader pythia_runtime: leader allowed, others 400.
//   - timesfm → leader timesfm_oracle (0.5.86 leader-table addition).
//   - mythos_swarm → no leader: any manual assignee still 400s
//     (0.3.33 sole-mutex preserved through the same gate).
//   - untouched-field PATCHes on a lab-bound issue stay allowed
//     (no false positive on title/status edits).
func TestAssigneeLabLockGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	if !experimental.IsAssigneeModelLab("pythia_oracle") || !experimental.IsAssigneeModelLab("timesfm") {
		t.Fatalf("catalog classification drifted: pythia_oracle/timesfm must be InteractionModelAssignee")
	}

	// Provision the pythia leader row in the fixture workspace so the
	// allow-path (assignee == leader) can be exercised. Cleanup removes
	// it again so other tests keep seeing a pristine agent table.
	const leaderName = "pythia_runtime"
	testPool.Exec(context.Background(),
		`DELETE FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, leaderName)
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":                 leaderName,
		"description":          "0.5.86 assignee-lock test fixture",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent(%s): expected 201, got %d: %s", leaderName, w.Code, w.Body.String())
	}
	var leaderResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&leaderResp); err != nil {
		t.Fatalf("decode CreateAgent response: %v", err)
	}
	leaderID, _ := leaderResp["id"].(string)
	if leaderID == "" {
		t.Fatalf("CreateAgent(%s): no id in response: %v", leaderName, leaderResp)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND name = $2`,
			testWorkspaceID, leaderName)
	})

	t.Run("create: leader agent assignee is allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":         "lab-lock-allow-leader",
			"lab_source":    "pythia_oracle",
			"assignee_type": "agent",
			"assignee_id":   leaderID,
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("expected success, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("create: non-leader agent assignee is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":         "lab-lock-reject-agent",
			"lab_source":    "pythia_oracle",
			"assignee_type": "agent",
			"assignee_id":   "33333333-3333-3333-3333-333333333333",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "locks the assignee to the lab agent (pythia_runtime)") {
			t.Errorf("expected leader-naming error, got: %s", w.Body.String())
		}
	})

	t.Run("create: member assignee is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":         "lab-lock-reject-member",
			"lab_source":    "timesfm",
			"assignee_type": "member",
			"assignee_id":   testUserID,
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "locks the assignee to the lab agent (timesfm_oracle)") {
			t.Errorf("expected timesfm leader-naming error, got: %s", w.Body.String())
		}
	})

	t.Run("create: no assignee is allowed (leader-rewrite fills it)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":      "lab-lock-empty-assignee",
			"lab_source": "pythia_oracle",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("expected success, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("update: reassigning a pythia-bound issue to a member is rejected", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":         "lab-lock-upd-reassign",
			"lab_source":    "pythia_oracle",
			"assignee_type": "agent",
			"assignee_id":   leaderID,
		})
		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
				"assignee_type": "member",
				"assignee_id":   testUserID,
			}),
			"id", created.ID,
		)
		testHandler.UpdateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("update: title-only PATCH on lab-bound issue stays allowed", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":      "lab-lock-upd-untouched",
			"lab_source": "pythia_oracle",
		})
		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
				"title": "lab-lock-upd-untouched-renamed",
			}),
			"id", created.ID,
		)
		testHandler.UpdateIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("untouched-field PATCH on assignee-model lab issue: expected 200, got %d: %s",
				w.Code, w.Body.String())
		}
	})

	t.Run("mythos keeps the strict no-manual-assignee mutex", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":         "lab-lock-mythos-strict",
			"lab_source":    "mythos_swarm",
			"assignee_type": "agent",
			"assignee_id":   leaderID,
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "requires the lab to own the assignee") {
			t.Errorf("expected roster-ownership error, got: %s", w.Body.String())
		}
	})

	t.Run("auxiliary lab never locks (causal_graph + member assignee allowed)", func(t *testing.T) {
		if !experimental.IsAuxiliaryModelLab("causal_graph") {
			t.Skip("causal_graph not auxiliary in catalog — contract drift")
		}
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":      "lab-lock-aux-no-lock",
			"lab_source": "causal_graph",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("auxiliary lab binding must not 400, got %d: %s", w.Code, w.Body.String())
		}
	})
}
