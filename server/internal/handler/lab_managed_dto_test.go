package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// loadMemberContextForTest loads the test user's member row and injects it
// (plus the workspace id) into the request context, mirroring what the
// production middleware does. Both single-fetch lab_managed tests below need
// it because GetAgent's private-agent gate and GetSquad's workspace lookup
// read the member from context when present.
func loadMemberContextForTest(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load member: %v", err)
	}
	return r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member))
}

// seedVisibilityRow inserts a lab-managed visibility row for the given
// (resource_type, resource_id) pair and registers a cleanup. The
// lab_managed DTO stamp keys off row EXISTENCE (ListLabManagedResourceIDs
// filters by resource_type only), so flag_key/hidden values are irrelevant
// to the assertion — they just need to satisfy the table's NOT NULL columns.
func seedVisibilityRow(t *testing.T, resourceType, resourceID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id, hidden)
		VALUES ('mythos_swarm', $1, $2, TRUE)
	`, resourceType, resourceID); err != nil {
		t.Fatalf("seed visibility row: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM experimental_resource_visibility WHERE resource_id = $1 AND resource_type = $2`, resourceID, resourceType)
	})
}

// TestGetAgent_StampsLabManaged pins the 0.3.56 lab_managed DTO contract at
// the single-fetch layer (SEC-P1-7). Pre-0.5.18 GET /api/agents/{id} returned
// the raw row without the stamp, so a hidden lab agent pasted by id into the
// URL rendered standalone-selectable in the AssigneePicker.
func TestGetAgent_StampsLabManaged(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	agentID := createHandlerTestAgent(t, "lab-managed-agent", []byte(`{}`))
	seedVisibilityRow(t, "agent", agentID)

	w := httptest.NewRecorder()
	r := newRequest(http.MethodGet, "/api/agents/"+agentID, nil)
	r = withURLParam(r, "id", agentID)
	r = loadMemberContextForTest(t, r)
	testHandler.GetAgent(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		LabManaged bool `json:"lab_managed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.LabManaged {
		t.Fatalf("expected lab_managed=true for agent with a visibility row, got %v", resp.LabManaged)
	}
}

// TestGetSquad_StampsLabManaged is the squad-side twin of the agent test.
func TestGetSquad_StampsLabManaged(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "lab-managed-squad-leader", []byte(`{}`))
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, 'Lab Managed Squad', '', $2, $3)
		RETURNING id
	`, testWorkspaceID, leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID)
	})
	seedVisibilityRow(t, "squad", squadID)

	w := httptest.NewRecorder()
	r := newRequest(http.MethodGet, "/api/squads/"+squadID, nil)
	r = withURLParam(r, "id", squadID)
	r = loadMemberContextForTest(t, r)
	testHandler.GetSquad(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		LabManaged bool `json:"lab_managed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.LabManaged {
		t.Fatalf("expected lab_managed=true for squad with a visibility row, got %v", resp.LabManaged)
	}
}
