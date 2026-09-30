package server

import (
	"fmt"
	"os"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// startAnalyzer starts the analyzer and reports the diagnostics of every
// file. If it can't start, compiler diagnostics fall back to Gradle.
func (s *Server) startAnalyzer() {
	s.az.restoreOnce.Do(s.restoreDiagnostics)
	s.progressBegin(progressAnalyzer, "Analyzing", "starting the analyzer")
	c, n, err := s.launchAnalyzer()
	if err != nil {
		s.progressEnd(progressAnalyzer, "unavailable")
		if s.ctx.Err() == nil {
			s.fallBackToGradle(err)
		}
		return
	}
	s.az.mu.Lock()
	s.az.client = c
	s.az.mu.Unlock()
	go s.watchAnalyzer(c)
	s.progressEnd(progressAnalyzer, fmt.Sprintf("%d files", n))
	s.log.Info("analyzer: started", "sinceStart", s.sinceStart())
	select {
	case <-s.loaded: // the files to diagnose, and the open buffers, are known
	case <-s.ctx.Done():
		return
	}
	if !s.diagnoseAllInBatches() {
		return
	}
	s.log.Info("analyzer: all files diagnosed", "sinceStart", s.sinceStart())
	s.saveDiagnostics()
	s.refreshCachedModel()
}

// analyzerClient returns the running analyzer, or nil.
func (s *Server) analyzerClient() *analyzer.Client {
	s.az.mu.Lock()
	defer s.az.mu.Unlock()
	return s.az.client
}

// diagnoseWithAnalyzer diagnoses files as they are in the analyzer's
// session (all files if paths is nil) and publishes the results.
func (s *Server) diagnoseWithAnalyzer(paths []string) {
	c := s.analyzerClient()
	if c == nil {
		return
	}
	// Open buffers are checked as they are in the editor, not on disk.
	paths = slices.DeleteFunc(slices.Clone(paths), func(p string) bool {
		if s.isOpen(p) {
			s.liveCheck(p)
			return true
		}
		return false
	})
	if len(paths) == 0 {
		return
	}
	files, err := c.Diagnose(s.ctx, paths)
	if err != nil {
		s.log.Warn("analyzer: diagnose", "err", err)
		return
	}
	updated := map[string]bool{}
	for _, p := range paths {
		updated[p] = true // no result: no diagnostics
	}
	for _, f := range files {
		content, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		s.recordDisk(f.Path, f.Diagnostics)
		s.setAnalyzerDiagnostics(f.Path, s.toProtocol(content, f.Diagnostics))
		delete(updated, f.Path)
	}
	for p := range updated {
		s.recordDisk(p, nil)
		s.setAnalyzerDiagnostics(p, nil)
	}
}

// setAnalyzerDiagnostics records and publishes the analyzer's diagnostics
// of a file.
func (s *Server) setAnalyzerDiagnostics(path string, diags []protocol.Diagnostic) {
	s.az.mu.Lock()
	if s.az.diags == nil {
		s.az.diags = map[string][]protocol.Diagnostic{}
	}
	if len(diags) == 0 {
		delete(s.az.diags, path)
	} else {
		s.az.diags[path] = diags
	}
	s.az.mu.Unlock()
	s.publishDiagnostics(path)
}

// analyzerDiagnostics returns the analyzer's diagnostics of a file.
func (s *Server) analyzerDiagnostics(path string) []protocol.Diagnostic {
	s.az.mu.Lock()
	defer s.az.mu.Unlock()
	return s.az.diags[path]
}

// toProtocol converts diagnostics with UTF-16 offsets into content to
// LSP diagnostics in the session's position encoding.
func (s *Server) toProtocol(content []byte, ds []analyzer.Diagnostic) []protocol.Diagnostic {
	if len(ds) == 0 {
		return nil
	}
	m := protocol.NewMapper(content, s.session.Encoding())
	var out []protocol.Diagnostic
	for _, d := range ds {
		start, end := utf16ToByte(content, d.Start), utf16ToByte(content, d.End)
		if end == start+1 && content[start] == '=' {
			// Some initializer errors point at the '='; show them on the value.
			start = skipAssignment(content, start)
			end = tokenEnd(content, start)
		}
		rng, err := m.OffsetRange(start, max(start, end))
		if err != nil {
			continue
		}
		sev := protocol.SeverityError
		switch d.Severity {
		case "warning":
			sev = protocol.SeverityWarning
		case "info":
			sev = protocol.SeverityInformation
		}
		out = append(out, protocol.Diagnostic{Range: rng, Severity: sev, Source: "kotlin", Message: d.Message})
	}
	return out
}

// utf16ToByte converts an offset in UTF-16 code units into a byte offset.
func utf16ToByte(content []byte, units int) int {
	off := 0
	for off < len(content) && units > 0 {
		r, size := utf8.DecodeRune(content[off:])
		if r >= 0x10000 {
			units -= 2
		} else {
			units--
		}
		off += size
	}
	return off
}

// startupBatch is how many files are diagnosed per request at startup, so
// that a save's instant check never waits long behind them.
const startupBatch = 50

// diagnoseAllInBatches diagnoses every Kotlin file of the workspace, and
// reports whether it got through them all.
func (s *Server) diagnoseAllInBatches() bool {
	var open, paths []string
	s.session.Read(func(sn *cache.Snapshot) {
		for f := range sn.Files() {
			switch {
			case !cache.IsKotlinFile(f.Path):
			case f.Overlay:
				open = append(open, f.Path)
			default:
				paths = append(paths, f.Path)
			}
		}
	})
	slices.Sort(paths)
	paths = append(open, paths...) // open buffers first: they are what the user is looking at
	for len(paths) > 0 && s.ctx.Err() == nil && s.analyzerClient() != nil {
		n := min(startupBatch, len(paths))
		s.diagnoseWithAnalyzer(paths[:n])
		paths = paths[n:]
	}
	return len(paths) == 0
}

// sinceStart is the time since the server started, for startup timings.
func (s *Server) sinceStart() time.Duration {
	return time.Since(s.started).Round(time.Millisecond)
}

// isOpen reports whether path is open in the editor.
func (s *Server) isOpen(path string) bool {
	open := false
	s.session.Read(func(sn *cache.Snapshot) {
		if f := sn.File(path); f != nil && f.Overlay {
			open = true
		}
	})
	return open
}

// refreshCachedModel checks a model taken from the cache against Gradle's,
// in the background, and restarts the analyzer if it changed (e.g. a
// dependency's version resolved differently).
func (s *Server) refreshCachedModel() {
	s.az.mu.Lock()
	cached, gradle, env := s.az.modelCached, s.az.gradle, s.az.env
	s.az.modelCached = false
	s.az.mu.Unlock()
	if !cached {
		return
	}
	changed, err := analyzer.RefreshModel(s.ctx, s.root, gradle, env)
	switch {
	case err != nil:
		s.log.Warn("analyzer: refreshing the project model", "err", err)
	case changed:
		s.log.Info("analyzer: the project model changed; restarting")
		s.restartAnalyzer()
	}
}
