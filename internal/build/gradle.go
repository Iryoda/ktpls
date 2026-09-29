package build

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config configures compilation.
type Config struct {
	Enabled bool
	// Command runs the build tool; empty means detect (./gradlew, or
	// gradle on PATH).
	Command []string
	// Tasks are the build tasks to run; empty means compileKotlin and
	// compileTestKotlin (in every Gradle project that has them).
	Tasks []string
	// Env adds environment variables, e.g. JAVA_HOME for the JDK the
	// build needs.
	Env map[string]string
}

var defaultTasks = []string{"compileKotlin", "compileTestKotlin"}

// Detect returns the command running Gradle for the project at root, or
// false if root isn't a Gradle project.
func Detect(root string) ([]string, bool) {
	if root == "" {
		return nil, false
	}
	if info, err := os.Stat(filepath.Join(root, "gradlew")); err == nil && info.Mode()&0o111 != 0 {
		return []string{filepath.Join(root, "gradlew")}, true
	}
	for _, name := range []string{"settings.gradle.kts", "settings.gradle", "build.gradle.kts", "build.gradle"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			if path, err := exec.LookPath("gradle"); err == nil {
				return []string{path}, true
			}
		}
	}
	return nil, false
}

// A Result is the outcome of one build.
type Result struct {
	Messages []Message
	// Compiled reports whether a Kotlin compile task produced a result
	// for changed sources: it ran, failed, or was restored from the build
	// cache (a cached compile succeeded, so it had no errors, though its
	// warnings aren't replayed). Up-to-date, skipped or unreached tasks
	// say nothing new. When Compiled, the errors are the complete set.
	Compiled bool
	// Failure summarizes a build failure other than compile errors (a
	// build script error, a missing JDK); "" if none.
	Failure  string
	Duration time.Duration
}

var (
	kotlinTaskRE = regexp.MustCompile(`^> Task (\S*compile\w*Kotlin\w*)(?: (UP-TO-DATE|SKIPPED|NO-SOURCE|FROM-CACHE|FAILED))?\s*$`)
	failureRE    = regexp.MustCompile(`(?s)\* What went wrong:\n(.*?)\n\* Try:`)
)

// Summarize reads a build's output into a Result.
func Summarize(out string, err error) Result {
	res := Result{Messages: ParseOutput(out)}
	for _, line := range strings.Split(out, "\n") {
		if m := kotlinTaskRE.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			switch m[2] {
			case "", "FAILED", "FROM-CACHE":
				res.Compiled = true
			}
		}
	}
	if err != nil {
		hasErrors := false
		for _, m := range res.Messages {
			hasErrors = hasErrors || m.Severity == Error
		}
		if !hasErrors {
			res.Failure = strings.TrimSpace(err.Error())
			if m := failureRE.FindStringSubmatch(out); m != nil {
				res.Failure = strings.TrimSpace(m[1])
			}
		}
	}
	return res
}

// A Runner builds the project in the background. Requests arriving while
// a build runs are coalesced into one more build, so builds never pile
// up and the last one always sees the latest saved sources.
type Runner struct {
	root  string
	cmd   []string
	tasks []string
	env   []string
	log   *slog.Logger
	start func()
	done  func(Result)

	mu      sync.Mutex
	running bool
	again   bool
	owned   map[int]bool // daemons started by our builds
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	idle    chan struct{} // closed when no build is running; nil while one is
}

// NewRunner returns a runner for the project at root, or nil if
// compilation is disabled or no build tool was found. start and done are
// called before and after each build, from the runner's goroutine.
func NewRunner(root string, cfg Config, log *slog.Logger, start func(), done func(Result)) *Runner {
	if !cfg.Enabled {
		return nil
	}
	cmd := cfg.Command
	if len(cmd) == 0 {
		var ok bool
		if cmd, ok = Detect(root); !ok {
			return nil
		}
	}
	tasks := cfg.Tasks
	if len(tasks) == 0 {
		tasks = defaultTasks
	}
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	return &Runner{root: root, cmd: cmd, tasks: tasks, env: env, log: log, start: start, done: done, owned: map[int]bool{}}
}

// Request asks for a build as soon as possible.
func (r *Runner) Request(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if r.cancel == nil {
		r.ctx, r.cancel = context.WithCancel(ctx)
	}
	if r.running {
		r.again = true
		return
	}
	r.running = true
	r.idle = make(chan struct{})
	go r.loop(r.ctx)
}

// Close stops a running build and the Gradle and Kotlin daemons our
// builds started (daemons that were already running, e.g. an IDE's, are
// left alone). It waits for them to exit.
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	idle := r.idle
	r.mu.Unlock()
	if idle != nil {
		<-idle // the build's process group is terminated on cancel
	}
	r.mu.Lock()
	owned := r.owned
	r.owned = map[int]bool{}
	r.mu.Unlock()
	for pid := range owned {
		if isDaemon(pid) {
			r.log.Info("stopping daemon started by our builds", "pid", pid)
			terminate(pid)
		}
	}
}

func (r *Runner) loop(ctx context.Context) {
	defer func() {
		r.mu.Lock()
		r.running = false
		close(r.idle)
		r.idle = nil
		r.mu.Unlock()
	}()
	for {
		before := daemonPIDs()
		res := r.run(ctx)
		// Daemons that appeared during the build are ours.
		for pid := range daemonPIDs() {
			if !before[pid] {
				r.mu.Lock()
				r.owned[pid] = true
				r.mu.Unlock()
			}
		}
		if ctx.Err() != nil {
			return
		}
		r.done(res)
		r.mu.Lock()
		again := r.again
		r.again = false
		r.mu.Unlock()
		if !again {
			return
		}
	}
}

func (r *Runner) run(ctx context.Context) Result {
	args := append(append([]string{}, r.cmd[1:]...), r.tasks...)
	// --continue compiles the modules that can be compiled even if
	// another fails.
	args = append(args, "--console=plain", "--continue")
	cmd := exec.CommandContext(ctx, r.cmd[0], args...)
	cmd.Dir = r.root
	cmd.Env = r.env
	ownProcessGroup(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	r.start()
	start := time.Now()
	r.log.Info("build started", "command", strings.Join(append([]string{r.cmd[0]}, args...), " "))
	err := cmd.Run()
	res := Summarize(out.String(), err)
	res.Duration = time.Since(start)
	if res.Failure != "" {
		r.log.Warn("build failed", "failure", res.Failure)
	}
	r.log.Info("build finished", "elapsed", res.Duration.Round(time.Millisecond), "compiled", res.Compiled, "messages", len(res.Messages), "err", fmt.Sprint(err))
	return res
}
