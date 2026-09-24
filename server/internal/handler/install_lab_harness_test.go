package handler

// Shared lab-install test harness. Formerly lived in
// install_code_canvas_test.go (the code_canvas lab was retired 0.5.122);
// renamed now that it is the generic fresh-workspace fixture for
// install-path tests across all labs.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// installLabTestFresh creates a throwaway (user, workspace) pair with an
// ONLINE LOCAL runtime. resolveWorkspaceOnlineRuntime only returns a
// non-zero UUID when BOTH status='online' AND runtime_mode='local';
// agent.runtime_id is NOT NULL, so an invalid UUID short-circuits to a FK
// violation. Workspace delete cascades the agent/runtime rows installs
// create.
func installLabTestFresh(t *testing.T, ctx context.Context, name string) (string, string) {
	t.Helper()
	var userID, workspaceID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, name+"-user", name+"@multica-test.ai").Scan(&userID); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, name+"-workspace", name+"-slug", "lab install test", "ILT").Scan(&workspaceID); err != nil {
		t.Fatalf("create test workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("create test member: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'local', $3, 'online', $4, '{}'::jsonb, $5, now())
		RETURNING id
	`, workspaceID, name+"-runtime", name+"-runtime-provider", "test runtime", userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create test runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return userID, workspaceID
}

// uuidToPgtype parses a canonical 8-4-4-4-12 hex UUID string into
// the pgtype.UUID the sqlc queries expect. google/uuid is the
// standard helper used across this package's other tests.
func uuidToPgtype(s string) pgtype.UUID {
	parsed, err := uuid.Parse(s)
	if err != nil {
		panic("test fixture: invalid uuid: " + err.Error())
	}
	return pgtype.UUID{
		Bytes: parsed,
		Valid: true,
	}
}

// pgtypeToString renders a pgtype.UUID back to its canonical
// 8-4-4-4-12 hex form for raw SQL parameters.
func pgtypeToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// cleanupVisibilityRows removes lab-visibility rows seeded for agents and
// squads in the test workspace, so a re-install starts from a clean slate.
func cleanupVisibilityRows(t *testing.T, workspaceID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM experimental_resource_visibility v
			WHERE v.resource_id IN (
				SELECT id FROM agent WHERE workspace_id = $1
				UNION ALL
				SELECT id FROM squad WHERE workspace_id = $1
			)
		`, workspaceID)
	})
}
