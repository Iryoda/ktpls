//go:build unix

package build

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// fakeDaemon is a command that looks like a Gradle daemon to daemonPIDs.
const fakeDaemon = `while :; do sleep 1; done`
const fakeDaemonName = "org.gradle.launcher.daemon.bootstrap.GradleDaemon"

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil && isDaemon(pid) }

func TestCloseStopsOnlyOurDaemons(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("needs setsid")
	}
	// A daemon that was running before (an IDE's): must survive.
	pre := exec.Command("sh", "-c", fakeDaemon, fakeDaemonName)
	pre.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := pre.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pre.Process.Kill(); pre.Wait() })
	time.Sleep(100 * time.Millisecond)
	before := daemonPIDs()

	// A fake Gradle that starts a detached daemon, like the real one.
	dir := t.TempDir()
	script := filepath.Join(dir, "gradlew")
	os.WriteFile(script, []byte("#!/bin/sh\nsetsid sh -c '"+fakeDaemon+"' "+fakeDaemonName+" >/dev/null 2>&1 </dev/null &\n"+
		"sleep 0.3\necho '> Task :compileKotlin'\n"), 0o755)
	done := make(chan Result, 1)
	r := NewRunner(dir, Config{Enabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}, func(res Result) { done <- res })
	if r == nil {
		t.Fatal("no runner")
	}
	r.Request(t.Context())
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("build didn't finish")
	}
	var ours []int
	for pid := range daemonPIDs() {
		if !before[pid] {
			ours = append(ours, pid)
		}
	}
	if len(ours) != 1 {
		t.Fatalf("started daemons: %v", ours)
	}
	t.Cleanup(func() { syscall.Kill(ours[0], syscall.SIGKILL) })

	r.Close()
	if alive(ours[0]) {
		t.Error("the daemon our build started is still running")
	}
	if !alive(pre.Process.Pid) {
		t.Error("a daemon we didn't start was stopped")
	}
	// No builds after Close.
	r.Request(t.Context())
	select {
	case <-done:
		t.Error("built after Close")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestCloseCancelsRunningBuild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-alive")
	script := filepath.Join(dir, "gradlew")
	// The "build" runs a child (like gradlew's Java client) that would
	// keep touching a file if it survived.
	os.WriteFile(script, []byte("#!/bin/sh\nsh -c 'while :; do touch "+marker+"; sleep 0.1; done' &\nwait\n"), 0o755)
	r := NewRunner(dir, Config{Enabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}, func(Result) {})
	r.Request(t.Context())
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	r.Close()
	if time.Since(start) > 6*time.Second {
		t.Errorf("Close took %v", time.Since(start))
	}
	os.Remove(marker)
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the build's child process survived Close")
	}
}
