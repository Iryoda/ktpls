package server

import (
	"context"
	"fmt"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return err
	}
	s.session.Open(path, params.TextDocument.Version, []byte(params.TextDocument.Text))
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
	// The open buffer's overlay is already up to date via didChange.
	return nil
}

func (s *Server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return err
	}
	s.session.Close(path)
	return nil
}
