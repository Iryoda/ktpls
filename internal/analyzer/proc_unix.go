//go:build unix

package analyzer

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup runs the JVM in its own process group, so it and any
// process it starts can be stopped together.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
