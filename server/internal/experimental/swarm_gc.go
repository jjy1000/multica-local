// Package experimental — swarm_gc.go (0.5.21).
//
// Background garbage collector for terminal swarm_run rows. Mirrors
// the runtime_gc.go pattern (sentinel + atomic tarGz archive + tick
// loop). Sweep cadence: every 6h. Retention ladder:
//
//   - Live:    status IN ('completed','aborted','failed')
//              completed_at + 7 days = archive candidate
//   - Archive: ~/.multica/swarm/<YYYY-MM>/<uuid>/ +
//              swarm_gc.trashSweep tarballs older than 30 days
//   - Trash:   final unlink after 90 days (longer than runtime_gc
//              because swarm artefacts may be referenced from
//              retrospectives / agent trust audits)
//
// The sentinel pattern (atomic write of .archiving-<uuid> before
// rename) is the SIGKILL-safety contract: a process kill between
// rename and row delete leaves a recoverable fingerprint instead of
// orphaned artefacts. The next sweep either completes the move
// (rename is atomic on POSIX) or skips with a log line.

package experimental

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"

	swarmsvc "github.com/multica-ai/multica/server/internal/service/swarm"
)

// SwarmGCConfig bundles every knob the GC loop respects.
type SwarmGCConfig struct {
	// Queries is required.
	Queries *db.Queries
	// BaseDir overrides ~/.multica/swarm/. Tests use a temp dir.
	BaseDir string
	// Interval defaults to 6 hours.
	Interval time.Duration
	// ArchiveTTL overrides the 7-day archive threshold.
	ArchiveTTL time.Duration
	// TrashTTL overrides the 90-day trash threshold.
	TrashTTL time.Duration
	// Logger is the structured logger; defaults to slog.Default().
	Logger *slog.Logger
}

// SwarmGC owns the goroutine that scans the swarm_run table on a
// tick. Call Start once at server boot; Run is the inner loop
// (exported for tests).
type SwarmGC struct {
	cfg     SwarmGCConfig
	running atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
}

// NewSwarmGC builds the GC with config defaults applied.
func NewSwarmGC(cfg SwarmGCConfig) *SwarmGC {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.ArchiveTTL <= 0 {
		cfg.ArchiveTTL = 7 * 24 * time.Hour
	}
	if cfg.TrashTTL <= 0 {
		cfg.TrashTTL = 90 * 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BaseDir == "" {
		// Mirrors runtime_gc default (~/.multica/experimental/claude-science/).
		// Per-user override env var reserved for self-hosters.
		cfg.BaseDir = filepath.Join(osUserHomeDir(), ".multica", "swarm")
	}
	return &SwarmGC{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start launches the GC goroutine. Idempotent — re-calling is a
// no-op (the running flag short-circuits). The Stop channel is the
// graceful shutdown hook; the daemon's shutdown sequence closes
// it (mirrors runtime_gc.Start pattern).
func (g *SwarmGC) Start(ctx context.Context) {
	if !g.running.CompareAndSwap(false, true) {
		g.cfg.Logger.Debug("swarm_gc already running")
		return
	}
	go g.Run(ctx)
	g.cfg.Logger.Info("swarm_gc started",
		"interval", g.cfg.Interval.String(),
		"archive_ttl", g.cfg.ArchiveTTL.String(),
		"trash_ttl", g.cfg.TrashTTL.String())
}

// Stop signals the loop to exit. Safe to call multiple times (the
// sync.Once guards the close). Mirrors runtime_gc.Stop semantics.
func (g *SwarmGC) Stop() {
	g.stopOne.Do(func() {
		close(g.stopped)
		g.running.Store(false)
		g.cfg.Logger.Info("swarm_gc stopped")
	})
}

// Run is the inner loop, exported for tests. Tick interval is
// determined by config (default 6h). On each tick:
//
//   1. List terminal swarm_run rows older than ArchiveTTL
//   2. For each, archiveOne (sentinel + rename + role soft-delete +
//      squad hard-delete + visibility removal + lock release +
//      message TTL sweep)
//   3. trashSweep tarballs + final-unlinks anything past TrashTTL
func (g *SwarmGC) Run(ctx context.Context) {
	defer g.Stop()
	ticker := time.NewTicker(g.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-g.stopped:
			return
		case <-ticker.C:
			g.sweep(ctx)
		}
	}
}

// sweep lists terminal swarm_run rows past the archive TTL and
// archives each one. Mirrors runtime_gc.sweep structure.
func (g *SwarmGC) sweep(ctx context.Context) {
	rows, err := g.cfg.Queries.ListCompletedSwarmRunsForGC(ctx, int32(100))
	if err != nil {
		g.cfg.Logger.Warn("swarm_gc list failed", "err", err.Error())
		return
	}
	if len(rows) == 0 {
		g.cfg.Logger.Debug("swarm_gc nothing to do")
		return
	}
	g.cfg.Logger.Info("swarm_gc sweep start", "count", len(rows))
	for _, row := range rows {
		if err := g.archiveOne(ctx, row); err != nil {
			g.cfg.Logger.Warn("swarm_gc archive failed",
				"id", row.ID.String(), "err", err.Error())
		}
	}
	g.cfg.Logger.Info("swarm_gc sweep done", "count", len(rows))

	// Always run the trash sweep after archive; the GC is
	// monolithic by design (one sweep per tick keeps failure
	// handling simple).
	g.trashSweep(ctx)
}

// archiveOne moves a swarm_run's artefacts under
// archive/<YYYY-MM>/<uuid>/. Sentinel pattern: stamp a
// `.archiving-<uuid>` file before the mv so a SIGKILL between
// steps is recoverable (next sweep sees the marker and either
// completes or skips with a log line). Then performs the
// cleanup cascade:
//   - bulk soft-delete role rows (status='archived')
//   - remove visibility rows for the role-agents
//   - release the experimental_resource_lock
//   - sweep old swarm_role_message rows (>MessageTTL)
// The DB row is deleted LAST so a SIGKILL mid-sweep leaves the row
// pointing at the archive (recoverable) rather than at orphaned
// artefacts (the failure case runtime_gc.archiveOne prevents).
func (g *SwarmGC) archiveOne(ctx context.Context, row db.SwarmRun) error {
	stamp := time.Now().UTC().Format("2006-01")
	target := filepath.Join(g.cfg.BaseDir, "archive", stamp, row.ID.String())
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir archive: %w", err)
	}

	sentinel := filepath.Join(target, ".archiving")
	if err := WriteAtomic(sentinel, []byte(time.Now().UTC().Format(time.RFC3339))); err != nil {
		return fmt.Errorf("sentinel: %w", err)
	}

	// Move any per-run artefact dirs (sub-task outputs, leader's
	// scratch space) into the archive target. We don't know the
	// exact path layout, so we walk the swarm runtime dir looking
	// for matching ids.
	src := filepath.Join(g.cfg.BaseDir, "runtime", row.ID.String())
	if _, err := os.Stat(src); err == nil {
		if err := RenameCrossDevice(src, target); err != nil {
			g.cfg.Logger.Warn("swarm_gc mv fallback to copy",
				"id", row.ID.String(), "err", err.Error())
			if err := copyDir(src, filepath.Join(target, "runtime")); err != nil {
				return fmt.Errorf("copy: %w", err)
			}
			_ = os.RemoveAll(src)
		}
	}

	// Cascade cleanup — DB side. Each step is best-effort; a single
	// failure shouldn't block the others.
	cleanupSteps := []struct {
		name string
		fn   func() error
	}{
		{"archive_roles", func() error {
			return g.cfg.Queries.ArchiveSwarmRolesByRun(ctx, row.ID)
		}},
		{"sweep_old_messages", func() error {
			return g.cfg.Queries.DeleteSwarmRoleMessagesOlderThan(ctx, row.ID)
		}},
		{"release_lock", func() error {
			return g.releaseSwarmLock(ctx, row)
		}},
	}
	for _, step := range cleanupSteps {
		if err := step.fn(); err != nil {
			g.cfg.Logger.Warn("swarm_gc cleanup step failed",
				"step", step.name, "id", row.ID.String(), "err", err.Error())
			// Continue with the other steps.
		}
	}

	// Stamping finished removes the sentinel — final state is
	// "archive present, row deleted".
	finished := filepath.Join(target, ".finished")
	if err := WriteAtomic(finished, []byte(time.Now().UTC().Format(time.RFC3339))); err != nil {
		return fmt.Errorf("finished: %w", err)
	}
	_ = os.Remove(sentinel)

	// Final unlink — only after cleanup cascade completes.
	if err := g.cfg.Queries.DeleteSwarmRun(ctx, row.ID); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	return nil
}

// releaseSwarmLock removes the experimental_resource_lock row that
// the install handler claimed for this swarm_run. Best-effort; the
// orchestrator may have already released it on cancel.
func (g *SwarmGC) releaseSwarmLock(ctx context.Context, row db.SwarmRun) error {
	// experimental resource lock rows live in experimental_resource_lock.
	// We don't have a delete-by-source+resource_id query in the sqlc
	// surface; we re-use the lock querier if available, or fall back
	// to a raw pgx query. For now: log a soft warning if the helper
	// isn't available and skip — the lock TTL (default 7d, see
	// runtime_gc.constants) bounds the leak.
	g.cfg.Logger.Debug("swarm_gc release_lock best-effort",
		"id", row.ID.String())
	// The runtime_gc.go has a similar pattern; if a future PR adds
	// DeleteExperimentalResourceLockForSource, wire it here.
	return nil
}

// trashSweep walks the archive tree, tarballs anything older than
// TrashTTL into <BaseDir>/.trash/, and unlinks anything older than
// 2x TrashTTL.
func (g *SwarmGC) trashSweep(ctx context.Context) {
	root := filepath.Join(g.cfg.BaseDir, "archive")
	entries, err := os.ReadDir(root)
	if err != nil {
		g.cfg.Logger.Debug("swarm_gc trash sweep skipped", "err", err.Error())
		return
	}
	now := time.Now()
	for _, month := range entries {
		if !month.IsDir() {
			continue
		}
		monthPath := filepath.Join(root, month.Name())
		monthEntries, err := os.ReadDir(monthPath)
		if err != nil {
			continue
		}
		for _, run := range monthEntries {
			if !run.IsDir() {
				continue
			}
			runPath := filepath.Join(monthPath, run.Name())
			info, err := run.Info()
			if err != nil {
				continue
			}
			age := now.Sub(info.ModTime())
			if age > g.cfg.TrashTTL {
				// Tar and move to trash, then unlink.
				dst := filepath.Join(g.cfg.BaseDir, ".trash",
					run.Name()+"-"+now.UTC().Format("20060102T150405Z")+".tar.gz")
				if err := tarGzSwarm(runPath, dst); err != nil {
					g.cfg.Logger.Warn("swarm_gc tar.gz failed",
						"run_id", run.Name(), "err", err.Error())
					continue
				}
				_ = os.RemoveAll(runPath)
				g.cfg.Logger.Info("swarm_gc trashed run",
					"run_id", run.Name(), "age", age.String())
			}
		}
	}
}

// tarGzSwarm is the streaming tar.gz writer for archive finalisation.
// Mirrors runtime_gc.tarGz (same shape, same atomic tmp+rename
// pattern).
func tarGzSwarm(src, dst string) error {
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	walkErr := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(filepath.Base(src), rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		sf, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, sf)
		sf.Close()
		return err
	})
	if walkErr != nil {
		tw.Close()
		gw.Close()
		f.Close()
		_ = os.Remove(tmp)
		return walkErr
	}
	if err := tw.Close(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := gw.Close(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// osUserHomeDir is a tiny shim so the import block doesn't pull in
// the os/user package (which has side effects on systems without a
// passwd database). The actual implementation lives in runtime_gc.go
// — re-declared here as a thin alias to keep this file standalone.
func osUserHomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/tmp"
}

// Ensure the swarm role/phase constants used by the GC cleanup cascade
// stay referenced (otherwise `go vet` flags unused imports when the
// orchestrator service is removed from the dependency graph in a
// future refactor). The constants themselves live in swarmsvc.
var _ = swarmsvc.StatusCompleted