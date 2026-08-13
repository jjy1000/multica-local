package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUserPluginRuntime_IngestEmittedArtifacts is the end-to-end runtime
// test: it runs a real `python3 -I entry.py` inside a plugin's persistent
// env dir (HOME overridden to a temp tree so pluginEnvDir/pluginArtifactDir
// resolve there), then ingests the files the run emitted and asserts they
// land in the plugin's artifact index with the correct type mapping.
//
// This exercises the genuinely new logic in user_plugin_runtime.go
// (snapshot → exec → diff → ingest → runs.json). The DB-backed HTTP layer
// (GetUserPluginBySlug) is not wired in unit mode — see the note in
// claude_science_runtime_smoke_test.go — so we drive the runtime core
// directly, which is where the risk lives.
func TestUserPluginRuntime_IngestEmittedArtifacts(t *testing.T) {
	if err := probePython3(); err != nil {
		t.Skipf("python3 not available: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	const slug = "runtime-demo"
	envDir, err := pluginEnvDir(slug)
	if err != nil {
		t.Fatalf("pluginEnvDir: %v", err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}

	// entry.py writes an html + a json file into its cwd (the env dir).
	entry := "" +
		"with open('index.html', 'w') as f:\n" +
		"    f.write('<h1>hello lab</h1>')\n" +
		"with open('data.json', 'w') as f:\n" +
		"    f.write('{\"ok\": true}')\n" +
		"print('done')\n"
	if err := os.WriteFile(filepath.Join(envDir, entryFileName), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	before := snapshotEnvDir(envDir)

	if out, err := runEntryInDir(slug, envDir); err != nil {
		t.Fatalf("python3 run failed: %v\n%s", err, out)
	}

	h := &Handler{}
	arts, err := h.ingestRunArtifacts(slug, envDir, before)
	if err != nil {
		t.Fatalf("ingestRunArtifacts: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d: %+v", len(arts), arts)
	}

	// The html file maps to type "html"; the json file maps to "file".
	byTitle := map[string]ArtifactMeta{}
	for _, a := range arts {
		byTitle[a.Title] = a
	}
	if got := byTitle["index.html"].Type; got != "html" {
		t.Errorf("index.html type = %q, want html", got)
	}
	if got := byTitle["data.json"].Type; got != "file" {
		t.Errorf("data.json type = %q, want file", got)
	}
	// Every file-backed artifact carries a raw-serve URL, not inline data.
	for _, a := range arts {
		if a.URL == "" {
			t.Errorf("artifact %q missing URL", a.Title)
		}
	}

	// The artifacts must be readable back through the shared index the
	// list/serve endpoints use, and their backing files must exist.
	artDir, err := pluginArtifactDir(slug)
	if err != nil {
		t.Fatalf("pluginArtifactDir: %v", err)
	}
	items, err := loadArtifactIndex(artDir)
	if err != nil {
		t.Fatalf("loadArtifactIndex: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("index has %d items, want 2", len(items))
	}
	for _, it := range items {
		if it.FileName == "" {
			t.Errorf("artifact %q has no backing file name", it.Title)
			continue
		}
		if _, err := os.Stat(filepath.Join(artDir, it.FileName)); err != nil {
			t.Errorf("backing file for %q missing: %v", it.Title, err)
		}
	}

	// A second run that changes nothing must not re-ingest the same files
	// (the mtime diff excludes unchanged files and entry.py).
	after := snapshotEnvDir(envDir)
	again, err := h.ingestRunArtifacts(slug, envDir, after)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("unchanged env should ingest 0 artifacts, got %d", len(again))
	}
}

// TestUserPluginRuntime_RunHistory verifies runs.json is written atomically
// and trimmed to the most recent maxRunHistory records.
func TestUserPluginRuntime_RunHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const slug = "history-demo"
	total := maxRunHistory + 5
	for i := 0; i < total; i++ {
		if err := appendRunRecord(slug, pluginRunRecord{
			ID:         "r" + string(rune('a'+i%26)),
			Status:     "completed",
			ExitCode:   0,
			DurationMs: i,
			Artifacts:  0,
			CreatedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("appendRunRecord[%d]: %v", i, err)
		}
	}

	path, err := pluginRunsPath(slug)
	if err != nil {
		t.Fatalf("pluginRunsPath: %v", err)
	}
	records, err := loadRunHistory(path)
	if err != nil {
		t.Fatalf("loadRunHistory: %v", err)
	}
	if len(records) != maxRunHistory {
		t.Fatalf("history len = %d, want %d (trimmed)", len(records), maxRunHistory)
	}
	// FIFO trim keeps the tail: the last record's DurationMs is total-1.
	if got := records[len(records)-1].DurationMs; got != total-1 {
		t.Errorf("last record DurationMs = %d, want %d", got, total-1)
	}
}

// TestUserPluginRuntime_TypeMapping pins the file → artifact-type mapping
// the ingest step relies on (png/svg → image, html → html, else → file).
func TestUserPluginRuntime_TypeMapping(t *testing.T) {
	cases := map[string]string{
		"chart.png":   "image",
		"vector.svg":  "image",
		"report.html": "html",
		"index.htm":   "html",
		"data.json":   "file",
		"table.csv":   "file",
		"notes.txt":   "file",
		"archive.bin": "file", // unknown ext → still captured as file
	}
	for name, want := range cases {
		kind := kindFromName(name)
		got := "file"
		switch kind {
		case "png", "svg":
			got = "image"
		case "html":
			got = "html"
		}
		if got != want {
			t.Errorf("type for %q = %q, want %q", name, got, want)
		}
	}
}

// TestUserPluginRuntime_DatabasePersistsNotIngested proves the "container
// database" guarantee: a lab opens the SQLite DB at MULTICA_PLUGIN_DB, the
// file survives across runs (state accumulates), and it is NOT captured as an
// artifact on any run — only the rendered deliverable (index.html) is.
func TestUserPluginRuntime_DatabasePersistsNotIngested(t *testing.T) {
	if err := probePython3(); err != nil {
		t.Skipf("python3 not available: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	const slug = "db-demo"
	envDir, err := pluginEnvDir(slug)
	if err != nil {
		t.Fatalf("pluginEnvDir: %v", err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}

	// entry.py opens the platform-provided SQLite DB, appends one row, and
	// renders the running total to an HTML deliverable. It relies on both
	// the MULTICA_PLUGIN_DB env var and the persistent env dir.
	entry := "" +
		"import os, sqlite3\n" +
		"db = os.environ['MULTICA_PLUGIN_DB']\n" +
		"c = sqlite3.connect(db)\n" +
		"c.execute('CREATE TABLE IF NOT EXISTS runs(id INTEGER PRIMARY KEY)')\n" +
		"c.execute('INSERT INTO runs DEFAULT VALUES')\n" +
		"c.commit()\n" +
		"n = c.execute('SELECT COUNT(*) FROM runs').fetchone()[0]\n" +
		"c.close()\n" +
		"open('index.html','w').write('<h1>runs=%d</h1>' % n)\n" +
		"print(n)\n"
	if err := os.WriteFile(filepath.Join(envDir, entryFileName), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	h := &Handler{}

	// --- Run 1 ---
	before1 := snapshotEnvDir(envDir)
	if out, err := runEntryInDir(slug, envDir); err != nil {
		t.Fatalf("run 1 failed: %v\n%s", err, out)
	}
	arts1, err := h.ingestRunArtifacts(slug, envDir, before1)
	if err != nil {
		t.Fatalf("ingest 1: %v", err)
	}
	// Only index.html is an artifact; data.db (+ any sidecars) are excluded.
	if len(arts1) != 1 || arts1[0].Title != "index.html" {
		t.Fatalf("run 1 artifacts = %+v, want exactly [index.html]", arts1)
	}
	// The database file must actually exist in the persistent env dir.
	if _, err := os.Stat(filepath.Join(envDir, pluginDBFileName)); err != nil {
		t.Fatalf("database not created at env/%s: %v", pluginDBFileName, err)
	}

	// --- Run 2 --- (state must accumulate; DB still not ingested) ---
	before2 := snapshotEnvDir(envDir)
	if out, err := runEntryInDir(slug, envDir); err != nil {
		t.Fatalf("run 2 failed: %v\n%s", err, out)
	}
	arts2, err := h.ingestRunArtifacts(slug, envDir, before2)
	if err != nil {
		t.Fatalf("ingest 2: %v", err)
	}
	if len(arts2) != 1 || arts2[0].Title != "index.html" {
		t.Fatalf("run 2 artifacts = %+v, want exactly [index.html] (DB excluded)", arts2)
	}

	// The re-rendered HTML must show the accumulated count (2), proving the
	// DB persisted across the two runs.
	html, err := os.ReadFile(filepath.Join(envDir, "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if !strings.Contains(string(html), "runs=2") {
		t.Errorf("index.html = %q, want it to report runs=2 (persisted state)", html)
	}
}

// TestUserPluginRuntime_IngestExcludesPrivateData pins the ingestion filter:
// databases, their sidecars, dotfiles and .pyc stay private; everything else
// is a deliverable.
func TestUserPluginRuntime_IngestExcludesPrivateData(t *testing.T) {
	cases := map[string]bool{
		"index.html":      true,
		"chart.png":       true,
		"report.pdf":      true,
		"data.json":       true,
		"entry.py":        false,
		"data.db":         false,
		"data.db-wal":     false,
		"data.db-shm":     false,
		"data.db-journal": false,
		"store.sqlite":    false,
		"store.sqlite3":   false,
		".hidden":         false,
		"module.pyc":      false,
	}
	for name, want := range cases {
		if got := isIngestableName(name); got != want {
			t.Errorf("isIngestableName(%q) = %v, want %v", name, got, want)
		}
	}
}

// runEntryInDir mirrors the handler's exec path: `python3 -I entry.py` with
// cwd = the plugin env dir and the same MULTICA_PLUGIN_* env the handler
// injects. Returns combined output for failure diagnostics.
func runEntryInDir(slug, envDir string) (string, error) {
	cmd := exec.Command("python3", "-I", entryFileName)
	cmd.Dir = envDir
	cmd.Env = pluginRuntimeEnv(slug, envDir)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runCommandInDir mirrors the subprocess exec path: run the given command +
// args with cwd = the plugin env dir and the same sandbox env the handler
// injects. Returns combined output for failure diagnostics.
func runCommandInDir(slug, envDir, command string, args ...string) (string, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = envDir
	cmd.Env = pluginRuntimeEnv(slug, envDir)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// TestPluginRuntimeManifest_SubprocessCommandFields pins the manifest parse
// contract for runtime_kind=subprocess: manifest.runtime.command + args are
// the argv the 0.5.18 runtime executes (replacing the old 501 slot).
func TestPluginRuntimeManifest_SubprocessCommandFields(t *testing.T) {
	var rm pluginRuntimeManifest
	if err := json.Unmarshal([]byte(`{"runtime":{"kind":"subprocess","command":"/usr/bin/env","args":["FOO=bar"]}}`), &rm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rm.Runtime.Command != "/usr/bin/env" {
		t.Fatalf("command = %q, want /usr/bin/env", rm.Runtime.Command)
	}
	if len(rm.Runtime.Args) != 1 || rm.Runtime.Args[0] != "FOO=bar" {
		t.Fatalf("args = %v, want [FOO=bar]", rm.Runtime.Args)
	}
}

// TestUserPluginRuntime_SubprocessCommandEmitsArtifacts exercises the
// subprocess runtime core (snapshot → exec command+args → ingest) without the
// DB-backed HTTP layer, matching the existing inline test. A manifest-declared
// command that emits a file into the env dir must surface as an artifact.
func TestUserPluginRuntime_SubprocessCommandEmitsArtifacts(t *testing.T) {
	if err := probePython3(); err != nil {
		t.Skipf("python3 not available: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	const slug = "subprocess-demo"
	envDir, err := pluginEnvDir(slug)
	if err != nil {
		t.Fatalf("pluginEnvDir: %v", err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}

	// A subprocess runtime runs manifest.runtime.command + args (not
	// python3 -I entry.py). Use python3 -c to emit a file into cwd.
	before := snapshotEnvDir(envDir)
	if out, err := runCommandInDir(slug, envDir, "python3", "-c",
		"open('report.txt','w').write('subprocess-ok')"); err != nil {
		t.Fatalf("subprocess run failed: %v\n%s", err, out)
	}

	h := &Handler{}
	arts, err := h.ingestRunArtifacts(slug, envDir, before)
	if err != nil {
		t.Fatalf("ingestRunArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %+v", len(arts), arts)
	}
	if arts[0].Title != "report.txt" || arts[0].Type != "file" {
		t.Fatalf("unexpected artifact: %+v", arts[0])
	}
}
