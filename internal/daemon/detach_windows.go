//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

func prepareDetachedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
