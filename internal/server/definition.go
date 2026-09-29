package server

import (
	"context"

	"github.com/Iryoda/kt-vibe-lsp/internal/cache"
	"github.com/Iryoda/kt-vibe-lsp/internal/kotlin"
	"github.com/Iryoda/kt-vibe-lsp/internal/protocol"
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
		locs = kotlin.Definition(f, sn.Index(), f.Mapper.PositionOffset(params.Position))
	})
	return locs, err
}
