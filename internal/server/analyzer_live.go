package server

import (
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/cache"
)

// defaultLiveDelay is how long typing must pause before a live check.
const defaultLiveDelay = 150 * time.Millisecond

// analyzerChanged schedules a live check of an edited buffer.
func (s *Server) analyzerChanged(path string) {
	if !cache.IsKotlinFile(path) || analyzer.IsBuildFile(path) || s.analyzerClient() == nil {
		return
	}
	s.az.mu.Lock()
	defer s.az.mu.Unlock()
	s.az.lastEdit = time.Now()
	if s.az.live == nil {
		s.az.live = map[string]*time.Timer{}
	}
	if t := s.az.live[path]; t != nil {
		t.Stop()
	}
	s.az.live[path] = time.AfterFunc(s.liveDelay(), func() { s.liveCheck(path) })
}

// liveDelay is the configured pause before a live check.
func (s *Server) liveDelay() time.Duration {
	if a := s.opts.Analyzer; a != nil && a.Delay > 0 {
		return time.Duration(a.Delay) * time.Millisecond
	}
	return defaultLiveDelay
}

// liveCheck checks an open buffer as it is now, and publishes the result
// unless the buffer changed again meanwhile. There is one check of a
// buffer at a time: edits during one only mark it for another.
func (s *Server) liveCheck(path string) {
	s.az.mu.Lock()
	if s.az.inflight == nil {
		s.az.inflight = map[string]bool{}
	}
	if s.az.inflight[path] {
		s.az.inflight[path] = false // checked again when the running check ends
		s.az.mu.Unlock()
		return
	}
	s.az.inflight[path] = true
	s.az.mu.Unlock()
	for {
		s.checkOverlay(path)
		s.az.mu.Lock()
		again := !s.az.inflight[path]
		if !again {
			delete(s.az.inflight, path)
		} else {
			s.az.inflight[path] = true
		}
		s.az.mu.Unlock()
		if !again {
			return
		}
	}
}

// checkOverlay checks an open buffer's current text once.
func (s *Server) checkOverlay(path string) {
	c := s.analyzerClient()
	if c == nil {
		return
	}
	var text []byte
	version := int32(-1)
	s.session.Read(func(sn *cache.Snapshot) {
		if f := sn.File(path); f != nil && f.Overlay {
			text, version = f.Content, f.Version
		}
	})
	if version < 0 {
		return
	}
	start := time.Now()
	ds, err := c.Check(s.ctx, path, string(text))
	if err != nil {
		s.log.Debug("analyzer: live check", "path", path, "err", err)
		return
	}
	s.log.Debug("analyzer: live check", "elapsed", time.Since(start).Round(time.Millisecond))
	current := int32(-1)
	s.session.Read(func(sn *cache.Snapshot) {
		if f := sn.File(path); f != nil {
			current = f.Version
		}
	})
	if current == version {
		s.setAnalyzerDiagnostics(path, s.toProtocol(text, ds))
	}
}
