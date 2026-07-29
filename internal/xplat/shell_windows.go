//go:build windows

package xplat

import "os/exec"

// setProcessGroup is a no-op on Windows: cmd.exe children are handled by the
// job the process already belongs to.
func setProcessGroup(c *exec.Cmd) {}

// killProcessGroup kills the command. Its children are not tracked here, which
// is a known gap on Windows and not one this package can close without a job
// object.
func killProcessGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}
