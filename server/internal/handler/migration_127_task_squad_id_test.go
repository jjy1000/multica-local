package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestAgentTaskQueueSquadIDColumnReadsAndWrites — sqlc roundtrip for the
// squad_id column added by migration 127. Locks in:
//
//   - NULL is the zero/legacy state (pre-migration rows).
//   - Non-NULL UUID survives an INSERT + SELECT roundtrip through sqlc.
//   - UPDATE from NULL to a UUID is reflected on subsequent SELECT.
//   - The column has NO foreign-key constraint referencing squad(id).
//     This is the explicit non-FK design choice from migration 127
//     (avoids cross-table lock risk against squad archive / hard-delete).
func TestAgentTaskQueueSquadIDColumnReadsAndWrites(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// No FK on agent_task_queue.squad_id → squad(id). Confirms the migration
	// comment's design intent survived sqlc regen. A future migration that
	// re-adds the FK would re-introduce the cross-table lock risk.
	var fkCount int
	if err := testPool.QueryRow(ctx, `
SELECT COUNT(*)
FROM pg_constraint
WHERE conrelid = 'agent_task_queue'::regclass
  AND contype = 'f'
  AND pg_get_constraintdef(oid) ILIKE '%squad_id%'
`).Scan(&fkCount); err != nil {
		t.Fatalf("query pg_constraint: %v", err)
	}
	if fkCount != 0 {
		t.Errorf("expected 0 FK constraints referencing squad_id on agent_task_queue, got %d", fkCount)
	}

	// Resolve the seeded runtime + agent used by every handler test in this
	// package. Reusing them keeps t.Cleanup cheap (the test infra already
	// cleans up the runtime/agent per-test).
	var runtimeID, agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("get test agent/runtime: %v", err)
	}

	// INSERT a task with squad_id = NULL.
	var taskID1 pgtype.UUID
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, is_leader_task, squad_id)
VALUES ($1, $2, 'queued', 0, FALSE, NULL)
RETURNING id
`, agentID, runtimeID).Scan(&taskID1); err != nil {
		t.Fatalf("insert task with NULL squad_id: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID1) })

	var readNull pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT squad_id FROM agent_task_queue WHERE id = $1`, taskID1,
	).Scan(&readNull); err != nil {
		t.Fatalf("read back NULL squad_id: %v", err)
	}
	if readNull.Valid {
		t.Errorf("expected NULL squad_id on freshly-inserted row, got valid=%v bytes=%v", readNull.Valid, readNull.Bytes)
	}

	// Resolve a squad to use for the non-NULL roundtrip. The shared test
	// workspace may or may not have any pre-seeded squads — fall back to
	// creating a transient one via the seedSquadForBriefing helper (which
	// properly threads leader_id through the FK). We only assert that the
	// column carries the UUID across an INSERT + SELECT, so a throwaway
	// squad is fine.
	var leaderID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&leaderID); err != nil {
		t.Fatalf("get leader agent for transient squad: %v", err)
	}
	squad := seedSquadForBriefing(t, leaderID, "migration-127-squad-id-test", "")
	squadID := squad.ID

	// UPDATE NULL → squadID.
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET squad_id = $1 WHERE id = $2`,
		squadID, taskID1,
	); err != nil {
		t.Fatalf("update squad_id NULL → value: %v", err)
	}
	var readSet pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT squad_id FROM agent_task_queue WHERE id = $1`, taskID1,
	).Scan(&readSet); err != nil {
		t.Fatalf("read back updated squad_id: %v", err)
	}
	if !readSet.Valid || readSet != squadID {
		t.Errorf("squad_id update not round-tripped: got valid=%v value=%+v want %+v", readSet.Valid, readSet, squadID)
	}

	// INSERT a task with squad_id = non-NULL from the start.
	var taskID2 pgtype.UUID
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, is_leader_task, squad_id)
VALUES ($1, $2, 'queued', 0, TRUE, $3)
RETURNING id
`, agentID, runtimeID, squadID).Scan(&taskID2); err != nil {
		t.Fatalf("insert task with non-NULL squad_id: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID2) })

	var readDirect pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT squad_id FROM agent_task_queue WHERE id = $1`, taskID2,
	).Scan(&readDirect); err != nil {
		t.Fatalf("read back non-NULL squad_id: %v", err)
	}
	if !readDirect.Valid || readDirect != squadID {
		t.Errorf("squad_id INSERT not round-tripped: got valid=%v value=%+v want %+v", readDirect.Valid, readDirect, squadID)
	}
}