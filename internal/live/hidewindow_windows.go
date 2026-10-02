package live

import (
	"os/exec"
	"syscall"
)

// hideWindow: keep the child CLI from opening its own console.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
