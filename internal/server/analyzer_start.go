package server

import (
	"fmt"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/build"
)

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

// launchAnalyzer writes the project model and starts an analyzer session.
func (s *Server) launchAnalyzer() (*analyzer.Client, int, error) {
	gradle, env, jarOpt, javaHome, mem := s.analyzerSettings()
	if len(gradle) == 0 {
		var ok bool
		if gradle, ok = build.Detect(s.root); !ok {
			return nil, 0, fmt.Errorf("no Gradle build in %s", s.root)
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
	start := time.Now()
	model, err := analyzer.WriteModel(s.ctx, s.root, gradle, env)
	if err != nil {
		return nil, 0, err
	}
	s.log.Info("project model written", "path", model, "elapsed", time.Since(start).Round(time.Millisecond))
	jvm := []string{"-Xmx" + mem, "-XX:+UseSerialGC", "-Djava.awt.headless=true"}
	c, err := analyzer.Start(java, jar, jvm, s.log)
	if err != nil {
		return nil, 0, err
	}
	r, err := c.Init(s.ctx, model, analyzer.JavaHome(java))
	if err != nil {
		c.Close()
		return nil, 0, err
	}
	s.log.Info("analyzer ready", "files", r.Files, "session", time.Duration(r.Millis)*time.Millisecond)
	return c, r.Files, nil
}
