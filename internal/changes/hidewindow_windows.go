//go:build windows

package changes

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps git from opening a console window that flickers over the dashboard.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
