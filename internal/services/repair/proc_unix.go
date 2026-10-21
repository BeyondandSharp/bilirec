// SPDX-License-Identifier: GPL-3.0-only

//go:build unix

package repair

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the sidecar in its own process group so
// killProcessTree can terminate the whole tree.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree sends SIGKILL to the sidecar's process group.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
