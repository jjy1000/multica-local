package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 0.5.106 claude_science_lab repairs, pinned here:
//
//   - loadManifestAsset must read through the pre-0.5.106 nested layout
//     (skills/<cat>/<name>/SKILL.md being a DIRECTORY whose child
//     SKILL.md is the real body) — the old code silently stored "" for
//     every skill.
//   - Install must hydrate skill bodies + frontmatter descriptions, and
//     a re-install must REPAIR rows still carrying empty content.
//   - The skills list/detail/file endpoints must serve the catalogue
//     with descriptions, full bodies, and supporting files (path
//     traversal rejected).
//   - Runtime execute must anchor session dirs at the session row id so
//     artifact bytes are fetchable, and session_id reuse must continue
//     in the same working directory.

// setResourcesDirForTest points MULTICA_RESOURCES_DIR at dir for the
// duration of the test. Not parallel-safe (process-global env), matching
// withTestManifestEnv's concurrency profile.
func setResourcesDirForTest(t *testing.T, dir string) func() {
	t.Helper()
	prev := os.Getenv(ManifestResourceDirEnv)
	if err := os.Setenv(ManifestResourceDirEnv, dir); err != nil {
		t.Fatalf("setenv %s: %v", ManifestResourceDirEnv, err)
	}
	return func() {
		if prev == "" {
			_ = os.Unsetenv(ManifestResourceDirEnv)
		} else {
			_ = os.Setenv(ManifestResourceDirEnv, prev)
		}
	}
}

// TestLoadManifestAsset_NestedSkillMDLayout covers the compat path for
// resource trees staged by pre-0.5.106 builds: body_path resolves to a
// directory named SKILL.md and the real body lives one level down.
func TestLoadManifestAsset_NestedSkillMDLayout(t *testing.T) {
	dir := t.TempDir()
	defer setResourcesDirForTest(t, dir)()

	skillDir := filepath.Join(dir, "claude-science", "skills", "research", "demo")
	nested := filepath.Join(skillDir, "SKILL.md") // directory, per the old layout bug
	if err := os.MkdirAll(filepath.Join(nested, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: demo\ndescription: \"Demo skill for nested layout.\"\n---\n\n# Demo\nbody text\n"
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "references", "tips.md"), []byte("# tips\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadManifestAsset("claude-science", "skills/research/demo/SKILL.md")
	if !strings.Contains(got, "# Demo") {
		t.Fatalf("nested SKILL.md body not read through directory: %q", got)
	}
	if desc := skillFrontmatterDescription(got); desc != "Demo skill for nested layout." {
		t.Fatalf("frontmatter description = %q", desc)
	}
	if desc := skillFrontmatterDescription("no frontmatter here"); desc != "" {
		t.Fatalf("description on frontmatter-less body = %q", desc)
	}
}

// TestInstallClaudeScience_SkillBodyContentRepaired pins the 0.5.106
// content contract over the repo fixture (canonical flat layout): the
// install hydrates body + frontmatter description, and a re-install
// repairs rows whose content is still empty (the pre-0.5.106 installs).
func TestInstallClaudeScience_SkillBodyContentRepaired(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "cs-skill-body")
	cleanupVisibilityRows(t, workspaceID)
	defer withTestManifestEnv(t)()

	const fixtureSkill = "fixture-perplexity-search"
	if err := testHandler.InstallClaudeScience(ctx, experimental.SourceClaudeScience, userID, workspaceID); err != nil {
		t.Fatalf("InstallClaudeScience: %v", err)
	}

	assertContent := func(stage string) {
		t.Helper()
		row, err := testHandler.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: uuidToPgtype(workspaceID),
			Name:        fixtureSkill,
		})
		if err != nil {
			t.Fatalf("%s: fixture skill missing: %v", stage, err)
		}
		if !strings.Contains(row.Content, "# Perplexity Search") {
			t.Fatalf("%s: skill content empty or wrong (len=%d)", stage, len(row.Content))
		}
		if !strings.HasPrefix(row.Description, "Perform AI-powered web searches") {
			t.Fatalf("%s: description not parsed from frontmatter: %q", stage, row.Description)
		}
	}
	assertContent("install")

	// Simulate a pre-0.5.106 row: empty body, generic description.
	if _, err := testPool.Exec(ctx, `UPDATE skill SET content = '', description = 'Imported from Claude Science manifest (research)' WHERE workspace_id = $1 AND name = $2`,
		workspaceID, fixtureSkill); err != nil {
		t.Fatalf("blank skill row: %v", err)
	}
	if err := testHandler.InstallClaudeScience(ctx, experimental.SourceClaudeScience, userID, workspaceID); err != nil {
		t.Fatalf("InstallClaudeScience (re-install): %v", err)
	}
	assertContent("re-install repair")
}

// TestClaudeScienceSkillsEndpoints exercises list → detail → file with
// a temp resources dir whose fixture skill carries a supporting
// references file. Also pins the traversal + extension guards on the
// file endpoint.
func TestClaudeScienceSkillsEndpoints(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "cs-skills-api")
	cleanupVisibilityRows(t, workspaceID)

	// Temp resources tree: nested-layout skill WITH a references file.
	dir := t.TempDir()
	defer setResourcesDirForTest(t, dir)()
	skillDir := filepath.Join(dir, "claude-science", "skills", "research", "demo-skill")
	nested := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(filepath.Join(nested, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: demo-skill\ndescription: \"Endpoint test skill.\"\n---\n\n# Demo Skill\ncontent\n"
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "references", "tips.md"), []byte("# tips\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "references", "blob.pdf"), []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Manifest declaring the skill so category + file resolution work.
	manifest := `{"schema_version":1,"experimental_source":"claude_science_lab","workspace":{"name":"t","slug":"claude-science"},"skills":[{"category":"research","name":"demo-skill","body_path":"skills/research/demo-skill/SKILL.md"}],"agents":[],"squads":[],"installed_at_build":true}`
	if err := os.WriteFile(filepath.Join(dir, "claude-science", "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := testHandler.InstallClaudeScience(ctx, experimental.SourceClaudeScience, userID, workspaceID); err != nil {
		t.Fatalf("InstallClaudeScience: %v", err)
	}

	r := chi.NewRouter()
	RegisterClaudeScienceSkillRoutes(r, testHandler)
	do := func(method, target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("X-User-ID", userID)
		r.ServeHTTP(w, req)
		return w
	}
	wsQ := "?workspace_id=" + workspaceID

	// List: contains the installed skill with description + category.
	w := do(http.MethodGet, "/api/experimental/claude-science/skills"+wsQ)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var list ClaudeScienceSkillsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found *ClaudeScienceSkillSummary
	for i := range list.Skills {
		if list.Skills[i].Name == "demo-skill" {
			found = &list.Skills[i]
		}
	}
	if found == nil {
		t.Fatalf("demo-skill missing from list (%d skills)", list.Total)
	}
	if found.Category != "research" || found.Description != "Endpoint test skill." {
		t.Fatalf("summary enrichment broken: %+v", found)
	}

	// Detail: full body + files list (pdf excluded by allowlist).
	w = do(http.MethodGet, "/api/experimental/claude-science/skills/demo-skill"+wsQ)
	if w.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	var detail ClaudeScienceSkillDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if !strings.Contains(detail.Content, "# Demo Skill") || !detail.Installed {
		t.Fatalf("detail body/installed wrong: installed=%v len=%d", detail.Installed, len(detail.Content))
	}
	var hasTips bool
	for _, f := range detail.Files {
		if f.Path == "references/tips.md" {
			hasTips = true
		}
		if f.Path == "references/blob.pdf" {
			t.Fatalf("pdf must not be listed: %+v", detail.Files)
		}
	}
	if !hasTips {
		t.Fatalf("references/tips.md missing from files: %+v", detail.Files)
	}

	// File: happy path.
	w = do(http.MethodGet, "/api/experimental/claude-science/skills/demo-skill/file?path=references/tips.md&workspace_id="+workspaceID)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "# tips") {
		t.Fatalf("file happy path: %d %s", w.Code, w.Body.String())
	}

	// File: traversal + extension guards.
	for _, bad := range []string{
		"path=../../etc/passwd",
		"path=..%2F..%2Fsecret.md",
		"path=/etc/passwd",
		"path=references/blob.pdf",
	} {
		w = do(http.MethodGet, "/api/experimental/claude-science/skills/demo-skill/file?"+bad+"&workspace_id="+workspaceID)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("file guard %q: want 400, got %d", bad, w.Code)
		}
	}

	// Unknown skill → 404.
	w = do(http.MethodGet, "/api/experimental/claude-science/skills/nope"+wsQ)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown skill: want 404, got %d", w.Code)
	}
}

// TestClaudeScienceRuntime_SessionContinuation pins the 0.5.106
// notebook-style contract: run 2 with session_id sees run 1's files,
// and artifact bytes resolve against the ROOT session directory (the
// pre-0.5.106 code anchored them at the per-run row id and 404'd).
func TestClaudeScienceRuntime_SessionContinuation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if err := probePython3(); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "cs-runtime-cont")
	agentID := "00000000-0000-0000-0000-0000000000aa"

	r := chi.NewRouter()
	r.Post("/api/experimental/claude-science-runtime/execute", testHandler.PostClaudeScienceRuntimeExecute)
	r.Get("/api/experimental/claude-science-runtime/artifacts/{artifactID}", testHandler.GetClaudeScienceRuntimeArtifactBytes)

	execute := func(body string) (*httptest.ResponseRecorder, RuntimeExecuteResponse) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/experimental/claude-science-runtime/execute", strings.NewReader(body))
		req.Header.Set("X-User-ID", userID)
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		var resp RuntimeExecuteResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w, resp
	}

	// Run 1: write state + emit an artifact.
	w1, resp1 := execute(`{
		"workspace_id": "` + workspaceID + `",
		"agent_id": "` + agentID + `",
		"language": "python",
		"code": "open('state.txt','w').write('run-one-state')\nprint('one-done')\n"
	}`)
	if w1.Code != http.StatusOK || resp1.Status != "completed" {
		t.Fatalf("run 1: %d %s", w1.Code, w1.Body.String())
	}
	if resp1.RootSessionID != resp1.SessionID {
		t.Fatalf("fresh run root=%s session=%s (must match)", resp1.RootSessionID, resp1.SessionID)
	}

	// Artifact bytes fetchable (regression: path anchored at dirName).
	var artifacts []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	rows, err := testHandler.Queries.ListExperimentalRuntimeArtifactsBySession(ctx, uuidToPgtype(resp1.SessionID))
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	for _, a := range rows {
		artifacts = append(artifacts, struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{a.ID.String(), a.Name})
	}
	if len(artifacts) == 0 {
		t.Fatal("run 1 produced no artifacts (state.txt expected)")
	}
	var stateArtifact string
	for _, a := range artifacts {
		if a.Name == "state.txt" {
			stateArtifact = a.ID
		}
	}
	if stateArtifact == "" {
		t.Fatalf("state.txt not ingested: %+v", artifacts)
	}
	wBytes := httptest.NewRecorder()
	reqBytes := httptest.NewRequest(http.MethodGet, "/api/experimental/claude-science-runtime/artifacts/"+stateArtifact, nil)
	reqBytes.Header.Set("X-User-ID", userID)
	r.ServeHTTP(wBytes, reqBytes)
	if wBytes.Code != http.StatusOK || !strings.Contains(wBytes.Body.String(), "run-one-state") {
		t.Fatalf("artifact bytes: %d %q", wBytes.Code, wBytes.Body.String())
	}

	// Run 2: continue in the same workspace, read run 1's file.
	w2, resp2 := execute(`{
		"workspace_id": "` + workspaceID + `",
		"agent_id": "` + agentID + `",
		"language": "python",
		"session_id": "` + resp1.RootSessionID + `",
		"code": "print('continue:', open('state.txt').read())\n"
	}`)
	if w2.Code != http.StatusOK || resp2.Status != "completed" {
		t.Fatalf("run 2: %d %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(resp2.Stdout, "continue: run-one-state") {
		t.Fatalf("run 2 stdout missing run-1 state: %q", resp2.Stdout)
	}
	if resp2.RootSessionID != resp1.RootSessionID {
		t.Fatalf("run 2 root drifted: %s != %s", resp2.RootSessionID, resp1.RootSessionID)
	}

	// Unknown session → 404.
	w3, _ := execute(`{
		"workspace_id": "` + workspaceID + `",
		"agent_id": "` + agentID + `",
		"language": "python",
		"session_id": "00000000-0000-0000-0000-0000000000bb",
		"code": "print('x')\n"
	}`)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("unknown session: want 404, got %d", w3.Code)
	}
}
