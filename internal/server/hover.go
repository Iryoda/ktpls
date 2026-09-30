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
	var local *kotlin.HoverResult
	guess := false
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		if hover = s.messageHover(sn, path, params.Position); hover != nil {
			return
		}
		if h := kotlin.Hover(f, sn.Index(), f.Mapper.PositionOffset(params.Position)); h != nil {
			if h.Local {
				local = h
			}
			guess = h.Guess
			hover = &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: h.Markdown},
				Range:    &h.Range,
			}
		}
	})
	switch {
	case err != nil:
	case hover == nil:
		// A library declaration, most likely: ask the compiler.
		hover = s.analyzerHover(ctx, path, params.Position)
	case guess:
		// Found by name alone: the compiler knows which it is.
		if h := s.analyzerHover(ctx, path, params.Position); h != nil {
			hover = h
		}
	case local != nil:
		// The compiler knows a local's type; the syntax only guesses it.
		if h := s.analyzerLocalHover(ctx, path, params.Position, local.Where); h != nil {
			hover = h
		}
	}
	return hover, err
}
