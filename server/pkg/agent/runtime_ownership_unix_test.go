//go:build unix

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestNewRuntimeCmdPutsChildInOwnProcessGroup pins the default that GH #7522
// showed was missing. Before MUL-6658, each backend had to remember to ask
// for a process group and most did not, so a group-wide signal could not
// reach their CLI at all. The group now comes from the one place a runtime
// process is built, which means a backend cannot launch without it.
func TestNewRuntimeCmdPutsChildInOwnProcessGroup(t *testing.T) {
	t.Parallel()

	cmd := newRuntimeCmd(exec.CommandContext(context.Background(), "/bin/sh", "-c", "true"))
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("newRuntimeCmd must set Setpgid so cancellation can signal the whole tree")
	}
	if cmd.Cancel == nil {
		t.Fatal("newRuntimeCmd must install a group-aware Cancel so the default does not leak the leader-only kill")
	}
}

// TestNewRuntimeCmdCancelKillsDescendants is the behaviour the default buys,
// on a command with no backend-specific cancellation logic at all: the tool
// subprocesses an agent spawned must die with it.
//
// os/exec's own Cancel kills the leader alone, which is what left a
// cancelled agent's descendants running. The fake here spawns a grandchild
// that outlives its parent, so killing the leader is not enough to pass.
func TestNewRuntimeCmdCancelKillsDescendants(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	pidFile := filepath.Join(tempDir, "pids")
	fakePath := filepath.Join(tempDir, "runtime")
	writeTestExecutable(t, fakePath, []byte("#!/bin/sh\n"+
		`( sleep 300 ) </dev/null >/dev/null 2>&1 &
printf '%s %s\n' "$$" "$!" > "$1"
sleep 300
`))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newRuntimeCmd(exec.CommandContext(ctx, fakePath, pidFile))
	if err := startOwnedProcessTree(cmd, slog.Default()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer releaseProcessGroup(cmd)

	_, grandchild := waitForOwnedPids(t, pidFile)

	// Cancel fires cmd.Cancel (the group-aware Cancel installed by
	// newRuntimeCmd), which signals the whole process group. The leader
	// and its grandchild must both die; the leader's death is what
	// closes stdout and unblocks Wait.
	cancel()

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled process was never reaped")
	}
	waitForProcessGone(t, grandchild)
}

// TestOutputOwnedReturnsStdoutOnSuccess pins the stdlib contract that
// cmd.Output() owns: stdout returned, nil error on success.
func TestOutputOwnedReturnsStdoutOnSuccess(t *testing.T) {
	t.Parallel()

	cmd := newRuntimeCmd(exec.CommandContext(context.Background(), "/bin/sh", "-c", "echo hello"))
	data, err := outputOwned(cmd, slog.Default())
	if err != nil {
		t.Fatalf("outputOwned: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("stdout = %q, want %q", string(data), "hello\n")
	}
}

// TestOutputOwnedAttachesStderrOnFailure pins the ExitError contract: when
// the probe fails, the bounded stderr tail is attached to *exec.ExitError
// (this is what runtime errors in the daemon log render against).
func TestOutputOwnedAttachesStderrOnFailure(t *testing.T) {
	t.Parallel()

	cmd := newRuntimeCmd(exec.CommandContext(context.Background(), "/bin/sh", "-c", "echo bad >&2; exit 1"))
	_, err := outputOwned(cmd, slog.Default())
	if err == nil {
		t.Fatal("expected the probe to fail")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error type = %T, want *exec.ExitError", err)
	}
	if len(exitErr.Stderr) == 0 {
		t.Fatal("ExitError.Stderr is empty; outputOwned must attach the bounded stderr sample")
	}
}

// TestCombinedOutputOwnedReturnsCombined is the CombinedOutput contract:
// stdout and stderr are interleaved onto one pipe and returned as one slice.
func TestCombinedOutputOwnedReturnsCombined(t *testing.T) {
	t.Parallel()

	cmd := newRuntimeCmd(exec.CommandContext(context.Background(), "/bin/sh", "-c", "echo a; echo b >&2; echo c"))
	data, err := combinedOutputOwned(cmd, slog.Default())
	if err != nil {
		t.Fatalf("combinedOutputOwned: %v", err)
	}
	got := string(data)
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(got, want) {
			t.Fatalf("combined output missing %q: %q", want, got)
		}
	}
}

// TestReleaseProcessGroupIsIdempotent pins the no-op semantics for the
// Unix half: releaseProcessGroup has no handle to drop, so calling it
// twice or before startOwnedProcessTree succeeds must not panic.
// (Windows' Job Object half requires a real handle and is exercised by
// proc_windows_test.go upstream; the fork's macOS-only build skips that
// side.)
func TestReleaseProcessGroupIsIdempotent(t *testing.T) {
	t.Parallel()

	cmd := newRuntimeCmd(exec.CommandContext(context.Background(), "/bin/sh", "-c", "true"))
	releaseProcessGroup(cmd)
	if err := startOwnedProcessTree(cmd, slog.Default()); err != nil {
		t.Fatalf("start: %v", err)
	}
	releaseProcessGroup(cmd)
	releaseProcessGroup(cmd)
	_ = cmd.Wait()
}

// waitForOwnedPids reads the "leader grandchild" pid pair the fake
// writes and returns (leader, grandchild). Only the grandchild pid is
// reaped by the assertion in the caller; the leader is reaped by Wait.
func waitForOwnedPids(t *testing.T, pidFile string) (int, int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(data) > 0 {
			var leader, grandchild int
			if n, _ := fmt.Sscan(string(data), &leader, &grandchild); n == 2 {
				return leader, grandchild
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the fake runtime to write its pids")
	return 0, 0
}

// waitForProcessGone polls kill(pid, 0) until ESRCH or the deadline.
func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after 5s", pid)
}