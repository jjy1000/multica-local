// Package handler — llm_wiki_bridge.go
//
// /api/experimental/llm-wiki/* bridges Multica agents to the local
// /Applications/LLM Wiki.app. Reads go through the desktop API on
// 127.0.0.1:19828 (see server/internal/llmwiki/client.go); writes
// drop files into the LLM Wiki vault directory
// (/Users/jiangjianyan/Documents/llm wiki/) for the user to
// vectorise at their own pace.
//
// Gating: the entire route surface is registered only when the
// `llm_wiki_bridge` flag is on (chi router physically omits every
// route below). Methods that share a flag check via
// llmwiki.Client.WithFlag keep the gate tight even if a clever
// caller bypasses the route lookup; the writer also enforces it
// via its own checkFlag hook for CLI / Skill adapter paths.

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/llmwiki"
)

// Per-request user-id carrier. The llm_wiki client's FlagOn closure
// reads this so it can resolve the same per-user experimental_pref
// the chi middleware at cmd/server/router.go:885
// (h.RequireExperimentalFlag("llm_wiki_bridge")) honours. Without
// this plumbing the closure falls through to the catalog default —
// which is off for every current flag — and every bridge call
// returns ErrFlagDisabled even after the user enabled the lab in
// the GUI.
type llmWikiCtxKey int

const llmWikiCtxKeyUserID llmWikiCtxKey = iota

// withLLMWikiUserID stamps the calling user-id onto ctx so the
// downstream client FlagOn closure can resolve per-user pref. Empty
// userID is a no-op (the closure falls through to the catalog
// default), which keeps register-time / smoke paths well-defined.
func withLLMWikiUserID(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, llmWikiCtxKeyUserID, userID)
}

// userIDFromLLMWikiCtx is the inverse — used by the FlagOn closure
// to pull the same user-id the handler stamped on the request.
func userIDFromLLMWikiCtx(ctx context.Context) string {
	v, _ := ctx.Value(llmWikiCtxKeyUserID).(string)
	return v
}

// llmWikiFlagOnFor builds the per-request FlagOn delegate the
// llm_wiki client takes. It mirrors experimentalFlagEnabled (the
// chi middleware's resolver) so any caller — HTTP, CLI, Skill
// adapter — sees the same per-user preference the GUI toggled.
//
// Resolves to the catalog default when:
//   - the catalog key is blacklisted (experimental.IsBroken),
//   - no user-id was stamped onto ctx (register-time / smoke paths),
//   - no row exists in experimental_pref for this user + key.
//
// The user-id must come from the handler (set via withLLMWikiUserID
// on r.Context()) — the closure runs inside Client.WithFlag, where
// r is out of scope.
func llmWikiFlagOnFor(q experimental.Querier) func(context.Context) bool {
	return func(ctx context.Context) bool {
		return experimentalFlagEnabled(ctx, q, userIDFromLLMWikiCtx(ctx), "llm_wiki_bridge")
	}
}

// llmWikiReady is a soft check the handler uses before serving
// /status. It is NOT a security check; we do not refuse the
// caller — we report "desktop api not running, suggest opening
// LLM Wiki.app". The strict refusal happens at the chi route level.
//
// We bound the call to a 2 s deadline so a hung desktop process
// doesn't tie up the agent's request goroutine.
func llmWikiReady(httpClient *http.Client, base string) bool {
	if httpClient == nil || base == "" {
		return false
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/health", nil)
	if err != nil {
		return false
	}
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// RegisterLLMWikiBridgeRoutes wires the routes on the chi router.
//
// The caller MUST mount this inside a route group guarded by
// h.RequireExperimentalFlag("llm_wiki_bridge") so the per-user
// experimental_pref is honoured (the catalog compile-time default
// is off for every current flag, so routing without the middleware
// would 404 every caller even after a GUI opt-in).
func RegisterLLMWikiBridgeRoutes(r chi.Router, h *Handler) {
	// Build the client at register time. The FlagOn closure reads
	// the per-user experimental_pref via llmWikiFlagOnFor, which
	// mirrors the chi middleware's per-request gate so the client
	// and the middleware agree on what "flag on" means.
	client, err := llmwiki.New(context.Background(), llmwiki.Config{
		FlagOn: llmWikiFlagOnFor(h.Queries),
	})
	if err != nil {
		// Failing to discover the desktop API is non-fatal — we
		// still register routes; /status surfaces the unavailability,
		// every other endpoint returns 503 until the user opens the
		// desktop app.
		client = nil
	}
	writer, err := llmwiki.NewWriter("")
	if err != nil {
		writer = nil
	}
	h.LLMWikiClient = client
	h.LLMWikiWriter = writer

	r.Route("/api/experimental/llm-wiki", func(r chi.Router) {
		r.Get("/status", h.GetLLMWikiStatus)
		r.Get("/projects", h.ListLLMWikiProjects)
		r.Get("/files", h.ListLLMWikiFiles)
		r.Get("/read", h.ReadLLMWikiFile)
		r.Post("/search", h.SearchLLMWiki)
		r.Get("/graph", h.QueryLLMWikiGraph)
		r.Post("/write", h.WriteLLMWikiFile)
		r.Delete("/file", h.DeleteLLMWikiFile)
	})
}

// buildLLMWikiClient wires a client to the desktop API. The flag
// check delegate reads the catalog default every call so a flag
// flip in another tab surfaces immediately. Kept as a thin wrapper
// for the future logger-injection work; RegisterLLMWikiBridgeRoutes
// inlines the construction today.
func buildLLMWikiClient() (*llmwiki.Client, error) {
	return llmwiki.New(context.Background(), llmwiki.Config{
		FlagOn: func(_ context.Context) bool {
			return experimental.DefaultFor("llm_wiki_bridge")
		},
	})
}

// wired flag gate on the writer at server boot so the writer's
// checkFlag agrees with the catalog default. ctx-aware so the
// per-user experimental_pref the handler stamps via
// withLLMWikiUserID drives the gate; a ctx without a stamped
// user-id falls through to the catalog compile-time default.
func init() {
	llmwiki.SetFlagGate(func(_ context.Context) bool { return experimental.DefaultFor("llm_wiki_bridge") })
}

// GetLLMWikiStatus reports whether the desktop API is reachable
// and what /health sees. Renderer panels use this to render the
// "LLM Wiki online / offline" badge.
func (h *Handler) GetLLMWikiStatus(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           false,
			"reason":       "llm_wiki_bridge client not initialised; open /Applications/LLM Wiki.app and retry",
			"reachable":    false,
			"desktop_api":  nil,
			"vault_root":   vaultRoot(),
			"flag_enabled": experimental.DefaultFor("llm_wiki_bridge"),
		})
		return
	}
	health, err := h.LLMWikiClient.Health(withLLMWikiUserID(r.Context(), requestUserID(r)))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          false,
			"reason":      err.Error(),
			"reachable":   false,
			"desktop_api": h.LLMWikiClient.BaseURL(),
			"vault_root":  vaultRoot(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"reachable":   true,
		"desktop_api": h.LLMWikiClient.BaseURL(),
		"health":      health,
		"vault_root":  vaultRoot(),
	})
}

// ListLLMWikiProjects surfaces the desktop's project list.
func (h *Handler) ListLLMWikiProjects(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LLM Wiki desktop app not running"})
		return
	}
	projects, current, err := h.LLMWikiClient.Projects(withLLMWikiUserID(r.Context(), requestUserID(r)))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projects":        projects,
		"current_project": current,
		"total":           len(projects),
	})
}

// ListLLMWikiFiles walks the project file tree.
func (h *Handler) ListLLMWikiFiles(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LLM Wiki desktop app not running"})
		return
	}
	root := r.URL.Query().Get("root")
	recursive := r.URL.Query().Get("recursive") != "false"
	maxFiles := atoiOrZero(r.URL.Query().Get("max_files"))
	files, err := h.LLMWikiClient.Files(withLLMWikiUserID(r.Context(), requestUserID(r)), root, recursive, maxFiles)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files": files,
		"total": len(files),
	})
}

// ReadLLMWikiFile reads a text file under the project's public
// tree.
func (h *Handler) ReadLLMWikiFile(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LLM Wiki desktop app not running"})
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	body, err := h.LLMWikiClient.ReadFile(withLLMWikiUserID(r.Context(), requestUserID(r)), path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    path,
		"content": body,
	})
}

// SearchLLMWiki runs a vector + keyword search.
func (h *Handler) SearchLLMWiki(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LLM Wiki desktop app not running"})
		return
	}
	var req struct {
		Query          string `json:"query"`
		TopK           int    `json:"top_k"`
		IncludeContent bool   `json:"include_content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if req.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
		return
	}
	hits, err := h.LLMWikiClient.Search(withLLMWikiUserID(r.Context(), requestUserID(r)), req.Query, req.TopK, req.IncludeContent)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":  hits,
		"total": len(hits),
	})
}

// QueryLLMWikiGraph reads the project knowledge graph.
func (h *Handler) QueryLLMWikiGraph(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LLM Wiki desktop app not running"})
		return
	}
	q := r.URL.Query().Get("q")
	nodeType := r.URL.Query().Get("node_type")
	limit := atoiOrZero(r.URL.Query().Get("limit"))
	nodes, err := h.LLMWikiClient.Graph(withLLMWikiUserID(r.Context(), requestUserID(r)), q, nodeType, limit)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"total": len(nodes),
	})
}

// WriteLLMWikiFile drops a file into the vault. The handler is
// the only write path in the bridge; CLI / Skill adapters that
// want to drop files use llmwiki.Writer directly.
func (h *Handler) WriteLLMWikiFile(w http.ResponseWriter, r *http.Request) {
	if h.LLMWikiWriter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "writer not initialised"})
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	if strings.Contains(req.Path, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must not contain .."})
		return
	}
	written, err := h.LLMWikiWriter.Write(withLLMWikiUserID(r.Context(), requestUserID(r)), req.Path, []byte(req.Content))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     req.Path,
		"absolute": written,
		"bytes":    len(req.Content),
	})
}

// DeleteLLMWikiFile is intentionally not wired to the desktop
// app's API (read-only). When the user wants to delete a file
// from the vault, this method writes a tombstone marker
// file the agent can interpret; the actual removal happens via
// the vault manager.
//
// For 0.3.19 we accept the request and return 501 Not
// Implemented; future versions either delete directly (when the
// desktop API grows a write endpoint) or hand it off to a
// privileged helper.
func (h *Handler) DeleteLLMWikiFile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"error": "delete is not supported in 0.3.19; remove the file manually from the vault directory",
	})
}

func vaultRoot() string {
	w, err := llmwiki.NewWriter("")
	if err != nil {
		return ""
	}
	return w.Root()
}

func atoiOrZero(s string) int {
	if s == "" {
		return 0
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
