// Package handler — install_semantica_test.go (0.5.22 Semantica × Multica Phase 2)
//
// Verifies the install handler's idempotency + leader-name + visibility-row
// contract. Mirrors install_pythia_visibility_test.go's shape so the
// same fixtures and shared testHandler are reused.
//
// These tests require a running PostgreSQL — they are skipped by
// handler_test.go's TestMain when DATABASE_URL is unreachable.
//
// install_semantica.go::upsertSemanticaDecisionAdvisorAgent calls
// resolveWorkspaceOnlineRuntime and binds the agent's runtime_id to
// it. Migration 004 makes agent.runtime_id NOT NULL with an FK to
// agent_runtime, so installCodeCanvasFresh (which provisions an
// online local runtime) is reused here.
package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestInstallSemantica_CreatesAgentAndVisibilityRow exercises the
// install path's happy case: a fresh install must (a) create the
// semantica_decision_advisor leader agent bound to an online local
// runtime, and (b) seed exactly one visibility row for that agent
// under flag_key='semantica'. The visibility row is the forward
// guard against a future flag-gated picker rewrite that reads
// experimental_resource_visibility (mirrors install_pythia.go).
func TestInstallSemantica_CreatesAgentAndVisibilityRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "semantica-fresh")
	cleanupVisibilityRows(t, workspaceID)

	if err := testHandler.InstallSemantica(ctx, userID, workspaceID); err != nil {
		t.Fatalf("InstallSemantica: %v", err)
	}

	agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        semanticaDecisionAdvisorName,
	})
	if err != nil {
		t.Fatalf("%s agent not created: %v", semanticaDecisionAdvisorName, err)
	}
	if !agentRow.RuntimeID.Valid {
		t.Fatalf("%s agent has no runtime_id — online local runtime not bound", semanticaDecisionAdvisorName)
	}

	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'agent'
		  AND resource_id = $2 AND hidden = TRUE
	`, semanticaSource, pgtypeToString(agentRow.ID)).Scan(&visCount); err != nil {
		t.Fatalf("visibility count: %v", err)
	}
	if visCount != 1 {
		t.Fatalf("visibility rows = %d, want 1", visCount)
	}
}

// TestInstallSemantica_IsIdempotent re-runs the install on the same
// workspace and asserts the agent + visibility counts stay at 1.
// Counts are workspace-scoped via JOIN so prior-run orphans on the
// shared dev database cannot inflate them.
func TestInstallSemantica_IsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "semantica-idem")
	cleanupVisibilityRows(t, workspaceID)

	for i := 0; i < 2; i++ {
		if err := testHandler.InstallSemantica(ctx, userID, workspaceID); err != nil {
			t.Fatalf("InstallSemantica (attempt %d): %v", i+1, err)
		}
	}

	var agentCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent
		WHERE workspace_id = $1 AND name = $2
	`, workspaceID, semanticaDecisionAdvisorName).Scan(&agentCount); err != nil {
		t.Fatalf("agent count: %v", err)
	}
	if agentCount != 1 {
		t.Fatalf("agent count = %d, want 1 (idempotency broken)", agentCount)
	}

	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility v
		JOIN agent a ON a.id = v.resource_id
		WHERE v.flag_key = $1 AND v.resource_type = 'agent'
		  AND a.workspace_id = $2
	`, semanticaSource, workspaceID).Scan(&visCount); err != nil {
		t.Fatalf("visibility count: %v", err)
	}
	if visCount != 1 {
		t.Fatalf("visibility rows = %d, want 1 (idempotency broken)", visCount)
	}
}

// TestInstallSemantica_LeaderNameMatchesLabLeaderTable pins the
// invariant that the agent_name the install handler creates matches
// the lab-leader table on both sides of the create/update split
// (service/issue.go defaultLeaderAgentForLab + handler/issue.go
// defaultLabLeaderForKey). A drift between the install handler and
// either table silently breaks the P0#4 auto-rewrite path. The test
// is cheap and pins the contract for any future rename.
func TestInstallSemantica_LeaderNameMatchesLabLeaderTable(t *testing.T) {
	t.Parallel()

	if name, ok := defaultLabLeaderForKey("semantica"); !ok || name != semanticaDecisionAdvisorName {
		t.Fatalf("defaultLabLeaderForKey(\"semantica\") = (%q, %v), want (%q, true)",
			name, ok, semanticaDecisionAdvisorName)
	}
}
