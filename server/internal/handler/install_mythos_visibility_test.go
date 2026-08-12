// Package handler — install_mythos_visibility_test.go (0.3.31 contract)
//
// Verifies the 0.3.31 visibility seeding in install_mythos.go:
// InstallMythos must create experimental_resource_visibility rows for
// every mythos_* agent (5 total) and the Mythos Swarm squad, so the
// regular agent / squad pickers hide them when the mythos_swarm flag
// is OFF. The seed path is upsertMythosVisibility, which uses
// InsertExperimentalResourceVisibility (ON CONFLICT DO NOTHING on the
// UNIQUE(flag_key, resource_type, resource_id) constraint), so a
// re-install on a populated workspace is a no-op for visibility.
//
// Why these tests exist: pre-0.3.31 the install path created only
// experimental_resource_lock rows. That kept the lock-driven picker
// filter happy (ListVisibleAgentsByWorkspace reads hidden=true from
// the lock table) but left filterLabsHiddenByDefault — the
// canonical, future-proof visibility source — silently empty for
// mythos. Any UI surface that switched from lock-driven to
// visibility-driven filtering (0.3.56 audit added the lab_managed
// DTO stamp driven by visibility-row existence) would re-leak the
// mythos agents and squad. The seeding landed with migration 157;
// these tests guard against a regression that drops the loop.
//
// These tests require a running PostgreSQL — they are skipped by
// handler_test.go's TestMain when DATABASE_URL is unreachable. The
// shared testHandler / testPool fixture is reused; the install path
// is purely a service-layer call, so no HTTP roundtrip is needed.
// The hermetic (user, workspace, member, online-runtime) fixture is
// installCodeCanvasFresh from install_code_canvas_test.go — the
// online runtime is harmless here (mythos provisions its own
// synthetic offline runtime) and lets mythos + pythia + claude_science
// tests share the same fresh-pair helper.
package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// mythosSquadName mirrors the hardcoded squad name in
// install_mythos.go::upsertMythosSquad. Declared locally so the
// test does not re-parse the install handler's internals.
const mythosSquadName = "Mythos Swarm"

// mythosAgentNames flattens the production roster (mythosAgents in
// install_mythos.go) into a []string for use with pgx's text[] codec.
func mythosAgentNames() []string {
	names := make([]string, 0, len(mythosAgents))
	for _, ag := range mythosAgents {
		names = append(names, ag.Name)
	}
	return names
}

// cleanupVisibilityRows registers a t.Cleanup that deletes every
// experimental_resource_visibility row whose resource_id resolves to
// an agent or squad in the given workspace. The visibility table has
// no FK to agent / squad (migration 150 declares resource_id as a
// bare UUID), so workspace deletion does NOT cascade — without this
// helper, each run orphans visibility rows on the shared test DB.
//
// Registered BEFORE install so the cleanup runs AFTER all workspace
// deletions in LIFO order — at cleanup time the agent / squad rows
// still exist and the IN-subquery matches.
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

// TestInstallMythos_SeedsVisibility walks the 0.3.31 contract: a
// fresh install must (a) create the 5 mythos_* agents, (b) create
// the Mythos Swarm squad, (c) seed one visibility row per roster
// agent linked to the right agent row, and (d) seed one squad
// visibility row linked to the Mythos Swarm squad. The rows must
// be hidden=TRUE — the row existence is what the lab_managed DTO
// stamp keys off, but hidden=TRUE is the legacy ListHiddenResourceIDs
// contract that the picker filter still honours.
func TestInstallMythos_SeedsVisibility(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "mythos-vis")
	cleanupVisibilityRows(t, workspaceID)

	if err := testHandler.InstallMythos(ctx, userID, workspaceID); err != nil {
		t.Fatalf("InstallMythos: %v", err)
	}

	wsUUID := uuidToPgtype(workspaceID)

	// (a)+(c) Every roster agent exists AND has exactly one
	// (flag_key='mythos_swarm', resource_type='agent', hidden=TRUE)
	// visibility row linked to it.
	for _, ag := range mythosAgents {
		agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: wsUUID,
			Name:        ag.Name,
		})
		if err != nil {
			t.Fatalf("agent %q not created: %v", ag.Name, err)
		}
		var vis int
		if err := testPool.QueryRow(ctx, `
			SELECT COUNT(*) FROM experimental_resource_visibility
			WHERE flag_key = $1 AND resource_type = 'agent'
			  AND resource_id = $2 AND hidden = TRUE
		`, mythosSource, pgtypeToString(agentRow.ID)).Scan(&vis); err != nil {
			t.Fatalf("visibility count for %s: %v", ag.Name, err)
		}
		if vis != 1 {
			t.Fatalf("visibility rows for %s = %d, want 1", ag.Name, vis)
		}
	}

	// (b)+(d) The Mythos Swarm squad exists AND has exactly one
	// (flag_key='mythos_swarm', resource_type='squad', hidden=TRUE)
	// visibility row linked to it.
	squadRow, err := testHandler.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
		WorkspaceID: wsUUID,
		Name:        mythosSquadName,
	})
	if err != nil {
		t.Fatalf("squad %q not created: %v", mythosSquadName, err)
	}
	var squadVis int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'squad'
		  AND resource_id = $2 AND hidden = TRUE
	`, mythosSource, pgtypeToString(squadRow.ID)).Scan(&squadVis); err != nil {
		t.Fatalf("squad visibility count: %v", err)
	}
	if squadVis != 1 {
		t.Fatalf("squad visibility rows = %d, want 1", squadVis)
	}
}

// TestInstallMythos_VisibilityIdempotent re-runs the install on the
// same workspace and asserts no visibility / agent / squad rows are
// duplicated. Counts are scoped to the test workspace via a JOIN to
// agent / squad, so orphan rows from prior test runs on the shared
// dev database cannot inflate the count (the visibility table has
// no FK, so a clean-up sweep is not equivalent to a cascade).
func TestInstallMythos_VisibilityIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "mythos-vis-idem")
	cleanupVisibilityRows(t, workspaceID)

	for i := 0; i < 2; i++ {
		if err := testHandler.InstallMythos(ctx, userID, workspaceID); err != nil {
			t.Fatalf("InstallMythos (attempt %d): %v", i+1, err)
		}
	}

	// Exactly one row per roster agent (no duplicate upserts).
	var agentCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent
		WHERE workspace_id = $1 AND name = ANY($2::text[])
	`, workspaceID, mythosAgentNames()).Scan(&agentCount); err != nil {
		t.Fatalf("agent count: %v", err)
	}
	if agentCount != len(mythosAgents) {
		t.Fatalf("agent count = %d, want %d (idempotency broken)",
			agentCount, len(mythosAgents))
	}

	// Exactly one Mythos Swarm squad.
	var squadCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM squad
		WHERE workspace_id = $1 AND name = $2
	`, workspaceID, mythosSquadName).Scan(&squadCount); err != nil {
		t.Fatalf("squad count: %v", err)
	}
	if squadCount != 1 {
		t.Fatalf("squad count = %d, want 1 (idempotency broken)", squadCount)
	}

	// Exactly one visibility row per agent — workspace-scoped via JOIN
	// so prior-run orphans do not pollute the count.
	var agentVis int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility v
		JOIN agent a ON a.id = v.resource_id
		WHERE v.flag_key = $1 AND v.resource_type = 'agent'
		  AND a.workspace_id = $2
	`, mythosSource, workspaceID).Scan(&agentVis); err != nil {
		t.Fatalf("agent visibility count: %v", err)
	}
	if agentVis != len(mythosAgents) {
		t.Fatalf("agent visibility rows = %d, want %d (idempotency broken)",
			agentVis, len(mythosAgents))
	}

	// Exactly one squad visibility row, workspace-scoped.
	var squadVis int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility v
		JOIN squad s ON s.id = v.resource_id
		WHERE v.flag_key = $1 AND v.resource_type = 'squad'
		  AND s.workspace_id = $2
	`, mythosSource, workspaceID).Scan(&squadVis); err != nil {
		t.Fatalf("squad visibility count: %v", err)
	}
	if squadVis != 1 {
		t.Fatalf("squad visibility rows = %d, want 1 (idempotency broken)", squadVis)
	}
}
