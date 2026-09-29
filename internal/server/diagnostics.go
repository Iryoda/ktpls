package server

import (
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// diagnosticsDelay debounces syntax diagnostics while the user types.
const diagnosticsDelay = 250 * time.Millisecond

// Syntax errors are reported only if they are new since the buffer was
// opened. The grammar fails on a small share of valid Kotlin, and errors
// present in a file as opened are far more likely to be such gaps than
// real mistakes; errors appearing while editing are the user's.

// openDiagnostics records the syntax-error baseline of a newly opened
// buffer and publishes (clears) its diagnostics.
func (s *Server) openDiagnostics(path string) {
	var baseline map[string]int
	s.session.Read(func(sn *cache.Snapshot) {
		if pf := parsedOverlay(sn, path); pf != nil {
			baseline = kotlin.ErrorKeys(kotlin.SyntaxErrors(pf))
		}
	})
	s.diagMu.Lock()
	s.baselines[path] = baseline
	s.diagMu.Unlock()
	s.publishDiagnostics(path)
}

// scheduleDiagnostics publishes path's diagnostics once edits pause.
func (s *Server) scheduleDiagnostics(path string) {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	if t, ok := s.diagTimer[path]; ok {
		t.Stop()
	}
	s.diagTimer[path] = time.AfterFunc(diagnosticsDelay, func() { s.publishDiagnostics(path) })
}

// closeDiagnostics forgets path and clears its diagnostics.
func (s *Server) closeDiagnostics(path string) {
	s.diagMu.Lock()
	if t, ok := s.diagTimer[path]; ok {
		t.Stop()
		delete(s.diagTimer, path)
	}
	delete(s.baselines, path)
	s.diagMu.Unlock()
	s.notifyDiagnostics(protocol.URIFromPath(path), nil, nil)
}

func (s *Server) publishDiagnostics(path string) {
	s.diagMu.Lock()
	baseline, open := s.baselines[path]
	s.diagMu.Unlock()
	if !open {
		return // closed meanwhile
	}
	var diags []protocol.Diagnostic
	var version *int32
	var uri protocol.DocumentURI
	s.session.Read(func(sn *cache.Snapshot) {
		f := sn.File(path)
		pf := parsedOverlay(sn, path)
		if pf == nil {
			return
		}
		v := f.Version
		version, uri = &v, f.URI
		for _, e := range kotlin.NewSyntaxErrors(kotlin.SyntaxErrors(pf), baseline) {
			diags = append(diags, protocol.Diagnostic{
				Range:    e.Range,
				Severity: protocol.SeverityWarning,
				Source:   "ktpls",
				Message:  e.Message,
			})
		}
	})
	if uri != "" {
		s.notifyDiagnostics(uri, version, diags)
	}
}

func (s *Server) notifyDiagnostics(uri protocol.DocumentURI, version *int32, diags []protocol.Diagnostic) {
	if diags == nil {
		diags = []protocol.Diagnostic{}
	}
	params := &protocol.PublishDiagnosticsParams{URI: uri, Version: version, Diagnostics: diags}
	if err := s.client.Notify("textDocument/publishDiagnostics", params); err != nil {
		s.log.Error("publishing diagnostics", "err", err)
	}
}

// parsedOverlay returns the open buffer at path with its tree, or nil.
func parsedOverlay(sn *cache.Snapshot, path string) *kotlin.ParsedFile {
	f := sn.File(path)
	if f == nil || !f.Overlay || f.Tree == nil {
		return nil
	}
	return &kotlin.ParsedFile{Path: f.Path, URI: f.URI, Content: f.Content, Tree: f.Tree, Mapper: f.Mapper, Summary: f.Summary}
}
