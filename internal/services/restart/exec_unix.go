//go:build !windows

package restart

import (
	"os"
	"syscall"
)

// Exec replaces the current process image with a fresh copy of the executable.
func Exec() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
