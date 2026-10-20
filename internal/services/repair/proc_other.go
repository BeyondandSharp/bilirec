// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix

package repair

import "os/exec"

// configureProcessGroup is a no-op on platforms without process groups.
func configureProcessGroup(_ *exec.Cmd) {}

// killProcessTree kills the direct child (v1 targets linux/amd64; this build
// path only needs to compile).
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
