package server

import "time"

// rebuildAnalyzer rebuilds the session from disk, once typing pauses,
// then re-diagnoses the files the saves may have affected.
func (s *Server) rebuildAnalyzer() {
	// A rebuild holds up checks for a second or two: not while typing.
	s.az.mu.Lock()
	if wait := rebuildDelay - time.Since(s.az.lastEdit); wait > 0 && !s.az.closing {
		s.az.timer = time.AfterFunc(wait, s.rebuildAnalyzer)
		s.az.mu.Unlock()
		return
	}
	s.az.mu.Unlock()
	s.az.rebuildMu.Lock()
	defer s.az.rebuildMu.Unlock()
	c := s.analyzerClient()
	if c == nil {
		return
	}
	s.az.mu.Lock()
	saved := s.az.saved
	s.az.saved = nil
	s.az.mu.Unlock()
	if len(saved) == 0 {
		return
	}
	start := time.Now()
	if _, err := c.Rebuild(s.ctx); err != nil {
		s.log.Warn("analyzer: rebuild", "err", err)
		return
	}
	affected := s.affectedFiles(saved)
	s.diagnoseWithAnalyzer(affected)
	s.saveDiagnostics()
	s.log.Info("analyzer: rebuilt", "files", len(affected), "elapsed", time.Since(start).Round(time.Millisecond))
}
