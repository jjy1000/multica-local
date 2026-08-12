// Package llmwiki — writer.go
//
// Write side of the LLM Wiki bridge. Reads use the Client (LLM Wiki
// desktop HTTP API); writes drop files directly into the LLM Wiki
// vault directory under ~/Documents/llm wiki/.
//
// We do NOT use the desktop API for writes because:
//
//  1. The 0.4.25 desktop API exposes only read endpoints (search,
//     read_file, graph, files, status) — the MCP server does not
//     surface a write endpoint either.
//  2. The user prefers to vectorise manually after each write, so
//     dropping the file directly into the vault directory and
//     letting the desktop app pick it up via its Source Watch rule
//     (raw/sources/) is the right shape.
//  3. Dropping files is reversible: a future "Rescan Sources" call
//     re-indexes everything; a misformatted write is just a plain
//     file in raw/sources/ the desktop app ignores.
//
// Hard rules:
//
//   - Path is relative to the user's vault root
//     (`/Users/jiangjianyan/Documents/llm wiki/`). The handler
//     rejects absolute paths, paths with `..` segments, and paths
//     that resolve outside the vault.
//   - One writer per process; the writer holds a per-call mutex on
//     the file write so concurrent agents don't tear the file.
//   - Idempotent: writing the same content twice produces the same
//     file bytes; the desktop app's hash-based dedupe catches
//     repeats during its rescan.

package llmwiki

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultVaultDir is the conventional location the desktop app
// reads from. The actual root can be overridden via the LLM Wiki
// vault setting; in 0.4.25 it's a sub-directory of the user's
// documents folder. We deliberately default to a path the user
// can verify visually.
const DefaultVaultDir = "/Users/jiangjianyan/Documents/llm wiki"

// ErrPathOutsideVault is returned when the caller tries to write
// to a path that resolves outside the configured vault directory.
var ErrPathOutsideVault = errors.New("path resolves outside the vault directory")

// Writer drops files into the LLM Wiki vault.
type Writer struct {
	root string
}

// NewWriter constructs a Writer rooted at root. Pass an empty
// string to use DefaultVaultDir.
func NewWriter(root string) (*Writer, error) {
	if root == "" {
		root = DefaultVaultDir
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("abs path: %w", err)
	}
	return &Writer{root: abs}, nil
}

// Write drops body into <vaultRoot>/<relPath> atomically. relPath
// may use forward slashes; the writer normalises to the host
// separator. The function returns ErrPathOutsideVault when relPath
// would escape the vault root.
//
// Existing files are overwritten. The caller is expected to
// surface the file's previous content (if any) to the user via the
// issue comment thread so the change is auditable.
//
// The ctx argument carries the caller's identity (stashed by the
// HTTP middleware via llmWikiCallerContext). The flag gate reads
// it to resolve per-user experimental_pref; a caller without an
// identity falls through to the catalog default.
func (w *Writer) Write(ctx context.Context, relPath string, body []byte) (string, error) {
	if err := w.checkFlag(ctx); err != nil {
		return "", err
	}
	if relPath == "" {
		return "", errors.New("relPath is required")
	}
	cleanRel := filepath.Clean(relPath)
	if strings.HasPrefix(cleanRel, "..") || strings.Contains(cleanRel, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideVault
	}
	full := filepath.Join(w.root, cleanRel)
	// Re-verify the resolved path is inside the vault. filepath.Join
	// already strips leading `..`, but a sneaky `safe/../../escape`
	// can still slip through on some platforms — guard against
	// both with a HasPrefix check on the cleaned absolute path.
	resolved, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("abs path: %w", err)
	}
	if !strings.HasPrefix(resolved, w.root+string(filepath.Separator)) && resolved != w.root {
		return "", ErrPathOutsideVault
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(resolved, body, 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return resolved, nil
}

// ListVaults returns the conventional location's sub-trees so the
// HTTP layer can show a navigation hint.
func (w *Writer) ListVaults(ctx context.Context) ([]string, error) {
	if err := w.checkFlag(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(w.root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Root returns the resolved vault root. Useful for surfacing in
// error messages so the user can locate their write.
func (w *Writer) Root() string { return w.root }

// checkFlag is the writer's flag gate; the HTTP layer enforces this
// for every method, but the writer also checks because the LLM Wiki
// writer is reachable from CLI / Skill adapters as well.
//
// In production this delegates to a package-level callback set by
// SetFlagGate; tests substitute their own. The gate takes a
// context.Context because the flag is resolved per calling user
// (experimental_pref), not from a process-wide constant — see
// handler/llm_wiki_bridge.go. A caller whose context carries no user
// identity (CLI / Skill adapter) resolves to the catalog default.
//
// The gate is guarded by flagMu so concurrent flag-flip updates during
// multi-workspace server boot don't race. gateOn copies the closure
// under the read lock and calls it unlocked, because the production
// gate does a database read.

var (
	flagMu sync.RWMutex
	flagG  = func(context.Context) bool { return true }
)

func gateOn(ctx context.Context) bool {
	flagMu.RLock()
	g := flagG
	flagMu.RUnlock()
	return g(ctx)
}

// SetFlagGate wires the llm_wiki_bridge flag check into the writer.
// Call once at server boot. Subsequent updates from any goroutine
// are serialised through the writer's internal mutex so a reader
// (gateOn / checkFlag) never observes a half-written closure.
//
// A nil callback restores the permissive default; the previous
// implementation (which used a separate `flagOn` boolean that was
// consulted only at the time SetFlagGate was called) had a race
// window where a concurrent reader could observe `flagOn=true`
// after the catalog default had flipped off. The current shape —
// one closure copied under the lock, called unlocked — closes that
// window.
func SetFlagGate(g func(ctx context.Context) bool) {
	flagMu.Lock()
	defer flagMu.Unlock()
	if g == nil {
		flagG = func(context.Context) bool { return true }
		return
	}
	flagG = g
}

func (w *Writer) checkFlag(ctx context.Context) error {
	if !gateOn(ctx) {
		return ErrFlagDisabled
	}
	return nil
}