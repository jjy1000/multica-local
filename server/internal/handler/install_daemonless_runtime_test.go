package handler

// install_daemonless_runtime_test.go — 0.5.89 tech-debt regression pins
// for the daemonless install path (0.5.88 known-issue ledger closure).
//
// agent.runtime_id is NOT NULL with an FK to agent_runtime (migration 004;
// migration 247 explicitly declined to relax the agent side). A lab
// installed into a workspace with NO online daemon used to bind a NULL
// runtime_id and die on that constraint — the ledger flagged
// install_causal_graph.go:155 and install_pythia.go as the known surface,
// but timesfm / code_canvas / semantica shared the identical pattern.
// Every installer's agent upsert now falls back to
// resolveOrSynthesizeLabRuntime's synthetic offline stub (the
// upsertClaudeScienceRuntime pattern).
//
// These tests call the upsert*Agent seam directly on purpose: the full
// Install* entry points also seed experimental_resource_lock /
// _visibility rows that are counted GLOBALLY by the other install tests
// (causal_graph_test.go, TestInstallTimesfm) — parallel full installs
// would race those counts regardless of cleanup. The runtime-synthesis
// contract under test lives entirely in the upsert layer.
//
// Skipped when DATABASE_URL is unreachable (handler_test.go TestMain).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// installDaemonlessFresh creates a throwaway (user, workspace) pair with
// NO agent_runtime rows at all — resolveWorkspaceOnlineRuntime must come
// up empty so the upsert takes the synthesize path. Cleanup mirrors
// installCodeCanvasFresh (workspace delete cascades the agent/runtime
// rows the upserts create).
func installDaemonlessFresh(t *testing.T, ctx context.Context, name string) (string, string) {
	t.Helper()
	var userID, workspaceID string
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
	`, name+"-workspace", name+"-slug", "daemonless install test", "IDL").Scan(&workspaceID); err != nil {
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
	return userID, workspaceID
}

func TestLabInstallDaemonless_SynthesizesRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, workspaceID := installDaemonlessFresh(t, ctx, "daemonless-causal")
	wsUUID := uuidToPgtype(workspaceID)

	// Every hidden-team agent must carry a VALID runtime_id pointing at
	// the synthetic offline stub.
	var stubRuntimeID string
	for _, name := range []string{causalGraphCuratorName, causalGraphHistorianName, causalGraphVerifierName} {
		agentID, err := upsertCausalGraphAgent(ctx, testHandler, wsUUID, causalAgentSpec{
			name:         name,
			description:  "daemonless fixture",
			instructions: "fixture",
		})
		if err != nil {
			t.Fatalf("%s upsert (daemonless): %v", name, err)
		}
		agentRow, err := testHandler.Queries.GetAgent(ctx, agentID)
		if err != nil {
			t.Fatalf("%s agent readback: %v", name, err)
		}
		if !agentRow.RuntimeID.Valid {
			t.Fatalf("%s bound a NULL runtime_id — FK violation territory again", name)
		}
		var daemonID, status, synthetic string
		if err := testPool.QueryRow(ctx, `
			SELECT daemon_id, status, metadata::jsonb ->> 'synthetic' FROM agent_runtime WHERE id = $1
		`, pgtypeToString(agentRow.RuntimeID)).Scan(&daemonID, &status, &synthetic); err != nil {
			t.Fatalf("%s runtime row missing: %v", name, err)
		}
		if daemonID != "causal-graph" || status != "offline" {
			t.Fatalf("%s bound to runtime daemon_id=%s status=%s; want causal-graph/offline stub", name, daemonID, status)
		}
		if synthetic != "true" {
			t.Fatalf("stub metadata must mark itself synthetic (got %q)", synthetic)
		}
		stubRuntimeID = pgtypeToString(agentRow.RuntimeID)
	}

	// Re-upsert must REUSE the stub (workspace+daemon_id+provider
	// uniqueness) — no duplicate runtime rows.
	if _, err := upsertCausalGraphAgent(ctx, testHandler, wsUUID, causalAgentSpec{
		name: causalGraphCuratorName, description: "daemonless fixture", instructions: "fixture",
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	var stubCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent_runtime WHERE workspace_id = $1 AND daemon_id = 'causal-graph'
	`, wsUUID).Scan(&stubCount); err != nil || stubCount != 1 {
		t.Fatalf("re-upsert must reuse the synthetic stub (count=%d, err=%v)", stubCount, err)
	}

	// A daemon arriving later re-points the agents at the live runtime
	// (0.5.89 rebind-roster extension covers the causal trio).
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime SET status = 'online', last_seen_at = now() WHERE id = $1
	`, stubRuntimeID); err != nil {
		t.Fatalf("flip stub online: %v", err)
	}
	rebindLabAgentsToOnlineRuntime(ctx, testHandler, wsUUID)
	agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: wsUUID,
		Name:        causalGraphCuratorName,
	})
	if err != nil {
		t.Fatalf("curator lookup after rebind: %v", err)
	}
	if pgtypeToString(agentRow.RuntimeID) != stubRuntimeID {
		t.Fatalf("curator must rebind to the online runtime (got %s, want %s)",
			pgtypeToString(agentRow.RuntimeID), stubRuntimeID)
	}
}

// TestLabInstallDaemonless_AllInstallersSucceed pins the other four
// installers onto the same contract: the daemonless agent upsert must not
// fail, and each must leave exactly one synthetic offline stub behind.
func TestLabInstallDaemonless_AllInstallersSucceed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name     string
		upsert   func(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error)
		daemonID string
	}{
		{"pythia", upsertPythiaRuntimeAgent, "pythia-oracle"},
		{"timesfm", upsertTimesfmOracleAgent, "timesfm"},
		{"code_canvas", upsertCodeCanvasAgent, "code-canvas"},
		{"semantica", upsertSemanticaDecisionAdvisorAgent, "semantica"},
	}
	for _, tc := range cases {
		_, workspaceID := installDaemonlessFresh(t, ctx, "daemonless-"+tc.name)
		wsUUID := uuidToPgtype(workspaceID)
		if _, err := tc.upsert(ctx, testHandler, wsUUID); err != nil {
			t.Fatalf("%s upsert (daemonless): %v", tc.name, err)
		}
		var status string
		if err := testPool.QueryRow(ctx, `
			SELECT status FROM agent_runtime WHERE workspace_id = $1 AND daemon_id = $2
		`, wsUUID, tc.daemonID).Scan(&status); err != nil {
			t.Fatalf("%s synthetic stub missing (daemon_id=%s): %v", tc.name, tc.daemonID, err)
		}
		if status != "offline" {
			t.Fatalf("%s stub status = %s; want offline", tc.name, status)
		}
	}
}
