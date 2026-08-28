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
