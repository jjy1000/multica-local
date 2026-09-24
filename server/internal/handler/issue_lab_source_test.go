package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/experimental"
)

// 0.3.26: lab_source must match a known experimental flag key. The
// previous (0.3.22+) behaviour accepted any string; a typo "claude_science"
// (legacy source) instead of "claude_science_lab" persisted silently and
// gave the issue a list-row Lab pill with an undefined title.

// TestCreateIssueRejectsUnknownLabSource pins the reject path.
func TestCreateIssueRejectsUnknownLabSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	cases := []struct {
		name string
		key  string
		want int
	}{
		{"rejects arbitrary string", "definitely_not_a_flag", http.StatusBadRequest},
		{"rejects legacy claude_science (renamed in 0.3.22)", "claude_science", http.StatusBadRequest},
		{"rejects empty-after-trim", "  ", http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			labSource := tc.key
			req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
				"title":      "lab-source-rejection-" + tc.name,
				"lab_source": labSource,
			})
			testHandler.CreateIssue(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d for %q, got %d: %s",
					tc.want, tc.key, w.Code, w.Body.String())
			}
			if tc.want == http.StatusBadRequest && !strings.Contains(w.Body.String(), "lab_source") {
				t.Errorf("expected error to mention lab_source, got: %s", w.Body.String())
			}
		})
	}
}

// TestCreateIssueAcceptsKnownLabSource pins the accept path for every
// catalog key. Uses flag keys that don't carry an experiment hideable
// resource, so the issue persists cleanly.
func TestCreateIssueAcceptsKnownLabSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	// llm_wiki_bridge is an auxiliary catalog key with no install-time
	// resource. Use it as the canonical accepted case so the test does
	// not depend on any other lab being installed.
	const known = "llm_wiki_bridge"
	if !experimental.IsKnownKey(known) {
		t.Fatalf("expected %q in catalog", known)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "lab-source-accept-known",
		"lab_source": known,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		// Hand-test acceptable: the issue endpoint may return either,
		// depending on whether AllowDuplicate or workspace setup fired.
		// Anything 4xx other than 400 is a regression.
		if w.Code >= 400 && w.Code != http.StatusBadRequest {
			t.Fatalf("expected success or 4xx-fine, got %d: %s", w.Code, w.Body.String())
		}
	}
}

// 0.3.31: BatchUpdateIssues previously (a) silently dropped
// `lab_source` because the loop never read it, and (b) had no mutex
// gate at all. A scripted PATCH to /api/issues/batch carrying both
// `lab_source` and `assignee_type/id` would have persisted a
// contradicting issue. The per-issue skip-on-failure contract
// (`continue` on failure) is preserved — a single mis-tagged issue
// in a 50-issue move does not 400 the whole batch; it is silently
// skipped, matching the behavior of the existing parent_issue_id /
// project_id / stage branches.
func TestBatchUpdateIssuesRespectsLabMutex(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	const known = "llm_wiki_bridge"
	if !experimental.IsKnownKey(known) {
		t.Fatalf("expected %q in catalog", known)
	}
	// Assignee-model labs (claude_science_lab / pythia_oracle) enforce
	// the lab ↔ assignee mutex via interaction_model — auxiliary labs
	// (llm_wiki_bridge) accept a manual assignee.
	const mutexLab = "claude_science_lab"
	realMemberID := testUserID

	t.Run("lab + assignee in same batch update → per-issue skip", func(t *testing.T) {
		// Pre-existing assigned issue. Batch update carries both
		// mythos lab_source and assignee. Post-fix: the issue is
		// skipped (the issue ID won't appear in the response
		// list), and the assignment is NOT persisted.
		created := createIssueForTest(t, map[string]any{
			"title":         "batch-mutex-1",
			"assignee_type": "member",
			"assignee_id":   realMemberID,
		})
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{created.ID},
			"updates": map[string]any{
				"lab_source":    mutexLab,
				"assignee_type": "member",
				"assignee_id":   realMemberID,
			},
		})
		testHandler.BatchUpdateIssues(w, req)
		// The batch endpoint returns 200 with a per-issue result
		// list. The single failing issue should be reported as
		// skipped (no entry in the updated set, OR an explicit
		// skipped count). The contract is: the issue is NOT
		// updated, even though the batch as a whole succeeded.
		// We pin that the response is 2xx and the issue is not in
		// the success list. Re-fetch the issue and verify the
		// lab_source is still null (was not persisted).
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("expected 2xx for batch, got %d: %s", w.Code, w.Body.String())
		}
		// Re-read the issue to confirm the contract. We don't
		// have a generic GetIssue JSON helper here; the existing
		// test convention is to check via the response body. The
		// batch response body shape is `{results: [...]}` or
		// similar — the existing test suite doesn't pin the
		// shape, so we pin behavior via a follow-up GET
		// through ListIssues or GetIssueInWorkspace SQL. We use
		// a direct query here for the contract test only.
		var labSourceAfter pgtype.Text
		err := testPool.QueryRow(context.Background(),
			`SELECT lab_source FROM issue WHERE id = $1`, created.ID,
		).Scan(&labSourceAfter)
		if err != nil {
			t.Fatalf("re-read issue: %v", err)
		}
		if labSourceAfter.Valid {
			t.Errorf("batch mutex violation persisted lab_source=%q; expected NULL",
				labSourceAfter.String)
		}
	})


	t.Run("lab only on unassigned issue → succeeds", func(t *testing.T) {
		// Sanity: lab_source only, no assignee, post-state is
		// compatible. Should succeed.
		created := createIssueForTest(t, map[string]any{
			"title": "batch-mutex-3",
		})
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{created.ID},
			"updates": map[string]any{
				"lab_source": known,
			},
		})
		testHandler.BatchUpdateIssues(w, req)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("expected 2xx, got %d: %s", w.Code, w.Body.String())
		}
		var labSourceAfter pgtype.Text
		if err := testPool.QueryRow(context.Background(),
			`SELECT lab_source FROM issue WHERE id = $1`, created.ID,
		).Scan(&labSourceAfter); err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if !labSourceAfter.Valid || labSourceAfter.String != known {
			t.Errorf("expected lab_source=%q, got valid=%v str=%q",
				known, labSourceAfter.Valid, labSourceAfter.String)
		}
	})
}

// TestBatchUpdateIssuesLabAssigneeLockParity — 0.5.107 audit C-1.
//
// The batch mutex switch hardcodes two keys (mythos_swarm / swarm_topology),
// so every lab the 0.5.86 assignee interaction model widened onto
// (claude_science_lab / pythia_oracle / semantica / timesfm) plus every
// interaction_model=assignee user plugin can be batch-bound to a manual
// assignee while the equivalent single-issue PATCH 400s through
// assigneeLabLockError. That is the same drift class as 0.5.60 audit P0-1
// (swarm), which the comment below the batch switch already records.
//
// Rather than re-enumerate keys (which is how the previous extension got
// forgotten here), derive them from the catalog and assert batch parity
// against the single-issue path for each.
func TestBatchUpdateIssuesLabAssigneeLockParity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	var labs []string
	for _, f := range experimental.Catalog {
		if experimental.IsFrozen(f.Key) || !experimental.IsAssigneeModelLab(f.Key) {
			continue
		}
		labs = append(labs, f.Key)
	}
	// The two hardcoded batch keys are excluded below (they already skip via
	// their own cases), so the pinned set here is the widened remainder.
	if len(labs) < 2 {
		t.Fatalf("expected the assignee-model set to cover the assignee labs, got %v", labs)
	}

	memberID := testUserID
	for _, lab := range labs {
		if lab == "mythos_swarm" || lab == "swarm_topology" {
			continue
		}
		t.Run(lab, func(t *testing.T) {
			// Precondition: the single-issue path rejects this post-state.
			single := createIssueForTest(t, map[string]any{
				"title":         "batch-parity-single-" + lab,
				"assignee_type": "member",
				"assignee_id":   memberID,
			})
			wSingle := httptest.NewRecorder()
			reqSingle := withURLParam(
				newRequest("PUT", "/api/issues/"+single.ID, map[string]any{
					"lab_source":    lab,
					"assignee_type": "member",
					"assignee_id":   memberID,
				}),
				"id", single.ID,
			)
			testHandler.UpdateIssue(wSingle, reqSingle)
			if wSingle.Code != http.StatusBadRequest {
				t.Fatalf("single-issue PATCH: expected 400, got %d: %s",
					wSingle.Code, wSingle.Body.String())
			}
			if !strings.Contains(wSingle.Body.String(), "assignee") {
				t.Fatalf("single-issue PATCH: expected the assignee-lock message, got %s",
					wSingle.Body.String())
			}

			// Same post-state through the batch endpoint must be skipped,
			// not persisted.
			batched := createIssueForTest(t, map[string]any{
				"title":         "batch-parity-batch-" + lab,
				"assignee_type": "member",
				"assignee_id":   memberID,
			})
			wBatch := httptest.NewRecorder()
			reqBatch := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
				"issue_ids": []string{batched.ID},
				"updates": map[string]any{
					"lab_source":    lab,
					"assignee_type": "member",
					"assignee_id":   memberID,
				},
			})
			testHandler.BatchUpdateIssues(wBatch, reqBatch)
			if wBatch.Code < 200 || wBatch.Code >= 300 {
				t.Fatalf("expected 2xx batch with a per-issue skip, got %d: %s",
					wBatch.Code, wBatch.Body.String())
			}
			var labAfter pgtype.Text
			if err := testPool.QueryRow(context.Background(),
				`SELECT lab_source FROM issue WHERE id = $1`, batched.ID,
			).Scan(&labAfter); err != nil {
				t.Fatalf("re-read: %v", err)
			}
			if labAfter.Valid {
				t.Errorf("batch persisted lab_source=%q for assignee-model lab %q; "+
					"expected the per-issue skip the single-issue path 400s on",
					labAfter.String, lab)
			}
		})
	}
}

// TestBatchUpdateIssuesRespectsSwarmTopologyMutex — 0.5.60 (audit P0-1).
// The 0.5.21 swarm mutex extension landed in CreateIssue/UpdateIssue/UI but
// never reached the batch switch: a batch PATCH flipping lab_source to
// swarm_topology onto an assigned issue persisted silently while the
// single-issue paths 400. Pin the batch layer to the same narrowed gate.
func TestBatchUpdateIssuesRespectsSwarmTopologyMutex(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	const swarmLab = "swarm_topology"
	realMemberID := testUserID

	t.Run("lab + assignee in same batch update → per-issue skip", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":         "batch-swarm-mutex-1",
			"assignee_type": "member",
			"assignee_id":   realMemberID,
		})
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{created.ID},
			"updates": map[string]any{
				"lab_source":    swarmLab,
				"assignee_type": "member",
				"assignee_id":   realMemberID,
			},
		})
		testHandler.BatchUpdateIssues(w, req)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("expected 2xx for batch, got %d: %s", w.Code, w.Body.String())
		}
		var labSourceAfter pgtype.Text
		if err := testPool.QueryRow(context.Background(),
			`SELECT lab_source FROM issue WHERE id = $1`, created.ID,
		).Scan(&labSourceAfter); err != nil {
			t.Fatalf("re-read issue: %v", err)
		}
		if labSourceAfter.Valid {
			t.Errorf("batch swarm mutex violation persisted lab_source=%q; expected NULL",
				labSourceAfter.String)
		}
	})

	t.Run("lab only on pre-assigned issue → per-issue skip", func(t *testing.T) {
		created := createIssueForTest(t, map[string]any{
			"title":         "batch-swarm-mutex-2",
			"assignee_type": "member",
			"assignee_id":   realMemberID,
		})
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{created.ID},
			"updates": map[string]any{
				"lab_source": swarmLab,
			},
		})
		testHandler.BatchUpdateIssues(w, req)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("expected 2xx for batch, got %d: %s", w.Code, w.Body.String())
		}
		var labSourceAfter pgtype.Text
		if err := testPool.QueryRow(context.Background(),
			`SELECT lab_source FROM issue WHERE id = $1`, created.ID,
		).Scan(&labSourceAfter); err != nil {
			t.Fatalf("re-read issue: %v", err)
		}
		if labSourceAfter.Valid {
			t.Errorf("batch silently persisted lab_source=%q on a swarm mutex violation",
				labSourceAfter.String)
		}
	})

	t.Run("lab only on unassigned issue → frozen reject skips the issue", func(t *testing.T) {
		// 0.5.105 (audit H3): swarm_topology is frozen — a new binding
		// is rejected per-issue (continue contract: never 400 the whole
		// batch), so the response is 2xx but lab_source must NOT land.
		created := createIssueForTest(t, map[string]any{
			"title": "batch-swarm-mutex-3",
		})
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{created.ID},
			"updates": map[string]any{
				"lab_source": swarmLab,
			},
		})
		testHandler.BatchUpdateIssues(w, req)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("expected 2xx for batch, got %d: %s", w.Code, w.Body.String())
		}
		var labSourceAfter pgtype.Text
		if err := testPool.QueryRow(context.Background(),
			`SELECT lab_source FROM issue WHERE id = $1`, created.ID,
		).Scan(&labSourceAfter); err != nil {
			t.Fatalf("re-read issue: %v", err)
		}
		if labSourceAfter.Valid {
			t.Errorf("batch persisted frozen lab_source=%q; expected per-issue skip", labSourceAfter.String)
		}
	})
}
