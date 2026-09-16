// Package handler — claude_science_runtime.go
//
// /api/experimental/claude-science-runtime/* executes Python code on
// behalf of claude_science agents and surfaces emitted artifacts.
// Lives under a Labs flag (catalog.go::claude_science_runtime).
// When the flag is off the chi router physically omits every route
// here, so no caller reaches the function bodies without an explicit
// opt-in.
//
// Hard constraints:
//
//  1. Code execution uses `python3 -I` (isolated mode, no site-
//     packages from PYTHONPATH) with a captured stdin of the snippet
//     plus a `cwd` of ~/.multica/experimental/claude-science/runtime/
//     <session_uuid>/. We do NOT add network egress allowlists here
//     — any defensive sandboxing belongs in the future "Lab sandbox"
//     subsystem (memory: next-code-environment-library).
//
//     1a. The subprocess gets runtimeSessionEnv's explicit allowlist
//     (PATH, HOME pinned to the session dir, UTF-8 locale) and NOT the
//     server's os.Environ(). A nil cmd.Env inherits everything, which
//     pre-0.5.107 handed an agent-authored snippet the daemon-injected
//     MULTICA_API_TOKEN, the JWT secret, DATABASE_URL and the real HOME
//     (→ ~/.multica/profiles/<name>/config.json). `-I` isolates
//     site-packages, never the environment.
//
//  2. Timeout: 30s default, 120s ceiling. Expired sessions are GC'd
//     by runtime_gc.go after 30 days. execute with session_id= targets
//     an existing session's working directory; an expired root
//     returns ErrSessionExpired (410).
//
//  2a. Session continuation (0.5.106): execute accepts session_id to
//     reuse a prior run's working directory — files written by earlier
//     runs stay visible, which is the notebook-style filesystem state.
//     The working directory is always <runtimeBaseDir>/<root session
//     id>/ and artifact row paths are anchored at that same name (the
//     pre-0.5.106 code anchored them at the per-run DB row id, which
//     never matched the on-disk uuid dir, so artifact bytes 404'd).
//
//  3. The handler never serves artifact bytes from disk without
//     first confirming session.workspace_id == caller's workspace
//     via h.workspaceMember — same defense pattern as
//     handler/claude_science_skills.go uses for visibility filtering.

package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// runtimeBaseDir is the parent directory for every session's
	// working directory. Sessions are isolated under
	// <runtimeBaseDir>/<session_uuid>/ so artifacts, stdout capture,
	// and any temp files do not collide between concurrent runs.
	runtimeBaseDir = ".multica/experimental/claude-science/runtime"

	// maxRuntimeCodeBytes bounds the agent-authored snippet length.
	// 64 KiB keeps a typical sklearn / pandas experiment readable
	// but blocks accidental file-drops.
	maxRuntimeCodeBytes = 64 * 1024

	// defaultRuntimeTimeout and maxRuntimeTimeout bound the exec
	// context.
	defaultRuntimeTimeout = 30 * time.Second
	maxRuntimeTimeout     = 120 * time.Second

	// maxRuntimeArtifactBytes bounds one ingested artifact. Ingest
	// reads each candidate fully into memory to hash it, so an
	// uncapped file is an out-of-memory the snippet controls; a run
	// emitting anything this large is an accidental file drop (a
	// serialized model, a full dataset dump), not a result.
	maxRuntimeArtifactBytes = 32 << 20
)

// ErrSessionExpired is returned when a caller tries to re-run or
// fetch artifacts from a session whose expires_at < now().
var ErrSessionExpired = errors.New("session expired")

// RuntimeExecuteRequest is the POST body for /runtime/execute.
type RuntimeExecuteRequest struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	IssueID     string `json:"issue_id,omitempty"`
	Language    string `json:"language,omitempty"`
	Code        string `json:"code"`
	TimeoutMs   int    `json:"timeout_ms,omitempty"`
	// SessionID optionally names an existing session whose working
	// directory this run should reuse (0.5.106 notebook-style
	// continuation: files written by earlier runs in the same
	// workspace are visible to this one). The referenced session must
	// belong to the same workspace and must not be expired.
	SessionID string `json:"session_id,omitempty"`
}

// RuntimeExecuteResponse is the wire shape returned to the client.
type RuntimeExecuteResponse struct {
	SessionID  string                `json:"session_id"`
	Status     string                `json:"status"`
	ExitCode   int                   `json:"exit_code"`
	Stdout     string                `json:"stdout"`
	Stderr     string                `json:"stderr"`
	DurationMs int                   `json:"duration_ms"`
	Artifacts  []RuntimeArtifactStub `json:"artifacts"`
	// RootSessionID is the session whose working directory this run
	// used. Passing it back as session_id continues in the same
	// workspace directory.
	RootSessionID string `json:"root_session_id"`
}

// RuntimeArtifactStub is the per-file metadata returned by execute +
// the artifact listing endpoint. Bytes are NOT inlined; the caller
// fetches the content via /artifacts/:id after permissioning.
type RuntimeArtifactStub struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
}

// runtimeArtifactsResponse is the listing shape.
type runtimeArtifactsResponse struct {
	Artifacts []RuntimeArtifactStub `json:"artifacts"`
	Total     int                   `json:"total"`
}

// RegisterClaudeScienceRuntimeRoutes wires up the runtime endpoints
// on the supplied chi router. The caller (router.go) MUST gate the
// entire call on experimental.DefaultFor("claude_science_lab")
// (0.3.22 consolidation: the runtime sandbox is one capability of
// the new `claude_science_lab` flag, replacing the 0.3.20
// `claude_science_runtime` flag). The routes physically disappear
// when the flag is off — that is the 0.3.6 hard rule #1 enforcement
// point (no surface import / init for off-by-default flags).
func RegisterClaudeScienceRuntimeRoutes(r chi.Router, h *Handler) {
	r.Route("/api/experimental/claude-science-runtime", func(r chi.Router) {
		r.Post("/execute", h.PostClaudeScienceRuntimeExecute)
		r.Get("/sessions", h.ListClaudeScienceRuntimeSessions)
		r.Get("/sessions/by-issue", h.ListClaudeScienceRuntimeSessionsByIssue)
		r.Get("/sessions/{sessionID}", h.GetClaudeScienceRuntimeSession)
		r.Get("/sessions/{sessionID}/artifacts", h.ListClaudeScienceRuntimeArtifacts)
		r.Get("/artifacts/{artifactID}", h.GetClaudeScienceRuntimeArtifactBytes)
		r.Delete("/sessions/{sessionID}", h.DeleteClaudeScienceRuntimeSession)
	})
}

// PostClaudeScienceRuntimeExecute is the entry point invoked by the
// Skill adapter and the renderer panel.
func (h *Handler) PostClaudeScienceRuntimeExecute(w http.ResponseWriter, r *http.Request) {
	var req RuntimeExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if req.Language == "" {
		req.Language = "python"
	}
	if req.Language != "python" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only python is supported"})
		return
	}
	if req.WorkspaceID == "" || req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and agent_id are required"})
		return
	}
	if len(req.Code) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
		return
	}
	if len(req.Code) > maxRuntimeCodeBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("code exceeds %d bytes", maxRuntimeCodeBytes)})
		return
	}

	timeout := defaultRuntimeTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
		if timeout > maxRuntimeTimeout {
			timeout = maxRuntimeTimeout
		}
	}

	wsID, err := util.ParseUUID(req.WorkspaceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is not a UUID"})
		return
	}
	agentID, err := util.ParseUUID(req.AgentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id is not a UUID"})
		return
	}
	if _, ok := h.workspaceMember(w, r, wsID.String()); !ok {
		return
	}

	var issueID pgtype.UUID
	if req.IssueID != "" {
		parsed, err := util.ParseUUID(req.IssueID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue_id is not a UUID"})
			return
		}
		issueID = parsed
	}

	if err := probePython3(); err != nil {
		// 0.3.45.2 bug fix (P1#4): post a failure comment to the
		// originating issue so the user sees the surface fail in their
		// task timeline, not as a silent HTTP error in the lab pane.
		// Without this the user thinks "实验室又没工作" because the
		// lab pane is a transient overlay and the issue is the only
		// persistent surface.
		if issueID.Valid {
			if _, cerr := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
				IssueID:     issueID,
				AuthorType:  "agent",
				AuthorID:    agentID,
				Content:     "Claude Lab runtime 调用失败:python3 不在 PATH 上(需要 Python 3.11+)。请安装后重试。",
				Type:        "comment",
				WorkspaceID: wsID,
			}); cerr != nil {
				slog.Warn("claude-science runtime: failure comment write failed",
					"issue", issueID, "err", cerr)
			}
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "python3 not available on PATH; install Python 3.11+ and retry",
		})
		return
	}

	// 0.5.106 session continuation: when session_id is supplied, the run
	// reuses that session's working directory so files written by
	// earlier runs stay visible (notebook-style filesystem state). The
	// referenced row must exist, belong to the same workspace, and not
	// be expired (ErrSessionExpired → 410, per the documented contract).
	rootID := pgtype.UUID{}
	if req.SessionID != "" {
		parsed, err := util.ParseUUID(req.SessionID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is not a UUID"})
			return
		}
		root, err := h.Queries.GetExperimentalClaudeRuntimeSession(r.Context(), parsed)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		if root.WorkspaceID != wsID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "session belongs to another workspace"})
			return
		}
		if root.ExpiresAt.Valid && time.Now().After(root.ExpiresAt.Time) {
			writeJSON(w, http.StatusGone, map[string]string{"error": ErrSessionExpired.Error()})
			return
		}
		rootID = root.ID
	}

	// Insert the session row FIRST, then derive the working directory
	// from the ROOT session id. Artifact paths are self-describing
	// (<dirName>/<file>) and the bytes endpoint joins runtimeBaseDir +
	// row.Path, so the dir name and the row id MUST agree for fresh
	// runs. The pre-0.5.106 code created a throwaway uuid dir and built
	// RelPath from the DB row id — the two never matched, so every
	// artifact bytes fetch 404'd. Reused runs insert their own history
	// row but keep the root's directory.
	inserted, err := h.Queries.InsertExperimentalClaudeRuntimeSession(r.Context(), db.InsertExperimentalClaudeRuntimeSessionParams{
		WorkspaceID: wsID,
		AgentID:     agentID,
		IssueID:     issueID,
		Language:    req.Language,
		Code:        req.Code,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session: " + err.Error()})
		return
	}
	dirName := inserted.ID
	if rootID.Valid {
		dirName = rootID
	}
	sessionDir, err := ensureRuntimeSessionDir(dirName.String())
	if err != nil {
		// Leave the queued row for the GC sweep; nothing executed.
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session dir: " + err.Error()})
		return
	}

	snippetPath := filepath.Join(sessionDir, "snippet.py")
	if err := os.WriteFile(snippetPath, []byte(req.Code), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not write snippet: " + err.Error()})
		return
	}

	type execResult struct {
		stdout, stderr string
		exit           int
		err            error
	}
	execCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resultCh := make(chan execResult, 1)
	start := time.Now()
	go func() {
		cmd := exec.CommandContext(execCtx, "python3", "-I", snippetPath)
		cmd.Dir = sessionDir
		// A nil cmd.Env silently inherits os.Environ(), so the
		// allowlist must be set explicitly to take effect.
		cmd.Env = runtimeSessionEnv(sessionDir)
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
		resultCh <- execResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
			exit:   exit,
			err:    runErr,
		}
	}()

	res := <-resultCh
	durationMs := int(time.Since(start) / time.Millisecond)

	finalStatus := "completed"
	if res.exit != 0 || res.err != nil {
		finalStatus = "failed"
		if errors.Is(res.err, context.DeadlineExceeded) {
			finalStatus = "timeout"
		}
	}

	// Persist finished session FIRST so the response references a
	// stable row before we walk the artifact directory. The stub
	// list is built and inserted afterwards so the response only
	// contains rows the client can fetch.
	finishedAt := time.Now()
	if _, err := h.Queries.UpdateExperimentalClaudeRuntimeSessionFinished(r.Context(), db.UpdateExperimentalClaudeRuntimeSessionFinishedParams{
		ID:         inserted.ID,
		Status:     finalStatus,
		ExitCode:   pgtype.Int4{Int32: int32(res.exit), Valid: true},
		Stdout:     pgtype.Text{String: res.stdout, Valid: true},
		Stderr:     pgtype.Text{String: res.stderr, Valid: true},
		DurationMs: pgtype.Int4{Int32: int32(durationMs), Valid: true},
		StartedAt:  pgtype.Timestamptz{Time: start, Valid: true},
		FinishedAt: pgtype.Timestamptz{Time: finishedAt, Valid: true},
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not stamp session finished: " + err.Error()})
		return
	}

	// Scan + persist artifacts. RelPath is anchored at the working
	// directory's name (dirName), which is what the bytes endpoint
	// joins against — for reused sessions the DB row id would point at
	// a directory that doesn't exist.
	artifacts, err := ingestSessionArtifacts(inserted.ID, dirName.String(), sessionDir)
	if err != nil {
		res.stderr += "\n[artifact scan error] " + err.Error()
	}
	persisted := make([]db.ExperimentalRuntimeArtifact, 0, len(artifacts))
	stubs := make([]RuntimeArtifactStub, 0, len(artifacts))
	for _, a := range artifacts {
		row, ierr := h.Queries.InsertExperimentalRuntimeArtifact(r.Context(), db.InsertExperimentalRuntimeArtifactParams{
			SessionID:   inserted.ID,
			WorkspaceID: wsID,
			Name:        a.Name,
			Kind:        a.Kind,
			Bytes:       int32(a.Bytes),
			Sha256:      a.SHA256,
			Path:        a.RelPath,
		})
		if ierr != nil {
			res.stderr += "\n[artifact insert error: " + a.Name + "] " + ierr.Error()
			continue
		}
		persisted = append(persisted, row)
		stubs = append(stubs, RuntimeArtifactStub{
			ID:        row.ID.String(),
			SessionID: row.SessionID.String(),
			Name:      row.Name,
			Kind:      row.Kind,
			Bytes:     int(row.Bytes),
			SHA256:    row.Sha256,
			URL:       fmt.Sprintf("/api/experimental/claude-science-runtime/artifacts/%s", row.ID.String()),
		})
	}

	// 0.3.27 B5: write the artifact manifest to the originating issue's
	// comment thread so the user sees the artifact list inline with
	// their task timeline. We post a single comment listing every
	// artifact (or the stdout summary when no artifacts were emitted
	// and the call supplied an issue_id) — keeping the post atomic so
	// the timeline doesn't dangle with partial results.
	//
	// Why a single comment: lab comments can be reviewed at a glance,
	// and editing the comment later is not a feature the issue UI
	// exposes.
	if issueID.Valid {
		commentBody := composeArtifactSummary(stubs, req.Code, res.exit, durationMs)
		if commentBody != "" {
			_, cerr := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
				IssueID:    issueID,
				AuthorType: "agent",
				AuthorID:   agentID,
				Content:    commentBody,
				Type:       "artifact",
				ParentID:   pgtype.UUID{},
			})
			// Comment failure is non-fatal — the artifacts are
			// already persisted in their own table; we just lose
			// the inline summary.
			_ = cerr
		}
	}

	writeJSON(w, http.StatusOK, RuntimeExecuteResponse{
		SessionID:     inserted.ID.String(),
		Status:        finalStatus,
		ExitCode:      res.exit,
		Stdout:        res.stdout,
		Stderr:        res.stderr,
		DurationMs:    durationMs,
		Artifacts:     stubs,
		RootSessionID: dirName.String(),
	})
}

// composeArtifactSummary builds the inline comment body listing the
// session's artifacts. Returns "" when there is nothing useful to
// write (no artifacts, no code, non-zero exit).
func composeArtifactSummary(stubs []RuntimeArtifactStub, code string, exit, durationMs int) string {
	if len(stubs) == 0 && code == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**OpenScience 执行摘要** (%d ms, exit %d)\n\n", durationMs, exit)
	if code != "" {
		fmt.Fprintf(&b, "```python\n%s\n```\n\n", code)
	}
	if len(stubs) > 0 {
		b.WriteString("**产物清单**\n")
		for _, s := range stubs {
			fmt.Fprintf(&b, "- [%s](%s) (%s, %d bytes)\n", s.Name, s.URL, s.Kind, s.Bytes)
		}
	}
	return b.String()
}

// ListClaudeScienceRuntimeSessions returns every session for the
// caller's workspace, newest first, capped at 50.
func (h *Handler) ListClaudeScienceRuntimeSessions(w http.ResponseWriter, r *http.Request) {
	wsRaw := r.URL.Query().Get("workspace_id")
	wsID, err := util.ParseUUID(wsRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is required"})
		return
	}
	if _, ok := h.workspaceMember(w, r, wsID.String()); !ok {
		return
	}
	rows, err := h.Queries.ListExperimentalClaudeRuntimeSessionsByWorkspace(r.Context(), wsID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": rows,
		"total":    len(rows),
	})
}

// ListClaudeScienceRuntimeSessionsByIssue (0.3.45.8) powers the Claude
// Lab "产物" tab. The renderer asks for sessions bound to a specific
// issue so the user sees the snippets their own agent produced, not a
// noisy workspace-wide dump of unrelated chat sessions.
//
// IMPORTANT: this route must register BEFORE /sessions/{sessionID} on
// the chi router; otherwise `{sessionID}` captures the literal string
// "by-issue" and ParseUUID returns 400 "sessionID is not a UUID".
// See RegisterClaudeScienceRuntimeRoutes — the order is preserved.
func (h *Handler) ListClaudeScienceRuntimeSessionsByIssue(w http.ResponseWriter, r *http.Request) {
	wsRaw := r.URL.Query().Get("workspace_id")
	wsID, err := util.ParseUUID(wsRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is required"})
		return
	}
	if _, ok := h.workspaceMember(w, r, wsID.String()); !ok {
		return
	}
	issueRaw := r.URL.Query().Get("issue_id")
	issueID, err := util.ParseUUID(issueRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue_id is required"})
		return
	}
	limit := int32(20)
	if v := r.URL.Query().Get("limit"); v != "" {
		// Defensive parse: a malformed or negative value falls back to
		// the default rather than blowing up the request. The cap at
		// 100 mirrors ListExperimentalClaudeRuntimeSessionsByWorkspace
		// (50) but doubled because by-issue is the user-facing surface.
		var n int
		if _, scanErr := fmt.Sscanf(v, "%d", &n); scanErr == nil && n > 0 && n <= 100 {
			limit = int32(n)
		}
	}
	rows, err := h.Queries.ListExperimentalClaudeRuntimeSessionsByIssue(r.Context(), db.ListExperimentalClaudeRuntimeSessionsByIssueParams{
		WorkspaceID: wsID,
		IssueID:     issueID,
		Limit:       limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": rows,
		"total":    len(rows),
	})
}

// GetClaudeScienceRuntimeSession returns a single session + its
// artifacts.
func (h *Handler) GetClaudeScienceRuntimeSession(w http.ResponseWriter, r *http.Request) {
	sessionID, err := util.ParseUUID(chi.URLParam(r, "sessionID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionID is not a UUID"})
		return
	}
	row, err := h.Queries.GetExperimentalClaudeRuntimeSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if _, ok := h.workspaceMember(w, r, row.WorkspaceID.String()); !ok {
		return
	}
	artifacts, err := h.Queries.ListExperimentalRuntimeArtifactsBySession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":   row,
		"artifacts": artifacts,
	})
}

// ListClaudeScienceRuntimeArtifacts returns the artifact list for a
// session, with byte counts + sha256 + per-kind URL.
func (h *Handler) ListClaudeScienceRuntimeArtifacts(w http.ResponseWriter, r *http.Request) {
	sessionID, err := util.ParseUUID(chi.URLParam(r, "sessionID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionID is not a UUID"})
		return
	}
	row, err := h.Queries.GetExperimentalClaudeRuntimeSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if _, ok := h.workspaceMember(w, r, row.WorkspaceID.String()); !ok {
		return
	}
	artifacts, err := h.Queries.ListExperimentalRuntimeArtifactsBySession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	stubs := make([]RuntimeArtifactStub, 0, len(artifacts))
	for _, a := range artifacts {
		stubs = append(stubs, RuntimeArtifactStub{
			ID:        a.ID.String(),
			SessionID: a.SessionID.String(),
			Name:      a.Name,
			Kind:      a.Kind,
			Bytes:     int(a.Bytes),
			SHA256:    a.Sha256,
			URL:       fmt.Sprintf("/api/experimental/claude-science-runtime/artifacts/%s", a.ID.String()),
		})
	}
	writeJSON(w, http.StatusOK, runtimeArtifactsResponse{Artifacts: stubs, Total: len(stubs)})
}

// GetClaudeScienceRuntimeArtifactBytes streams the artifact's bytes
// from disk after re-verifying the workspace ACL.
func (h *Handler) GetClaudeScienceRuntimeArtifactBytes(w http.ResponseWriter, r *http.Request) {
	artifactID, err := util.ParseUUID(chi.URLParam(r, "artifactID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artifactID is not a UUID"})
		return
	}
	row, err := h.Queries.GetExperimentalRuntimeArtifact(r.Context(), artifactID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
		return
	}
	if _, ok := h.workspaceMember(w, r, row.WorkspaceID.String()); !ok {
		return
	}
	fullPath := filepath.Join(runtimeBaseDirExpanded(), row.Path)
	f, err := os.Open(fullPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact bytes missing on disk"})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mimeForKind(row.Kind))
	w.Header().Set("Cache-Control", "private, max-age=300")
	// 0.5.107 (F-006 parity): snippet output is agent-generated, so the
	// html/svg kinds must not render inline when the URL is navigated
	// to directly on the API origin. The renderer-side consumers are
	// unaffected — ArtifactImage/ArtifactIframe re-materialise the body
	// as a blob: URL and SvgInline/ChartFetch read it as text, none of
	// which consult Content-Disposition. %q escapes the basename so a
	// snippet-authored filename cannot inject response headers.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(row.Path)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// DeleteClaudeScienceRuntimeSession drops the row. Idempotent.
func (h *Handler) DeleteClaudeScienceRuntimeSession(w http.ResponseWriter, r *http.Request) {
	sessionID, err := util.ParseUUID(chi.URLParam(r, "sessionID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionID is not a UUID"})
		return
	}
	row, err := h.Queries.GetExperimentalClaudeRuntimeSession(r.Context(), sessionID)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := h.workspaceMember(w, r, row.WorkspaceID.String()); !ok {
		return
	}
	if err := h.Queries.DeleteExperimentalClaudeRuntimeSession(r.Context(), sessionID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runtimeBaseDirExpanded returns the absolute runtime base directory
// under the current user's home.
func runtimeBaseDirExpanded() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return runtimeBaseDir
	}
	return filepath.Join(home, runtimeBaseDir)
}

// ensureRuntimeSessionDir makes (or re-opens, for session continuation)
// the working directory for a session whose row id is already known:
// <runtimeBaseDir>/<sessionID>/. Reuse over an existing directory is
// the point — earlier runs' files stay visible to later runs.
func ensureRuntimeSessionDir(sessionID string) (path string, err error) {
	base := runtimeBaseDirExpanded()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", base, err)
	}
	path = filepath.Join(base, sessionID)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", path, err)
	}
	return path, nil
}

// runtimeSessionEnv is the environment the snippet subprocess runs
// with. It deliberately does NOT inherit the parent server's
// os.Environ(). The desktop server is the daemon's child and carries
// the profile-bearing HOME plus the daemon-injected MULTICA_API_TOKEN
// and the connection/JWT secrets — inheriting all of that would let
// any snippet read the user's JWT via ~/.multica/profiles/<name>/
// config.json or replay the task token against privileged endpoints.
// `python3 -I` alone is not a boundary: it isolates site-packages, not
// the environment.
//
// Same contract as pluginRuntimeEnv (handler/user_plugin_runtime.go),
// minus the MULTICA_PLUGIN_* variables — those are the plugin inline
// runtime's documented callback contract and have no meaning here.
// TMPDIR is intentionally not overridden: it stays in the shared
// /tmp so interpreter scratch files are not swept into the session
// directory and ingested as phantom artifacts.
func runtimeSessionEnv(sessionDir string) []string {
	path := strings.TrimSpace(os.Getenv("PATH"))
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{
		"PATH=" + path,
		"HOME=" + sessionDir,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
}

// probePython3 runs `python3 -I -c "pass"` and exits non-zero if
// python3 is missing. Pre-flight per memory 0.3.10 ENOENT
// prevention contract.
func probePython3() error {
	cmd := exec.Command("python3", "-I", "-c", "pass")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("python3 probe failed: %w", err)
	}
	return nil
}

// sessionArtifact is the in-memory shape produced by the artifact
// scan; the handler then persists one row per artifact.
type sessionArtifact struct {
	Name    string
	Kind    string
	Bytes   int
	SHA256  string
	RelPath string
}

// ingestSessionArtifacts scans sessionDir for files the python
// invocation emitted. dirName is the on-disk directory basename the
// bytes endpoint resolves row.Path against — the root session id, not
// necessarily this execution's row id.
func ingestSessionArtifacts(sessionID pgtype.UUID, dirName, sessionDir string) ([]sessionArtifact, error) {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("readdir: %w", err)
	}
	out := make([]sessionArtifact, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "snippet.py" {
			continue
		}
		kind := kindFromName(name)
		if kind == "" {
			continue
		}
		full := filepath.Join(sessionDir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Stat before reading: the hash below pulls the whole file
		// into memory, so an oversized emit is an OOM the snippet
		// controls. Skipped, like an unknown extension.
		if info.Size() > maxRuntimeArtifactBytes {
			slog.Warn("claude-science runtime: artifact over size cap, not ingested",
				"session_id", sessionID.String(), "name", name, "size", info.Size())
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out = append(out, sessionArtifact{
			Name:    name,
			Kind:    kind,
			Bytes:   len(data),
			SHA256:  hex.EncodeToString(sum[:]),
			RelPath: filepath.Join(dirName, name),
		})
	}
	return out, nil
}

// kindFromName maps a filename to its experimental_runtime_artifact
// .kind value. Returns "" for unknown extensions so the caller can
// skip them silently.
func kindFromName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png":
		return "png"
	case ".svg":
		return "svg"
	case ".html", ".htm":
		return "html"
	case ".json":
		return "json"
	case ".csv":
		return "csv"
	case ".md":
		return "md"
	case ".txt":
		return "txt"
	case ".log":
		return "log"
	default:
		return ""
	}
}

// mimeForKind returns a content-type for the artifact response
// header. Falls back to application/octet-stream for unknown kinds.
func mimeForKind(kind string) string {
	switch kind {
	case "png":
		return "image/png"
	case "svg":
		return "image/svg+xml"
	case "html":
		return "text/html; charset=utf-8"
	case "json":
		return "application/json"
	case "csv":
		return "text/csv; charset=utf-8"
	case "md":
		return "text/markdown; charset=utf-8"
	case "txt", "log":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
