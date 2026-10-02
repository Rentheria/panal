//go:build !windows

package delegate

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the agent in its own process group, so the whole tree
// can be signalled at once.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interruptTree forwards ctrl+c (SIGINT) to the agent's process group: in its
// own group it does not get the terminal's.
func interruptTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	}
}

// killTree kills the agent's whole process group.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
