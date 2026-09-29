package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) ([]protocol.DocumentSymbol, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var syms []protocol.DocumentSymbol
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		syms = kotlin.DocumentSymbols(f.Summary)
	})
	if syms == nil {
		syms = []protocol.DocumentSymbol{}
	}
	return syms, err
}

func (s *Server) WorkspaceSymbol(ctx context.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	var syms []protocol.SymbolInformation
	s.session.Read(func(sn *cache.Snapshot) {
		syms = kotlin.WorkspaceSymbols(sn.Index(), params.Query)
	})
	if syms == nil {
		syms = []protocol.SymbolInformation{}
	}
	return syms, nil
}
