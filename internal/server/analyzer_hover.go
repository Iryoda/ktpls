package server

import (
	"context"
	"strings"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// hoverTimeout bounds a hover asked of the analyzer: its first requests
// run on a cold JVM.
const hoverTimeout = 10 * time.Second

// analyzerHover asks the analyzer about what the syntax-based hover can't
// resolve: library declarations (their signature with this call's types,
// and their docs from the library's sources jar).
func (s *Server) analyzerHover(ctx context.Context, path string, pos protocol.Position) *protocol.Hover {
	c := s.analyzerClient()
	if c == nil || !cache.IsKotlinFile(path) {
		return nil
	}
	var text []byte
	var mapper *protocol.Mapper
	s.session.Read(func(sn *cache.Snapshot) {
		if f := sn.File(path); f != nil {
			text, mapper = f.Content, f.Mapper
			if mapper == nil { // not open: mappers are for overlays
				mapper = protocol.NewMapper(text, s.session.Encoding())
			}
		}
	})
	if mapper == nil {
		return nil
	}
	off := mapper.PositionOffset(pos)
	ctx, cancel := context.WithTimeout(ctx, hoverTimeout)
	defer cancel()
	h, err := c.Hover(ctx, path, string(text), textutil.UTF16Len(text[:min(off, len(text))]))
	if err != nil || h == nil || h.Signature == "" {
		if err != nil {
			s.log.Debug("analyzer: hover", "err", err)
		}
		return nil
	}
	rng, err := mapper.OffsetRange(textutil.UTF16ToByte(text, h.Start), textutil.UTF16ToByte(text, h.End))
	if err != nil {
		return nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: hoverMarkdown(h)},
		Range:    &rng,
	}
}

// hoverMarkdown renders the analyzer's hover like the syntax-based one:
// the declaration, where it is, and its docs.
func hoverMarkdown(h *analyzer.HoverInfo) string {
	var b strings.Builder
	b.WriteString("```kotlin\n" + h.Signature + "\n```")
	if h.Call != "" {
		b.WriteString("\n\n*in this call:*\n```kotlin\n" + h.Call + "\n```")
	}
	if h.Container != "" {
		b.WriteString("\n\n*in `" + h.Container + "`*")
	}
	if doc := renderDoc(h); doc != "" {
		b.WriteString("\n\n---\n\n" + doc)
	}
	return b.String()
}

func renderDoc(h *analyzer.HoverInfo) string {
	switch {
	case h.Doc == "":
		return ""
	case h.DocLanguage == "java":
		return kotlin.RenderJavadoc(h.Doc)
	default:
		return kotlin.RenderKDoc(h.Doc)
	}
}
