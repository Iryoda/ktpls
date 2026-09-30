package server

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
)

// analyzerSaved handles a saved file in analyzer mode. A Kotlin file is
// checked at once and a session rebuild is scheduled; a build file
// restarts the analyzer, as the project model may have changed.
func (s *Server) analyzerSaved(path string) {
	c := s.analyzerClient()
	if c == nil {
		return
	}
	if isBuildFile(path) {
		go s.restartAnalyzer()
		return
	}
	var text []byte
	s.session.Read(func(sn *cache.Snapshot) { text = fileContent(sn, path) })
	if ds, err := c.Check(s.ctx, path, string(text)); err == nil {
		s.setAnalyzerDiagnostics(path, s.toProtocol(text, ds))
	} else {
		s.log.Warn("analyzer: check", "path", path, "err", err)
	}
	s.az.mu.Lock()
	if s.az.saved == nil {
		s.az.saved = map[string]bool{}
	}
	s.az.saved[path] = true
	if s.az.timer != nil {
		s.az.timer.Stop()
	}
	s.az.timer = time.AfterFunc(rebuildDelay, s.rebuildAnalyzer)
	s.az.mu.Unlock()
}

func isBuildFile(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".gradle") || strings.HasSuffix(base, ".gradle.kts") || base == "gradle.properties"
}
