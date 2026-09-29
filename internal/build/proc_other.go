//go:build !unix

package build

import (
	"os"
	"os/exec"
)

func ownProcessGroup(cmd *exec.Cmd) {}

func terminate(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}
