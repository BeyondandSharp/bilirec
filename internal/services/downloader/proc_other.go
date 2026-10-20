// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix

package downloader

import "os/exec"

// configureProcessGroup is a no-op on platforms without process groups.
func configureProcessGroup(_ *exec.Cmd) {}

// killProcessTree kills the direct child. Grandchildren are reaped by the OS
// on platforms without process-group support; this build path only needs to
// compile (v1 targets linux/amd64).
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
