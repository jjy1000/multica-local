//go:build !windows

package agent

import (
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

// hideAgentWindow is a no-op on non-Windows platforms.
func hideAgentWindow(cmd *exec.Cmd) {}

// configureProcessGroup puts the child into its own process group (it becomes
// the group leader, so the group id equals the child pid). This lets the
// daemon signal the entire tree — the agent CLI plus any tool subprocess it
// spawns — in one call, instead of killing only the direct child and leaking
// grandchildren that keep running (and, for opencode, spinning on EPIPE) after
// a task is cancelled or the daemon restarts. See signalProcessGroup.
//
// Called by newRuntimeCmd in runtime_ownership.go, which is the single
// point where a runtime process is constructed. No backend calls it
// directly: the group has to exist for every runtime process, and
// per-backend opt-in did not deliver that (GH #7522 / MUL-6658).
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// startOwnedProcessTree is a plain Start on non-Windows platforms:
// newRuntimeCmd already put the child in its own process group before it
// existed, so there is nothing left to claim once it is running. The logger
// is unused here; Windows needs it to report degraded ownership.
//
// It is still the only way this package starts a long-lived runtime
// process, so the two platforms share one call site per backend
// (MUL-6658 / GH #7522).
func startOwnedProcessTree(cmd *exec.Cmd, _ *slog.Logger) error { return cmd.Start() }

// releaseProcessGroup is a no-op on non-Windows platforms: a process
// group needs no handle and is gone once its members are.
func releaseProcessGroup(cmd *exec.Cmd) {}

// signalProcessGroup sends sig to the whole process group led by p (when the
// command was started with configureProcessGroup), falling back to the single
// process if the group send fails. Targeting the group (negative pid) reaches
// the descendants the agent spawned, not just the leader.
func signalProcessGroup(p *os.Process, sig syscall.Signal) {
	if p == nil {
		return
	}
	if err := syscall.Kill(-p.Pid, sig); err != nil {
		_ = p.Signal(sig)
	}
}

// signalProcessGroupCmd is the cmd-shaped counterpart used by runtime_ownership.go's
// Cancel + runOwned helpers, where the cmd is in scope but cmd.Process may
// still be nil (Cancel runs before Start in some cases, so a nil-check is
// required — see runtime_ownership.go).
func signalProcessGroupCmd(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	signalProcessGroup(cmd.Process, sig)
}
