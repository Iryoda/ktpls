package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/build"
)

// errNoBuild: the workspace has no Gradle build (a lone file, a library's
// sources): there are no compiler diagnostics to give, and nothing to warn
// about.
var errNoBuild = errors.New("no Gradle build")

// analyzerSettings resolves the analyzer's options.
func (s *Server) analyzerSettings() (gradle, env []string, jarOpt, javaHome, mem string) {
	mem = "2g"
	if c := s.opts.Compile; c != nil {
		gradle = c.Command
		for k, v := range c.Env {
			env = append(env, k+"="+v)
		}
		javaHome = c.Env["JAVA_HOME"]
	}
	if a := s.opts.Analyzer; a != nil {
		jarOpt = a.Jar
		if a.JavaHome != "" {
			javaHome = a.JavaHome
		}
		if a.MaxMemory != "" {
			mem = a.MaxMemory
		}
	}
	return gradle, env, jarOpt, javaHome, mem
}

// launchAnalyzer starts an analyzer session. The JVM starts while the
// project model is read, which takes a Gradle run unless it is cached.
func (s *Server) launchAnalyzer() (*analyzer.Client, int, error) {
	gradle, env, jarOpt, javaHome, mem := s.analyzerSettings()
	if len(gradle) == 0 {
		var ok bool
		if gradle, ok = build.Detect(s.root); !ok {
			return nil, 0, errNoBuild
		}
	}
	jar, err := analyzer.FindJar(jarOpt)
	if err != nil {
		return nil, 0, err
	}
	java, err := analyzer.FindJava(javaHome)
	if err != nil {
		return nil, 0, fmt.Errorf("no java: %w", err)
	}
	c, err := analyzer.Start(java, jar, jvmArgs(jar, mem), s.log)
	if err != nil {
		return nil, 0, err
	}
	start := time.Now()
	model, cached, err := analyzer.Model(s.ctx, s.root, gradle, env)
	if err != nil {
		c.Close()
		return nil, 0, err
	}
	s.log.Info("project model", "cached", cached, "elapsed", time.Since(start).Round(time.Millisecond))
	r, err := c.Init(s.ctx, model, analyzer.JavaHome(java))
	if err != nil {
		c.Close()
		return nil, 0, err
	}
	s.log.Info("analyzer ready", "files", r.Files, "session", time.Duration(r.Millis)*time.Millisecond)
	s.az.mu.Lock()
	s.az.modelCached = cached
	s.az.gradle, s.az.env = gradle, env
	s.az.mu.Unlock()
	return c, r.Files, nil
}

// jvmArgs are the analyzer JVM's options. A class data sharing archive,
// written on the first run of a jar, makes later starts load faster.
func jvmArgs(jar, mem string) []string {
	args := []string{"-Xmx" + mem, "-XX:+UseSerialGC", "-Djava.awt.headless=true", "-XX:+IgnoreUnrecognizedVMOptions"}
	if archive := analyzer.ClassArchive(jar); archive != "" {
		args = append(args, "-XX:+AutoCreateSharedArchive", "-XX:SharedArchiveFile="+archive)
	}
	return args
}
