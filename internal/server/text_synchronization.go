package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return err
	}
	s.session.Open(path, params.TextDocument.Version, []byte(params.TextDocument.Text))
	s.openDiagnostics(path)
	return nil
}

func (s *Server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return err
	}
	var base []byte
	s.session.Read(func(sn *cache.Snapshot) {
		if f := sn.File(path); f != nil {
			base = f.Content
		}
	})
	content, err := applyChanges(base, s.session.Encoding(), params.ContentChanges)
	if err != nil {
		return fmt.Errorf("didChange %s: %w", path, err)
	}
	s.session.Change(path, params.TextDocument.Version, content)
	s.scheduleDiagnostics(path)
	s.mu.Lock()
	live := s.diagMode == modeAnalyzer
	s.mu.Unlock()
	if live {
		s.analyzerChanged(path)
	}
	return nil
}

// applyChanges applies content changes to base in order. We advertise full
// sync, so each change normally replaces the whole document, but ranged
// changes are handled too, for robustness.
func applyChanges(base []byte, enc protocol.PositionEncodingKind, changes []protocol.TextDocumentContentChangeEvent) ([]byte, error) {
	content := base
	for _, ch := range changes {
		if ch.Range == nil {
			content = []byte(ch.Text)
			continue
		}
		if content == nil {
			return nil, fmt.Errorf("ranged change to unknown document")
		}
		start, end := protocol.NewMapper(content, enc).RangeOffsets(*ch.Range)
		next := make([]byte, 0, len(content)-(end-start)+len(ch.Text))
		next = append(next, content[:start]...)
		next = append(next, ch.Text...)
		next = append(next, content[end:]...)
		content = next
	}
	return content, nil
}

func (s *Server) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	// The open buffer's overlay is already up to date via didChange; a
	// save is what the build sees, so compile.
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return err
	}
	base := filepath.Base(path)
	if cache.IsKotlinFile(path) || strings.HasSuffix(base, ".gradle") || base == "gradle.properties" {
		s.mu.Lock()
		mode := s.diagMode
		s.mu.Unlock()
		if mode == modeAnalyzer {
			s.analyzerSaved(path)
			return nil
		}
		s.diagMu.Lock()
		s.savedSinceBuild[path] = true
		s.diagMu.Unlock()
		s.requestBuild()
	}
	return nil
}

func (s *Server) DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	s.maybeRescan(true)
	return nil
}

func (s *Server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return err
	}
	s.session.Close(path)
	s.closeDiagnostics(path)
	return nil
}
