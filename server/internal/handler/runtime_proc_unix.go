//go:build !windows

package handler

import (
	"os/exec"
	"syscall"
)

// configureRuntimeCmd puts the runtime child (python3) into its own process
// group and makes context cancellation kill the whole group. Without this,
// CommandContext only kills the direct child: any grandchild the snippet
// spawned keeps running AND keeps the inherited stdout/stderr pipe open,
// which leaves cmd.Run() blocked on the pipe-copy goroutine until WaitDelay
// finally cuts it loose. Mirrors server/pkg/agent/proc_other.go.
func configureRuntimeCmd(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the whole group; fall back to the
		// single process if the group send fails.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
