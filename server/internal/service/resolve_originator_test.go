package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestResolveOriginatorForIssueTask_AgentCreateIssueInheritsParentTask covers
// the MUL-4305 fix: an agent that creates an issue through the ordinary
// `issue create` path gets origin_type='agent_create' + origin_id=<acting
// task>. The issue creator is the agent, but the top-of-chain human lives on
// that acting task and must be inherited so downstream assignment /
// squad-leader runs (and the A2A mentions they emit) keep the originator.
//
// Fork port note: this test was new in upstream `86c3f3052` (MUL-4305 PR #5149).
// The fork did not port upstream's seedOriginatorFanout helper, so this file
// rolls its own minimal fanout seed (one workspace, one agent, one parent
// task) using the same DATABASE_URL resolver the other service tests use
// (task_claim_race_test.go's newTaskClaimRacePool).
func TestResolveOriginatorForIssueTask_AgentCreateIssueInheritsParentTask(t *testing.T) {
	pool := newAgentCreateResolvePool(t)

	userID, parentTaskID := seedAgentCreateOriginFanout(t, pool)

	svc := &TaskService{Queries: db.New(pool)}
	issue := db.Issue{
		CreatorType: "agent",
		OriginType:  pgtypeText("agent_create"),
		OriginID:    parentTaskID,
	}

	got := svc.resolveOriginatorForIssueTask(context.Background(), issue, pgtype.UUID{})
	if !got.Valid {
		t.Fatalf("expected agent_create issue to inherit originator, got invalid")
	}
	if got.Bytes != userID.Bytes {
		t.Errorf("originator = %s, want %s", util.UUIDToString(got), util.UUIDToString(userID))
	}
}

// TestOriginatorForIssueTask_MatchesResolverForAgentCreate pins the
// gate/enqueue consistency guarantee from MUL-4305: the exported
// OriginatorForIssueTask (used by the squad-leader access gate) must return
// the SAME human the unexported resolver persists on the task row. If these
// drift, an agent-created issue could be attributed correctly on the task row
// yet denied by a gate that computed a different (empty) originator.
//
// Fork port note: this is the load-bearing assertion that proves the
// exported OriginatorForIssueTask is genuinely the same code path as the
// internal resolver — the upstream wrapper is one line, but a regression
// (e.g. accidentally routing agent/system origins through a different
// branch) would silently break the consistency contract.
func TestOriginatorForIssueTask_MatchesResolverForAgentCreate(t *testing.T) {
	pool := newAgentCreateResolvePool(t)

	userID, parentTaskID := seedAgentCreateOriginFanout(t, pool)

	svc := &TaskService{Queries: db.New(pool)}
	issue := db.Issue{
		CreatorType: "agent",
		OriginType:  pgtypeText("agent_create"),
		OriginID:    parentTaskID,
	}

	gate := svc.OriginatorForIssueTask(context.Background(), issue, pgtype.UUID{})
	write := svc.resolveOriginatorForIssueTask(context.Background(), issue, pgtype.UUID{})
	if gate.Bytes != write.Bytes || gate.Valid != write.Valid {
		t.Fatalf("gate originator %s != write originator %s",
			util.UUIDToString(gate), util.UUIDToString(write))
	}
	if !gate.Valid || gate.Bytes != userID.Bytes {
		t.Errorf("gate originator = %s, want %s", util.UUIDToString(gate), util.UUIDToString(userID))
	}
}

// newAgentCreateResolvePool mirrors newTaskClaimRacePool (task_claim_race_test.go)
// but its own helper so the resolve_originator_test file is self-contained.
func newAgentCreateResolvePool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAgentCreateOriginFanout seeds a minimal agent + running task carrying a
// user as originator_user_id, returns (userID, parentTaskID). Stand-in for
// the upstream seedOriginatorFanout helper that the fork never ported.
func seedAgentCreateOriginFanout(t *testing.T, pool *pgxpool.Pool) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	randBytes := make([]byte, 8)
	_, _ = rand.Read(randBytes)
	suffix := hex.EncodeToString(randBytes)

	var userID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "MUL-4305 originator "+suffix, "mul-4305-"+suffix+"@multica.local",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Workspace owned by that user via the member table.
	var workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "MUL-4305 ws", "mul-4305-"+suffix, "MUL-4305 resolve_originator test workspace", "M43",
	).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// Runtime row required by the agent FK.
	var runtimeID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status)
		VALUES ($1, 'MUL-4305 runtime', 'cloud', 'multica-cloud', 'offline') RETURNING id
	`, workspaceID,
	).Scan(&runtimeID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	// Agent row in the seeded workspace (default cloud runtime, private visibility).
	var agentID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, visibility, owner_id)
		VALUES ($1, 'MUL-4305 agent', 'cloud', $2, 'private', $3) RETURNING id
	`, workspaceID, runtimeID, userID,
	).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Parent task carrying the human as originator_user_id.
	var parentTaskID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, originator_user_id)
		VALUES ($1, $2, 'running', 0, $3) RETURNING id
	`, agentID, runtimeID, userID,
	).Scan(&parentTaskID); err != nil {
		t.Fatalf("seed parent task: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, parentTaskID)
		pool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		pool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1`, workspaceID)
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})

	return userID, parentTaskID
}

func pgtypeText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}