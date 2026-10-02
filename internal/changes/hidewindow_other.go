//go:build !windows

package changes

import "os/exec"

func hideWindow(*exec.Cmd) {}
