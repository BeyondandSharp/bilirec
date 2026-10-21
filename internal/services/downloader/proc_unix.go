// SPDX-License-Identifier: GPL-3.0-only

//go:build unix

package downloader

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the child in its own process group so that
// killProcessTree can terminate the whole tree (including grandchildren).
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree sends SIGKILL to the child's process group.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
