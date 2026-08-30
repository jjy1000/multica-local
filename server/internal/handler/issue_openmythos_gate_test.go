package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 0.5.90 OpenMythos enhancer-only gate. Sole mode is disabled for NEW
// mythos_swarm bindings — the swarm lab runs exclusively as the enhancer
// outer loop paired with a target assignee. Pinned here because the gate
// spans CreateIssue + UpdateIssue with different scoping rules:
//   - create: any lab_source=mythos_swarm + lab_mode=sole write → 400.
//   - update: only PATCHes that TOUCH lab_mode on a mythos+sole
//     post-state 400 — legacy sole-bound issues stay editable in every
//     other field (forward-only law).
//
// The enhancer happy paths (target pairing + assignee requirement) are
// pinned alongside so the gate cannot silently lock the lab out
// entirely.
func TestOpenMythosSoleModeGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	t.Run("create: mythos + sole is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":      "openmythos-sole-create",
			"lab_source": "mythos_swarm",
			"lab_mode":   "sole",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "lab_mode='sole' is disabled for mythos_swarm") {
			t.Errorf("expected OpenMythos sole-disabled error, got: %s", w.Body.String())
		}
	})

	t.Run("create: mythos + enhancer with agent target is allowed", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":         "openmythos-enhancer-allow",
			"lab_source":    "mythos_swarm",
			"lab_mode":      "enhancer",
			"assignee_type": "member",
			"assignee_id":   testUserID,
		})
		if created.ID == "" {
			t.Fatal("enhancer create returned no id")
		}
	})

	t.Run("create: mythos + enhancer without assignee is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":      "openmythos-enhancer-no-target",
			"lab_source": "mythos_swarm",
			"lab_mode":   "enhancer",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "requires an assignee") {
			t.Errorf("expected target-required error, got: %s", w.Body.String())
		}
	})

	t.Run("update: title-only PATCH on enhancer-bound issue stays allowed", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":         "openmythos-upd-untouched",
			"lab_source":    "mythos_swarm",
			"lab_mode":      "enhancer",
			"assignee_type": "member",
			"assignee_id":   testUserID,
		})
		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
				"title": "openmythos-upd-untouched-renamed",
			}),
			"id", created.ID,
		)
		testHandler.UpdateIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("untouched-field PATCH on enhancer-bound issue: expected 200, got %d: %s",
				w.Code, w.Body.String())
		}
	})

	t.Run("update: PATCH lab_mode back to sole is rejected", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":         "openmythos-upd-sole",
			"lab_source":    "mythos_swarm",
			"lab_mode":      "enhancer",
			"assignee_type": "member",
			"assignee_id":   testUserID,
		})
		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
				"lab_mode": "sole",
			}),
			"id", created.ID,
		)
		testHandler.UpdateIssue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "lab_mode='sole' is disabled for mythos_swarm") {
			t.Errorf("expected OpenMythos sole-disabled error, got: %s", w.Body.String())
		}
	})

	t.Run("run API: mode=sole is rejected at the run surface too", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/experimental/mythos-swarm/run?workspace_id="+testWorkspaceID, map[string]any{
			"problem": "openmythos sole run probe",
			"mode":    "sole",
		})
		testHandler.RunMythosSwarm(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "disabled for mythos_swarm") {
			t.Errorf("expected sole-disabled error, got: %s", w.Body.String())
		}
	})

	t.Run("run API: enhancer without root_issue_id is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/experimental/mythos-swarm/run?workspace_id="+testWorkspaceID, map[string]any{
			"problem": "openmythos rootless probe",
		})
		testHandler.RunMythosSwarm(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "requires root_issue_id") {
			t.Errorf("expected root-required error, got: %s", w.Body.String())
		}
	})
}
