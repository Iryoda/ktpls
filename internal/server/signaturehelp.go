package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var help *protocol.SignatureHelp
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		help = kotlin.SignatureHelp(f, sn.Index(), f.Mapper.PositionOffset(params.Position))
	})
	return help, err
}
