// 0.5.22 MUL-4010 regression coverage for CreateAgentFromTemplate.
//
// CreateAgentFromTemplate used to accept only the legacy visibility field
// and drop it on the floor: neither permission_mode nor invocation_targets
// flowed into the INSERT, so the SQL default
// (COALESCE(sqlc.narg('permission_mode'), 'private')) pinned every
// template-created agent as private in the new invocation-permission model
// (MUL-3963). Since canInvokeAgent reads permission_mode — not the legacy
// visibility column — a request that asked for a workspace-shared agent
// (old Web/CLI/Desktop sending visibility="workspace", or new Web sending
// permission_mode=public_to + invocation_targets) silently landed as
// owner-only.
//
// These tests pin the post-fix behaviour: parsePermissionInput runs on
// the template path too, the invocation allow-list is persisted inside
// the same tx as the agent row, and the response reflects the
// derived legacy visibility so a client that asked for
// visibility="workspace" sees the round-trip correctly.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateAgentFromTemplate_LegacyVisibilityMapsToPermission is the
// MUL-4010 regression: the template create path used to persist
// permission_mode='private' (the SQL default) regardless of the incoming
// legacy `visibility="workspace"` value, so an agent that the caller
// asked to be workspace-shared silently became owner-only in
// canInvokeAgent.
//
// After the fix, parsePermissionInput runs on the template path too:
//   - visibility "workspace" -> permission_mode public_to + workspace target
//   - visibility "private"   -> permission_mode private + no targets
func TestCreateAgentFromTemplate_LegacyVisibilityMapsToPermission(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)

	// commit-message ships with zero external skills so this test never
	// touches the network fetch path. Any zero-skill template would do.
	const templateSlug = "commit-message"
	if _, ok := agentTemplates.Get(templateSlug); !ok {
		t.Fatalf("expected template %q to be loaded", templateSlug)
	}

	create := func(name, visibility string) CreateAgentFromTemplateResponse {
		w := httptest.NewRecorder()
		testHandler.CreateAgentFromTemplate(w, newRequest("POST", "/api/agents/from-template?workspace_id="+testWorkspaceID, map[string]any{
			"template_slug": templateSlug,
			"name":          name,
			"runtime_id":    runtimeID,
			"visibility":    visibility,
		}))
		if w.Code != http.StatusCreated {
			t.Fatalf("create %q (visibility=%s): expected 201, got %d: %s", name, visibility, w.Code, w.Body.String())
		}
		var resp CreateAgentFromTemplateResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, resp.Agent.ID)
		})
		return resp
	}

	ws := create("template-legacy-workspace", "workspace")
	if ws.Agent.PermissionMode != "public_to" {
		t.Errorf("workspace-template agent permission_mode = %q, want public_to", ws.Agent.PermissionMode)
	}
	if ws.Agent.Visibility != "workspace" {
		t.Errorf("workspace-template agent derived visibility = %q, want workspace", ws.Agent.Visibility)
	}
	foundWorkspaceTarget := false
	for _, tgt := range ws.Agent.InvocationTargets {
		if tgt.TargetType == "workspace" {
			foundWorkspaceTarget = true
		}
	}
	if !foundWorkspaceTarget {
		t.Errorf("workspace-template agent invocation_targets = %+v, want a workspace target", ws.Agent.InvocationTargets)
	}
	// Additional DB-level check: canInvokeAgent's persisted inputs — the
	// row's permission_mode column AND at least one invocation-target row
	// — must both be present. A response that reflected the intent but a
	// row that didn't would still deny non-owner invokes at runtime.
	var mode string
	if err := testPool.QueryRow(context.Background(),
		`SELECT permission_mode FROM agent WHERE id = $1`, ws.Agent.ID).Scan(&mode); err != nil {
		t.Fatalf("load persisted permission_mode: %v", err)
	}
	if mode != "public_to" {
		t.Errorf("persisted permission_mode = %q, want public_to", mode)
	}
	var targetCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_invocation_target WHERE agent_id = $1 AND target_type = 'workspace'`,
		ws.Agent.ID,
	).Scan(&targetCount); err != nil {
		t.Fatalf("load persisted workspace targets: %v", err)
	}
	if targetCount != 1 {
		t.Errorf("persisted workspace target rows = %d, want 1", targetCount)
	}

	priv := create("template-legacy-private", "private")
	if priv.Agent.PermissionMode != "private" {
		t.Errorf("private-template agent permission_mode = %q, want private", priv.Agent.PermissionMode)
	}
	if priv.Agent.Visibility != "private" {
		t.Errorf("private-template agent derived visibility = %q, want private", priv.Agent.Visibility)
	}
	if len(priv.Agent.InvocationTargets) != 0 {
		t.Errorf("private-template agent invocation_targets = %+v, want none", priv.Agent.InvocationTargets)
	}
}

// TestCreateAgentFromTemplate_PublicToWithMemberTarget verifies that
// when the new-Web-shape (permission_mode + invocation_targets) arrives
// on the template path, it is honoured — a member allow-list is
// persisted verbatim instead of being silently dropped and replaced with
// the SQL default.
//
// Targets a freshly inserted user so the test never collides with the
// canonical Handler Test User (cleanupHandlerTestFixture would wipe the
// target row before the SELECT runs).
func TestCreateAgentFromTemplate_PublicToWithMemberTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)

	const templateSlug = "commit-message"
	if _, ok := agentTemplates.Get(templateSlug); !ok {
		t.Fatalf("expected template %q to be loaded", templateSlug)
	}

	// Fresh member to grant invocation access to.
	targetUserID := createTemplatePermissionTestMember(t, "template-invoke-target@multica.ai")

	w := httptest.NewRecorder()
	testHandler.CreateAgentFromTemplate(w, newRequest("POST", "/api/agents/from-template?workspace_id="+testWorkspaceID, map[string]any{
		"template_slug":   templateSlug,
		"name":            "template-public-to-member",
		"runtime_id":      runtimeID,
		"permission_mode": "public_to",
		"invocation_targets": []map[string]any{
			{"target_type": "member", "target_id": targetUserID},
		},
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateAgentFromTemplateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, resp.Agent.ID)
	})

	if resp.Agent.PermissionMode != "public_to" {
		t.Errorf("permission_mode = %q, want public_to", resp.Agent.PermissionMode)
	}
	// public_to limited to a specific member -> derived legacy visibility
	// collapses to "private" (only workspace-target public_to derives to
	// "workspace"); the real audience is the member allow-list below.
	if resp.Agent.Visibility != "private" {
		t.Errorf("derived legacy visibility = %q, want private (member-only public_to)", resp.Agent.Visibility)
	}
	sawMember := false
	for _, tgt := range resp.Agent.InvocationTargets {
		if tgt.TargetType == "member" && tgt.TargetID != nil && *tgt.TargetID == targetUserID {
			sawMember = true
		}
	}
	if !sawMember {
		t.Errorf("invocation_targets = %+v, want a member target for %s", resp.Agent.InvocationTargets, targetUserID)
	}

	// DB-level check: the member row is persisted, not just echoed back.
	var persisted int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_invocation_target WHERE agent_id = $1 AND target_type = 'member' AND target_id = $2`,
		resp.Agent.ID, targetUserID,
	).Scan(&persisted); err != nil {
		t.Fatalf("load persisted member target: %v", err)
	}
	if persisted != 1 {
		t.Errorf("persisted member target rows = %d, want 1", persisted)
	}
}

// createTemplatePermissionTestMember inserts a one-off user + member
// row for a single test. The user is added to the canonical test
// workspace so canInvokeAgent can resolve the workspace-member check,
// and the cleanup t.Cleanup removes both rows so the next test run has
// a clean slate. Fork-local helper (the upstream
// `createPermissionTestMember` lives in agent_permission_test.go which
// the fork never ported — the helpers there are upstream-only).
func createTemplatePermissionTestMember(t *testing.T, email string) string {
	t.Helper()

	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Template Permission Target", email,
	).Scan(&userID); err != nil {
		t.Fatalf("create permission test member user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE user_id = $1`, userID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		testWorkspaceID, userID,
	); err != nil {
		t.Fatalf("add permission test member to workspace: %v", err)
	}
	return userID
}
