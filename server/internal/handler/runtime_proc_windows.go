//go:build windows

package handler

import "os/exec"

// configureRuntimeCmd is a no-op on Windows: CommandContext's default
// Kill plus cmd.WaitDelay covers child cleanup there.
func configureRuntimeCmd(cmd *exec.Cmd) {}
