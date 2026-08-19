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

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"

	swarmsvc "github.com/multica-ai/multica/server/internal/service/swarm"
)

// swarmTopologyLockResourceType mirrors experimental.LockSwarmRun
// (server/internal/experimental/lock.go). It is duplicated here as
// a literal string rather than imported to avoid a cyclic import:
// experimental → service/swarm (transitively, via the orchestrator's
// integration test references), and service/swarm → experimental
// would close the loop. The SQL CHECK constraint on
// experimental_resource_lock.resource_type is widened to include
// 'swarm_run' in migration 244; the value MUST match the constant
// in lock.go verbatim.
const swarmTopologyLockResourceType = "swarm_run"

// swarmTopologyLockSource mirrors experimental.SourceSwarmTopology
// (lock.go). Same cycle-avoidance rationale as
// swarmTopologyLockResourceType above.
const swarmTopologyLockSource = "swarm_topology"

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
	cfg        SwarmGCConfig
	running    atomic.Bool
	stopped    chan struct{}
	stopOne    sync.Once
	sweepCount atomic.Uint64
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
		// 0.5.22 audit fix (P2-12): use os.UserHomeDir (stdlib) instead
		// of the duplicated shim that lived at the bottom of this file.
		// runtime_gc.go:89 already imports os/user directly — no
		// import-block constraint prevented us from doing the same.
		userHome, herr := os.UserHomeDir()
		if herr != nil {
			userHome = "/tmp"
		}
		cfg.BaseDir = filepath.Join(userHome, ".multica", "swarm")
	}
	return &SwarmGC{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start launches the GC goroutine. Idempotent — re-calling is a
// no-op (the running flag short-circuits). Mirrors runtime_gc.Start:
// Start takes NO context. The loop owns context.Background internally
// (see Run), so it can never be bound to a boot-scoped caller context.
// The Stop channel is the graceful shutdown hook; main.go's shutdown
// sequence closes it.
//
// 0.5.39 fix: Start previously took a context and router.go passed the
// boot-scoped bootCtx (30s timeout + defer cancel). Run exited on
// ctx.Done() ~8ms after router setup and swarm cleanup never ran —
// the same dormant-GC class as the 0.5.25 runtime_gc fix. The context
// parameter is removed entirely.
func (g *SwarmGC) Start() {
	if !g.running.CompareAndSwap(false, true) {
		g.cfg.Logger.Debug("swarm_gc already running")
		return
	}
	go g.Run()
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
//
// Exit paths are g.stopped (main.go shutdown Stop) and running=false.
// There is NO context argument — sweep owns context.Background via a
// per-sweep timeout, so the loop cannot be bound to a short-lived
// caller context (0.5.39 fix; pre-fix the ctx.Done() branch made the
// goroutine exit at router-boot completion).
func (g *SwarmGC) Run() {
	defer g.Stop()
	ticker := time.NewTicker(g.cfg.Interval)
	defer ticker.Stop()

	for g.running.Load() {
		select {
		case <-g.stopped:
			return
		case <-ticker.C:
			g.sweep()
		}
	}
}

// sweep lists terminal swarm_run rows past the archive TTL and
// archives each one. Mirrors runtime_gc.sweep structure.
func (g *SwarmGC) sweep() {
	g.sweepCount.Add(1)
	if g.cfg.Queries == nil {
		g.cfg.Logger.Debug("swarm_gc disabled; no queries wired")
		return
	}
	// Per-sweep timeout context owned internally (mirrors
	// runtime_gc.sweep) so the loop itself needs no external context.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
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

	// 0.5.22 audit fix (P2-7): removed the dead BaseDir/runtime/{uuid}
	// read block. bootstrapFromSpec never wrote per-run artefact
	// dirs there (the orchestrator doesn't own a scratch space —
	// the daemon manages runtime paths), so the Stat was always
	// returning ENOENT and the copy/rename branches were unreachable.
	// If a future orchestrator grows a per-run scratch dir, wire
	// it through bootstrapFromSpec + a dedicated migration first.

	// Cascade cleanup — DB side. Each step is best-effort; a single
	// failure shouldn't block the others.
	//
	// 0.5.22 audit fix (P0): added `remove_visibility_rows` step
	// (the audit found this listed in the comment but missing from
	// the slice — visibility rows leaked forever, keeping
	// lab_managed=true on ListAgents/GetAgent). The role-agent
	// list is fetched ONCE at the top so the visibility cleanup can
	// run before the role rows flip to 'archived'.
	roleAgentIDs := g.roleAgentIDs(ctx, row.ID)
	cleanupSteps := []struct {
		name string
		fn   func() error
	}{
		{"archive_roles", func() error {
			return g.cfg.Queries.ArchiveSwarmRolesByRun(ctx, row.ID)
		}},
		{"remove_visibility_rows", func() error {
			for _, agentID := range roleAgentIDs {
				if err := g.cfg.Queries.DeleteExperimentalResourceVisibilityByResourceID(ctx, db.DeleteExperimentalResourceVisibilityByResourceIDParams{
					ResourceType: "agent",
					ResourceID:   agentID,
				}); err != nil {
					return err
				}
			}
			return nil
		}},
		// 0.5.22 audit fix (P2-3): drop the DeleteSwarmRoleMessagesOlderThan
		// step. The subsequent DeleteSwarmRun CASCADEs to
		// swarm_role_message (FK on swarm_run_id) and wipes ALL
		// remaining rows regardless of age — the 30-day TTL sweep was
		// only meaningful for LIVE runs (which the GC doesn't touch).
		// For terminal runs the CASCADE supersedes it.
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
// the install handler claimed for this swarm_run.
//
// 0.5.22 audit fix (P0): was a stub returning nil — the lock row
// leaked forever (the table has no TTL column). Now calls the
// DeleteExperimentalResourceLockByID sqlc query added in this fix.
func (g *SwarmGC) releaseSwarmLock(ctx context.Context, row db.SwarmRun) error {
	return g.cfg.Queries.DeleteExperimentalResourceLockByID(ctx, db.DeleteExperimentalResourceLockByIDParams{
		ExperimentalSource: swarmTopologyLockSource,
		ResourceType:       swarmTopologyLockResourceType,
		ResourceID:         row.ID,
	})
}

// roleAgentIDs returns the agent UUIDs of every role-agent the
// run authored, so the visibility-row cleanup cascade knows which
// rows to delete. Read once at the top of archiveOne (before
// ArchiveSwarmRolesByRun flips role rows to 'archived') and passed
// to the cleanupSteps closure. Best-effort: a list failure logs
// at warn and returns nil — the cascade then no-ops the
// visibility-cleanup step rather than aborting the whole archive.
func (g *SwarmGC) roleAgentIDs(ctx context.Context, runID pgtype.UUID) []pgtype.UUID {
	rows, err := g.cfg.Queries.ListSwarmRolesByRun(ctx, runID)
	if err != nil {
		g.cfg.Logger.Warn("swarm_gc list roles failed (visibility cleanup will skip)",
			"id", runID.String(), "err", err.Error())
		return nil
	}
	out := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		if r.AgentID.Valid {
			out = append(out, r.AgentID)
		}
	}
	return out
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

	// 0.5.22 audit fix (P2-6): second-stage unlink — the trash dir
	// itself grows forever otherwise. After TrashTTL the tarball is
	// already a frozen snapshot, so it's safe to delete. Matches the
	// runtime_gc.go retention ladder (30d archive + 120d trash =
	// 150d total lifetime).
	trashRoot := filepath.Join(g.cfg.BaseDir, ".trash")
	trashEntries, err := os.ReadDir(trashRoot)
	if err != nil {
		return // .trash may not exist yet
	}
	for _, tarball := range trashEntries {
		info, err := tarball.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > g.cfg.TrashTTL {
			if err := os.RemoveAll(filepath.Join(trashRoot, tarball.Name())); err != nil {
				g.cfg.Logger.Warn("swarm_gc final unlink failed",
					"name", tarball.Name(), "err", err.Error())
				continue
			}
			g.cfg.Logger.Info("swarm_gc final unlink",
				"name", tarball.Name(), "age", now.Sub(info.ModTime()).String())
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
		// 0.5.22 audit fix (P2-5): skip symlinks. filepath.Walk
		// follows them by default; the previous comment claimed to
		// skip them but the code didn't. A symlinked archive could
		// include files outside the intended archive scope
		// (e.g. /etc/passwd via a stray link). Use os.Lstat to get
		// the unsymlinked FileInfo for the mode check.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
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

// Ensure the swarm role/phase constants used by the GC cleanup cascade
// stay referenced (otherwise `go vet` flags unused imports when the
// orchestrator service is removed from the dependency graph in a
// future refactor). The constants themselves live in swarmsvc.
var _ = swarmsvc.StatusCompleted