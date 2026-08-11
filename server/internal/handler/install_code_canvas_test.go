// Package handler — install_code_canvas_test.go (0.3.56+)
//
// Verifies the 0.3.56 fix for the code_canvas install P0:
//
//	pre-0.3.56: InstallCodeCanvas only created the `code_canvas_worker`
//	leader agent + an experimental_resource_visibility row. When an
//	issue with lab_source='code_canvas' auto-dispatched to the leader
//	via assignDefaultLabAgentOnUpdate, the agent ran its placeholder
//	instructions and the issue stayed pending forever — the agent
//	had no skill body to read.
//
//	0.3.56 fix: the install handler also provisions the embedded
//	`multica-code-canvas` SKILL.md body into a workspace `skill` row
//	and binds it to the leader via `agent_skill`. BuiltinSkills() at
//	the runtime layer auto-loads the embedded body for every agent
//	regardless, so the explicit agent_skill row is the testable seam
//	the install handler owns.
//
// These tests require a running PostgreSQL — they are skipped by
// handler_test.go's TestMain when DATABASE_URL is unreachable. The
// shared testHandler / testUserID / testWorkspaceID fixture
// (handler_test.go:120) is reused; the install path is purely a
// service-layer call, so no HTTP roundtrip is needed.

package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// codeCanvasWorkerName is the leader agent row the install handler
// creates. Re-declared locally so the test can assert on the row
// without re-parsing the install handler's internals.
const codeCanvasWorkerName = "code_canvas_worker"

// installCodeCanvasFresh creates a throwaway (user, workspace) pair
// the test owns — t.Cleanup tears it down. Returning hermetic
// fixtures per test is what lets the idempotency assertion
// re-install on the same workspace without conflicting with the
// shared handler_test.go fixture state.
//
// An online agent_runtime is also created and bound to the
// workspace: agent.runtime_id is NOT NULL in the schema, and the
// install handler refuses to create an agent with an invalid
// RuntimeID (it would crash on the FK + NOT NULL constraints). The
// status='online' status is what `resolveWorkspaceOnlineRuntime`
// keys off in install_code_canvas.go.
func installCodeCanvasFresh(t *testing.T, ctx context.Context, name string) (string, string) {
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
	`, name+"-workspace", name+"-slug", "install_code_canvas test", "ICC").Scan(&workspaceID); err != nil {
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
	// Online local runtime — install_code_canvas.go::resolveWorkspaceOnlineRuntime
	// only returns a non-zero UUID when BOTH status='online' AND
	// runtime_mode='local'. agent.runtime_id is NOT NULL, so an
	// invalid UUID short-circuits to a FK violation.
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

// TestInstallCodeCanvas_BindsBuiltinSkill exercises the 0.3.56
// fix: a fresh install must (a) create the code_canvas_worker
// agent, (b) create the `multica-code-canvas` skill row in the
// workspace, and (c) create the agent_skill row binding them.
// Pre-0.3.56 (b) and (c) were skipped, leaving the auto-dispatched
// agent with no skill body — the issue stayed pending forever.
func TestInstallCodeCanvas_BindsBuiltinSkill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "code-canvas-bind")

	if err := testHandler.InstallCodeCanvas(ctx, userID, workspaceID); err != nil {
		t.Fatalf("InstallCodeCanvas: %v", err)
	}

	wsUUID := uuidToPgtype(workspaceID)

	// (a) the leader agent exists.
	agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: wsUUID,
		Name:        codeCanvasWorkerName,
	})
	if err != nil {
		t.Fatalf("code_canvas_worker agent not created: %v", err)
	}

	// (b) the multica-code-canvas skill row exists in the workspace.
	skillRow, err := testHandler.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: wsUUID,
		Name:        codeCanvasBuiltinSkillName,
	})
	if err != nil {
		t.Fatalf("multica-code-canvas skill not created: %v", err)
	}
	if skillRow.Name != codeCanvasBuiltinSkillName {
		t.Fatalf("skill name = %q, want %q", skillRow.Name, codeCanvasBuiltinSkillName)
	}
	if len(skillRow.Content) == 0 {
		t.Fatalf("skill content is empty — embedded SKILL.md body not materialised")
	}

	// (c) the agent_skill row exists binding the two.
	bound, err := testHandler.Queries.ListAgentSkills(ctx, agentRow.ID)
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	found := false
	for _, s := range bound {
		if s.Name == codeCanvasBuiltinSkillName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("agent_skill binding missing: agent %q has skills %v, want one named %q",
			codeCanvasWorkerName, skillNames(bound), codeCanvasBuiltinSkillName)
	}

	// (d) the visibility row exists (carry-over from the pre-fix
	// path — guards against accidental regression of the existing
	// upsertCodeCanvasVisibility call).
	agentUUID := pgtypeToString(agentRow.ID)
	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = 'code_canvas' AND resource_type = 'agent' AND resource_id = $1
	`, agentUUID).Scan(&visCount); err != nil {
		t.Fatalf("visibility count: %v", err)
	}
	if visCount == 0 {
		t.Fatalf("experimental_resource_visibility row missing for code_canvas_worker")
	}
}

// TestInstallCodeCanvas_Idempotent re-runs the install on the same
// workspace and asserts no rows are duplicated: still one
// code_canvas_worker agent, still one multica-code-canvas skill
// row, still one agent_skill binding. The handler is documented as
// idempotent; this test is the contract.
func TestInstallCodeCanvas_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "code-canvas-idem")

	for i := 0; i < 2; i++ {
		if err := testHandler.InstallCodeCanvas(ctx, userID, workspaceID); err != nil {
			t.Fatalf("InstallCodeCanvas (attempt %d): %v", i+1, err)
		}
	}

	// Exactly one code_canvas_worker agent.
	var agentCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent
		WHERE workspace_id = $1 AND name = $2
	`, workspaceID, codeCanvasWorkerName).Scan(&agentCount); err != nil {
		t.Fatalf("agent count: %v", err)
	}
	if agentCount != 1 {
		t.Fatalf("agent count = %d, want 1 (idempotency broken)", agentCount)
	}

	// Exactly one multica-code-canvas skill row.
	var skillCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM skill
		WHERE workspace_id = $1 AND name = $2
	`, workspaceID, codeCanvasBuiltinSkillName).Scan(&skillCount); err != nil {
		t.Fatalf("skill count: %v", err)
	}
	if skillCount != 1 {
		t.Fatalf("skill count = %d, want 1 (idempotency broken)", skillCount)
	}

	// Exactly one agent_skill binding for the leader.
	wsUUID := uuidToPgtype(workspaceID)
	agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: wsUUID,
		Name:        codeCanvasWorkerName,
	})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	var bindingCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent_skill WHERE agent_id = $1 AND skill_id = (
			SELECT id FROM skill WHERE workspace_id = $2 AND name = $3
		)
	`, pgtypeToString(agentRow.ID), workspaceID, codeCanvasBuiltinSkillName).Scan(&bindingCount); err != nil {
		t.Fatalf("binding count: %v", err)
	}
	if bindingCount != 1 {
		t.Fatalf("agent_skill binding count = %d, want 1 (idempotency broken)", bindingCount)
	}
}

// skillNames extracts the name column from a list of skill rows so
// failure messages stay readable.
func skillNames(skills []db.Skill) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.Name)
	}
	return out
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
