//go:build !windows

package xplat

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group.
//
// Killing the shell is not enough. `sh -c "npm install"` spawns children, and
// signalling only the shell leaves them running with the run's working tree
// open — the timeout appears to work while the work does not actually stop.
func setProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup signals the whole group the command started.
func killProcessGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	// The negative pid is the group. Fall back to the process itself if the
	// group is gone, so a cancel still stops something.
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
		return c.Process.Kill()
	}
	return nil
}
