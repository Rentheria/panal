//go:build windows

package models

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps the CLI from opening a console window over the dashboard.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
