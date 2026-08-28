package handler

// mythos_stalled_run_gc_test.go (0.5.87) — DB-backed pin for
// ListStalledMythosRunsForGC, the stalled-run reap clock behind the
// mythos reaper (swarm orchestrator port). The Go-side decision logic
// is pinned DB-less in internal/service/mythos/reaper_test.go; this
// file pins the SQL semantics: which rows the two stall clocks
// condemn, and — just as important — which healthy rows they must
// never touch (a resumed enhancer run refreshes its heartbeat within
// 30s of boot, so a 6h sweep can never reap a live one).

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// marshalStallState encodes a supervision_state JSONB the way the
// supervise loop writes it (json.Marshal of the Go struct — RFC3339
// timestamps), so the SQL cast `(supervision_state->>'last_check_at')
// ::timestamptz` sees the exact production shape.
func marshalStallState(t *testing.T, state any) []byte {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return raw
}

func TestListStalledMythosRunsForGCSelectors(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("requires DB")
	}
	ctx := context.Background()
	q := testHandler.Queries

	wsID, err := uuid.Parse(testWorkspaceID)
	if err != nil {
		t.Fatalf("parse test workspace id: %v", err)
	}
	userID, err := uuid.Parse(testUserID)
	if err != nil {
		t.Fatalf("parse test user id: %v", err)
	}
	ws := pgtype.UUID{Bytes: wsID, Valid: true}
	user := pgtype.UUID{Bytes: userID, Valid: true}

	insertRun := func(t *testing.T, status, mode string, startedAt time.Time, state any) pgtype.UUID {
		t.Helper()
		run, err := q.CreateMythosRun(ctx, db.CreateMythosRunParams{
			WorkspaceID:   ws,
			CreatorUserID: user,
			Problem:       fmt.Sprintf("gc-selector-pin %s/%s", status, mode),
		})
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		if state == nil {
			// supervision_state is NOT NULL; an empty object exercises
			// the COALESCE-to-started_at branch of the stall clock.
			state = `{}`
		} else {
			state = marshalStallState(t, state)
		}
		if _, err := testPool.Exec(ctx,
			`UPDATE mythos_run SET status = $2, mode = $3, started_at = $4, supervision_state = $5::jsonb WHERE id = $1`,
			run.ID, status, mode, startedAt, state,
		); err != nil {
			t.Fatalf("shape run %s: %v", run.ID.String(), err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(ctx, `DELETE FROM mythos_run WHERE id = $1`, run.ID)
		})
		return run.ID
	}

	now := time.Now()
	staleHeartbeat := map[string]any{"phase": "supervising", "last_check_at": now.Add(-3 * time.Hour).UTC().Format(time.RFC3339Nano)}
	freshHeartbeat := map[string]any{"phase": "supervising", "last_check_at": now.Add(-1 * time.Minute).UTC().Format(time.RFC3339Nano)}

	condemned := map[string]bool{}
	condemned[insertRun(t, "running", "sole", now.Add(-48*time.Hour), nil).String()] = true
	condemned[insertRun(t, "supervising", "enhancer", now.Add(-72*time.Hour), staleHeartbeat).String()] = true

	// Spared margins sit safely INSIDE the clocks (not on the 1h/24h
	// boundaries): the SQL now() runs a few ms after the Go `now`, so a
	// row inserted at exactly -1h reads as -1h-ε in the query.
	spared := map[string]bool{}
	spared[insertRun(t, "running", "sole", now.Add(-55*time.Minute), nil).String()] = true
	spared[insertRun(t, "supervising", "enhancer", now.Add(-72*time.Hour), freshHeartbeat).String()] = true
	// NULL-state COALESCEs to started_at — a fresh row must survive.
	spared[insertRun(t, "supervising", "enhancer", now.Add(-55*time.Minute), nil).String()] = true
	spared[insertRun(t, "completed", "enhancer", now.Add(-72*time.Hour), staleHeartbeat).String()] = true

	rows, err := q.ListStalledMythosRunsForGC(ctx, 100)
	if err != nil {
		t.Fatalf("list stalled: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID.String()] = true
	}
	for id := range condemned {
		if !got[id] {
			t.Errorf("stalled run %s was not selected for reap", id)
		}
	}
	for id := range spared {
		if got[id] {
			t.Errorf("healthy run %s was selected — the reap clock would kill a live run", id)
		}
	}
}
