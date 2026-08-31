// Package llmwiki talks to the locally-installed /Applications/LLM Wiki.app
// desktop API, exposing the same capability surface as the bundled
// llm-wiki MCP server but through the Rust desktop API on
// http://127.0.0.1:<port>/api/v1/.
//
// Why a direct HTTP client rather than an MCP stdio bridge?
//
//   1. The MCP server (0.4.25) embeds the same call surface and uses
//      this API as its backend; replicating the JSON-RPC would just
//      add a hop.
//   2. multica server is a Go binary. Spawing the JS MCP server and
//      driving JSON-RPC over stdio would force us to vendor @model
//      contextprotocol/sdk or take a hard dependency on a separate
//      stdio binary, which conflicts with the "Lives under Labs"
//      constraint (we'd carry a runtime that flags could not toggle
//      off).
//   3. Read-only callers — multica agents — do not need MCP
//      streaming/cancel; the desktop API is plain HTTP and is what
//      the MCP server forwards to.
//
// Wiring:
//
//   - The 19827 / 19828 ports are picked up at runtime via lsof on
//     `llm-wiki`. The first loopback port that responds with the LLM
//     Wiki health JSON wins.
//   - Auth: bearer token. The desktop app (v0.6.x) stores a
//     long-lived token in its Tauri app-state
//     (~/Library/Application Support/com.llmwiki.app/app-state.json,
//     apiConfig.token); older builds wrote a standalone auth.json.
//     DiscoverToken auto-detects every layout, plus the env var
//     LLM_WIKI_API_TOKEN and Multica's own store
//     (~/.multica/llm-wiki.json — see token_store.go).
//   - Flag gate: every caller in this package is wrapped via
//     (*Client).WithFlag(ctx, "llm_wiki_bridge"). If the flag is off
//     the helper returns a sentinel error
//     ErrFlagDisabled; the HTTP handler maps it to a 403.
//
// Concurrency:
//
//   - One *Client per process; the client holds a single
//     http.Client with a 45-second timeout (the /graph endpoint on
//     a mature vault takes ~20s). Requests are safe for
//     concurrent use.
//   - listFiles / search etc. cap the response at topK / maxFiles
//     the upstream permits; we surface upstream clamps as-is.
package llmwiki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrFlagDisabled is returned when a caller invokes a method without
// first checking the llm_wiki_bridge flag. The HTTP layer maps this
// to 403; the Skill adapter surfaces it as a polite refusal.
var ErrFlagDisabled = errors.New("llm_wiki_bridge flag is off")

// ErrUnauthorized is returned when the desktop API rejects the bearer
// token (expired / rotated / not configured).
var ErrUnauthorized = errors.New("llm_wiki unauthorized")

// DefaultAPIPath is the suffix the LLM Wiki desktop API exposes.
// Used in conjunction with the per-port base URL picked up at
// New().
const DefaultAPIPath = "/api/v1"

// Client is a thin HTTP wrapper around the LLM Wiki desktop API.
// Construct one per process via New(); share via the global helper.
type Client struct {
	mu        sync.RWMutex
	baseURL   string
	token     string
	http      *http.Client
	flagOn    func(ctx context.Context) bool
}

// Config bundles the knobs New() needs. Healthy defaults are filled
// in for any field left zero.
type Config struct {
	// BaseURL is the loopback base of the LLM Wiki desktop API, e.g.
//	"http://127.0.0.1:19828". Empty means probe at New().
	BaseURL string
	// Token is the bearer token the desktop app issued; empty
	// means read from disk via DiscoverToken.
	Token string
	// HTTPTimeout bounds each request; 45s default. Measured on a
	// 170-source vault: /graph takes ~20s, so the original 5s default
	// (tuned when the vault was a handful of pages) timed out every
	// graph query. Everything is localhost; the cost of a generous
	// ceiling is only a slower failure when the app is wedged.
	HTTPTimeout time.Duration
	// FlagOn is the Labs flag gate. Required — every method calls
	// it before issuing a network request. Pass
	// experimental.DefaultFor as the implementation.
	FlagOn func(ctx context.Context) bool
}

// New constructs a Client. Discovers BaseURL / Token when the
// matching Config field is empty. Returns an error when the LLM
// Wiki desktop app is not running (no probe response).
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.FlagOn == nil {
		return nil, errors.New("llmwiki.New: FlagOn is required")
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	c := &Client{
		token:  cfg.Token,
		http:   &http.Client{Timeout: timeout},
		flagOn: cfg.FlagOn,
	}

	if cfg.BaseURL == "" {
		base, err := DiscoverBaseURL(ctx, c.http)
		if err != nil {
			return nil, fmt.Errorf("could not locate LLM Wiki desktop API: %w", err)
		}
		c.baseURL = normalizeBaseURL(base)
	} else {
		c.baseURL = normalizeBaseURL(cfg.BaseURL)
	}

	if c.token == "" {
		tok, err := DiscoverToken(ctx)
		if err != nil {
			// Token missing is non-fatal at construction — the
			// first request will surface ErrUnauthorized and the
			// caller can surface the issue to the user.
			tok = ""
		}
		c.token = tok
	}
	return c, nil
}

// WithFlag returns a sentinel error when the flag is off. Callers
// chain this at the start of every method so the HTTP handler
// stays clean.
func (c *Client) WithFlag(ctx context.Context) error {
	if c.flagOn == nil || !c.flagOn(ctx) {
		return ErrFlagDisabled
	}
	return nil
}

// BaseURL returns the loopback base URL the client is bound to
// (e.g. "http://127.0.0.1:19828"). The HTTP handler uses it to
// surface the desktop API endpoint in the /status response so the
// renderer can show "API 端口" without a second probe call.
// Empty string means the client never resolved a port.
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
}

// Health calls /api/v1/health on the desktop API.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	if err := c.WithFlag(ctx); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.getJSON(ctx, "/health", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Projects lists known LLM Wiki projects; includes the active
// project when one is open in the desktop app.
func (c *Client) Projects(ctx context.Context) ([]Project, *Project, error) {
	if err := c.WithFlag(ctx); err != nil {
		return nil, nil, err
	}
	var raw struct {
		Projects      []Project `json:"projects"`
		CurrentProject *Project  `json:"currentProject"`
	}
	if err := c.getJSON(ctx, "/projects", nil, &raw); err != nil {
		return nil, nil, err
	}
	return raw.Projects, raw.CurrentProject, nil
}

// Search executes a vector + keyword search against the active
// project. topK <= 0 falls back to the upstream clamp (typically 10).
//
// 0.5.92: the v0.6.x app returns its hits under `results`
// (vectorHits/tokenHits/graphHits are additive side-channels); the
// 0.4.x-era shape decoded `hits` and silently produced zero results
// even once the /api/v1 prefix fix made the request land. Decode
// both, new key first.
func (c *Client) Search(ctx context.Context, query string, topK int, includeContent bool) ([]SearchHit, error) {
	if err := c.WithFlag(ctx); err != nil {
		return nil, err
	}
	body := map[string]any{
		"query":          query,
		"topK":           topK,
		"includeContent": includeContent,
	}
	var raw struct {
		Results []SearchHit `json:"results"`
		Hits    []SearchHit `json:"hits"`
	}
	if err := c.postJSON(ctx, "/projects/current/search", body, &raw); err != nil {
		return nil, err
	}
	if raw.Results != nil {
		return raw.Results, nil
	}
	return raw.Hits, nil
}

// Files lists the project tree, optionally rooted at wiki / sources
// / all.
//
// 0.5.92 drift fixes against the v0.6.x app:
//   - `recursive` is now ALWAYS sent explicitly. Omitting the param
//     makes the app default to recursive=true, and a listing that
//     crosses its hard cap (2000 entries) is a 413 ERROR, not the
//     truncation the 0.4.x-era code assumed.
//   - When a recursive listing does hit that cap, Files retries
//     non-recursively so callers get at least the top-level tree
//     instead of a bare error; drill-down stays available via
//     narrower root= calls.
func (c *Client) Files(ctx context.Context, root string, recursive bool, maxFiles int) ([]FileNode, error) {
	if err := c.WithFlag(ctx); err != nil {
		return nil, err
	}
	if root == "" {
		root = "wiki"
	}
	list := func(rec bool) ([]FileNode, error) {
		q := url.Values{}
		q.Set("root", root)
		// Explicit either way — omission means recursive on v0.6.x.
		if rec {
			q.Set("recursive", "true")
		} else {
			q.Set("recursive", "false")
		}
		if maxFiles > 0 {
			q.Set("maxFiles", fmt.Sprintf("%d", maxFiles))
		}
		var raw struct {
			Files     []FileNode `json:"files"`
			Truncated bool       `json:"truncated"`
		}
		if err := c.getJSON(ctx, "/projects/current/files?"+q.Encode(), nil, &raw); err != nil {
			return nil, err
		}
		return raw.Files, nil
	}
	files, err := list(recursive)
	if err == nil || !recursive || !strings.Contains(err.Error(), "exceeds maxFiles") {
		return files, err
	}
	// Cap cliff on the recursive walk — degrade to the top level.
	return list(false)
}

// ReadFile reads a text file under the project's public tree.
// The desktop API rejects paths under /.llm-wiki/ — ReadFile
// surfaces the 404 as ErrUnauthorized-of-sorts (the desktop app
// disallows reading the agent-private namespace).
func (c *Client) ReadFile(ctx context.Context, relPath string) (string, error) {
	if err := c.WithFlag(ctx); err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("path", relPath)
	var raw struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.getJSON(ctx, "/projects/current/files/content?"+q.Encode(), nil, &raw); err != nil {
		return "", err
	}
	return raw.Content, nil
}

// Graph queries the project knowledge graph.
func (c *Client) Graph(ctx context.Context, q, nodeType string, limit int) ([]map[string]any, error) {
	if err := c.WithFlag(ctx); err != nil {
		return nil, err
	}
	p := url.Values{}
	if q != "" {
		p.Set("q", q)
	}
	if nodeType != "" {
		p.Set("node_type", nodeType)
	}
	if limit > 0 {
		p.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/projects/current/graph"
	if encoded := p.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var raw struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := c.getJSON(ctx, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw.Nodes, nil
}

// Project mirrors the desktop API's project record.
type Project struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Active bool   `json:"active"`
}

// SearchHit is one retrieval result.
type SearchHit struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
	Path    string  `json:"path"`
	Content string  `json:"content,omitempty"`
}

// FileNode is one entry in the project tree.
type FileNode struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // file / directory
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// getJSON / postJSON are internal helpers. We centralise HTTP error
// handling so the public methods stay short.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	c.mu.RLock()
	base := c.baseURL
	tok := c.token
	c.mu.RUnlock()

	u := base + path
	if query != nil {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	c.mu.RLock()
	base := c.baseURL
	tok := c.token
	c.mu.RUnlock()

	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("llm_wiki %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// normalizeBaseURL guarantees the client base always carries the
// /api/v1 prefix every getJSON/postJSON call-site path assumes.
//
// 0.5.92 regression: the 0.3.x→0.5.91 builds composed request URLs
// as bare base + path, with the prefix present only because the unit
// tests baked it into Config.BaseURL. Production's DiscoverBaseURL
// returns the bare loopback host, so every read verb
// (projects/files/read/search/graph) hit e.g.
// http://127.0.0.1:19828/projects/... and 404'd against the desktop
// app — while /status stayed green because the app happens to also
// serve /health at the root. Normalising here closes that gap for
// both the discovered and the explicitly-configured shapes; a
// BaseURL that already ends in /api/v1 (the old test convention) is
// left untouched.
func normalizeBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return ""
	}
	if strings.HasSuffix(u, DefaultAPIPath) {
		return u
	}
	return u + DefaultAPIPath
}

// DiscoverBaseURL finds the loopback port LLM Wiki is listening on
// by issuing a /health probe against 19827 / 19828. The desktop app
// binds both ports for backwards compatibility, but only one is the
// API port; we hit /health (which both respond to) to verify.
//
// We avoid shelling out to `lsof` so this works in the bundled app
// sandbox where lsof is not always available.
func DiscoverBaseURL(ctx context.Context, hc *http.Client) (string, error) {
	candidates := []string{"http://127.0.0.1:19827", "http://127.0.0.1:19828"}
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	for _, base := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+DefaultAPIPath+"/health", nil)
		if err != nil {
			continue
		}
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte("\"ok\":true")) {
			return base, nil
		}
	}
	return "", errors.New("LLM Wiki desktop app not running on 19827 or 19828")
}

// DiscoverToken resolves the LLM Wiki desktop API bearer token. The
// desktop app moved its storage across versions, so the lookup is a
// priority chain:
//
//  1. $LLM_WIKI_API_TOKEN — explicit override, the same env var the
//     app's own MCP config ships with
//  2. ~/.multica/llm-wiki.json — the key the user pasted into
//     Multica (Labs → LLM Wiki page, or
//     `multica experimental llm-wiki token --set`)
//  3. ~/Library/Application Support/com.llmwiki.app/app-state.json
//     (apiConfig.token) — what the v0.6.x Tauri app persists
//  4. legacy auth.json layouts — pre-0.6 builds, kept so a
//     downgraded app keeps working
//
// 0.5.92 regression: the old implementation only knew the legacy
// auth.json paths, which no current app build writes — the bridge
// silently ran tokenless and only survived because the app allowed
// unauthenticated local access.
func DiscoverToken(ctx context.Context) (string, error) {
	if tok := os.Getenv("LLM_WIKI_API_TOKEN"); tok != "" {
		return tok, nil
	}
	home, err := homeFn()
	if err != nil {
		return "", err
	}
	for _, p := range TokenCandidatePaths(home) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if tok := parseTokenJSON(b); tok != "" {
			return tok, nil
		}
	}
	return "", errors.New("LLM Wiki API token not found; generate one in LLM Wiki.app Settings → API + MCP, then paste it in Multica Labs → LLM Wiki (or `multica experimental llm-wiki token --set <token>`)")
}

// homeFn is the home-dir source, a package var so tests can redirect
// the candidate scan into a temp directory. Not for production use.
var homeFn = os.UserHomeDir

// TokenCandidatePaths lists the on-disk files DiscoverToken reads,
// most-authoritative first: Multica's own token store, then the
// desktop app's app-state, then the legacy auth.json layouts.
func TokenCandidatePaths(home string) []string {
	return []string{
		filepath.Join(home, ".multica", "llm-wiki.json"),
		filepath.Join(home, "Library", "Application Support", "com.llmwiki.app", "app-state.json"),
		filepath.Join(home, "Library", "Application Support", "LLM Wiki", "auth.json"),
		filepath.Join(home, ".config", "LLM Wiki", "auth.json"),
		filepath.Join(home, ".llm-wiki", "auth.json"),
	}
}

// parseTokenJSON extracts a bearer token from any token-file shape
// the app has shipped: {"token": "..."} (auth.json, Multica store)
// and {"apiConfig": {"token": "..."}} (Tauri app-state). Returns ""
// for unparseable or token-less payloads.
func parseTokenJSON(b []byte) string {
	var top struct {
		Token     string `json:"token"`
		APIConfig struct {
			Token string `json:"token"`
		} `json:"apiConfig"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return ""
	}
	if top.Token != "" {
		return top.Token
	}
	return top.APIConfig.Token
}
