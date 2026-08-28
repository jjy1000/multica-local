// Package mythos — reaper_test.go (0.5.87).
//
// DB-less unit tests for the stalled-run reaper (swarm orchestrator
// port). The stall CLOCKS themselves live in SQL
// (ListStalledMythosRunsForGC — verified by the DB-backed suite); the
// tests here pin the Go-side decision logic via the reapQ seam:
// which rows get the supervision_state merge, what the merged state
// preserves, and that one bad row never strands the batch.

package mythos

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeReapQ records every write so assertions can inspect the exact
// params the reaper produced.
type fakeReapQ struct {
	rows    []db.MythosRun
	status  map[pgtype.UUID]string // run id → status written
	states  map[pgtype.UUID][]byte // run id → supervision_state written
	failSet map[pgtype.UUID]error  // run id → SetMythosRunStatus error
	listErr error
}

func (f *fakeReapQ) ListStalledMythosRunsForGC(ctx context.Context, limit int32) ([]db.MythosRun, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if int32(len(f.rows)) > limit {
		return f.rows[:limit], nil
	}
	return f.rows, nil
}

func (f *fakeReapQ) SetMythosRunStatus(ctx context.Context, arg db.SetMythosRunStatusParams) (db.MythosRun, error) {
	if err := f.failSet[arg.ID]; err != nil {
		return db.MythosRun{}, err
	}
	if f.status == nil {
		f.status = make(map[pgtype.UUID]string)
	}
	f.status[arg.ID] = arg.Status
	return db.MythosRun{}, nil
}

func (f *fakeReapQ) SetMythosRunSupervisionState(ctx context.Context, arg db.SetMythosRunSupervisionStateParams) error {
	if f.states == nil {
		f.states = make(map[pgtype.UUID][]byte)
	}
	f.states[arg.ID] = arg.SupervisionState
	return nil
}

func reaperService(q *fakeReapQ) *Service {
	svc := &Service{}
	svc.reaper = reapFields{reapStop: make(chan struct{})}
	svc.reaper.reapQ = q
	return svc
}

func stalledRun(id pgtype.UUID, status, mode string, state []byte) db.MythosRun {
	return db.MythosRun{
		ID:               id,
		Status:           status,
		Mode:             mode,
		StartedAt:        pgtype.Timestamptz{Time: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), Valid: true},
		SupervisionState: state,
	}
}

// Reaping a stalled supervising run must fail the row AND merge
// phase='aborted' + abort_reason='stalled_reap' into the existing
// supervision_state — tick history (total_ticks, sub-task counters)
// survives so the supervise panel keeps its timeline.
func TestReapStalledRunsMergesAbortReasonIntoSupervisingState(t *testing.T) {
	existing := SupervisionState{
		Phase:         PhaseSupervising,
		StartedAt:     time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		LastCheckAt:   time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC),
		TotalTicks:    340,
		SubTasksTotal: 4,
		SubTasksDone:  1,
	}
	raw, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	fake := &fakeReapQ{rows: []db.MythosRun{stalledRun(id, "supervising", "enhancer", raw)}}
	svc := reaperService(fake)

	reaped, err := svc.ReapStalledRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 1 {
		t.Fatalf("reaped = %d, want 1", reaped)
	}
	if got := fake.status[id]; got != "failed" {
		t.Fatalf("status = %q, want failed", got)
	}
	if len(fake.states[id]) == 0 {
		t.Fatal("supervision_state not written")
	}
	var merged SupervisionState
	if err := json.Unmarshal(fake.states[id], &merged); err != nil {
		t.Fatal(err)
	}
	if merged.Phase != PhaseAborted {
		t.Errorf("phase = %q, want aborted", merged.Phase)
	}
	if merged.AbortReason != StalledReapAbortReason {
		t.Errorf("abort_reason = %q, want %q", merged.AbortReason, StalledReapAbortReason)
	}
	// History preservation — the whole point of the merge path.
	if merged.TotalTicks != 340 || merged.SubTasksTotal != 4 || merged.SubTasksDone != 1 {
		t.Errorf("tick history lost: %+v", merged)
	}
	if merged.StartedAt != existing.StartedAt {
		t.Errorf("started_at = %v, want %v", merged.StartedAt, existing.StartedAt)
	}
	// LastCheckAt must NOT be bumped by the reaper — no tick happened.
	if !merged.LastCheckAt.Equal(existing.LastCheckAt) {
		t.Errorf("last_check_at = %v, want unchanged %v", merged.LastCheckAt, existing.LastCheckAt)
	}
}

// A stalled 'running' row (sole pipeline orphaned by a restart) is
// failed by status flip alone — no supervision_state blob is invented
// post-hoc.
func TestReapStalledRunsFailsRunningRowWithoutStateWrite(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	fake := &fakeReapQ{rows: []db.MythosRun{stalledRun(id, "running", "sole", nil)}}
	svc := reaperService(fake)

	reaped, err := svc.ReapStalledRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 1 {
		t.Fatalf("reaped = %d, want 1", reaped)
	}
	if got := fake.status[id]; got != "failed" {
		t.Fatalf("status = %q, want failed", got)
	}
	if len(fake.states) != 0 {
		t.Errorf("supervision_state written for a running row: %v", fake.states)
	}
}

// A supervising row with a corrupt state blob still gets reaped; the
// blob is replaced with a minimal honest aborted state carrying the
// run's started_at.
func TestReapStalledRunsReplacesCorruptState(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{3}, Valid: true}
	started := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	run := stalledRun(id, "supervising", "enhancer", []byte("{not json"))
	run.StartedAt = pgtype.Timestamptz{Time: started, Valid: true}
	fake := &fakeReapQ{rows: []db.MythosRun{run}}
	svc := reaperService(fake)

	reaped, err := svc.ReapStalledRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 1 {
		t.Fatalf("reaped = %d, want 1", reaped)
	}
	var merged SupervisionState
	if err := json.Unmarshal(fake.states[id], &merged); err != nil {
		t.Fatal(err)
	}
	if merged.Phase != PhaseAborted || merged.AbortReason != StalledReapAbortReason {
		t.Errorf("merged = %+v, want aborted/%s", merged, StalledReapAbortReason)
	}
	if merged.StartedAt.UTC() != started {
		t.Errorf("started_at = %v, want %v (taken from the row)", merged.StartedAt, started)
	}
}

// One row's status write failing must not strand the rest of the
// batch — the swarm GC's per-row best-effort contract.
func TestReapStalledRunsContinuesPastPerRowFailure(t *testing.T) {
	bad := pgtype.UUID{Bytes: [16]byte{4}, Valid: true}
	good := pgtype.UUID{Bytes: [16]byte{5}, Valid: true}
	fake := &fakeReapQ{
		rows:    []db.MythosRun{stalledRun(bad, "running", "sole", nil), stalledRun(good, "running", "sole", nil)},
		failSet: map[pgtype.UUID]error{bad: errors.New("db down")},
	}
	svc := reaperService(fake)

	reaped, err := svc.ReapStalledRuns(context.Background())
	if err != nil {
		t.Fatalf("batch error = %v, want nil (per-row best-effort)", err)
	}
	if reaped != 1 {
		t.Fatalf("reaped = %d, want 1", reaped)
	}
	if fake.status[bad] != "" {
		t.Error("failed row should have no status write")
	}
	if fake.status[good] != "failed" {
		t.Error("healthy row must still be reaped")
	}
}

// A list failure surfaces as an error with zero reaped, and an empty
// batch is a clean no-op.
func TestReapStalledRunsListErrorAndEmptyBatch(t *testing.T) {
	svc := reaperService(&fakeReapQ{listErr: errors.New("db down")})
	if reaped, err := svc.ReapStalledRuns(context.Background()); err == nil || reaped != 0 {
		t.Fatalf("reaped=%d err=%v, want 0/list error", reaped, err)
	}

	svc = reaperService(&fakeReapQ{})
	if reaped, err := svc.ReapStalledRuns(context.Background()); err != nil || reaped != 0 {
		t.Fatalf("reaped=%d err=%v, want clean no-op", reaped, err)
	}
}

// Stop must be safe on a Service that never started the reaper
// (nil channel) and on double shutdown.
func TestStopReaperNilAndDoubleSafe(t *testing.T) {
	svc := &Service{} // bypasses NewService — nil reapStop
	svc.Stop()        // must not panic

	svc = reaperService(&fakeReapQ{})
	svc.Stop()
	svc.Stop() // sync.Once guards the double close
}
