package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) Completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var list *protocol.CompletionList
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		list = kotlin.Complete(f, sn.Index(), f.Mapper.PositionOffset(params.Position))
	})
	return list, err
}
