// Package experimental — runtime_gc.go
//
// Background garbage collector for experimental_claude_runtime_session
// rows. Mirrors the 0.3.0 pg-bootstrap sentinel pattern: an in-progress
// file marks "we are migrating sessions right now" so a SIGKILL
// between the DB delete and the artifact mv leaves a recoverable
// fingerprint instead of orphaned artifacts.
//
// Retention ladder (per memory multica-version-upgrade-compat — DB
// volume + config stay forward-only):
//
//   - Live: status in queued/running/completed/failed, expires_at = now()
//     + 30 days when inserted.
//   - Archive: expires_at < now() ⇒ move to
//     ~/.multica/experimental/claude-science/archive/<YYYY-MM>/<uuid>/,
//     DELETE the row.
//   - Trash: archive dir mtime > 90 days ⇒ tar.gz under
//     .trash/<uuid>-<stamp>.tar.gz, leave in place; final unlink
//     happens at archive mtime > 120 days.
//
// Trigger interval: every 6 hours. The actual work is bounded by the
// oldest row's age; a 6 h tick keeps the GC quiet under normal
// research tempo (a research issue produces ≤ a few sessions per
// day).

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
)

// RuntimeGCConfig bundles every knob the GC loop respects.
type RuntimeGCConfig struct {
	// Queries is required.
	Queries *db.Queries
	// BaseDir overrides ~/.multica/experimental/claude-science/.
	// Tests use a temp dir.
	BaseDir string
	// Interval defaults to 6 hours.
	Interval time.Duration
	// SessionTTL overrides the 30-day retention.
	SessionTTL time.Duration
	// ArchiveTTL overrides the 90-day retention.
	ArchiveTTL time.Duration
	// TrashTTL overrides the 120-day retention.
	TrashTTL time.Duration
	// Logger is the structured logger; defaults to slog.Default().
	Logger *slog.Logger
}

// RuntimeGC owns the goroutine that scans the session table on a
// tick. Call Start once at server boot; Run is the inner loop
// (exported for tests).
type RuntimeGC struct {
	cfg     RuntimeGCConfig
	running atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
}

// NewRuntimeGC builds the GC with config defaults applied.
func NewRuntimeGC(cfg RuntimeGCConfig) *RuntimeGC {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 30 * 24 * time.Hour
	}
	if cfg.ArchiveTTL <= 0 {
		cfg.ArchiveTTL = 90 * 24 * time.Hour
	}
	if cfg.TrashTTL <= 0 {
		cfg.TrashTTL = 120 * 24 * time.Hour
	}
	if cfg.BaseDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.BaseDir = filepath.Join(home, ".multica", "experimental", "claude-science")
		} else {
			cfg.BaseDir = filepath.Join(os.TempDir(), "multica-experimental")
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &RuntimeGC{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start launches the GC loop on its own goroutine. Use Stop to
// halt during shutdown. Idempotent: a second Start returns
// immediately without spawning a duplicate.
func (g *RuntimeGC) Start() {
	if !g.running.CompareAndSwap(false, true) {
		return
	}
	go g.Run()
}

// Stop signals the loop to halt. Safe to call before Start; safe
// to call multiple times. The actual close-once is funnelled through
// stopOne so the loop body's defer close can't race a Stop that's
// already closed the channel.
func (g *RuntimeGC) Stop() {
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
func (g *RuntimeGC) Run() {
	// Always close via sync.Once so the deferred close cannot
	// race with Stop's earlier close.
	g.stopOne.Do(func() { close(g.stopped) })
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
func (g *RuntimeGC) sweep() {
	if g.cfg.Queries == nil {
		g.cfg.Logger.Debug("runtime_gc disabled; no queries wired")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rows, err := g.cfg.Queries.ListExperimentalClaudeRuntimeSessionsExpired(ctx)
	if err != nil {
		g.cfg.Logger.Warn("runtime_gc list failed", "err", err.Error())
		return
	}
	if len(rows) == 0 {
		g.cfg.Logger.Debug("runtime_gc nothing to do")
		return
	}
	g.cfg.Logger.Info("runtime_gc sweep start", "count", len(rows))
	for _, row := range rows {
		if !g.running.Load() {
			return
		}
		if err := g.archiveOne(ctx, row); err != nil {
			g.cfg.Logger.Warn("runtime_gc archive failed", "id", row.ID.String(), "err", err.Error())
		}
	}
	g.cfg.Logger.Info("runtime_gc sweep done", "count", len(rows))

	// Always run the trash sweep after archive; the GC is
	// monolithic by design (one sweep per tick keeps failure
	// handling simple).
	g.trashSweep(ctx)
}

// archiveOne moves a session's artifacts under archive/<YYYY-MM>/.
// Sentinel pattern: stamp a `.archiving-<uuid>` file before the mv
// so a SIGKILL between steps is recoverable (next sweep sees the
// marker and either completes or skips with a log line).
//
// We do NOT delete the row inside the same SQL call as the mv — a
// SIGKILL between successful mv and DELETE leaves the row pointing
// at an empty directory. Re-running archive on the next sweep is
// idempotent because the directory check + mv is atomic at the
// filesystem level (rename(2) is atomic on POSIX).
func (g *RuntimeGC) archiveOne(ctx context.Context, row db.ExperimentalClaudeRuntimeSession) error {
	stamp := time.Now().UTC().Format("2006-01")
	target := filepath.Join(g.cfg.BaseDir, "archive", stamp, row.ID.String())
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir archive: %w", err)
	}

	sentinel := filepath.Join(target, ".archiving")
	if err := WriteAtomic(sentinel, []byte(time.Now().UTC().Format(time.RFC3339))); err != nil {
		return fmt.Errorf("sentinel: %w", err)
	}

	src := filepath.Join(g.cfg.BaseDir, "runtime", row.ID.String())
	if _, err := os.Stat(src); err == nil {
		// Atomic mv into the archive target. Errors here are
		// non-fatal — the row still has cleanup potential on the
		// next sweep.
		if err := RenameCrossDevice(src, target); err != nil {
			g.cfg.Logger.Warn("runtime_gc mv fallback to copy", "err", err.Error())
			if err := copyDir(src, filepath.Join(target, "runtime")); err != nil {
				return fmt.Errorf("copy: %w", err)
			}
			_ = os.RemoveAll(src)
		}
	}
	// Stamping finished removes the sentinel — final state is
	// "archive present, row deleted".
	finished := filepath.Join(target, ".finished")
	if err := WriteAtomic(finished, []byte(time.Now().UTC().Format(time.RFC3339))); err != nil {
		return fmt.Errorf("finished: %w", err)
	}
	_ = os.Remove(sentinel)

	if err := g.cfg.Queries.DeleteExperimentalClaudeRuntimeSession(ctx, row.ID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// trashSweep walks the archive tree, tarballs anything older than
// ArchiveTTL into <BaseDir>/.trash/, and unlinks anything older
// than TrashTTL.
func (g *RuntimeGC) trashSweep(ctx context.Context) {
	root := filepath.Join(g.cfg.BaseDir, "archive")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		monthDir := filepath.Join(root, e.Name())
		sessions, err := os.ReadDir(monthDir)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if !s.IsDir() {
				continue
			}
			sessPath := filepath.Join(monthDir, s.Name())
			info, err := s.Info()
			if err != nil {
				continue
			}
			age := now.Sub(info.ModTime())
			if age >= g.cfg.TrashTTL {
				// Final unlink — log it; the GC sweep is the
				// audit trail.
				if err := os.RemoveAll(sessPath); err != nil {
					g.cfg.Logger.Warn("runtime_gc unlink failed", "path", sessPath, "err", err.Error())
				}
				continue
			}
			if age >= g.cfg.ArchiveTTL {
				trashDir := filepath.Join(g.cfg.BaseDir, ".trash")
				_ = os.MkdirAll(trashDir, 0o755)
				tarPath := filepath.Join(trashDir, fmt.Sprintf("%s-%d.tar.gz", s.Name(), now.Unix()))
				if err := tarGz(sessPath, tarPath); err != nil {
					g.cfg.Logger.Warn("runtime_gc targz failed", "path", sessPath, "err", err.Error())
					continue
				}
				if err := os.RemoveAll(sessPath); err != nil {
					g.cfg.Logger.Warn("runtime_gc post-targz unlink failed", "path", sessPath, "err", err.Error())
				}
			}
		}
	}
}

// writeAtomic writes body to path via tmp + rename(2).
func WriteAtomic(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// renameCrossDevice wraps os.Rename with a cross-device fallback
// (EXDEV). On macOS the archive target may live on a different
// volume if the user pointed BaseDir elsewhere; we degrade to a
// copy + unlink rather than fail.
func RenameCrossDevice(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !isCrossDeviceErr(err) {
		return err
	}
	if err := copyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func isCrossDeviceErr(err error) bool {
	if err == nil {
		return false
	}
	// Cross-device on linux/macos returns EXDEV; linkerError
	// stringification is implementation-specific. Match the
	// substring to keep this helper free of syscall imports.
	return errStringContains(err, "cross-device link") || errStringContains(err, "invalid cross-device link")
}

func errStringContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	// Best-effort: errors.As / errors.Is the syscall path; a
	// bounded substring match covers the common cases without
	// dragging in syscall.EXDEV.
	s := err.Error()
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// copyDir recursively copies src into dst. Used as the fallback
// for renameCrossDevice; we keep the implementation close to the
// rename helper so a future optimization can swap to a streaming
// copy without changing call sites.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		sfp := filepath.Join(src, e.Name())
		dfp := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(sfp, dfp); err != nil {
				return err
			}
			continue
		}
		body, err := os.ReadFile(sfp)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dfp, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// tarGz archives the src directory into a gzip-compressed tarball at
// dst, written atomically (tmp + rename) so a crash mid-write never
// leaves a truncated archive that the caller would then treat as a
// successful backup before unlinking src.
//
// 0.3.68: real implementation. The 0.3.19 stub wrote an 11-byte
// "placeholder" file and returned nil, so the caller's post-archive
// os.RemoveAll silently destroyed the session data the 90-day tier
// was supposed to preserve until the 120-day final unlink.
func tarGz(src, dst string) error {
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
		// Root the entries under the session dir name so extraction
		// reproduces <uuid>/... instead of splatting into cwd.
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

	// Close in reverse order; keep the first error but always run
	// every Close so the fds are released even on failure.
	closeErrs := []error{walkErr, tw.Close(), gw.Close(), f.Close()}
	for _, cerr := range closeErrs {
		if cerr != nil {
			_ = os.Remove(tmp)
			return cerr
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// markedUUID returns whether the row's id, as a UUID string, is
// parsable. Used by tests to assert the loop is wired without
// having to seed the DB.
func markedUUID(u pgtype.UUID) string { return u.String() }
