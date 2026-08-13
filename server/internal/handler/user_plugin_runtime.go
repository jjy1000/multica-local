// Package handler — user_plugin_runtime.go
//
// POST /api/user-plugins/{slug}/run executes a user plugin's inline code
// in a per-plugin persistent environment and ingests any files it emits
// as plugin artifacts (so the Labs panel's ArtifactGallery renders them
// with no extra round-trip).
//
// This is the runtime consumer that closes the user_plugin loop:
// plugins were creatable / listable / displayable but had no /run
// endpoint, so runtime_kind: inline | subprocess had no consumer.
//
// Design (mirrors claude_science_runtime.go, deliberately reusing its
// same-package helpers instead of re-declaring them):
//
//   - inline    → run `python3 -I entry.py` with cwd = the plugin's
//     persistent env dir (~/.multica/plugins/<slug>/env/). New/changed
//     files are diffed out and copied into the plugin's artifacts/ dir.
//   - subprocess → run the manifest.runtime.command (argv, no shell) in the
//     same sandbox env; 0.5.18 closed the prior "reserved upgrade slot".
//   - none       → 400 (the plugin declared no runtime).
//
// Storage is zero-migration: run history lives in a flat runs.json
// (atomic tmp+rename), artifacts reuse the existing file-backed index
// (loadArtifactIndex / saveArtifactIndex / storedArtifact). Everything is
// home-directory scoped, matching the artifact endpoints — no
// workspace gate, since user plugins are server-global.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const (
	// entryFileName is the fixed name of the executed snippet inside the
	// plugin's env directory. Keeping it fixed lets a plugin persist its
	// code across runs (write once, re-run with an empty body).
	entryFileName = "entry.py"

	// pluginDBFileName is the conventional SQLite database a plugin gets
	// for free: it lives in the persistent env dir, so state survives
	// across runs (the on-demand "container database"). The path is handed
	// to the run via the MULTICA_PLUGIN_DB env var; the file and its
	// SQLite sidecars are excluded from artifact ingestion (see
	// isIngestableName) so private data never leaks into the panel.
	pluginDBFileName = "data.db"

	// maxRunHistory caps runs.json so it stays a small, readable ledger
	// rather than an unbounded log. Older records are dropped FIFO.
	maxRunHistory = 50
)

// pluginRuntimeManifest is the subset of a plugin manifest the runtime
// reads. `manifest.runtime` is a pure-additive convention — normalizeManifest
// only validates that the blob is legal JSON, so no schema change is needed.
// The runtime_kind DB column remains the authoritative dispatch key; the
// manifest carries only the low-code entry source and an optional timeout.
type pluginRuntimeManifest struct {
	Runtime struct {
		Kind      string   `json:"kind"`
		EntryCode string   `json:"entry_code"`
		Command   string   `json:"command"`
		Args      []string `json:"args"`
		TimeoutMs int      `json:"timeout_ms"`
	} `json:"runtime"`
}

// runUserPluginRequest is the (optional) POST body. An empty body re-runs
// whatever entry.py already lives in the env dir.
type runUserPluginRequest struct {
	Code      string `json:"code"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

// runUserPluginResponse is the wire shape returned to the caller.
type runUserPluginResponse struct {
	Status     string         `json:"status"`
	ExitCode   int            `json:"exit_code"`
	Stdout     string         `json:"stdout"`
	Stderr     string         `json:"stderr"`
	DurationMs int            `json:"duration_ms"`
	Artifacts  []ArtifactMeta `json:"artifacts"`
}

// pluginRunRecord is one entry in runs.json. It is a compact summary; the
// full stdout/stderr live only in the HTTP response (not persisted, to keep
// the ledger small).
type pluginRunRecord struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	ExitCode   int       `json:"exit_code"`
	DurationMs int       `json:"duration_ms"`
	Artifacts  int       `json:"artifacts"`
	CreatedAt  time.Time `json:"created_at"`
}

// pluginEnvDir resolves the persistent execution environment for a plugin:
// ~/.multica/plugins/<slug>/env/. Persistent across runs (unlike the
// claude-science per-session dirs) so a plugin behaves like a small
// long-lived workspace — the seed of the "container-like own environment"
// the design calls for. Not created here; callers MkdirAll on first use.
func pluginEnvDir(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".multica", "plugins", slug, "env"), nil
}

// pluginRunsPath resolves ~/.multica/plugins/<slug>/runs.json.
func pluginRunsPath(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".multica", "plugins", slug, "runs.json"), nil
}

// pluginRuntimeEnv builds the environment the plugin process runs with. It
// deliberately does NOT inherit the parent server's os.Environ(). In the
// desktop co-resident deployment the server is the daemon's child and
// carries the daemon-injected MULTICA_API_TOKEN plus the user's profile-
// bearing HOME; inheriting all of that would let a malicious entry.py read
// the user's JWT (via HOME → ~/.multica/profiles/<name>/config.json) or
// re-use the task token to call privileged endpoints. Instead we build an
// explicit, minimal env and layer on the platform contract a lab relies on:
//
//   - PATH                — kept so python3 and stdlib helpers resolve.
//   - HOME                — pinned to the plugin env dir so any ~/-relative
//     write stays inside the sandbox, never the user's real home.
//   - LANG / LC_ALL       — a deterministic UTF-8 locale for stable text I/O.
//   - MULTICA_PLUGIN_SLUG — the plugin's slug.
//   - MULTICA_PLUGIN_ENV  — the persistent env dir (also the process cwd).
//   - MULTICA_PLUGIN_DB   — a stable SQLite path (env/data.db). Because the
//     env dir persists across runs, a lab can open this DB with the stdlib
//     sqlite3 module and accumulate state (e.g. a keymap graph it renders on
//     the next run) with zero setup and no external database.
//
// python3 is invoked with -I (isolated mode). Combined with the minimal env
// — which carries no PYTHONPATH / PYTHONSTARTUP — this closes the module-
// shadowing and startup-hook vectors as well. See 0.3.63 SEC hardening.
func pluginRuntimeEnv(slug, envDir string) []string {
	path := strings.TrimSpace(os.Getenv("PATH"))
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{
		"PATH=" + path,
		"HOME=" + envDir,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"MULTICA_PLUGIN_SLUG=" + slug,
		"MULTICA_PLUGIN_ENV=" + envDir,
		"MULTICA_PLUGIN_DB=" + filepath.Join(envDir, pluginDBFileName),
	}
}

// isIngestableName reports whether a top-level env file should be captured as
// an artifact after a run. It filters out the entry script and the plugin's
// private persistent data — most importantly the SQLite database and its
// sidecar journals — so a lab can keep state across runs (the "container
// database") without that state re-appearing as a new file artifact in the
// panel's Artifacts tab on every single run.
func isIngestableName(name string) bool {
	if name == entryFileName {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false // hidden / dotfiles are private scratch
	}
	lower := strings.ToLower(name)
	for _, suffix := range []string{
		".db", ".db-wal", ".db-shm", ".db-journal",
		".sqlite", ".sqlite-wal", ".sqlite-shm", ".sqlite-journal",
		".sqlite3", ".sqlite3-wal", ".sqlite3-shm", ".sqlite3-journal",
		".pyc",
	} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	return true
}

// RunUserPlugin executes a plugin's inline runtime and returns the run
// result plus the artifacts it produced.
// POST /api/user-plugins/{slug}/run
func (h *Handler) RunUserPlugin(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	if !validateUserPluginSlug(slug) {
		writeError(w, http.StatusBadRequest, "invalid plugin slug")
		return
	}

	// Load the plugin row (same query Update/Delete use). Missing → 404.
	plugin, err := h.Queries.GetUserPluginBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load plugin")
		return
	}
	if plugin.Status != "active" {
		writeError(w, http.StatusConflict, "plugin is not active")
		return
	}

	// Dispatch on the authoritative runtime_kind column. Both "inline" and
	// "subprocess" fall through to the shared exec path below; they differ
	// only in how the command + argv are resolved (python entry.py vs a
	// manifest-declared command).
	switch plugin.RuntimeKind {
	case "none":
		writeError(w, http.StatusBadRequest, "plugin declares no runtime")
		return
	case "inline", "subprocess":
		// fall through
	default:
		writeError(w, http.StatusBadRequest, "unsupported runtime_kind")
		return
	}

	// Optional body: an empty/absent body re-runs the persisted entry.py.
	var req runUserPluginRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	// Parse the manifest runtime block (best-effort; a malformed block just
	// leaves the zero value, which is fine — the column already gated us).
	var rm pluginRuntimeManifest
	if len(plugin.ManifestJson) > 0 {
		_ = json.Unmarshal(plugin.ManifestJson, &rm)
	}

	envDir, err := pluginEnvDir(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve env directory")
		return
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		slog.Error("user plugin run: mkdir env failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create env directory")
		return
	}

	// Resolve the command + argv to execute. Both runtime kinds share the
	// sandbox (minimal env + timeout + artifact ingestion) and differ only in
	// how the child is described:
	//
	//   inline     → write entry.py from body code / manifest entry_code /
	//                a previously persisted entry.py, then run python3 -I.
	//   subprocess → run the manifest.runtime.command directly (argv, no
	//                shell) with manifest.runtime.args.
	var command string
	var commandArgs []string
	if plugin.RuntimeKind == "subprocess" {
		command = strings.TrimSpace(rm.Runtime.Command)
		if command == "" {
			writeError(w, http.StatusBadRequest,
				"subprocess plugin declares no manifest.runtime.command")
			return
		}
		// Defense-in-depth: exec.CommandContext never invokes a shell, so these
		// metacharacters would only make path lookup fail — but rejecting them
		// keeps the intent unambiguous and matches the minimal-env boundary the
		// inline path already enforces.
		if strings.ContainsAny(command, ";&|`$<>()\n\r\t") {
			writeError(w, http.StatusBadRequest,
				"subprocess command contains shell metacharacters")
			return
		}
		commandArgs = rm.Runtime.Args
	} else {
		// inline: resolve code source in priority order (request body code →
		// manifest.runtime.entry_code → existing entry.py), then pre-flight the
		// interpreter the same way the claude-science runtime does.
		code := req.Code
		if code == "" {
			code = rm.Runtime.EntryCode
		}
		entryPath := filepath.Join(envDir, entryFileName)
		if code != "" {
			if len(code) > maxRuntimeCodeBytes {
				writeJSON(w, http.StatusRequestEntityTooLarge,
					map[string]string{"error": fmt.Sprintf("code exceeds %d bytes", maxRuntimeCodeBytes)})
				return
			}
			if err := os.WriteFile(entryPath, []byte(code), 0o644); err != nil {
				slog.Error("user plugin run: write entry failed", "slug", slug, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to write entry code")
				return
			}
		} else {
			// No inline code anywhere — fall back to a previously written entry.py.
			info, statErr := os.Stat(entryPath)
			if statErr != nil || info.Size() == 0 {
				writeError(w, http.StatusBadRequest,
					"no code provided and no entry.py present in the plugin env")
				return
			}
		}
		if err := probePython3(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "python3 not available on PATH; install Python 3.11+ and retry",
			})
			return
		}
		command = "python3"
		commandArgs = []string{"-I", entryFileName}
	}

	// Resolve the timeout: request body → manifest → default, capped.
	timeout := defaultRuntimeTimeout
	if ms := req.TimeoutMs; ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	} else if ms := rm.Runtime.TimeoutMs; ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	if timeout > maxRuntimeTimeout {
		timeout = maxRuntimeTimeout
	}

	// Snapshot the env dir before running so we can diff out the files the
	// run produced or modified.
	before := snapshotEnvDir(envDir)

	execCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	type execResult struct {
		stdout, stderr string
		exit           int
		err            error
	}
	resultCh := make(chan execResult, 1)
	start := time.Now()
	go func() {
		cmd := exec.CommandContext(execCtx, command, commandArgs...)
		cmd.Dir = envDir
		cmd.Env = pluginRuntimeEnv(slug, envDir)
		// Final backstop: if a grandchild inherits the stdout/stderr
		// pipes and outlives the kill, don't let cmd.Run() block the
		// HTTP handler forever waiting on the pipe copy.
		cmd.WaitDelay = 10 * time.Second
		configureRuntimeCmd(cmd)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		exit := 0
		if runErr != nil {
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				exit = ee.ExitCode()
			} else {
				exit = -1
			}
		}
		resultCh <- execResult{stdout: stdout.String(), stderr: stderr.String(), exit: exit, err: runErr}
	}()

	res := <-resultCh
	durationMs := int(time.Since(start) / time.Millisecond)

	status := "completed"
	if res.exit != 0 || res.err != nil {
		status = "failed"
		if errors.Is(res.err, context.DeadlineExceeded) {
			status = "timeout"
		}
	}

	// Ingest new/changed files into the plugin's artifact store.
	artifacts, ingestErr := h.ingestRunArtifacts(slug, envDir, before)
	if ingestErr != nil {
		res.stderr += "\n[artifact ingest error] " + ingestErr.Error()
	}

	// Append a compact record to runs.json (best-effort).
	if err := appendRunRecord(slug, pluginRunRecord{
		ID:         generateArtifactID(),
		Status:     status,
		ExitCode:   res.exit,
		DurationMs: durationMs,
		Artifacts:  len(artifacts),
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		slog.Warn("user plugin run: failed to append run record", "slug", slug, "error", err)
	}

	writeJSON(w, http.StatusOK, runUserPluginResponse{
		Status:     status,
		ExitCode:   res.exit,
		Stdout:     res.stdout,
		Stderr:     res.stderr,
		DurationMs: durationMs,
		Artifacts:  artifacts,
	})
}

// snapshotEnvDir records filename → modtime for the top-level files in dir.
// Directories and read errors are skipped; a missing dir yields an empty map.
func snapshotEnvDir(dir string) map[string]time.Time {
	out := make(map[string]time.Time)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out[e.Name()] = info.ModTime()
	}
	return out
}

// ingestRunArtifacts diffs the env dir against the pre-run snapshot and
// copies every new/changed file (except entry.py) into the plugin's
// artifacts/ store, reusing the same index the upload/list endpoints use.
// Type mapping mirrors kindFromName: png/svg → image, html → html, else →
// file. The ArtifactGallery then renders them with no extra plumbing.
func (h *Handler) ingestRunArtifacts(slug, envDir string, before map[string]time.Time) ([]ArtifactMeta, error) {
	after := snapshotEnvDir(envDir)

	// Collect changed names in a stable order (map iteration is random).
	changed := make([]string, 0, len(after))
	for name, mt := range after {
		if !isIngestableName(name) {
			continue
		}
		if prev, existed := before[name]; existed && prev.Equal(mt) {
			continue
		}
		changed = append(changed, name)
	}
	if len(changed) == 0 {
		return []ArtifactMeta{}, nil
	}

	artDir, err := pluginArtifactDir(slug)
	if err != nil {
		return []ArtifactMeta{}, err
	}
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		return []ArtifactMeta{}, fmt.Errorf("mkdir artifact dir: %w", err)
	}

	items, err := loadArtifactIndex(artDir)
	if err != nil {
		return []ArtifactMeta{}, err
	}

	// A run can emit multiple files within the same nanosecond, so derive
	// unique IDs from a monotonically offset base instead of calling
	// generateArtifactID() in a tight loop.
	baseNano := time.Now().UnixNano()
	produced := make([]ArtifactMeta, 0, len(changed))
	for i, name := range changed {
		srcData, rerr := os.ReadFile(filepath.Join(envDir, name))
		if rerr != nil {
			continue
		}
		kind := kindFromName(name)
		artType := "file"
		switch kind {
		case "png", "svg":
			artType = "image"
		case "html":
			artType = "html"
		}
		ext := filepath.Ext(name)
		id := fmt.Sprintf("%x", baseNano+int64(i))
		destName := id + ext
		if werr := os.WriteFile(filepath.Join(artDir, destName), srcData, 0o644); werr != nil {
			return produced, fmt.Errorf("write artifact %s: %w", name, werr)
		}
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = mimeForKind(kind)
		}
		meta := storedArtifact{
			ArtifactMeta: ArtifactMeta{
				ID:        id,
				Type:      artType,
				Title:     name,
				MimeType:  mimeType,
				Size:      int64(len(srcData)),
				URL:       fmt.Sprintf("/api/user-plugins/%s/artifacts/%s/raw", slug, id),
				CreatedAt: time.Now().UTC(),
			},
			FileName: destName,
		}
		items = append(items, meta)
		produced = append(produced, meta.ArtifactMeta)
	}

	if err := saveArtifactIndex(artDir, items); err != nil {
		return produced, err
	}
	return produced, nil
}

// loadRunHistory reads runs.json; a missing file returns an empty slice.
func loadRunHistory(path string) ([]pluginRunRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read runs: %w", err)
	}
	var records []pluginRunRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse runs: %w", err)
	}
	return records, nil
}

// appendRunRecord appends a record to runs.json, trims to the most recent
// maxRunHistory, and writes atomically (tmp + rename).
func appendRunRecord(slug string, rec pluginRunRecord) error {
	path, err := pluginRunsPath(slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir plugin dir: %w", err)
	}
	records, err := loadRunHistory(path)
	if err != nil {
		return err
	}
	records = append(records, rec)
	if len(records) > maxRunHistory {
		records = records[len(records)-maxRunHistory:]
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runs: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp runs: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename runs: %w", err)
	}
	return nil
}
