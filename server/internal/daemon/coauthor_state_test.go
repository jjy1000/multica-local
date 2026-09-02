package daemon

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
)

// coauthorSweepTestDaemon builds a minimal daemon wired to a real repo cache
// whose root sits inside workspacesRoot, the production layout on the fork.
func coauthorSweepTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	root := t.TempDir()
	d := &Daemon{
		logger:     slog.Default(),
		workspaces: make(map[string]*workspaceState),
	}
	d.cfg.WorkspacesRoot = root
	d.repoCache = repocache.New(filepath.Join(root, ".repos"), slog.Default())
	return d
}

// seedIsolatedCheckout creates an env root shaped like production —
// <root>/<ws>/<short-id>/workdir/<repo>/.git — with a legacy unconditional
// hook in the checkout's own hooks dir, plus the owner marker Prepare writes.
func seedIsolatedCheckout(t *testing.T, root, wsID, taskShort, repoName string, ownerWorkspace string) string {
	t.Helper()
	envRoot := filepath.Join(root, wsID, taskShort)
	checkout := filepath.Join(envRoot, "workdir", repoName)
	if err := os.MkdirAll(filepath.Join(checkout, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "#!/bin/sh\n# multica:prepare-commit-msg:co-authored-by\n# legacy unconditional trailer\nexit 0\n"
	if err := os.WriteFile(filepath.Join(checkout, ".git", "hooks", "prepare-commit-msg"), []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := execenv.EnvRootOwner{TaskID: taskShort}
	if ownerWorkspace != "" {
		owner.WorkspaceID = ownerWorkspace
	}
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envRoot, ".task_owner"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(checkout, ".git", "hooks", "prepare-commit-msg")
}

func TestReconcileIsolatedCoAuthoredByHooksAttributedByOwner(t *testing.T) {
	t.Parallel()

	d := coauthorSweepTestDaemon(t)
	owned := seedIsolatedCheckout(t, d.cfg.WorkspacesRoot, "ws-a", "aaaabbbb", "repo", "ws-a")
	foreign := seedIsolatedCheckout(t, d.cfg.WorkspacesRoot, "ws-b", "ccccdddd", "repo", "ws-b")
	// Pre-marker stock: no owner record, directory name is the evidence.
	legacy := seedIsolatedCheckout(t, d.cfg.WorkspacesRoot, "ws-a", "eeeeffff", "repo", "")
	os.Remove(filepath.Join(d.cfg.WorkspacesRoot, "ws-a", "eeeeffff", ".task_owner"))

	d.workspaces["ws-a"] = newWorkspaceState("ws-a", nil, "", nil, json.RawMessage(`{"co_authored_by_enabled":false}`))

	// Publishing ws-a disabled must reconcile the owned and legacy-shaped
	// checkouts, and never touch another workspace's hooks.
	d.publishCoAuthoredByState("ws-a", func(string) bool { return false })

	for _, path := range []string{owned, legacy} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("disabled setting did not remove hook at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("another workspace's hook was rewritten: %v", err)
	}
}

func TestPublishCoAuthoredByStateWritesStateFile(t *testing.T) {
	t.Parallel()

	d := coauthorSweepTestDaemon(t)
	d.workspaces["ws-a"] = newWorkspaceState("ws-a", nil, "", nil, json.RawMessage(`{"co_authored_by_enabled":true}`))

	d.publishCoAuthoredByState("ws-a", d.workspaceCoAuthoredByEnabled)

	path := d.repoCache.(*repocache.Cache).CoAuthoredByStatePath("ws-a")
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "1\n" {
		t.Fatalf("state file = %q err=%v, want enabled", contents, err)
	}
}

func TestEnvRootBelongsToWorkspace(t *testing.T) {
	t.Parallel()

	d := coauthorSweepTestDaemon(t)
	mark := func(envRoot string, owner execenv.EnvRootOwner) {
		t.Helper()
		data, err := json.Marshal(owner)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(envRoot, ".task_owner"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	marked := filepath.Join(d.cfg.WorkspacesRoot, "ws-a", "aaaabbbb")
	if err := os.MkdirAll(marked, 0o755); err != nil {
		t.Fatal(err)
	}
	mark(marked, execenv.EnvRootOwner{WorkspaceID: "ws-a", TaskID: "aaaabbbb"})
	if !d.envRootBelongsToWorkspace("ws-a", marked, "ws-a") {
		t.Fatal("owner record naming ws-a was not attributed to ws-a")
	}
	if d.envRootBelongsToWorkspace("ws-a", marked, "ws-b") {
		t.Fatal("owner record naming ws-a was attributed to ws-b")
	}

	unmarked := filepath.Join(d.cfg.WorkspacesRoot, "ws-a", "eeeeffff")
	if err := os.MkdirAll(unmarked, 0o755); err != nil {
		t.Fatal(err)
	}
	if !d.envRootBelongsToWorkspace("ws-a", unmarked, "ws-a") {
		t.Fatal("pre-marker stock under its own workspace dir was not attributed")
	}
	if d.envRootBelongsToWorkspace("ws-b", unmarked, "ws-a") {
		t.Fatal("directory name must be the workspace for unmarked roots")
	}

	tampered := filepath.Join(d.cfg.WorkspacesRoot, "ws-a", "ab12cd34")
	if err := os.MkdirAll(tampered, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tampered, ".task_owner"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d.envRootBelongsToWorkspace("ws-a", tampered, "ws-a") {
		t.Fatal("an unreadable marker must attribute nothing")
	}
}
