//go:build windows

package alerts

import (
	"os/exec"
	"syscall"
)

// hideWindow: keep powershell from opening a console that flickers over the dashboard.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
