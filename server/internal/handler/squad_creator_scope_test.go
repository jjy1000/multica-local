package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestCanMutateSquad covers the squad creator-scope gate introduced after
// audit: only the squad creator or a workspace owner may mutate a squad.
// Without this gate, every workspace admin could edit or archive a squad
// they did not create.
func TestCanMutateSquad(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorUUID := util.MustParseUUID(testUserID)
	otherUUID := util.MustParseUUID("00000000-0000-0000-0000-000000000001")
	zeroUUID := util.MustParseUUID("00000000-0000-0000-0000-000000000000")

	cases := []struct {
		name   string
		member db.Member
		squad  db.Squad
		want   bool
	}{
		{
			name:   "owner always can",
			member: db.Member{UserID: otherUUID, Role: "owner"},
			squad:  db.Squad{CreatorID: creatorUUID},
			want:   true,
		},
		{
			name:   "creator can mutate own squad",
			member: db.Member{UserID: creatorUUID, Role: "admin"},
			squad:  db.Squad{CreatorID: creatorUUID},
			want:   true,
		},
		{
			name:   "non-creator admin cannot mutate",
			member: db.Member{UserID: otherUUID, Role: "admin"},
			squad:  db.Squad{CreatorID: creatorUUID},
			want:   false,
		},
		{
			name:   "non-creator member cannot mutate",
			member: db.Member{UserID: otherUUID, Role: "member"},
			squad:  db.Squad{CreatorID: creatorUUID},
			want:   false,
		},
		{
			name:   "legacy zero-uuid creator falls back to admin",
			member: db.Member{UserID: otherUUID, Role: "admin"},
			squad:  db.Squad{CreatorID: zeroUUID},
			want:   true,
		},
		{
			name:   "legacy zero-uuid creator still rejects member",
			member: db.Member{UserID: otherUUID, Role: "member"},
			squad:  db.Squad{CreatorID: zeroUUID},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := testHandler.canMutateSquad(tc.member, tc.squad)
			if got != tc.want {
				t.Fatalf("canMutateSquad(role=%q, creator=%v) = %v, want %v",
					tc.member.Role, tc.squad.CreatorID, got, tc.want)
			}
		})
	}
}

// TestIsZeroUUID pins the helper that distinguishes legacy rows (zero UUID
// creator) from real creators. Both Valid:false (NULL) and Valid:true with
// all-zero bytes must fall back to the admin path.
func TestIsZeroUUID(t *testing.T) {
	zero := util.MustParseUUID("00000000-0000-0000-0000-000000000000")
	real := util.MustParseUUID("11111111-1111-1111-1111-111111111111")

	if !isZeroUUID(zero) {
		t.Fatal("zero UUID should be detected as zero")
	}
	if isZeroUUID(real) {
		t.Fatal("real UUID should not be detected as zero")
	}
	nullUUID := pgtype.UUID{Valid: false}
	if !isZeroUUID(nullUUID) {
		t.Fatal("NULL UUID should fall back to zero-path")
	}
}

// TestUpdateSquad_NonCreatorAdminForbidden wires a real squad with creator
// = workspace owner (testUserID) and verifies that a non-creator admin
// member receives 403 when calling UpdateSquad. This is the audit-pinned
// regression test.
func TestUpdateSquad_NonCreatorAdminForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "Squad CreatorScope Leader", nil)

	// Squad is created by testUserID (the workspace owner). Any *additional*
	// admin member is a non-creator admin relative to this squad.
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, 'Scope Test Squad', '', $2, $3)
		RETURNING id
	`, testWorkspaceID, leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID)
	})

	// A new user + admin member for this workspace.
	nonCreatorAdminUserID := createNonOwnerUser(t, "Squad Scope NonCreator")
	nonCreatorAdminMemberID := createNonOwnerAdminMember(t, nonCreatorAdminUserID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE id = $1`, nonCreatorAdminMemberID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, nonCreatorAdminUserID)
	})

	w := httptest.NewRecorder()
	r := newRequest("PUT", "/api/squads/"+squadID, map[string]any{
		"name": "Pwned",
	})
	r = withURLParam(r, "id", squadID)
	r.Header.Set("X-User-ID", nonCreatorAdminUserID)
	r.Header.Set("X-Workspace-ID", testWorkspaceID)

	// Inject workspace + member into context so workspaceIDFromURL works
	// the same way the production middleware does.
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(nonCreatorAdminUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load non-creator admin member: %v", err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", squadID)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	testHandler.UpdateSquad(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-creator admin, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Verify name was NOT updated despite the 403 attempt.
	var currentName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM squad WHERE id = $1`, squadID).Scan(&currentName); err != nil {
		t.Fatalf("reload squad: %v", err)
	}
	if currentName != "Scope Test Squad" {
		t.Fatalf("squad name was mutated despite 403: got %q", currentName)
	}
}

// TestUpdateSquad_CreatorCanMutate verifies the gate does NOT block the
// legitimate creator path. testUserID is both workspace owner and squad
// creator in this fixture.
func TestUpdateSquad_CreatorCanMutate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "Squad CreatorOK Leader", nil)

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, 'Creator OK', '', $2, $3)
		RETURNING id
	`, testWorkspaceID, leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID)
	})

	w := httptest.NewRecorder()
	r := newRequest("PUT", "/api/squads/"+squadID, map[string]any{
		"name": "Creator Renamed",
	})
	r = withURLParam(r, "id", squadID)
	// Inject workspace + member so workspaceIDFromURL resolves via context.
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load creator member: %v", err)
	}
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member))
	testHandler.UpdateSquad(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for creator, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["name"] != "Creator Renamed" {
		t.Fatalf("squad name not updated; got %v", resp["name"])
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// createNonOwnerUser inserts a fresh user row and returns its UUID.
func createNonOwnerUser(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $1 || '@local') RETURNING id
	`, name).Scan(&id); err != nil {
		t.Fatalf("create user %q: %v", name, err)
	}
	return id
}

// createNonOwnerAdminMember binds a user as admin in the test workspace.
// Caller is responsible for cleanup.
func createNonOwnerAdminMember(t *testing.T, userID string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'admin')
		RETURNING id
	`, testWorkspaceID, userID).Scan(&id); err != nil {
		t.Fatalf("create admin member: %v", err)
	}
	return id
}
