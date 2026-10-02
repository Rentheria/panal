//go:build !windows

package live

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
