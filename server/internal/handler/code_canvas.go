// Package handler — code_canvas.go (0.5.18 M4)
//
// POST /api/experimental/code-canvas/issues/{issueId}/artifacts
// GET  /api/experimental/code-canvas/issues/{issueId}/artifacts
//
// Issue-bound code_canvas output panel. The desktop subprocess-manager
// owns the stdlib-only Python service (apps/desktop/vendor/code-canvas/
// run.sh); the Go server renders a pasted snippet through its loopback
// /render endpoint, then persists the self-contained HTML canvas so the
// LabOutputPanel can show it after the fact (pre-M4 rendering was
// ephemeral and issue-unbound).
//
// Hard rules (matching forecast_issue.go):
//
//  1. Both routes are gated by experimental.DefaultFor("code_canvas")
//     in router.go — when the flag is off the routes physically do not
//     exist, so off-flag callers see a 404 rather than a misleading
//     response.
//
//  2. The issue is resolved through loadIssueForUser (identifier-or-UUID
//     + workspace-membership scope). A foreign-workspace UUID is a 404
//     and never leaks issue data.
//
//  3. The handler never spawns the code_canvas subprocess; it reads the
//     loopback URL from the registry. When the URL is unregistered the
//     POST returns 503 "code_canvas service not ready".
//
//  4. The rendered HTML is a loopback-produced, already-escaped canvas
//     (run.sh html.escape()s every code fragment). We trust it but cap
//     its length defensively (1 MiB) before persisting.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// codeCanvasMaxCodeBytes aligns with MAX_CODE_BYTES in
	// apps/desktop/vendor/code-canvas/run.sh.
	codeCanvasMaxCodeBytes = 200_000
	// codeCanvasMaxHTMLBytes is a defensive cap on the rendered canvas;
	// a runaway renderer must not be allowed to balloon a DB row.
	codeCanvasMaxHTMLBytes = 1_000_000
	// codeCanvasDefaultLanguage is applied when the caller omits `language`.
	codeCanvasDefaultLanguage = "text"
	codeCanvasDefaultLimit    = 20
	codeCanvasMaxLimit        = 100
)

// RegisterCodeCanvasRoutes wires the issue-bound render + history
// endpoints onto the supplied chi router. The caller MUST gate this on
// experimental.DefaultFor("code_canvas") — off-flag the routes vanish.
//
// Both routes are literal paths sharing one {issueId} param; there is no
// competing literal-vs-param ordering concern (0.3.45.8 lesson) because
// POST and GET differ by method, not by segment.
func RegisterCodeCanvasRoutes(r chi.Router, h *Handler) {
	r.Post("/api/experimental/code-canvas/issues/{issueId}/artifacts", h.codeCanvasCreateArtifact)
	r.Get("/api/experimental/code-canvas/issues/{issueId}/artifacts", h.codeCanvasListArtifacts)
}

// validateCodeCanvasInput normalises the render request body. Exported as
// a small pure function so the empty / oversize rules can be unit-tested
// without touching the HTTP or DB plumbing. The code itself is returned
// byte-for-byte (whitespace may be significant for the target language);
// only the emptiness check trims.
func validateCodeCanvasInput(code, language string) (string, string, error) {
	if strings.TrimSpace(code) == "" {
		return "", "", fmt.Errorf("code is required")
	}
	if len(code) > codeCanvasMaxCodeBytes {
		return "", "", fmt.Errorf("code exceeds %d bytes", codeCanvasMaxCodeBytes)
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = codeCanvasDefaultLanguage
	}
	return code, language, nil
}

// codeCanvasLoopbackURL returns the manager-registered loopback base URL
// for the code_canvas subprocess, or "" when it is not up. Mirrors the
// read in forecast_issue.go::sourceForForecast.
func codeCanvasLoopbackURL() string {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	if reg == nil {
		return ""
	}
	return reg.LoopbackURL("code_canvas")
}

// renderCodeCanvas POSTs {code, language} to the code_canvas /render
// endpoint and returns the self-contained HTML canvas body. A non-2xx,
// transport error, or timeout returns a non-nil error; the returned HTML
// is the already-escaped canvas the loopback produced.
func renderCodeCanvas(ctx context.Context, baseURL, code, language string) (string, error) {
	payload, err := json.Marshal(map[string]string{"code": code, "language": language})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/render", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 5 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("render failed: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if len(body) > codeCanvasMaxHTMLBytes {
		return "", fmt.Errorf("render failed: response too large")
	}
	return string(body), nil
}

type codeCanvasCreateRequest struct {
	Code     string `json:"code"`
	Language string `json:"language"`
}

// codeCanvasArtifact is the wire shape for both the create and list
// endpoints. pgtype UUID / timestamptz are converted to RFC3339 strings
// so the renderer never has to parse a raw pgtype value.
type codeCanvasArtifact struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	IssueID     string `json:"issue_id"`
	Code        string `json:"code"`
	Language    string `json:"language"`
	HTML        string `json:"html"`
	CreatedAt   string `json:"created_at"`
}

func codeCanvasArtifactFromRow(row db.CodeCanvasArtifact) codeCanvasArtifact {
	return codeCanvasArtifact{
		ID:          uuidToString(row.ID),
		WorkspaceID: uuidToString(row.WorkspaceID),
		IssueID:     uuidToString(row.IssueID),
		Code:        row.Code,
		Language:    row.Language,
		HTML:        row.Html,
		CreatedAt:   row.CreatedAt.Time.Format(time.RFC3339),
	}
}

// codeCanvasCreateArtifact serves
// POST /api/experimental/code-canvas/issues/{issueId}/artifacts.
// Renders the pasted snippet through the loopback /render endpoint and
// persists the canvas, returning the full row with 201.
func (h *Handler) codeCanvasCreateArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueId"))
	if !ok {
		return
	}

	var req codeCanvasCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	code, language, err := validateCodeCanvasInput(req.Code, req.Language)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	baseURL := codeCanvasLoopbackURL()
	if baseURL == "" {
		writeError(w, http.StatusServiceUnavailable, "code_canvas service not ready")
		return
	}

	html, err := renderCodeCanvas(r.Context(), baseURL, code, language)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	row, err := h.Queries.CreateCodeCanvasArtifact(r.Context(), db.CreateCodeCanvasArtifactParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		Code:        code,
		Language:    language,
		Html:        html,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist artifact: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, codeCanvasArtifactFromRow(row))
}

// codeCanvasListArtifacts serves
// GET /api/experimental/code-canvas/issues/{issueId}/artifacts?limit=N.
// Returns persisted canvases for the bound issue, newest first.
func (h *Handler) codeCanvasListArtifacts(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueId"))
	if !ok {
		return
	}

	limit := codeCanvasDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= codeCanvasMaxLimit {
			limit = n
		}
	}

	rows, err := h.Queries.ListCodeCanvasArtifactsByIssue(r.Context(), db.ListCodeCanvasArtifactsByIssueParams{
		IssueID: issue.ID,
		Limit:   int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list artifacts: "+err.Error())
		return
	}

	out := make([]codeCanvasArtifact, 0, len(rows))
	for _, row := range rows {
		out = append(out, codeCanvasArtifactFromRow(row))
	}
	writeJSON(w, http.StatusOK, out)
}
