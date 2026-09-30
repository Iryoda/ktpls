package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) Definition(ctx context.Context, params *protocol.DefinitionParams) ([]protocol.Location, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var locs []protocol.Location
	guess := false
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		if locs = s.messageDefinition(sn, path, params.Position); locs != nil {
			return
		}
		locs, guess = kotlin.DefinitionGuess(f, sn.Index(), f.Mapper.PositionOffset(params.Position))
	})
	if err != nil || len(locs) > 0 && !guess {
		return locs, err
	}
	// A library declaration, or one found by name alone (the receiver's
	// type is unknown): the compiler knows which it is.
	if exact := s.analyzerDefinition(ctx, path, params.Position); len(exact) > 0 {
		return exact, nil
	}
	return locs, nil
}
