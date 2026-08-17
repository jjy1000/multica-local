// Tests for SemanticaGC (0.5.30 P1-3 — synthesizer Round 7).
//
// The GC is filesystem-only (no db.Queries), so the test
// scaffolding is mkdtempSync + mtime-driven files. Mirrors the
// RuntimeGC precedent (TestRuntimeGC_RunSweepsBeforeExit, 0.5.25 fix):
// assert SweepCount increments so a regression-loop-closure bug is
// caught.

package experimental

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSemanticaGC_SweepRemovesStaleProvenance(t *testing.T) {
	tmp := t.TempDir()
	// Lay out: ~/.multica/workspaces/ws-A/semantica-graph.json.provenance
	// (90d+ old) and same with -provenance recent (kept).
	wsOld := filepath.Join(tmp, "workspaces", "ws-A")
	require.NoError(t, os.MkdirAll(wsOld, 0o755))
	oldPath := filepath.Join(wsOld, "semantica-graph.json.provenance")
	require.NoError(t, os.WriteFile(oldPath, []byte("stale"), 0o600))
	oldMtime := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldMtime, oldMtime))

	recent := filepath.Join(wsOld, "semantica-graph.json.provenance.recent")
	require.NoError(t, os.WriteFile(recent, []byte("fresh"), 0o600))
	recentMtime := time.Now().Add(-1 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(recent, recentMtime, recentMtime))

	gc := NewSemanticaGC(SemanticaGCConfig{
		BaseDir:             tmp,
		Interval:            100 * time.Millisecond,
		ProvenanceRetention: 90 * 24 * time.Hour,
	})
	defer gc.Stop()

	// Drive one sweep synchronously (don't depend on the ticker).
	gc.sweep()

	// Old provenance gone, recent kept.
	_, err := os.Stat(oldPath)
	require.True(t, os.IsNotExist(err), "stale provenance should be removed: %v", err)
	_, err = os.Stat(recent)
	require.NoError(t, err, "fresh provenance should remain")
}

func TestSemanticaGC_RunSweepsBeforeExit(t *testing.T) {
	// Mirrors TestRuntimeGC_RunSweepsBeforeExit — catches the
	// Run()-loop-exits-before-ticker-fires regression that bit
	// RuntimeGC pre-0.5.25.
	tmp := t.TempDir()
	gc := NewSemanticaGC(SemanticaGCConfig{
		BaseDir:  tmp,
		Interval: 20 * time.Millisecond,
	})
	defer gc.Stop()
	gc.Start()

	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if gc.SweepCount() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected ≥2 sweep invocations within 80ms (Interval=20ms), got %d", gc.SweepCount())
}

func TestSemanticaGC_LegacyGlobalPath(t *testing.T) {
	// P0-2 cp migration leaves the legacy
	// ~/.multica/semantica-graph.json.provenance file on disk as a
	// backup. The GC must sweep it under the same retention window
	// — otherwise the legacy file accumulates indefinitely.
	tmp := t.TempDir()
	legacy := filepath.Join(tmp, "semantica-graph.json.provenance")
	require.NoError(t, os.WriteFile(legacy, []byte("legacy"), 0o600))
	oldMtime := time.Now().Add(-120 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(legacy, oldMtime, oldMtime))

	gc := NewSemanticaGC(SemanticaGCConfig{
		BaseDir:             tmp,
		Interval:            24 * time.Hour,
		ProvenanceRetention: 90 * 24 * time.Hour,
	})
	gc.sweep()

	_, err := os.Stat(legacy)
	require.True(t, os.IsNotExist(err), "legacy provenance should be swept: %v", err)
}

func TestSemanticaGC_OrphanAPIKeySwept(t *testing.T) {
	// P1-1 moved X-API-Key transport to in-memory IPC, so any
	// leftover *.api-key file is dead data. The GC sweeps these
	// on the same retention window.
	tmp := t.TempDir()
	key := filepath.Join(tmp, "semantica-graph.json.api-key")
	require.NoError(t, os.WriteFile(key, []byte("old-secret"), 0o600))
	oldMtime := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(key, oldMtime, oldMtime))

	gc := NewSemanticaGC(SemanticaGCConfig{
		BaseDir:             tmp,
		ProvenanceRetention: 90 * 24 * time.Hour,
	})
	gc.sweep()

	_, err := os.Stat(key)
	require.True(t, os.IsNotExist(err), "orphan api-key should be swept: %v", err)
}

func TestSemanticaGC_StartStopIdempotent(t *testing.T) {
	tmp := t.TempDir()
	gc := NewSemanticaGC(SemanticaGCConfig{BaseDir: tmp})
	gc.Start()
	gc.Start() // no-op
	gc.Stop()
	gc.Stop() // no-op
}