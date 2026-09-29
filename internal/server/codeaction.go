package server

import (
	"context"
	"encoding/json"
	"os"

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
		read := func(path string) []byte {
			if f := sn.File(path); f != nil && f.Content != nil {
				return f.Content
			}
			content, _ := os.ReadFile(path)
			return content
		}
		for _, a := range kotlin.CodeActionsWith(f, sn.Index(), f.Mapper.PositionOffset(params.Range.Start), read) {
			if !kindRequested(a.Kind, params.Context.Only) {
				continue
			}
			action := protocol.CodeAction{Title: a.Title, Kind: a.Kind}
			if a.Open != nil {
				action.Command = &protocol.Command{Title: a.Title, Command: kotlin.OpenCommand, Arguments: []any{a.Open}}
			} else {
				changes := map[protocol.DocumentURI][]protocol.TextEdit{}
				if len(a.Edits) > 0 {
					changes[params.TextDocument.URI] = a.Edits
				}
				for uri, edits := range a.Other {
					changes[uri] = append(changes[uri], edits...)
				}
				action.Edit = &protocol.WorkspaceEdit{Changes: changes}
			}
			actions = append(actions, action)
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

func (s *Server) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (any, error) {
	switch params.Command {
	case kotlin.OpenCommand:
		if len(params.Arguments) != 1 {
			return nil, protocol.Errorf(protocol.CodeInvalidParams, "%s: want one location argument", params.Command)
		}
		var loc protocol.Location
		if err := json.Unmarshal(params.Arguments[0], &loc); err != nil {
			return nil, protocol.Errorf(protocol.CodeInvalidParams, "%s: %v", params.Command, err)
		}
		sel := protocol.Range{Start: loc.Range.Start, End: loc.Range.Start}
		return nil, s.client.Call("window/showDocument", &protocol.ShowDocumentParams{URI: loc.URI, TakeFocus: true, Selection: &sel})
	}
	return nil, protocol.Errorf(protocol.CodeInvalidParams, "unknown command %q", params.Command)
}
