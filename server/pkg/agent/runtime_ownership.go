// Package agent — runtime process-tree ownership helpers (port of MUL-6658).
//
// These helpers are the single point at which an agent backend's exec.Cmd
// becomes an owned process tree. They mirror upstream's contract:
//
//   - newRuntimeCmd applies the lifecycle defaults every runtime process in
//     this package gets: own process group + group-aware Cancel. A backend
//     that wants a graceful shutdown assigns its own cmd.Cancel afterwards
//     and wins (claude, dsh, deveco do this in upstream; in fork, claude +
//     opencode already do).
//   - startOwnedProcessTree is the only way the agent package starts a
//     long-lived runtime process. It is paired with releaseProcessGroup
//     after the Wait so the platform's ownership handle is dropped
//     (Windows Job Object; a no-op on Unix).
//   - runOwned / outputOwned / combinedOutputOwned give synchronous probes
//     (--version, model discovery) the same start + reap + release a task
//     launch gets. os/exec's Run/Output/CombinedOutput call Start themselves
//     and so own nothing — that was the synchronous half of GH #7522.
//
// Process-group ownership closes the leak where a cancelled task's tool
// subprocesses (MCP servers, shells) outlived the leader and, in some
// cases, kept running for tens of minutes after the daemon had already
// marked the task done and revoked its token.
//
// Fork note: this is a macOS-only build of the Unix half. The Windows
// Job Object surface is intentionally absent — see proc_windows.go,
// which carries fork's own smaller Window-specific helpers (HideWindow).
package agent

import (
	"bytes"
	"errors"
	"log/slog"
	"os/exec"
	"syscall"
	"time"
)

// probeWaitDelay bounds how long a finished probe waits on output pipes its
// descendants left open. It matches the bound detectCLIVersion already sets
// by hand. The timer only starts once the child has exited or the context
// is done, so a healthy probe never pays it.
const probeWaitDelay = 2 * time.Second

// probeStderrSampleBytes bounds the stderr kept for a failed probe's error.
// os/exec bounds the same sample at 32 KiB; a CLI stuck in a log loop should
// not be able to grow the daemon's heap through a --version call.
const probeStderrSampleBytes = 32 << 10

// newRuntimeCmd applies the process-lifecycle defaults every runtime process
// in this package gets. It runs at construction because both of them have to
// be in place before the process exists.
//
// Both used to be opt-in, and opt-in is why GH #7522 happened. Of the many
// places this package starts a process, only codex and opencode asked for a
// process group; the rest left their CLI in the daemon's group, where a
// group-wide signal cannot reach it. os/exec's default Cancel is the same
// leak by another route: it kills the leader alone, so a cancelled task's
// tool subprocesses — MCP servers, shells, whatever the agent spawned —
// survive it.
//
// A backend that wants a graceful shutdown instead of an immediate kill
// assigns its own cmd.Cancel after construction and wins (claude and
// opencode do, because they drive SIGTERM → grace → SIGKILL themselves).
func newRuntimeCmd(cmd *exec.Cmd) *exec.Cmd {
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		signalProcessGroupCmd(cmd, syscall.SIGKILL)
		return nil
	}
	return cmd
}

// runOwned is Run() over an owned process tree: start, wait, drop ownership.
//
// Without a bound this waits for its own cleanup and never returns. A
// descendant that inherited the output pipes holds them open after the
// leader exits; cmd.Wait blocks until the copy goroutines see EOF; and the
// thing that would close those pipes is the release below, which runs
// after Wait. WaitDelay is defined for exactly this case — "a child
// process that exits but leaves its I/O pipes unclosed" — and
// cancellation is no help, because a probe like a --version check runs
// on the caller's context before any task timeout exists.
//
// A caller that set its own bound keeps it (claude.go's detectCLIVersion
// has had one since MUL-3812 for this exact shape).
func runOwned(cmd *exec.Cmd, logger *slog.Logger) error {
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = probeWaitDelay
	}
	if err := startOwnedProcessTree(cmd, logger); err != nil {
		return err
	}
	err := cmd.Wait()
	// The probe is over the moment its leader is: nothing it spawned
	// should outlive the answer. Signalling before the release covers
	// Unix, where releasing a process group is a no-op; on Windows
	// closing the Job Object would take the tree down on its own.
	signalProcessGroupCmd(cmd, syscall.SIGKILL)
	releaseProcessGroup(cmd)
	return err
}

// outputOwned is cmd.Output() over an owned process tree. It matches the
// stdlib's contract — stdout returned, a failed run's stderr attached to
// the *exec.ExitError — except that the stderr sample is the last
// probeStderrSampleBytes rather than the stdlib's head-and-tail, which is
// where a CLI's actual failure line ends up.
func outputOwned(cmd *exec.Cmd, logger *slog.Logger) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// A caller that set its own Stderr wants it; only fill in the sample
	// when nothing else is watching, exactly as Output() does.
	var stderr *tailBuffer
	if cmd.Stderr == nil {
		stderr = &tailBuffer{max: probeStderrSampleBytes}
		cmd.Stderr = stderr
	}

	err := runOwned(cmd, logger)
	var exitErr *exec.ExitError
	if stderr != nil && errors.As(err, &exitErr) {
		exitErr.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

// combinedOutputOwned is cmd.CombinedOutput() over an owned process tree.
// Stdout and Stderr are the same writer value, which is what makes
// os/exec give them one pipe and therefore one interleaving, as the
// stdlib does.
func combinedOutputOwned(cmd *exec.Cmd, logger *slog.Logger) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if cmd.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := runOwned(cmd, logger)
	return combined.Bytes(), err
}

// tailBuffer keeps the last max bytes written to it and discards the rest.
// It always reports a full write, so a child is never blocked or shortened
// by the bound.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }