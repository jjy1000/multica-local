package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestIsDuplicatePendingTaskErr locks the driver-code detection (upstream
// b8d03bc3f, MUL-7326): only a 23505 on a pending-task index name is a benign
// duplicate-pending-task race. A different unique constraint (e.g. the agent
// name) or a plain error must not be mistaken for it, and the sentinel must
// remain detectable via errors.Is by callers. This runs without a database.
func TestIsDuplicatePendingTaskErr(t *testing.T) {
	for _, indexName := range []string{
		"idx_one_pending_task_per_issue_agent",
		"idx_one_pending_task_per_issue_agent_v2",
		"idx_one_pending_task_per_issue_agent_thread",
	} {
		dup := &pgconn.PgError{Code: "23505", ConstraintName: indexName}
		if !isDuplicatePendingTaskErr(dup) {
			t.Errorf("expected pending-task index %q to be recognized", indexName)
		}
	}
	if isDuplicatePendingTaskErr(&pgconn.PgError{Code: "23505", ConstraintName: "agent_workspace_name_unique"}) {
		t.Fatal("a different unique constraint must not be treated as a duplicate pending task")
	}
	if isDuplicatePendingTaskErr(&pgconn.PgError{Code: "40001", ConstraintName: "idx_one_pending_task_per_issue_agent"}) {
		t.Fatal("a non-unique-violation SQLSTATE must not be treated as a duplicate pending task")
	}
	if isDuplicatePendingTaskErr(errors.New("boom")) {
		t.Fatal("a non-pg error must not be treated as a duplicate pending task")
	}

	wrapped := fmt.Errorf("%w: duplicate pending task", ErrDuplicatePendingTask)
	if !errors.Is(wrapped, ErrDuplicatePendingTask) {
		t.Fatal("the wrapped sentinel must stay detectable via errors.Is")
	}
}

// TestEnqueueTaskForIssueCoalescesDuplicatePendingTask is the service-level
// regression for the issue-assignee enqueue path (upstream b8d03bc3f,
// MUL-7326). EnqueueTaskForIssue routes through enqueueIssueTask, a write
// point of the idx_one_pending_task_per_issue_agent unique index. A
// concurrent duplicate on this path used to surface as a raw create-task
// error (HTTP 500 with the leaked constraint name); it must now return the
// typed ErrDuplicatePendingTask sentinel and leave exactly one pending task.
func TestEnqueueTaskForIssueCoalescesDuplicatePendingTask(t *testing.T) {
	pool := newDuplicatePendingTaskPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, userID, agentID, issueID := seedDuplicatePendingTaskFixture(t, pool)

	issueStruct := db.Issue{
		ID:           util.MustParseUUID(issueID),
		AssigneeID:   util.MustParseUUID(agentID),
		Priority:     "medium",
		CreatorType:  "member",
		CreatorID:    util.MustParseUUID(userID),
		WorkspaceID:  util.MustParseUUID(workspaceID),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}

	// First assignee enqueue creates the pending task.
	if _, err := svc.EnqueueTaskForIssue(ctx, issueStruct); err != nil {
		t.Fatalf("first EnqueueTaskForIssue: %v", err)
	}

	// Second enqueue for the same (issue, agent) collides on the unique index.
	_, err := svc.EnqueueTaskForIssue(ctx, issueStruct)
	if !errors.Is(err, ErrDuplicatePendingTask) {
		t.Fatalf("second EnqueueTaskForIssue: err = %v, want ErrDuplicatePendingTask", err)
	}
	// The returned error must NOT carry the raw Postgres constraint name or
	// SQLSTATE — those used to leak into upper-layer warning logs.
	for _, leak := range []string{"idx_one_pending_task_per_issue_agent", "23505", "SQLSTATE", "duplicate key"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("duplicate error leaked %q: %v", leak, err)
		}
	}

	assertExactlyOnePendingTask(t, pool, issueID, agentID)
}

// TestEnqueueTaskForMentionCoalescesDuplicatePendingTask pins the same typed
// sentinel on the mention/squad-leader enqueue path (enqueueMentionTask). The
// fork never ported upstream #5958's mention-side normalization, so both
// write points land together here (upstream b8d03bc3f).
func TestEnqueueTaskForMentionCoalescesDuplicatePendingTask(t *testing.T) {
	pool := newDuplicatePendingTaskPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, userID, agentID, issueID := seedDuplicatePendingTaskFixture(t, pool)

	issueStruct := db.Issue{
		ID:           util.MustParseUUID(issueID),
		AssigneeID:   util.MustParseUUID(agentID),
		Priority:     "medium",
		CreatorType:  "member",
		CreatorID:    util.MustParseUUID(userID),
		WorkspaceID:  util.MustParseUUID(workspaceID),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}

	// First mention creates the pending task.
	if _, err := svc.EnqueueTaskForMention(ctx, issueStruct, util.MustParseUUID(agentID), pgtype.UUID{}); err != nil {
		t.Fatalf("first EnqueueTaskForMention: %v", err)
	}

	// Second mention for the same (issue, agent) collides on the unique index.
	_, err := svc.EnqueueTaskForMention(ctx, issueStruct, util.MustParseUUID(agentID), pgtype.UUID{})
	if !errors.Is(err, ErrDuplicatePendingTask) {
		t.Fatalf("second EnqueueTaskForMention: err = %v, want ErrDuplicatePendingTask", err)
	}
	for _, leak := range []string{"idx_one_pending_task_per_issue_agent", "23505", "SQLSTATE", "duplicate key"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("duplicate error leaked %q: %v", leak, err)
		}
	}

	assertExactlyOnePendingTask(t, pool, issueID, agentID)
}

// assertExactlyOnePendingTask counts queued/dispatched tasks for the pair.
func assertExactlyOnePendingTask(t *testing.T, pool *pgxpool.Pool, issueID, agentID string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status IN ('queued','dispatched')`,
		issueID, agentID).Scan(&n); err != nil {
		t.Fatalf("count pending tasks: %v", err)
	}
	if n != 1 {
		t.Fatalf("pending task count = %d, want exactly 1", n)
	}
}

// newDuplicatePendingTaskPool mirrors newAgentCreateResolvePool
// (resolve_originator_test.go): same DATABASE_URL resolver the other service
// tests use, skipping (not failing) when the database is unavailable.
func newDuplicatePendingTaskPool(t *testing.T) *pgxpool.Pool {
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

// seedDuplicatePendingTaskFixture seeds a minimal (workspace, user, agent
// with runtime, issue-assigned-to-agent) chain and returns their IDs as
// strings. Stand-in for the upstream seedAttributionFixture helper that the
// fork never ported. The unique index under test is
// idx_one_pending_task_per_issue_agent ((issue_id, agent_id) WHERE status IN
// ('queued','dispatched'), migration 037).
func seedDuplicatePendingTaskFixture(t *testing.T, pool *pgxpool.Pool) (workspaceID, userID, agentID, issueID string) {
	t.Helper()
	ctx := context.Background()

	randBytes := make([]byte, 8)
	_, _ = rand.Read(randBytes)
	suffix := hex.EncodeToString(randBytes)

	var uid, wsID, runtimeID, agID, issID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "dup-pending user "+suffix, "dup-pending-"+suffix+"@multica.local",
	).Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "dup-pending ws", "dup-pending-"+suffix, "duplicate pending task test workspace", "DPT",
	).Scan(&wsID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, wsID, uid); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status)
		VALUES ($1, 'dup-pending runtime', 'cloud', 'multica-cloud', 'offline') RETURNING id
	`, wsID,
	).Scan(&runtimeID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, visibility, owner_id)
		VALUES ($1, 'dup-pending agent', 'cloud', $2, 'private', $3) RETURNING id
	`, wsID, runtimeID, uid,
	).Scan(&agID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Issue assigned to the agent — the row the enqueue funnel targets.
	// Issue numbers are unique per workspace and this workspace is fresh
	// (random slug suffix), so number 1 cannot collide.
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, assignee_type, assignee_id)
		VALUES ($1, 'dup-pending fixture', 'in_progress', 'medium', $2, 'member', 1, 0, 'agent', $3)
		RETURNING id
	`, wsID, uid, agID,
	).Scan(&issID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issID)
		pool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issID)
		pool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agID)
		pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		pool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1`, wsID)
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
		pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, uid)
	})

	return util.UUIDToString(wsID), util.UUIDToString(uid), util.UUIDToString(agID), util.UUIDToString(issID)
}
