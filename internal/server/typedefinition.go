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
	return locs, err
}
