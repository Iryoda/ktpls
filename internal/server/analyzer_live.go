package server

import (
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
)

// liveDelay is how long typing must pause before a live check.
const liveDelay = 300 * time.Millisecond

// analyzerChanged schedules a live check of an edited buffer.
func (s *Server) analyzerChanged(path string) {
	if !cache.IsKotlinFile(path) || isBuildFile(path) || s.analyzerClient() == nil {
		return
	}
	s.az.mu.Lock()
	defer s.az.mu.Unlock()
	if s.az.live == nil {
		s.az.live = map[string]*time.Timer{}
	}
	if t := s.az.live[path]; t != nil {
		t.Stop()
	}
	s.az.live[path] = time.AfterFunc(liveDelay, func() { s.liveCheck(path) })
}

// liveCheck checks an open buffer as it is now, and publishes the result
// unless the buffer changed again meanwhile.
func (s *Server) liveCheck(path string) {
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
	ds, err := c.Check(s.ctx, path, string(text))
	if err != nil {
		s.log.Debug("analyzer: live check", "path", path, "err", err)
		return
	}
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
