package server

import (
	"errors"

	"github.com/Iryoda/ktpls/internal/analyzer"
)

// watchAnalyzer waits for the analyzer process to end. An unexpected exit
// restarts it once; after that, diagnostics fall back to Gradle.
func (s *Server) watchAnalyzer(c *analyzer.Client) {
	<-c.Done()
	s.az.mu.Lock()
	current := s.az.client == c
	closing := s.az.closing
	if current {
		s.az.client = nil
	}
	s.az.restarts++
	restarts := s.az.restarts
	s.az.mu.Unlock()
	if !current || closing || s.ctx.Err() != nil {
		return
	}
	if restarts > 1 {
		s.fallBackToGradle(errors.New("the analyzer stopped unexpectedly more than once"))
		return
	}
	s.log.Warn("analyzer stopped unexpectedly; restarting")
	s.startAnalyzer()
}

// restartAnalyzer replaces the analyzer with a new one (the project model
// may have changed).
func (s *Server) restartAnalyzer() {
	s.az.mu.Lock()
	c := s.az.client
	s.az.client = nil
	s.az.mu.Unlock()
	if c != nil {
		c.Close()
	}
	s.startAnalyzer()
}

// closeAnalyzer stops the analyzer for good.
func (s *Server) closeAnalyzer() {
	s.az.mu.Lock()
	s.az.closing = true
	c := s.az.client
	s.az.client = nil
	if s.az.timer != nil {
		s.az.timer.Stop()
	}
	for _, t := range s.az.live {
		t.Stop()
	}
	s.az.mu.Unlock()
	if c != nil {
		c.Close()
	}
}
