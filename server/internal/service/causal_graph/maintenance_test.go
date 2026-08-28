// Package causalgraph — maintenance ticker lifecycle tests (0.5.83
// WL3 S2). DB-less: a nil pool makes sweeps no-ops, so the Start/Stop/
// Run contract is pinned without DATABASE_URL, mirroring
// internal/experimental/semantica_gc_test.go (the pattern this ticker
// mirrors).
package causalgraph

import (
	"testing"
	"time"
)

// TestCausalMaintenance_RunSweepsBeforeExit catches the
// Run()-loop-exits-before-ticker-fires regression class
// (RuntimeGC pre-0.5.25 precedent, SemanticaGC mirror).
func TestCausalMaintenance_RunSweepsBeforeExit(t *testing.T) {
	m := NewCausalMaintenance(nil, CausalMaintenanceConfig{Interval: 20 * time.Millisecond})
	defer m.Stop()
	m.Start()

	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.SweepCount() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected ≥2 sweep invocations within 80ms (Interval=20ms), got %d", m.SweepCount())
}

// TestCausalMaintenance_StartStopIdempotent pins the lifecycle
// contract: double Start is a no-op, double Stop is a no-op.
func TestCausalMaintenance_StartStopIdempotent(t *testing.T) {
	m := NewCausalMaintenance(nil, CausalMaintenanceConfig{})
	m.Start()
	m.Start() // no-op
	m.Stop()
	m.Stop() // no-op
}

// TestCausalMaintenance_StopBeforeStartIsSafe pins that Stop without
// a prior Start does not panic (the shutdown chain calls Stop
// unconditionally).
func TestCausalMaintenance_StopBeforeStartIsSafe(t *testing.T) {
	m := NewCausalMaintenance(nil, CausalMaintenanceConfig{})
	m.Stop()
}

// TestShouldSweepOnBoot pins the DB-anchor decision (0.5.84 P0 #4
// "maintenance ticker DB-anchored against restart"). Pure function;
// table-driven; no DB needed. The 31-day-ago case is the exact
// scenario the audit called out: a 24h-cadence ticker restarted after
// >24h of downtime must NOT wait another full Interval before its
// first sweep.
func TestShouldSweepOnBoot(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		lastSweep time.Time
		interval  time.Duration
		want      bool
	}{
		{
			name:      "31d ago, 24h interval → sweep now (audit scenario)",
			lastSweep: now.Add(-31 * 24 * time.Hour),
			interval:  24 * time.Hour,
			want:      true,
		},
		{
			name:      "1h ago, 24h interval → wait for ticker",
			lastSweep: now.Add(-1 * time.Hour),
			interval:  24 * time.Hour,
			want:      false,
		},
		{
			name:      "exactly interval ago → sweep (boundary)",
			lastSweep: now.Add(-24 * time.Hour),
			interval:  24 * time.Hour,
			want:      true,
		},
		{
			name:      "just under interval → wait",
			lastSweep: now.Add(-24*time.Hour + time.Millisecond),
			interval:  24 * time.Hour,
			want:      false,
		},
		{
			name:      "zero interval (misconfig) → never sweep",
			lastSweep: now.Add(-365 * 24 * time.Hour),
			interval:  0,
			want:      false,
		},
		{
			name:      "49d ago, 30d interval → sweep (stale scenario)",
			lastSweep: now.Add(-49 * 24 * time.Hour),
			interval:  30 * 24 * time.Hour,
			want:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldSweepOnBoot(tc.lastSweep, tc.interval, now)
			if got != tc.want {
				t.Errorf("shouldSweepOnBoot(%v, %v, %v) = %v, want %v",
					tc.lastSweep.Format(time.RFC3339), tc.interval, now.Format(time.RFC3339),
					got, tc.want)
			}
		})
	}
}

// TestCausalMaintenance_BootSweepSkipsOnNilPool pins that a nil-pool
// ticker (the db-less test path) short-circuits bootSweep without
// panicking — bootSweep must never touch the DB on a nil pool.
func TestCausalMaintenance_BootSweepSkipsOnNilPool(t *testing.T) {
	m := NewCausalMaintenance(nil, CausalMaintenanceConfig{Interval: 24 * time.Hour})
	m.bootSweep() // must not panic; sweepCount unchanged
	if m.SweepCount() != 0 {
		t.Errorf("bootSweep with nil pool should not increment SweepCount, got %d", m.SweepCount())
	}
}
