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
		locs = kotlin.Definition(f, sn.Index(), f.Mapper.PositionOffset(params.Position))
	})
	if len(locs) == 0 && err == nil {
		// A library declaration, most likely: ask the compiler.
		locs = s.analyzerDefinition(ctx, path, params.Position)
	}
	return locs, err
}
