// SemanticaGC (0.5.30 P1-3 — synthesizer Round 7) sweeps per-workspace
// semantica-graph*.provenance files older than 90 days and orphaned
// .api-key files (pre-P1-1 legacy).
//
// Why filesystem-only (not db-backed): Semantica is an external
// Python service that owns its own storage shape. The provenance
// files are SQLite/JSON under
// ~/.multica/workspaces/<wsId>/semantica-graph.json.provenance
// (set by apps/desktop/vendor/semantica/run.sh:151). Multica's
// `experimental_claude_runtime_session` retention ladder (the
// existing RuntimeGC) operates on PG rows; this GC operates on the
// Semantica files separately. Both wire into Handler for the
// before-quit Stop() chain.
//
// Lifecycle mirrors RuntimeGC: Start launches the loop on its own
// goroutine; Stop is funnelled through stopOne so the loop body's
// defer can't race a Stop that's already closed the channel. Idempotent.
// Run is exported for tests.

package experimental

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SemanticaGCConfig bundles every knob the GC loop respects.
type SemanticaGCConfig struct {
	// BaseDir overrides ~/.multica. Tests use a temp dir.
	BaseDir string
	// Interval defaults to 24 hours. Provenance retention is
	// measured in days, so a sub-daily cadence is wasteful.
	Interval time.Duration
	// ProvenanceRetention defaults to 90 days.
	ProvenanceRetention time.Duration
	// Logger is the structured logger; defaults to slog.Default().
	Logger *slog.Logger
}

// SemanticaGC owns the goroutine that scans the Semantica
// filesystem on a tick.
type SemanticaGC struct {
	cfg     SemanticaGCConfig
	running atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
	// sweepCount increments every time sweep() is invoked. Exposed
	// for tests so a regression of the Run() loop body is caught
	// the same way RuntimeGC's TestRuntimeGC_RunSweepsBeforeExit
	// does (0.5.25 fix precedent).
	sweepCount atomic.Uint64
}

// NewSemanticaGC builds the GC with config defaults applied.
func NewSemanticaGC(cfg SemanticaGCConfig) *SemanticaGC {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.ProvenanceRetention <= 0 {
		cfg.ProvenanceRetention = 90 * 24 * time.Hour
	}
	if cfg.BaseDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.BaseDir = filepath.Join(home, ".multica")
		} else {
			cfg.BaseDir = filepath.Join(os.TempDir(), "multica-semantica")
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &SemanticaGC{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start launches the GC loop on its own goroutine. Idempotent.
func (g *SemanticaGC) Start() {
	if !g.running.CompareAndSwap(false, true) {
		return
	}
	go g.Run()
}

// Stop signals the loop to halt. Safe to call before Start; safe
// to call multiple times.
func (g *SemanticaGC) Stop() {
	if !g.running.Load() {
		return
	}
	g.running.Store(false)
	g.stopOne.Do(func() {
		if g.stopped != nil {
			close(g.stopped)
		}
	})
}

// Run is the loop body. Exported so tests can drive it directly
// without spawning a goroutine.
func (g *SemanticaGC) Run() {
	t := time.NewTicker(g.cfg.Interval)
	defer t.Stop()

	for g.running.Load() {
		select {
		case <-t.C:
			g.sweep()
		case <-g.stopped:
			return
		}
	}
}

// sweep runs once and returns; safe to call from tests.
func (g *SemanticaGC) sweep() {
	g.sweepCount.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	g.sweepOnce(ctx)
}

func (g *SemanticaGC) sweepOnce(ctx context.Context) {
	cutoff := time.Now().Add(-g.cfg.ProvenanceRetention)

	// 1. Per-workspace provenance files (post-P0-2 layout):
	//    ~/.multica/workspaces/<wsId>/semantica-graph*.provenance
	workspacesDir := filepath.Join(g.cfg.BaseDir, "workspaces")
	g.sweepGlob(ctx, filepath.Join(workspacesDir, "*", "semantica-graph*.provenance"), cutoff)

	// 2. Legacy global provenance file (pre-P0-2 layout; the
	//    P0-2 cp migration copies into the new path but the legacy
	//    file stays on disk as a backup). After the cp succeeds,
	//    the legacy file is redundant — sweep it on the same
	//    retention cadence.
	g.sweepGlob(ctx, filepath.Join(g.cfg.BaseDir, "semantica-graph*.provenance"), cutoff)

	// 3. Pre-P1-1 orphan .api-key files. P1-1 moved the X-API-Key
	//    transport to in-memory IPC, so any leftover *.api-key file
	//    is dead data. Defense-in-depth sweep with the same
	//    retention — user can always regenerate the file by
	//    enabling SEMANTICA_REQUIRE_AUTH=1 once and disabling it.
	g.sweepGlob(ctx, filepath.Join(g.cfg.BaseDir, "semantica-graph*.api-key"), cutoff)
	g.sweepGlob(ctx, filepath.Join(workspacesDir, "*", "semantica-graph*.api-key"), cutoff)
}

// sweepGlob walks a glob, removing files whose mtime is older than
// cutoff. Empty glob is a no-op (Glob returns ErrNoMatch for nil
// matches). Errors are logged at Warn; one bad file must not abort
// the rest of the sweep.
func (g *SemanticaGC) sweepGlob(ctx context.Context, pattern string, cutoff time.Time) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		// filepath.ErrBadPattern (a malformed glob) is a code bug;
		// other errors are also typically non-recoverable. Log and
		// bail rather than spam retries.
		g.cfg.Logger.Warn("semantica_gc glob failed",
			"pattern", pattern, "err", err.Error())
		return
	}
	if len(matches) == 0 {
		return
	}
	for _, path := range matches {
		select {
		case <-ctx.Done():
			return
		default:
		}
		info, err := os.Stat(path)
		if err != nil {
			// File vanished between Glob and Stat (manager
			// restart, cp migration). Not an error.
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil {
			g.cfg.Logger.Warn("semantica_gc remove failed",
				"path", path, "err", err.Error())
			continue
		}
		g.cfg.Logger.Info("semantica_gc removed stale file",
			"path", path, "age", time.Since(info.ModTime()).String())
	}
}

// SweepCount returns the cumulative sweep invocation count. Tests
// use this to assert the loop body actually fires (mirrors
// RuntimeGC.sweepCount, 0.5.25 fix precedent).
func (g *SemanticaGC) SweepCount() uint64 { return g.sweepCount.Load() }

// pathContainsBase is a safety check for tests that override
// BaseDir. We never want a future glob expansion to escape the
// configured root, especially when the loop sees a stale symlink.
// Currently unused in production code; kept as a guardrail.
func (g *SemanticaGC) pathContainsBase(path string) bool {
	rel, err := filepath.Rel(g.cfg.BaseDir, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}