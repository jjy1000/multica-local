package repocache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedLegacyHook writes the pre-MUL-6921 unconditional hook — the shape an
// upgraded host carries in every checkout made by an earlier release. It
// carries the daemon marker (so it is ours to reconcile) but reads no state.
func seedLegacyHook(t *testing.T, hooksDir string) string {
	t.Helper()
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	legacy := "#!/bin/sh\n# multica:prepare-commit-msg:co-authored-by\n# legacy unconditional trailer\ngit interpret-trailers --in-place --trailer 'Co-authored-by: multica-agent <github@multica.ai>' \"$1\"\n"
	if err := os.WriteFile(hookPath, []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	return hookPath
}

// seedBareCacheDir creates a directory isBareRepo recognizes (HEAD + objects),
// the shape of a bare cache entry, without a real clone.
func seedBareCacheDir(t *testing.T, barePath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(barePath, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(barePath, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishReconcilesLegacyBareCacheHook(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	cache := New(cacheRoot, testLogger())
	barePath := filepath.Join(cacheRoot, "ws-1", "repo.git")
	seedBareCacheDir(t, barePath)
	hookPath := seedLegacyHook(t, filepath.Join(barePath, "hooks"))

	// Disabled: the legacy hook reads no state, so the only way a published
	// "off" can reach it is deletion.
	if err := cache.WriteCoAuthoredByState("ws-1", false); err != nil {
		t.Fatalf("publish state: %v", err)
	}
	if err := cache.ReconcileCoAuthoredByHooks("ws-1", false); err != nil {
		t.Fatalf("reconcile hooks: %v", err)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("disabled legacy hook was not removed: %v", err)
	}
	state, err := os.ReadFile(cache.CoAuthoredByStatePath("ws-1"))
	if err != nil || strings.TrimSpace(string(state)) != "0" {
		t.Fatalf("state file = %q err=%v, want 0", state, err)
	}

	// Enabled: the legacy hook is rewritten to the gated script so a later
	// toggle-off applies at commit time.
	if err := cache.WriteCoAuthoredByState("ws-1", true); err != nil {
		t.Fatalf("publish state: %v", err)
	}
	if err := cache.ReconcileCoAuthoredByHooks("ws-1", true); err != nil {
		t.Fatalf("reconcile hooks: %v", err)
	}
	contents, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("enabled hook missing: %v", err)
	}
	if !strings.Contains(string(contents), "STATE_FILE=") {
		t.Fatal("reconciled hook is not the gated script")
	}
	if !strings.Contains(string(contents), cache.CoAuthoredByStatePath("ws-1")) {
		t.Fatal("gated hook does not reference the workspace state file")
	}
	state, err = os.ReadFile(cache.CoAuthoredByStatePath("ws-1"))
	if err != nil || strings.TrimSpace(string(state)) != "1" {
		t.Fatalf("state file = %q err=%v, want 1", state, err)
	}
}

func TestReconcileLeavesForeignHookAlone(t *testing.T) {
	t.Parallel()

	cache := New(t.TempDir(), testLogger())
	hooksDir := filepath.Join(cache.root, "ws-1", "repo.git", "hooks")
	seedBareCacheDir(t, filepath.Join(cache.root, "ws-1", "repo.git"))
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	userHook := "#!/bin/sh\n# my own hook\necho hi\n"
	if err := os.WriteFile(hookPath, []byte(userHook), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cache.reconcileHookAt(hooksDir, "ws-1", false); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	contents, err := os.ReadFile(hookPath)
	if err != nil || string(contents) != userHook {
		t.Fatalf("user hook was touched: %q err=%v", contents, err)
	}
}

func TestApplyCoAuthoredBySettingPrefersPublishedState(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	cache := New(cacheRoot, testLogger())
	// A real repo so rev-parse resolves the common dir; its .git is the
	// checkout's own metadata — the isolated-checkout shape.
	worktree := createTestRepo(t)

	// Snapshot says enabled, but the daemon published disabled — the hook must
	// go, because the snapshot may predate the toggle.
	if err := cache.WriteCoAuthoredByState("ws-1", false); err != nil {
		t.Fatalf("publish: %v", err)
	}
	cache.applyCoAuthoredBySetting(worktree, WorktreeParams{WorkspaceID: "ws-1", CoAuthoredByEnabled: true})
	if _, err := os.Stat(filepath.Join(worktree, ".git", "hooks", "prepare-commit-msg")); !os.IsNotExist(err) {
		t.Fatalf("published-off setting did not remove the hook: %v", err)
	}

	// Nothing published anymore (state file removed): the snapshot decides.
	if err := os.Remove(cache.CoAuthoredByStatePath("ws-1")); err != nil {
		t.Fatal(err)
	}
	cache.applyCoAuthoredBySetting(worktree, WorktreeParams{WorkspaceID: "ws-1", CoAuthoredByEnabled: true})
	contents, err := os.ReadFile(filepath.Join(worktree, ".git", "hooks", "prepare-commit-msg"))
	if err != nil {
		t.Fatalf("snapshot-enabled checkout did not install the hook: %v", err)
	}
	if !strings.Contains(string(contents), "STATE_FILE=") {
		t.Fatal("installed hook is not the gated script")
	}
}

// The gate is the fix: a hook on disk must honor the state file's CURRENT
// value at commit time, and a missing state file must mean what hook presence
// meant before the state file existed — trailer on.
func TestGatedHookHonorsStateFileAtCommitTime(t *testing.T) {
	t.Parallel()

	repo := createTestRepo(t)
	cacheRoot := t.TempDir()
	statePath := filepath.Join(cacheRoot, "ws-1", coAuthoredByStateFile)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}

	writeState := func(value string) {
		t.Helper()
		if err := os.WriteFile(statePath, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitSubject := func() {
		t.Helper()
		runGitAuthored(t, repo, "commit", "--allow-empty", "-m", "gate check")
	}
	readMsg := func() string {
		t.Helper()
		out, err := os.ReadFile(filepath.Join(repo, ".git", "COMMIT_EDITMSG"))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	if err := installCoAuthoredByHook(repo, statePath); err != nil {
		t.Fatalf("install gated hook: %v", err)
	}

	writeState("0")
	commitSubject()
	if strings.Contains(readMsg(), "Co-authored-by:") {
		t.Fatal("state 0 still produced a Co-authored-by trailer")
	}

	// Missing state file: hook presence meant trailer before the state file
	// existed, and it still does.
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	commitSubject()
	if !strings.Contains(readMsg(), "Co-authored-by:") {
		t.Fatal("missing state file did not keep the trailer")
	}

	writeState("1")
	commitSubject()
	if !strings.Contains(readMsg(), "Co-authored-by:") {
		t.Fatal("state 1 did not append the trailer")
	}
}
