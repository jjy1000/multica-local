package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// TestHandleTask_DoesNotCallStartTaskItself is the regression guard for
// issue #3999 race A. handleTask must not call /tasks/{id}/start before
// runner.run — the runner is now responsible for calling StartTask only
// after execenv.Prepare/Reuse has put env.WorkDir on disk, so consumers
// that read status==running can resolve the workdir path without racing
// the daemon's os.MkdirAll.
//
// Before the fix: handleTask called StartTask before invoking the runner,
// flipping the server-side state to "running" while the per-task workdir
// still didn't exist on disk. Hermes/OpenClaw agents that resolved
// /multica_workspaces/{ws}/{short-id}/workdir from the running signal
// would then hit FileNotFoundError.
func TestHandleTask_DoesNotCallStartTaskItself(t *testing.T) {
	t.Parallel()

	var (
		startCalls   atomic.Int64
		runnerCalled atomic.Bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/start"):
			startCalls.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots:     make(map[string]int),
		cancelPollInterval: time.Hour, // disable poll-cancel path; we only care about the entry-side ordering
	}

	// Fake runner that does NOT call StartTask — production runTask does
	// the call itself, after Prepare/Reuse confirms env.WorkDir on disk.
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		runnerCalled.Store(true)
		return TaskResult{Status: "completed"}, nil
	})

	task := Task{
		ID:          "task-no-start",
		WorkspaceID: "ws-no-start",
		RuntimeID:   "rt-1",
		IssueID:     "issue-no-start",
		Agent:       &AgentData{Name: "test-agent"},
	}

	d.handleTask(context.Background(), task, 0)

	if !runnerCalled.Load() {
		t.Fatal("fake runner was never invoked — handleTask aborted before runner.run, can't assert ordering")
	}
	if got := startCalls.Load(); got != 0 {
		t.Fatalf("handleTask called /start %d time(s); StartTask must be runTask's responsibility now (issue #3999 race A)", got)
	}
}

// TestRunTask_StartTaskCalledAfterWorkdirOnDisk is the behavioral regression
// guard for issue #3999 race A. Calls runTask directly with a missing agent
// binary so the run aborts at exec time — but only AFTER reaching the
// post-Prepare StartTask call. The fake server records whether the per-task
// workdir already exists on disk at the moment /start is hit; before the
// fix it did not.
func TestRunTask_StartTaskCalledAfterWorkdirOnDisk(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	workspaceID := "ws-runtask"
	taskID := "task-runtask-after-mkdir"
	expectedEnvRoot := execenv.PredictRootDir(workspacesRoot, workspaceID, taskID)
	expectedWorkDir := filepath.Join(expectedEnvRoot, "workdir")

	var (
		startCalled   atomic.Bool
		workdirOnDisk atomic.Bool
		envRootOnDisk atomic.Bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			startCalled.Store(true)
			if info, err := os.Stat(expectedWorkDir); err == nil && info.IsDir() {
				workdirOnDisk.Store(true)
			}
			if info, err := os.Stat(expectedEnvRoot); err == nil && info.IsDir() {
				envRootOnDisk.Store(true)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// Provider entry intentionally points at a non-existent binary: runTask
	// reaches Prepare → StartTask → ReportProgress before agent.Backend.Run
	// fails at exec time. We don't care about the eventual error; the
	// regression guard is the order of /start vs. os.MkdirAll(envRoot).
	missingBin := filepath.Join(t.TempDir(), "definitely-not-claude")
	d := &Daemon{
		client:         NewClient(srv.URL),
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:     make(map[string]*workspaceState),
		runtimeIndex:   map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots: make(map[string]int),
		cfg: Config{
			WorkspacesRoot: workspacesRoot,
			Agents: map[string]AgentEntry{
				"claude": {Path: missingBin, Model: ""},
			},
		},
	}

	task := Task{
		ID:          taskID,
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-runtask",
		Agent:       &AgentData{Name: "test-agent"},
	}

	taskLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The Run() failure is expected; we only assert the pre-Run ordering.
	_, _ = d.runTask(context.Background(), task, "claude", 0, taskLog)

	if !startCalled.Load() {
		t.Fatal("runTask did not call /start — Fix A's StartTask placement is missing")
	}
	if !envRootOnDisk.Load() {
		t.Fatal("envRoot did not exist on disk when /start was called — Prepare must run before StartTask (issue #3999 race A)")
	}
	if !workdirOnDisk.Load() {
		t.Fatal("envRoot/workdir did not exist on disk when /start was called — os.MkdirAll must complete before StartTask (issue #3999 race A)")
	}
}

func TestRunTask_ExtendsPrepareLeaseDuringStartTask(t *testing.T) {
	oldRefresh := taskPrepareLeaseRefresh
	oldTimeout := taskPrepareLeaseTimeout
	taskPrepareLeaseRefresh = 10 * time.Millisecond
	taskPrepareLeaseTimeout = 500 * time.Millisecond
	t.Cleanup(func() {
		taskPrepareLeaseRefresh = oldRefresh
		taskPrepareLeaseTimeout = oldTimeout
	})

	workspacesRoot := t.TempDir()
	workspaceID := "ws-runtask-start-lease"
	taskID := "task-runtask-start-lease"
	var (
		startEntered     atomic.Bool
		leaseDuringStart atomic.Bool
		closeLeaseOnce   sync.Once
	)
	leaseSeenDuringStart := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-lease"):
			if startEntered.Load() {
				leaseDuringStart.Store(true)
				closeLeaseOnce.Do(func() { close(leaseSeenDuringStart) })
			}
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/start"):
			startEntered.Store(true)
			select {
			case <-leaseSeenDuringStart:
			case <-time.After(2 * time.Second):
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	missingBin := filepath.Join(t.TempDir(), "definitely-not-claude")
	d := &Daemon{
		client:         NewClient(srv.URL),
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:     make(map[string]*workspaceState),
		runtimeIndex:   map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots: make(map[string]int),
		cfg: Config{
			WorkspacesRoot: workspacesRoot,
			Agents: map[string]AgentEntry{
				"claude": {Path: missingBin, Model: ""},
			},
		},
	}

	task := Task{
		ID:          taskID,
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-runtask-start-lease",
		Agent:       &AgentData{Name: "test-agent"},
	}

	taskLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, _ = d.runTask(context.Background(), task, "claude", 0, taskLog)

	if !startEntered.Load() {
		t.Fatal("runTask did not call /start")
	}
	if !leaseDuringStart.Load() {
		t.Fatal("prepare lease was not extended while /start was still in flight")
	}
}

// TestHandleTask_KeepsEnvRootActiveAcrossCompletion is the regression guard
// for issue #3999 race B. After runner.run returns, the in-process active
// guard installed inside runTask (defer unmarkActiveEnvRoot at the
// goroutine's exit) has already fired by the time handleTask calls
// reportTaskResult and execenv.WriteGCMeta. Without an outer guard at the
// handleTask level, the GC loop sees a window where the directory has
// neither isActiveEnvRoot nor a .gc_meta.json file — falling through to
// orphanByMTime, gated only by the 72h GCOrphanTTL.
//
// This test fakes the inner guard's lifecycle (mark + deferred unmark),
// then asserts that at the moment /complete is hit (i.e. between runner.run
// returning and WriteGCMeta running), isActiveEnvRoot(envRoot) is still
// true thanks to the outer guard handleTask installs.
func TestHandleTask_KeepsEnvRootActiveAcrossCompletion(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	workspaceID := "ws-active-during-complete"
	taskID := "task-active-during-complete"
	expectedEnvRoot := execenv.PredictRootDir(workspacesRoot, workspaceID, taskID)

	var (
		completeCalled   atomic.Bool
		activeAtComplete atomic.Bool
	)

	d := &Daemon{
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots:     make(map[string]int),
		cancelPollInterval: time.Hour,
		cfg:                Config{WorkspacesRoot: workspacesRoot},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			completeCalled.Store(true)
			// This is the exact window race B exposed: the inner deferred
			// unmark has already fired (see fake runner below); only the
			// outer guard installed by handleTask keeps the env root in the
			// active set at this moment.
			if d.isActiveEnvRoot(expectedEnvRoot) {
				activeAtComplete.Store(true)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	d.client = NewClient(srv.URL)

	// Fake runner mimics the real runTask's mark/defer-unmark pair. Without
	// the outer guard added in handleTask, the deferred unmark would bring
	// isActiveEnvRoot back to false before reportTaskResult fires.
	d.runner = taskRunnerFunc(func(_ context.Context, tk Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		predicted := execenv.PredictRootDir(d.cfg.WorkspacesRoot, tk.WorkspaceID, tk.ID)
		d.markActiveEnvRoot(predicted)
		defer d.unmarkActiveEnvRoot(predicted)
		return TaskResult{
			Status:  "completed",
			EnvRoot: predicted,
		}, nil
	})

	task := Task{
		ID:          taskID,
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-active-during-complete",
		Agent:       &AgentData{Name: "test-agent"},
	}

	d.handleTask(context.Background(), task, 0)

	if !completeCalled.Load() {
		t.Fatal("/complete was never hit — handleTask did not reach reportTaskResult")
	}
	if !activeAtComplete.Load() {
		t.Fatal("env root was NOT in the active set at /complete time — issue #3999 race B regression: GC could reclaim the directory between runner.run returning and WriteGCMeta landing on disk")
	}
	// And the outer guard must have been released by the time handleTask
	// returned, otherwise we'd be leaking active marks across tasks.
	if d.isActiveEnvRoot(expectedEnvRoot) {
		t.Fatal("env root remained active after handleTask returned — outer guard's deferred unmark did not fire")
	}
}

// TestRunTask_InjectsPrivateTaskTempDir is the regression guard for the
// per-task temp-dir fix (MUL-5799). A high-load sub-agent delegation run
// would spawn a Hermes subprocess whose $TMPDIR inherited the daemon's
// long /var/folders/.../T/ path; once a deep workdir-derived filename was
// appended under it, the resulting AF_UNIX path exceeded the 104-byte
// (macOS) / 108-byte (Linux) sun_path cap and the agent CLI failed with
// "AF_UNIX path too long". The fix routes every spawned agent's TMPDIR
// (and the Windows-flavored TMP/TEMP aliases) onto a private, short
// /tmp/multica-<uid>/task-<hash> directory that ensureTaskTempDir
// creates. The test verifies:
//   - TMPDIR / TMP / TEMP all point at the private dir (not at any
//     custom_env override the agent might have tried to inject — the
//     isBlockedEnvKey blocklist must catch that)
//   - the private dir exists on disk at the moment the agent CLI runs
//   - the dir does NOT live under the long envRoot (defeating the
//     original bug)
//   - on Unix the full path stays short enough for AF_UNIX socket bind
//     even after a typical Hermes-style suffix is appended
func TestRunTask_InjectsPrivateTaskTempDir(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel — drop the parallel
	// marker (this test pins MULTICA_AGENT_TEMP_BASE to "" for
	// cross-test isolation hardening; see the Setenv call below).

	// Hardening: this test reads MULTICA_AGENT_TEMP_BASE indirectly
	// via taskTempDirPath. Sibling tests (TestRunTask_TaskTempBase*
	// etc.) set it via t.Setenv — clear it here so a future re-ordering
	// of the test file cannot make this test see their override.
	t.Setenv("MULTICA_AGENT_TEMP_BASE", "")
	t.Cleanup(func() { t.Setenv("MULTICA_AGENT_TEMP_BASE", "") })

	workspacesRoot := t.TempDir()
	workspaceID := "ws-private-temp"
	taskID := "task-private-temp"
	envRoot := execenv.PredictRootDir(workspacesRoot, workspaceID, taskID)
	expectedTempDir := taskTempDirPath(envRoot, taskID)

	captureFile := filepath.Join(t.TempDir(), "agent-env.txt")
	fakeBin := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
if [ -d "$TMPDIR" ]; then tmpdir_exists=1; else tmpdir_exists=0; fi
printf 'TMPDIR=%s\nTMP=%s\nTEMP=%s\nTMPDIR_EXISTS=%s\n' "$TMPDIR" "$TMP" "$TEMP" "$tmpdir_exists" > "$CAPTURE_FILE"
IFS= read -r _
printf '%s\n' '{"type":"system","session_id":"sess-private-temp"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"sess-private-temp","result":"done"}'
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots:     make(map[string]int),
		cancelPollInterval: time.Hour,
		cfg: Config{
			WorkspacesRoot: workspacesRoot,
			AgentTimeout:   5 * time.Second,
			ServerBaseURL:  srv.URL,
			Agents: map[string]AgentEntry{
				"claude": {Path: fakeBin, Model: ""},
			},
		},
	}

	// CustomEnv tries to override TMPDIR / TMP / TEMP — isBlockedEnvKey
	// must reject them and the daemon-injected value must win. CAPTURE_FILE
	// is a legitimate (non-blocked) key the fake script needs to know
	// where to write its captured env.
	task := Task{
		ID:          taskID,
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-private-temp",
		AuthToken:   "mat_private_temp",
		Agent: &AgentData{
			ID:   "agent-private-temp",
			Name: "test-agent",
			CustomEnv: map[string]string{
				"CAPTURE_FILE": captureFile,
				"TMPDIR":       "/shared/tmp",
				"TMP":          "/shared/tmp",
				"TEMP":         "/shared/tmp",
			},
		},
	}

	taskLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := d.runTask(context.Background(), task, "claude", 0, taskLog)
	if err != nil {
		t.Fatalf("runTask failed: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("runTask status = %q, want completed (comment=%q)", result.Status, result.Comment)
	}

	raw, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatalf("read captured agent env: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		got[line[:eq]] = line[eq+1:]
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if got[key] != expectedTempDir {
			t.Fatalf("%s = %q, want private task temp dir %q", key, got[key], expectedTempDir)
		}
	}
	if got["TMPDIR_EXISTS"] != "1" {
		t.Fatalf("agent did not see task temp dir on disk: captured env %v", got)
	}
	if strings.Contains(expectedTempDir, envRoot) {
		t.Fatalf("task temp dir must not live under long env root %q: got %q", envRoot, expectedTempDir)
	}
	if os.PathSeparator == '/' && len(expectedTempDir)+afUnixSunPathSuffixReserve >= afUnixSunPathCap {
		t.Fatalf("task temp dir must stay short for Unix-domain sockets: len(%q)+%d >= cap %d", expectedTempDir, afUnixSunPathSuffixReserve, afUnixSunPathCap)
	}
}

// TestRunTask_TaskTempBaseOverride verifies the MULTICA_AGENT_TEMP_BASE
// env var successfully relocates the per-task temp dir. Self-hosters on
// a read-only /tmp (e.g. a hardened macOS sandbox) can point at a
// writable alternate. The override must be honored even when the
// default /tmp/multica-<uid> does not exist yet (ensureTaskTempDir
// MkdirAll's the root, so a fresh override just works).
func TestRunTask_TaskTempBaseOverride(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel — drop the parallel marker
	// (this test exercises a per-test env override; parallelism would
	// race the override with sibling tests' inherited env).
	//
	// We also explicitly clear MULTICA_AGENT_TEMP_BASE for sibling tests
	// that run before us in serial order (TestRunTask_InjectsPrivateTaskTempDir
	// reads the env indirectly via taskTempDirPath). t.Cleanup ensures
	// the unset survives even if this test panics mid-run.

	// The override base must be short enough that the final task
	// dir + AF_UNIX sun_path budget fits — t.TempDir() returns the
	// long macOS /var/folders/.../T/ path which exceeds the cap, so
	// we use a short /tmp/multica-test-* dir instead (matches the
	// real-world override shape: a self-hoster pointing at /tmp).
	overrideBase, err := os.MkdirTemp("/tmp", "multica-test-override-")
	if err != nil {
		t.Fatalf("create short override base: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(overrideBase) })
	t.Setenv("MULTICA_AGENT_TEMP_BASE", overrideBase)
	t.Cleanup(func() { t.Setenv("MULTICA_AGENT_TEMP_BASE", "") })

	workspacesRoot := t.TempDir()
	workspaceID := "ws-override-base"
	taskID := "task-override-base"
	envRoot := execenv.PredictRootDir(workspacesRoot, workspaceID, taskID)

	// expectedTempDir uses the helper rather than re-deriving, so this
	// test stays correct if ensureTaskTempDir's path layout ever changes
	// (e.g. a per-workspace subdir is added).
	expectedTempDir := taskTempDirPath(envRoot, taskID)
	if !strings.HasPrefix(expectedTempDir, overrideBase) {
		t.Fatalf("taskTempDirPath(%q, %q) = %q; want it under override base %q", envRoot, taskID, expectedTempDir, overrideBase)
	}

	captureFile := filepath.Join(t.TempDir(), "agent-env.txt")
	fakeBin := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
printf 'TMPDIR=%s\n' "$TMPDIR" > "$CAPTURE_FILE"
IFS= read -r _
printf '%s\n' '{"type":"system","session_id":"sess-override-base"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"sess-override-base","result":"done"}'
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots:     make(map[string]int),
		cancelPollInterval: time.Hour,
		cfg: Config{
			WorkspacesRoot: workspacesRoot,
			AgentTimeout:   5 * time.Second,
			ServerBaseURL:  srv.URL,
			Agents: map[string]AgentEntry{
				"claude": {Path: fakeBin, Model: ""},
			},
		},
	}

	task := Task{
		ID:          taskID,
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-override-base",
		AuthToken:   "mat_override",
		Agent: &AgentData{
			ID:   "agent-override-base",
			Name: "test-agent",
			CustomEnv: map[string]string{
				"CAPTURE_FILE": captureFile,
			},
		},
	}

	taskLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := d.runTask(context.Background(), task, "claude", 0, taskLog)
	if err != nil {
		t.Fatalf("runTask failed: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("runTask status = %q, want completed (comment=%q)", result.Status, result.Comment)
	}

	raw, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatalf("read captured agent env: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		got[line[:eq]] = line[eq+1:]
	}
	if got["TMPDIR"] != expectedTempDir {
		t.Fatalf("TMPDIR = %q, want override-base path %q", got["TMPDIR"], expectedTempDir)
	}
}

// TestRunTask_TaskTempBaseInvalidFailsStartup pins the MULTICA_AGENT_TEMP_BASE
// validation contract: a misconfigured override fails task startup with
// a clear error rather than silently falling back to /tmp (which on a
// hardened host may itself be the failure mode the user is trying to
// escape). Cases — missing dir, not-a-dir, non-writable, too-long
// path — each must reject without spawning the agent CLI.
func TestRunTask_TaskTempBaseInvalidFailsStartup(t *testing.T) {
	// t.Setenv in subtests is incompatible with t.Parallel at this level
	// either — subtest env overrides would not be properly isolated if
	// the parent ran in parallel with siblings. Run sequentially.

	validBase := t.TempDir()
	missing := filepath.Join(validBase, "missing")
	notDir := filepath.Join(validBase, "not-a-dir")
	if err := os.WriteFile(notDir, []byte("file"), 0o600); err != nil {
		t.Fatalf("write not-dir fixture: %v", err)
	}
	// Read-only base: chmod 0o500 (r-x, no write). The writeprobe inside
	// ensureTaskTempDir must fail. Cleanup needs write, so loosen again
	// before t.TempDir's recursive remove runs.
	readOnlyBase := t.TempDir()
	if err := os.Chmod(readOnlyBase, 0o500); err != nil {
		t.Fatalf("chmod read-only base: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyBase, 0o700) })

	// Too-long override: build a nested dir deep enough that the final
	// per-task path would exceed afUnixSunPathCap. The check rejects
	// silently-long overrides that would defeat the MUL-5799 fix.
	longBase := filepath.Join(t.TempDir(), strings.Repeat("x", 80))
	if err := os.MkdirAll(longBase, 0o700); err != nil {
		t.Fatalf("create long-base fixture: %v", err)
	}

	cases := []struct {
		name              string
		base              string
		mustMentionSubstr string // empty = just "MULTICA_AGENT_TEMP_BASE"
	}{
		{name: "missing dir rejected", base: missing, mustMentionSubstr: "MULTICA_AGENT_TEMP_BASE"},
		{name: "non-directory rejected", base: notDir, mustMentionSubstr: "MULTICA_AGENT_TEMP_BASE"},
		{name: "non-writable dir rejected", base: readOnlyBase, mustMentionSubstr: "MULTICA_AGENT_TEMP_BASE"},
		{name: "too-long path rejected", base: longBase, mustMentionSubstr: "AF_UNIX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MULTICA_AGENT_TEMP_BASE", tc.base)

			// Pure helper-level validation: ensureTaskTempDir's contract
			// is the same regardless of caller. Pinning it here keeps
			// the regression surface tight — the agent CLI never even
			// gets spawned when the validation rejects.
			_, err := ensureTaskTempDir("env-root", "task-id")
			if err == nil {
				// Some processes (root in particular) can write
				// through a 0o500 dir; skip rather than fail in that
				// case so the test stays portable.
				if tc.base == readOnlyBase {
					t.Skip("process can write to the read-only fixture")
				}
				t.Fatalf("ensureTaskTempDir accepted invalid MULTICA_AGENT_TEMP_BASE %q", tc.base)
			}
			if !strings.Contains(err.Error(), tc.mustMentionSubstr) {
				t.Fatalf("error must name %q so the misconfig is debuggable; got %v", tc.mustMentionSubstr, err)
			}
		})
	}
}
