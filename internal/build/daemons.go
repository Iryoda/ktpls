package build

import (
	"os/exec"
	"strconv"
	"strings"
)

// daemonMarkers identify the long-lived JVMs a Gradle build leaves
// behind: the Gradle daemon, and the Kotlin compile daemon it starts.
var daemonMarkers = []string{
	"org.gradle.launcher.daemon.bootstrap.GradleDaemon",
	"org.jetbrains.kotlin.daemon.KotlinCompileDaemon",
}

// daemonPIDs returns the PIDs of the running Gradle and Kotlin daemons.
func daemonPIDs() map[int]bool {
	out, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		return nil
	}
	pids := map[int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, m := range daemonMarkers {
			if strings.Contains(line, m) {
				if pid, err := strconv.Atoi(fields[0]); err == nil {
					pids[pid] = true
				}
				break
			}
		}
	}
	return pids
}

// isDaemon reports whether pid is (still) a Gradle or Kotlin daemon, so a
// recycled PID is never signaled.
func isDaemon(pid int) bool {
	return daemonPIDs()[pid]
}
