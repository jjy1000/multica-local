package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// ---------------------------------------------------------------------------
// experimental — 0.3.19+ dispatcher for the runtime + llm-wiki bridges
//
// The `multica experimental …` surface covers the two Labs surfaces the
// Skill adapter calls into. Both backends are gated behind their own
// Labs flag and registered on the chi router only when the flag is on;
// the CLI mirrors the flag check here so a Skill body that forgot to
// gate itself still gets a polite refusal instead of a 404.
//
// Hard rule: every verb here talks to the local multica server on
// $MULTICA_API_URL (default http://127.0.0.1:8090). When the runtime
// subprocess is not running on 8090 we surface a clear "server not
// reachable" error and exit non-zero; this is the same behaviour every
// other `multica …` verb that walks the HTTP API exhibits.
// ---------------------------------------------------------------------------

var experimentalCmd = &cobra.Command{
	Use:   "experimental",
	Short: "Experimental Labs surfaces (runtime sandbox + LLM Wiki bridge)",
}

var experimentalFlagsCmd = &cobra.Command{
	Use:   "flags",
	Short: "List the catalog of Labs flags and their effective state",
	RunE:  runExperimentalFlags,
}

var experimentalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every Labs flag with its runtime kind and proxy prefix (0.3.19 P8 / Blueprint)",
	RunE:  runExperimentalList,
}

var experimentalInspectCmd = &cobra.Command{
	Use:   "inspect <flag-key>",
	Short: "Print the local registry + remote effective state for one flag",
	Args:  cobra.ExactArgs(1),
	RunE:  runExperimentalInspect,
}

var experimentalStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Per-workspace install/visibility summary for every Labs flag",
	RunE:  runExperimentalStatus,
}

var experimentalGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage-collect orphan visibility rows for a flag (0.3.45.2 P2#11)",
	Args:  cobra.ExactArgs(1),
	RunE:  runExperimentalGC,
}

var claudeScienceRuntimeRootCmd = &cobra.Command{
	Use:   "claude-lab",
	Short: "Claude Research Lab (Labs: claude_science_lab) — runtime sandbox; replaces 0.3.20 claude-science-runtime",
}

var claudeScienceRuntimeExecCmd = &cobra.Command{
	Use:   "execute",
	Short: "Execute a Python snippet against the workspace sandbox",
	RunE:  runClaudeScienceRuntimeExecute,
}

var claudeScienceRuntimeSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List runtime sessions for a workspace",
	RunE:  runClaudeScienceRuntimeSessions,
}

var claudeScienceRuntimeArtifactsCmd = &cobra.Command{
	Use:   "artifacts",
	Short: "List runtime artifacts for a session",
	RunE:  runClaudeScienceRuntimeArtifacts,
}

var claudeScienceRuntimeDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a runtime session",
	RunE:  runClaudeScienceRuntimeDelete,
}

var llmWikiRootCmd = &cobra.Command{
	Use:   "llm-wiki",
	Short: "LLM Wiki local bridge (Labs: llm_wiki_bridge)",
}

var llmWikiStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether the desktop API is reachable",
	RunE:  runLLMWikiStatus,
}

var llmWikiProjectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List LLM Wiki projects",
	RunE:  runLLMWikiProjects,
}

var llmWikiFilesCmd = &cobra.Command{
	Use:   "files",
	Short: "List files under a project",
	RunE:  runLLMWikiFiles,
}

var llmWikiReadCmd = &cobra.Command{
	Use:   "read",
	Short: "Read a text file from the project",
	RunE:  runLLMWikiRead,
}

var llmWikiSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Vector + keyword search the project",
	RunE:  runLLMWikiSearch,
}

var llmWikiGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Query the project knowledge graph",
	RunE:  runLLMWikiGraph,
}

var llmWikiWriteCmd = &cobra.Command{
	Use:   "write",
	Short: "Drop a file into the LLM Wiki vault directory",
	RunE:  runLLMWikiWrite,
}

// Per-command flag variables. We keep them at package level so
// runExperimentalFlags stays trivial.
var (
	runtimeExecCodeFile    string
	runtimeExecTimeoutMs   int
	runtimeExecAgentID     string
	runtimeExecWorkspaceID string
	runtimeSessionsWS      string
	runtimeArtifactsSess   string
	runtimeDeleteSess      string

	llmFilesRoot      string
	llmFilesRecursive bool
	llmReadPath       string
	llmSearchQuery    string
	llmSearchTopK     int
	llmSearchContent  bool
	llmGraphQ         string
	llmGraphNode      string
	llmGraphLimit     int
	llmWritePath      string
	llmWriteContent   string

	outputJSON bool
)

func init() {
	rootCmd.AddCommand(experimentalCmd)
	experimentalCmd.AddCommand(
		experimentalFlagsCmd,
		experimentalListCmd,
		experimentalInspectCmd,
		experimentalStatusCmd,
		experimentalGCCmd,
	)

	experimentalCmd.AddCommand(claudeScienceRuntimeRootCmd)
	claudeScienceRuntimeRootCmd.AddCommand(
		claudeScienceRuntimeExecCmd,
		claudeScienceRuntimeSessionsCmd,
		claudeScienceRuntimeArtifactsCmd,
		claudeScienceRuntimeDeleteCmd,
	)

	claudeScienceRuntimeExecCmd.Flags().StringVar(&runtimeExecCodeFile, "code-file", "", "path to a python file to execute")
	claudeScienceRuntimeExecCmd.Flags().IntVar(&runtimeExecTimeoutMs, "timeout-ms", 30000, "exec timeout in ms (max 120000)")
	claudeScienceRuntimeExecCmd.Flags().StringVar(&runtimeExecAgentID, "agent-id", "", "agent UUID (defaults to MULTICA_AGENT_ID env or zero UUID)")
	claudeScienceRuntimeExecCmd.Flags().StringVar(&runtimeExecWorkspaceID, "workspace-id", "", "workspace UUID")
	claudeScienceRuntimeSessionsCmd.Flags().StringVar(&runtimeSessionsWS, "workspace-id", "", "workspace UUID")
	claudeScienceRuntimeArtifactsCmd.Flags().StringVar(&runtimeArtifactsSess, "session-id", "", "session UUID")
	claudeScienceRuntimeDeleteCmd.Flags().StringVar(&runtimeDeleteSess, "session-id", "", "session UUID")

	experimentalCmd.AddCommand(llmWikiRootCmd)
	llmWikiRootCmd.AddCommand(
		llmWikiStatusCmd,
		llmWikiProjectsCmd,
		llmWikiFilesCmd,
		llmWikiReadCmd,
		llmWikiSearchCmd,
		llmWikiGraphCmd,
		llmWikiWriteCmd,
	)

	llmWikiFilesCmd.Flags().StringVar(&llmFilesRoot, "root", "wiki", "wiki|sources|all")
	llmWikiFilesCmd.Flags().BoolVar(&llmFilesRecursive, "recursive", true, "recursive listing")
	llmWikiReadCmd.Flags().StringVar(&llmReadPath, "path", "", "vault-relative path (e.g. wiki/foo.md)")
	llmWikiSearchCmd.Flags().StringVar(&llmSearchQuery, "query", "", "search query")
	llmWikiSearchCmd.Flags().IntVar(&llmSearchTopK, "top-k", 8, "max results")
	llmWikiSearchCmd.Flags().BoolVar(&llmSearchContent, "include-content", false, "include full page content")
	llmWikiGraphCmd.Flags().StringVar(&llmGraphQ, "q", "", "text filter")
	llmWikiGraphCmd.Flags().StringVar(&llmGraphNode, "node-type", "", "optional node type filter")
	llmWikiGraphCmd.Flags().IntVar(&llmGraphLimit, "limit", 50, "max nodes")
	llmWikiWriteCmd.Flags().StringVar(&llmWritePath, "path", "", "vault-relative path")
	llmWikiWriteCmd.Flags().StringVar(&llmWriteContent, "content", "", "file content; reads from stdin if empty")

	for _, c := range []*cobra.Command{
		claudeScienceRuntimeExecCmd,
		claudeScienceRuntimeSessionsCmd,
		claudeScienceRuntimeArtifactsCmd,
		claudeScienceRuntimeDeleteCmd,
		llmWikiStatusCmd,
		llmWikiProjectsCmd,
		llmWikiFilesCmd,
		llmWikiReadCmd,
		llmWikiSearchCmd,
		llmWikiGraphCmd,
		llmWikiWriteCmd,
	} {
		c.Flags().BoolVar(&outputJSON, "output", false, "Emit JSON envelope instead of pretty text")
	}
}

func experimentalAPIURL() string {
	if v := os.Getenv("MULTICA_API_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8090"
}

func experimentalToken() string {
	if v := os.Getenv("MULTICA_API_TOKEN"); v != "" {
		return v
	}
	if path := os.Getenv("MULTICA_API_TOKEN_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func experimentalHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

type experimentalFlag struct {
	Key            string `json:"key"`
	Enabled        bool   `json:"enabled"`
	DefaultEnabled bool   `json:"default_enabled"`
	Title          struct {
		En string `json:"en"`
		Zh string `json:"zh"`
	} `json:"title"`
	Description struct {
		En string `json:"en"`
		Zh string `json:"zh"`
	} `json:"description"`
}

type experimentalFlagsResponse struct {
	Flags []experimentalFlag `json:"flags"`
}

type experimentalEnvelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func runExperimentalFlags(cmd *cobra.Command, _ []string) error {
	var out experimentalFlagsResponse
	if err := experimentalGET(cmd.Context(), "/api/experimental-flags", &out); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(out)
	}
	fmt.Printf("%-30s %-7s %s\n", "FLAG", "ENABLED", "TITLE (zh)")
	for _, f := range out.Flags {
		enabled := "off"
		if f.Enabled {
			enabled = "ON"
		}
		fmt.Printf("%-30s %-7s %s\n", f.Key, enabled, f.Title.Zh)
	}
	return nil
}

// runExperimentalList walks the local Go registry (catalog.json
// snapshot) and prints one row per flag with its runtime kind and
// proxy prefix — the same shape Blueprint P8 specifies. The output
// does NOT round-trip the server, so the command runs without any
// multica instance running and is the canonical developer-side
// sanity check ("did my catalog edit take?").
func runExperimentalList(cmd *cobra.Command, _ []string) error {
	flags := localCatalogSnapshot()
	if outputJSON {
		return writeJSONOutput(flags)
	}
	fmt.Printf("%-26s %-10s %-12s %s\n", "FLAG", "RUNTIME", "DEFAULT", "PROXY PREFIX")
	for _, f := range flags {
		def := "off"
		if f.DefaultEnabled {
			def = "ON"
		}
		prefix := f.ProxyPrefix
		if prefix == "" {
			prefix = "—"
		}
		fmt.Printf("%-26s %-10s %-12s %s\n", f.Key, f.Runtime, def, prefix)
	}
	return nil
}

// runExperimentalStatus (0.3.45.2 P2#11) prints a per-workspace roll-up
// of installed Labs flags + resource counts. The query joins
// experimental_pref with experimental_resource_visibility so the
// operator can see which workspaces have actually enabled + installed
// each flag and how many resources (agent / autopilot / skill / squad)
// are present. This is the canonical "is this lab wired up" check.
//
// Round-trips the database via a local libpq connection; reads the
// connection string from the same env vars Multica's server uses
// (DATABASE_URL) so it works on dev + packaged without flags.
func runExperimentalStatus(cmd *cobra.Command, _ []string) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required for `multica experimental status`")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `
		SELECT pref.flag_key,
		       count(DISTINCT pref.user_id) AS opted_in_users,
		       count(DISTINCT vis.resource_id) AS hidden_resources
		FROM experimental_pref pref
		LEFT JOIN experimental_resource_visibility vis
		  ON vis.flag_key = pref.flag_key AND vis.hidden = TRUE
		WHERE pref.enabled = TRUE
		GROUP BY pref.flag_key
		ORDER BY pref.flag_key
	`)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	fmt.Printf("%-28s %-12s %s\n", "FLAG", "OPTED_IN", "HIDDEN_ROWS")
	for rows.Next() {
		var key string
		var optedIn, hidden int64
		if err := rows.Scan(&key, &optedIn, &hidden); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		fmt.Printf("%-28s %-12d %d\n", key, optedIn, hidden)
	}
	return rows.Err()
}

// runExperimentalGC (0.3.45.2 P2#11) deletes visibility rows for a
// given flag whose underlying resource is gone (e.g. an agent that
// was hard-deleted from the workspace but the visibility row was
// left behind by a partial rollback). Bounded by flag_key only;
// the SQL filter is the resource_id NOT IN (subquery) so we never
// delete a row whose target still exists.
//
// Idempotent and safe to re-run.
func runExperimentalGC(cmd *cobra.Command, args []string) error {
	flagKey := args[0]
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required for `multica experimental gc`")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	tag, err := conn.Exec(ctx, `
		DELETE FROM experimental_resource_visibility
		WHERE flag_key = $1
		  AND (
		    (resource_type = 'agent'    AND resource_id NOT IN (SELECT id FROM agent))
		    OR (resource_type = 'autopilot' AND resource_id NOT IN (SELECT id FROM autopilot))
		    OR (resource_type = 'skill'    AND resource_id NOT IN (SELECT id FROM skill))
		    OR (resource_type = 'squad'    AND resource_id NOT IN (SELECT id FROM squad))
		  )
	`, flagKey)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	fmt.Printf("gc: removed %d orphan visibility rows for flag %q\n", tag.RowsAffected(), flagKey)
	return nil
}

// runExperimentalInspect prints the catalog snapshot + the remote
// effective state for one flag. Server must be running; we
// tolerate it being down by reporting the offline column as "—".
func runExperimentalInspect(cmd *cobra.Command, args []string) error {
	flagKey := args[0]
	snapshot, ok := localCatalogFlag(flagKey)
	if !ok {
		return fmt.Errorf("flag %q not in catalog; run `multica experimental list`", flagKey)
	}

	remote := map[string]bool{}
	remoteErr := ""
	if flags, err := fetchEffectiveFlags(cmd.Context()); err == nil {
		for _, f := range flags.Flags {
			remote[f.Key] = f.Enabled
		}
	} else {
		remoteErr = err.Error()
	}

	status := "off"
	if remote[flagKey] {
		status = "ON"
	}
	def := "off"
	if snapshot.DefaultEnabled {
		def = "ON"
	}

	view := map[string]any{
		"key":             flagKey,
		"title_en":        snapshot.TitleEn,
		"title_zh":        snapshot.TitleZh,
		"runtime":         snapshot.Runtime,
		"proxy_prefix":    snapshot.ProxyPrefix,
		"loopback_service": snapshot.LoopbackService,
		"manifest_path":   snapshot.ManifestPath,
		"effective":       status,
		"default_enabled": def,
		"remote_error":    remoteErr,
	}
	if outputJSON {
		return writeJSONOutput(view)
	}

	fmt.Printf("Flag:        %s\n", flagKey)
	fmt.Printf("Title (zh):  %s\n", snapshot.TitleZh)
	fmt.Printf("Title (en):  %s\n", snapshot.TitleEn)
	fmt.Printf("Runtime:     %s\n", snapshot.Runtime)
	fmt.Printf("Proxy:       %s (service=%s)\n", snapshot.ProxyPrefix, snapshot.LoopbackService)
	fmt.Printf("Manifest:    %s\n", snapshot.ManifestPath)
	fmt.Printf("Catalog:     default=%s\n", def)
	fmt.Printf("Effective:   %s%s\n", status, remoteSuffix(remoteErr))
	return nil
}

func remoteSuffix(err string) string {
	if err == "" {
		return ""
	}
	return "  [server unreachable: " + err + "]"
}

type localCatalogEntry struct {
	Key             string `json:"key"`
	Runtime         string `json:"runtime"`
	DefaultEnabled  bool   `json:"default_enabled"`
	ProxyPrefix     string `json:"proxy_prefix,omitempty"`
	LoopbackService string `json:"loopback_service,omitempty"`
	ManifestPath    string `json:"manifest_path,omitempty"`
	TitleEn         string `json:"title_en,omitempty"`
	TitleZh         string `json:"title_zh,omitempty"`
}

// localCatalogSnapshot is the developer's view of the catalog the
// CLI binary was compiled with. It reads the package-level
// `internal/experimental.Catalog` via a derived accessor so the
// CLI does not need a running server.
func localCatalogSnapshot() []localCatalogEntry {
	return cliCatalogSnapshot()
}

func localCatalogFlag(key string) (localCatalogEntry, bool) {
	for _, f := range cliCatalogSnapshot() {
		if f.Key == key {
			return f, true
		}
	}
	return localCatalogEntry{}, false
}

// fetchEffectiveFlags issues a single GET against the server. The
// error path is informative — `inspect` tolerates the server being
// down by reporting remote_error rather than failing the whole
// command.
func fetchEffectiveFlags(ctx context.Context) (experimentalFlagsResponse, error) {
	var out experimentalFlagsResponse
	if err := experimentalGET(ctx, "/api/experimental-flags", &out); err != nil {
		return out, err
	}
	return out, nil
}

func runClaudeScienceRuntimeExecute(cmd *cobra.Command, _ []string) error {
	if runtimeExecWorkspaceID == "" {
		return errors.New("--workspace-id is required")
	}
	if runtimeExecCodeFile == "" {
		return errors.New("--code-file is required")
	}
	code, err := os.ReadFile(runtimeExecCodeFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", runtimeExecCodeFile, err)
	}
	agentID := runtimeExecAgentID
	if agentID == "" {
		agentID = os.Getenv("MULTICA_AGENT_ID")
	}
	if agentID == "" {
		// Skill callers always pass agent_id; ad-hoc CLI invocations
		// fall back to a generic synthetic id so the handler still
		// accepts the request.
		agentID = "00000000-0000-0000-0000-000000000000"
	}
	payload := map[string]any{
		"workspace_id": runtimeExecWorkspaceID,
		"agent_id":     agentID,
		"language":     "python",
		"code":         string(code),
		"timeout_ms":   runtimeExecTimeoutMs,
	}
	var raw experimentalEnvelope
	if err := experimentalPOST(cmd.Context(), "/api/experimental/claude-science-runtime/execute", payload, &raw, false); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	fmt.Printf("session=%v status=%v exit=%v duration_ms=%v\n",
		data["session_id"], data["status"], data["exit_code"], data["duration_ms"])
	if s, _ := data["stdout"].(string); s != "" {
		fmt.Println("--- stdout ---")
		fmt.Println(s)
	}
	if s, _ := data["stderr"].(string); s != "" {
		fmt.Println("--- stderr ---")
		fmt.Println(s)
	}
	if arts, _ := data["artifacts"].([]any); len(arts) > 0 {
		fmt.Println("--- artifacts ---")
		for _, a := range arts {
			m, _ := a.(map[string]any)
			fmt.Printf("  %s  %s  (%v bytes)\n", m["name"], m["kind"], m["bytes"])
		}
	}
	return nil
}

func runClaudeScienceRuntimeSessions(cmd *cobra.Command, _ []string) error {
	if runtimeSessionsWS == "" {
		return errors.New("--workspace-id is required")
	}
	var raw experimentalEnvelope
	path := "/api/experimental/claude-science-runtime/sessions?workspace_id=" + runtimeSessionsWS
	if err := experimentalGET(cmd.Context(), path, &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	rows, _ := data["sessions"].([]any)
	fmt.Printf("%d sessions\n", len(rows))
	for _, r := range rows {
		m, _ := r.(map[string]any)
		fmt.Printf("  %v  %v  exit=%v  %v ms  created_at=%v\n",
			m["id"], m["status"], m["exit_code"], m["duration_ms"], m["created_at"])
	}
	return nil
}

func runClaudeScienceRuntimeArtifacts(cmd *cobra.Command, _ []string) error {
	if runtimeArtifactsSess == "" {
		return errors.New("--session-id is required")
	}
	var raw experimentalEnvelope
	path := "/api/experimental/claude-science-runtime/sessions/" + runtimeArtifactsSess + "/artifacts"
	if err := experimentalGET(cmd.Context(), path, &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	rows, _ := data["artifacts"].([]any)
	fmt.Printf("%d artifacts\n", len(rows))
	for _, a := range rows {
		m, _ := a.(map[string]any)
		fmt.Printf("  %-40s  %-6s  %v B  %v\n", m["name"], m["kind"], m["bytes"], m["url"])
	}
	return nil
}

func runClaudeScienceRuntimeDelete(cmd *cobra.Command, _ []string) error {
	if runtimeDeleteSess == "" {
		return errors.New("--session-id is required")
	}
	path := "/api/experimental/claude-science-runtime/sessions/" + runtimeDeleteSess
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodDelete, experimentalAPIURL()+path, nil)
	if err != nil {
		return err
	}
	if tok := experimentalToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := experimentalHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete %s: %s", resp.Status, string(body))
	}
	fmt.Println("ok")
	return nil
}

func runLLMWikiStatus(cmd *cobra.Command, _ []string) error {
	var raw experimentalEnvelope
	if err := experimentalGET(cmd.Context(), "/api/experimental/llm-wiki/status", &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	if raw.OK {
		fmt.Println("ok")
	} else {
		fmt.Printf("not ok: %s\n", raw.Error)
	}
	return nil
}

func runLLMWikiProjects(cmd *cobra.Command, _ []string) error {
	var raw experimentalEnvelope
	if err := experimentalGET(cmd.Context(), "/api/experimental/llm-wiki/projects", &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	if cur, ok := data["current_project"].(map[string]any); ok && cur != nil {
		fmt.Printf("current: %v (%v)\n", cur["name"], cur["path"])
	}
	rows, _ := data["projects"].([]any)
	for _, p := range rows {
		m, _ := p.(map[string]any)
		fmt.Printf("  %v  %v  %v\n", m["id"], m["name"], m["path"])
	}
	return nil
}

func runLLMWikiFiles(cmd *cobra.Command, _ []string) error {
	var raw experimentalEnvelope
	path := fmt.Sprintf("/api/experimental/llm-wiki/files?root=%s&recursive=%t", llmFilesRoot, llmFilesRecursive)
	if err := experimentalGET(cmd.Context(), path, &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	rows, _ := data["files"].([]any)
	fmt.Printf("%d files\n", len(rows))
	for _, f := range rows {
		m, _ := f.(map[string]any)
		fmt.Printf("  %v\n", m["path"])
	}
	return nil
}

func runLLMWikiRead(cmd *cobra.Command, _ []string) error {
	if llmReadPath == "" {
		return errors.New("--path is required")
	}
	var raw experimentalEnvelope
	path := "/api/experimental/llm-wiki/read?path=" + urlQueryEscape(llmReadPath)
	if err := experimentalGET(cmd.Context(), path, &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	if s, _ := data["content"].(string); s != "" {
		fmt.Println(s)
	}
	return nil
}

func runLLMWikiSearch(cmd *cobra.Command, _ []string) error {
	if llmSearchQuery == "" {
		return errors.New("--query is required")
	}
	payload := map[string]any{
		"query":          llmSearchQuery,
		"top_k":          llmSearchTopK,
		"include_content": llmSearchContent,
	}
	var raw experimentalEnvelope
	if err := experimentalPOST(cmd.Context(), "/api/experimental/llm-wiki/search", payload, &raw, true); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	rows, _ := data["hits"].([]any)
	for _, h := range rows {
		m, _ := h.(map[string]any)
		fmt.Printf("  %v\n", m["path"])
		if s, _ := m["snippet"].(string); s != "" {
			fmt.Printf("    %s\n", s)
		}
	}
	return nil
}

func runLLMWikiGraph(cmd *cobra.Command, _ []string) error {
	var raw experimentalEnvelope
	path := "/api/experimental/llm-wiki/graph?"
	q := []string{}
	if llmGraphQ != "" {
		q = append(q, "q="+urlQueryEscape(llmGraphQ))
	}
	if llmGraphNode != "" {
		q = append(q, "node_type="+urlQueryEscape(llmGraphNode))
	}
	if llmGraphLimit > 0 {
		q = append(q, fmt.Sprintf("limit=%d", llmGraphLimit))
	}
	if err := experimentalGET(cmd.Context(), path+strings.Join(q, "&"), &raw); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	rows, _ := data["nodes"].([]any)
	fmt.Printf("%d nodes\n", len(rows))
	for _, n := range rows {
		m, _ := n.(map[string]any)
		fmt.Printf("  %v\n", m["id"])
	}
	return nil
}

func runLLMWikiWrite(cmd *cobra.Command, _ []string) error {
	if llmWritePath == "" {
		return errors.New("--path is required")
	}
	if strings.Contains(llmWritePath, "..") {
		return errors.New("--path must not contain ..")
	}
	body := llmWriteContent
	if body == "" {
		// Read from stdin.
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		body = string(b)
	}
	payload := map[string]any{
		"path":    llmWritePath,
		"content": body,
	}
	var raw experimentalEnvelope
	if err := experimentalPOST(cmd.Context(), "/api/experimental/llm-wiki/write", payload, &raw, true); err != nil {
		return err
	}
	if outputJSON {
		return writeJSONOutput(raw)
	}
	data, _ := raw.Data.(map[string]any)
	fmt.Printf("wrote %v bytes to %v\n", data["bytes"], data["absolute"])
	return nil
}

func experimentalGET(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, experimentalAPIURL()+path, nil)
	if err != nil {
		return err
	}
	if tok := experimentalToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := experimentalHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("flag off or route not registered (404); open Settings → Labs and enable the corresponding flag")
	}
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("flag off (403): %s", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("get %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func experimentalPOST(ctx context.Context, path string, body any, out any, wantEnvelope bool) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, experimentalAPIURL()+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := experimentalToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := experimentalHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("flag off or route not registered (404); open Settings → Labs and enable the corresponding flag")
	}
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("flag off (403): %s", strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("post %s: %s — %s", path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if wantEnvelope {
		return json.Unmarshal(raw, out)
	}
	// Some handlers return raw session/artifact shape rather than an
	// envelope; allow either.
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// writeJSONOutput mirrors the helper used by other multica commands.
func writeJSONOutput(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// urlQueryEscape wraps the standard library with the encoding the LLM
// Wiki handler expects (form path encoding).
func urlQueryEscape(s string) string {
	q := ""
	for _, r := range s {
		switch {
		case r == ' ':
			q += "%20"
		case r == '/' || r == '-' || r == '_' || r == '.' || r == '~':
			q += string(r)
		default:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				q += string(r)
			} else {
				q += fmt.Sprintf("%%%02X", r)
			}
		}
	}
	return q
}

// ensure filepath import is referenced so goimports doesn't drop it
// when we later trim the file.
var _ = filepath.Clean

// cliCatalogSnapshot reads the in-process experimental.Catalog via a
// thin Go-package coupling so the CLI binary is self-contained.
// When the catalog grows past ~30 flags we should switch to a JSON
// file embedded next to the binary; for 0.3.19 the live link is
// fine.
func cliCatalogSnapshot() []localCatalogEntry {
	out := make([]localCatalogEntry, 0, len(experimental.Catalog))
	for _, f := range experimental.Catalog {
		out = append(out, localCatalogEntry{
			Key:             f.Key,
			Runtime:         f.RuntimeKind,
			DefaultEnabled:  f.DefaultVal,
			ProxyPrefix:     f.ProxyPrefix,
			LoopbackService: f.LoopbackService,
			ManifestPath:    f.ManifestPath,
			TitleEn:         f.Title.En,
			TitleZh:         f.Title.Zh,
		})
	}
	return out
}
