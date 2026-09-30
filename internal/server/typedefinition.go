package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) ([]protocol.Location, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var locs []protocol.Location
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		locs = kotlin.TypeDefinition(f, sn.Index(), f.Mapper.PositionOffset(params.Position))
	})
	if err == nil && len(locs) == 0 {
		// A type the syntax can't infer (a lambda parameter of a library
		// call), or a library type: ask the compiler.
		locs = s.analyzerTypeDefinition(ctx, path, params.Position)
	}
	return locs, err
}

// Declaration is Definition: Kotlin has no separate declarations (an
// expect declaration aside).
func (s *Server) Declaration(ctx context.Context, params *protocol.DeclarationParams) ([]protocol.Location, error) {
	return s.Definition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: params.TextDocumentPositionParams})
}
