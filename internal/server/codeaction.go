package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) CodeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	actions := []protocol.CodeAction{}
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		for _, a := range kotlin.CodeActions(f, sn.Index(), f.Mapper.PositionOffset(params.Range.Start)) {
			if !kindRequested(a.Kind, params.Context.Only) {
				continue
			}
			actions = append(actions, protocol.CodeAction{
				Title: a.Title,
				Kind:  a.Kind,
				Edit:  &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{params.TextDocument.URI: a.Edits}},
			})
		}
	})
	return actions, err
}

// kindRequested reports whether an action of kind matches the client's
// "only" filter: a kind matches itself and its sub-kinds.
func kindRequested(kind string, only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, o := range only {
		if kind == o || len(kind) > len(o) && kind[:len(o)] == o && kind[len(o)] == '.' {
			return true
		}
	}
	return false
}
