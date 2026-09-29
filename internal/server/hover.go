package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) Hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var hover *protocol.Hover
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		if h := kotlin.Hover(f, sn.Index(), f.Mapper.PositionOffset(params.Position)); h != nil {
			hover = &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: h.Markdown},
				Range:    &h.Range,
			}
		}
	})
	return hover, err
}
