//go:build unix

package build

import (
	"os/exec"
	"syscall"
	"time"
)

// ownProcessGroup runs cmd in its own process group, and makes canceling
// it terminate the whole group (the gradlew script and the Java client it
// starts), not just the script.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
}

// terminate stops pid: SIGTERM, then SIGKILL if it is still a daemon
// after a grace period.
func terminate(pid int) {
	if syscall.Kill(pid, syscall.SIGTERM) != nil {
		return
	}
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if !isDaemon(pid) {
			return
		}
	}
	syscall.Kill(pid, syscall.SIGKILL)
}
