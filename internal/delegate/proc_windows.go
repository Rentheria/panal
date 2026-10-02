//go:build windows

package delegate

import (
	"os/exec"
	"strconv"
)

// setProcessGroup: nothing to do on Windows. The agent shares panal's
// console, so ctrl+c reaches it directly, as it does any console process.
func setProcessGroup(cmd *exec.Cmd) {}

// interruptTree: the console already delivered ctrl+c to the agent.
func interruptTree(cmd *exec.Cmd) {}

// killTree kills the agent and every process it started (taskkill /T), so
// a shell or test runner it left behind does not keep running.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	if err := k.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
