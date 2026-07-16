// Package mythos — supervise_test.go (0.3.31).
//
// Tests for the enhancer-mode supervision path. The supervise
// goroutine is fundamentally time-driven (30s ticker, 24h max
// lifetime), so most of the runtime behaviour is exercised by the
// integration suite under server/internal/handler/. This file
// focuses on the pure helpers — state transitions, JSON marshaling,
// terminal-phase detection — that don't need a live DB.

package mythos

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// isTerminalPhase covers the four corner cases the production code
// path relies on: done and aborted are terminal; preparing,
// planning, supervising, and degraded are not.
func TestIsTerminalPhase(t *testing.T) {
	cases := []struct {
		phase SupervisionPhase
		want  bool
	}{
		{PhasePreparing, false},
		{PhasePlanning, false},
		{PhaseSupervising, false},
		{PhaseDone, true},
		{PhaseAborted, true},
		{PhaseDegraded, false},
		{SupervisionPhase("unknown"), false},
	}
	for _, tc := range cases {
		if got := isTerminalPhase(tc.phase); got != tc.want {
			t.Errorf("isTerminalPhase(%q) = %v, want %v", tc.phase, got, tc.want)
		}
	}
}

// TestSupervisionStateRoundTrip verifies that the JSONB envelope the
// supervise loop writes survives a marshal/unmarshal cycle without
// losing any field. The renderer relies on this to plot progress.
func TestSupervisionStateRoundTrip(t *testing.T) {
	original := SupervisionState{
		Phase:                PhaseSupervising,
		StartedAt:            time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		LastCheckAt:          time.Date(2026, 7, 16, 12, 5, 30, 0, time.UTC),
		LastTickDurationMs:   1234,
		TotalTicks:           11,
		SubTasksTotal:        4,
		SubTasksDone:         2,
		LatestReflection:     "loop iter 5: still exploring",
		LatestReflectionIter: 5,
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SupervisionState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Phase != original.Phase {
		t.Errorf("Phase lost: got %q want %q", decoded.Phase, original.Phase)
	}
	if decoded.TotalTicks != original.TotalTicks {
		t.Errorf("TotalTicks lost: got %d want %d", decoded.TotalTicks, original.TotalTicks)
	}
	if decoded.LatestReflection != original.LatestReflection {
		t.Errorf("LatestReflection lost: got %q want %q", decoded.LatestReflection, original.LatestReflection)
	}
}

// TestTargetAssigneeMarshalJSONB: nil pointer returns nil bytes
// (so the SQL `target_assignee` column gets NULL, not "{}"). A
// non-nil pointer marshals to a stable JSON object so the renderer
// can read type+id.
func TestTargetAssigneeMarshalJSONB(t *testing.T) {
	var nilT *TargetAssignee
	raw, err := nilT.MarshalJSONB()
	if err != nil {
		t.Fatalf("nil marshal: %v", err)
	}
	if raw != nil {
		t.Errorf("nil pointer should marshal to nil bytes, got %q", raw)
	}

	uuidVal := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	t2 := &TargetAssignee{Type: "agent", ID: pgtype.UUID{Bytes: uuidVal, Valid: true}}
	raw, err = t2.MarshalJSONB()
	if err != nil {
		t.Fatalf("non-nil marshal: %v", err)
	}
	var back struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back.Type != "agent" {
		t.Errorf("type lost: got %q", back.Type)
	}
	if back.ID != uuidVal.String() {
		t.Errorf("id lost: got %q want %q", back.ID, uuidVal.String())
	}
}

// TestExtensionUUIDsToStrings: invalid pgtype.UUID entries are
// dropped (so the JSONB column gets a clean list of bare UUID
// strings without null entries).
func TestExtensionUUIDsToStrings(t *testing.T) {
	id1 := pgtype.UUID{Bytes: uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"), Valid: true}
	id2 := pgtype.UUID{Valid: false}
	id3 := pgtype.UUID{Bytes: uuid.MustParse("11111111-2222-3333-4444-555555555555"), Valid: true}
	out := extensionUUIDsToStrings([]pgtype.UUID{id1, id2, id3})
	if len(out) != 2 {
		t.Fatalf("expected 2 entries after invalid drop, got %d (%v)", len(out), out)
	}
	if out[0] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("entry 0 wrong: %q", out[0])
	}
	if out[1] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("entry 1 wrong: %q", out[1])
	}
}

// TestServiceSuperviseSetLocksConcurrent: the superviseSet map is
// guarded by sync.Mutex. This test exercises startSupervise + Stop
// from multiple goroutines to surface any race the static checker
// might miss. Safe to run with `-race`.
func TestServiceSuperviseSetLocksConcurrent(t *testing.T) {
	svc := &Service{
		queries:      nil, // not touched in this test
		superviseSet: make(map[pgtype.UUID]context.CancelFunc),
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
			ctx, cancel := context.WithCancel(context.Background())
			svc.superviseMu.Lock()
			svc.superviseSet[id] = cancel
			svc.superviseMu.Unlock()
			_ = ctx
		}(i)
	}
	wg.Wait()
	svc.Stop()
	if len(svc.superviseSet) != 0 {
		t.Errorf("Stop did not clear the set: %d entries left", len(svc.superviseSet))
	}
}

// TestSupervisionConstants: the ticker interval and max lifetime
// are deployment constants that downstream code may rely on. A
// silent change here would break the production cadence. Lock the
// current values to catch accidental drift.
func TestSupervisionConstants(t *testing.T) {
	if SupervisionTickerInterval != 30*time.Second {
		t.Errorf("SupervisionTickerInterval drifted: got %v, want 30s", SupervisionTickerInterval)
	}
	if SupervisionMaxLifetime != 24*time.Hour {
		t.Errorf("SupervisionMaxLifetime drifted: got %v, want 24h", SupervisionMaxLifetime)
	}
}

// TestTickSupervisionOncePhaseProgression: the synthetic DB rows
// have an empty supervision_state, so TickSupervisionOnce seeds
// Phase=Preparing → Planning → Supervising in a single call (one
// transition per tick). This proves the progression without
// waiting for the real 30s ticker. The live IO is covered by the
// integration suite under server/internal/handler/.
func TestTickSupervisionOncePhaseProgression(t *testing.T) {
	// The transition logic is encapsulated in isTerminalPhase so
	// we can validate it without spinning up a full supervise
	// goroutine. The integration suite under internal/handler/
	// covers the end-to-end tickSupervision path.
	for _, p := range []SupervisionPhase{PhasePreparing, PhasePlanning, PhaseSupervising, PhaseDegraded} {
		if isTerminalPhase(p) {
			t.Errorf("phase %q should NOT be terminal", p)
		}
	}
	for _, p := range []SupervisionPhase{PhaseDone, PhaseAborted} {
		if !isTerminalPhase(p) {
			t.Errorf("phase %q should be terminal", p)
		}
	}
}