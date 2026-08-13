package handler

import (
	"context"
	"testing"
)

// TestSeedPluginVisibility_ScopesToInstallerWorkspace pins the F-013 fix:
// seedPluginVisibility must only seed visibility rows for resources living in
// the installer's workspace. Pre-fix the agent/squad/autopilot lookups were
// cross-workspace (LIMIT 1 by name), so a plugin in workspace A could seed a
// hidden row for an agent named the same in workspace B — hiding that agent
// from its own workspace's pickers.
func TestSeedPluginVisibility_ScopesToInstallerWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Two workspaces, each with an agent sharing the same name.
	_, wsA := installCodeCanvasFresh(t, ctx, "f013-ws-a")
	_, wsB := installCodeCanvasFresh(t, ctx, "f013-ws-b")

	insertAgent := func(wsID, name string) string {
		t.Helper()
		var runtimeID string
		if err := testPool.QueryRow(ctx,
			`SELECT id FROM agent_runtime WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
			wsID,
		).Scan(&runtimeID); err != nil {
			t.Fatalf("runtime for workspace %s: %v", wsID, err)
		}
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config,
				runtime_id, visibility, max_concurrent_tasks, owner_id, instructions,
				custom_env, custom_args, mcp_config)
			VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)
			RETURNING id
		`, wsID, name, runtimeID, testUserID).Scan(&id); err != nil {
			t.Fatalf("insert agent %q in workspace %s: %v", name, wsID, err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id)
		})
		return id
	}

	agentA := insertAgent(wsA, "shared-name")
	agentB := insertAgent(wsB, "shared-name")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `
			DELETE FROM experimental_resource_visibility
			WHERE resource_type = 'agent' AND resource_id IN ($1, $2)
		`, agentA, agentB)
	})

	manifest := []byte(`{"capabilities":{"agents":["shared-name"]}}`)
	const flagKey = "user_f013-plugin"

	// Seed from workspace A only.
	testHandler.seedPluginVisibility(ctx, flagKey, manifest, uuidToPgtype(wsA))

	var visA, visB int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'agent' AND resource_id = $2
	`, flagKey, agentA).Scan(&visA); err != nil {
		t.Fatalf("count visA: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'agent' AND resource_id = $2
	`, flagKey, agentB).Scan(&visB); err != nil {
		t.Fatalf("count visB: %v", err)
	}
	if visA != 1 {
		t.Fatalf("workspace A agent visibility rows = %d, want 1", visA)
	}
	if visB != 0 {
		t.Fatalf("workspace B agent visibility rows = %d, want 0 (cross-workspace seeding!)", visB)
	}
}
