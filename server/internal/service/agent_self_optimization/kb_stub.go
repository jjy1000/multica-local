// Package agent_self_optimization — kb_stub.go (0.3.45.1).
//
// MVP "knowledge base" sink: writes the per-run report to
// `~/.multica/learning-vault/<workspaceId>/<runId>.md` on the local
// filesystem. The mock is intentionally cheap (no IPC, no schema,
// no extra dependency) so the runner can ship end-to-end without
// dragging the LLM Wiki vault through the dependency graph.
//
// 0.3.46+ follow-up: swap the writer for a real KB integration —
// either
//   - call POST /api/experimental/llm-wiki/write (gated on
//     llm_wiki_bridge flag), or
//   - introduce a desktop IPC channel kb:append-learning that drives
//     tolaria.create_note on the user's LLM Wiki vault.
//
// The KBWriter interface in runner.go is the seam: production wires
// the stub, tests wire a fake.
package agent_self_optimization

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// KBWriter is the seam between the runner and the persistence layer
// for the "external knowledge base". The stub writer appends a
// markdown file under ~/.multica/learning-vault/.
//
// Returns the absolute path written on success so the caller can
// persist it into agent_self_opt_run.kb_appendix_path.
type KBWriter interface {
	Append(ctx context.Context, workspaceID uuid.UUID, markdown string) (string, error)
}

// FileSystemKBWriter writes reports to the local filesystem under
// ~/.multica/learning-vault/<workspaceID>/<runID-or-timestamp>.md.
// Concurrency: one process, one workspace at a time (the runner is
// gated by the Service's advisory lock per workspace). We do not
// lock at the file level — the advisory lock is the contract.
type FileSystemKBWriter struct {
	// RootDir is the directory the writer operates under. Defaults
	// to ~/.multica/learning-vault when zero.
	RootDir string
}

// NewFileSystemKBWriter returns a writer rooted at the supplied
// directory (or ~/.multica/learning-vault when dir is empty). The
// directory is created lazily on the first Append call.
func NewFileSystemKBWriter(dir string) *FileSystemKBWriter {
	if dir == "" {
		dir = defaultLearningVaultDir()
	}
	return &FileSystemKBWriter{RootDir: dir}
}

// Append writes the markdown body to a workspace-scoped file. The
// filename embeds a timestamp + short random suffix so multiple runs
// in the same workspace stay distinguishable.
//
// Returns the absolute path of the file written. I/O errors are
// surfaced verbatim; the caller decides whether to abort the run.
func (w *FileSystemKBWriter) Append(ctx context.Context, workspaceID uuid.UUID, markdown string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := filepath.Join(w.RootDir, workspaceID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create learning vault dir: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	short := uuid.NewString()[:8]
	name := fmt.Sprintf("%s-%s.md", stamp, short)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(markdown), 0o644); err != nil {
		return "", fmt.Errorf("write learning vault file: %w", err)
	}
	return full, nil
}

// defaultLearningVaultDir returns the canonical learning-vault
// location under the user's home dir. The directory is created
// lazily on the first Append; we don't MkdirAll here so a fresh
// install without the daemon running doesn't leave a stray empty
// directory.
//
// Path note: ~/.multica/ is the same root that the rest of the
// desktop app uses (see CLAUDE.md "Desktop runtime config"). Putting
// the learning vault under it keeps everything self-contained and
// survives upgrades (the DMG only replaces /Applications/Multica.app).
func defaultLearningVaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Last-resort fallback: /tmp. The desktop app always has
		// $HOME set, so this branch only fires in test runs.
		return filepath.Join(os.TempDir(), "multica-learning-vault")
	}
	return filepath.Join(home, ".multica", "learning-vault")
}