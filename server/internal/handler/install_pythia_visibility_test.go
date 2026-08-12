// Package handler — install_pythia_visibility_test.go (0.3.54 contract)
//
// Verifies the 0.3.54 visibility forward-guard in install_pythia.go:
// InstallPythia must seed an experimental_resource_visibility row for
// the pythia_runtime leader agent under flag_key='pythia_oracle',
// so the future flag-gated picker filter
// (filterLabsHiddenByDefault(flagKey, HideAgent)) hides it from the
// regular agent picker when pythia_oracle is OFF.
//
// The current renderer-side picker reads the lock table's hidden=true
// rows (ListVisibleAgentsByWorkspace), so a missing visibility row
// would not visibly leak the agent today. The visibility table is
// the canonical source-of-truth for any future flag-driven UI
// surface (and for the 0.3.56 lab_managed DTO stamp); without the
// seed loop, a migration that switches the picker to the visibility
// table would silently re-leak pythia_runtime. These tests pin the
// 0.3.54 fix in place.
//
// These tests require a running PostgreSQL — they are skipped by
// handler_test.go's TestMain when DATABASE_URL is unreachable. The
// shared testHandler / testPool fixture is reused; the install path
// is purely a service-layer call, so no HTTP roundtrip is needed.
//
// install_pythia.go::upsertPythiaRuntimeAgent calls
// resolveWorkspaceOnlineRuntime and binds the agent's runtime_id to
// it. Migration 004 makes agent.runtime_id NOT NULL with an FK to
// agent_runtime, so a fresh install without an online local runtime
// would fail the FK / NOT NULL constraint. installCodeCanvasFresh
// provisions one (status='online', runtime_mode='local'), satisfying
// the binding path.
package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// pythiaRuntimeAgentName mirrors the hardcoded agent name in
// install_pythia.go::upsertPythiaRuntimeAgent. Declared locally so
// the test does not re-parse the install handler's internals.
const pythiaRuntimeAgentName = "pythia_runtime"

// TestInstallPythia_SeedsVisibility exercises the 0.3.54 contract:
// a fresh install must (a) create the pythia_runtime leader agent
// bound to an online local runtime, and (b) seed exactly one
// (flag_key='pythia_oracle', resource_type='agent', hidden=TRUE)
// visibility row linked to it.
func TestInstallPythia_SeedsVisibility(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "pythia-vis")
	cleanupVisibilityRows(t, workspaceID)

	if err := testHandler.InstallPythia(ctx, userID, workspaceID); err != nil {
		t.Fatalf("InstallPythia: %v", err)
	}

	agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        pythiaRuntimeAgentName,
	})
	if err != nil {
		t.Fatalf("pythia_runtime agent not created: %v", err)
	}
	// Runtime binding is the agent.runtime_id NOT NULL prerequisite
	// (migration 004). If this fires the test fixture's online
	// runtime is missing or the rebind path regressed.
	if !agentRow.RuntimeID.Valid {
		t.Fatal("pythia_runtime agent has no runtime_id — online local runtime not bound")
	}

	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'agent'
		  AND resource_id = $2 AND hidden = TRUE
	`, pythiaSource, pgtypeToString(agentRow.ID)).Scan(&visCount); err != nil {
		t.Fatalf("visibility count: %v", err)
	}
	if visCount != 1 {
		t.Fatalf("visibility rows = %d, want 1", visCount)
	}
}

// TestInstallPythia_VisibilityIdempotent re-runs the install on the
// same workspace and asserts the agent + visibility counts stay at 1.
// Counts are workspace-scoped via JOIN so prior-run orphans on the
// shared dev database cannot inflate them.
func TestInstallPythia_VisibilityIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "pythia-vis-idem")
	cleanupVisibilityRows(t, workspaceID)

	for i := 0; i < 2; i++ {
		if err := testHandler.InstallPythia(ctx, userID, workspaceID); err != nil {
			t.Fatalf("InstallPythia (attempt %d): %v", i+1, err)
		}
	}

	var agentCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent
		WHERE workspace_id = $1 AND name = $2
	`, workspaceID, pythiaRuntimeAgentName).Scan(&agentCount); err != nil {
		t.Fatalf("agent count: %v", err)
	}
	if agentCount != 1 {
		t.Fatalf("agent count = %d, want 1 (idempotency broken)", agentCount)
	}

	// Workspace-scoped via JOIN — immune to orphan visibility rows
	// from prior test runs (the visibility table has no FK, so a
	// workspace cascade-delete does not sweep orphan rows).
	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility v
		JOIN agent a ON a.id = v.resource_id
		WHERE v.flag_key = $1 AND v.resource_type = 'agent'
		  AND a.workspace_id = $2
	`, pythiaSource, workspaceID).Scan(&visCount); err != nil {
		t.Fatalf("visibility count: %v", err)
	}
	if visCount != 1 {
		t.Fatalf("visibility rows = %d, want 1 (idempotency broken)", visCount)
	}
}
