//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// prepareDetachedCommand gives the engine its own session. MCP hosts and
// terminals routinely terminate their whole process group when they close;
// the engine is a daemon and must outlive the client that happened to start it.
func prepareDetachedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
