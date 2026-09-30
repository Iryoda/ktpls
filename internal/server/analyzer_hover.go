package server

import (
	"context"
	"os"
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
	h, text, mapper := s.analyzerResolve(ctx, path, pos)
	if h == nil {
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

// analyzerLocalHover describes a local variable or lambda parameter with
// the type the compiler infers, as what it is (where).
func (s *Server) analyzerLocalHover(ctx context.Context, path string, pos protocol.Position, where string) *protocol.Hover {
	h, text, mapper := s.analyzerResolve(ctx, path, pos)
	if h == nil {
		return nil
	}
	rng, err := mapper.OffsetRange(textutil.UTF16ToByte(text, h.Start), textutil.UTF16ToByte(text, h.End))
	if err != nil {
		return nil
	}
	md := "```kotlin\n" + h.Signature + "\n```"
	if where != "" {
		md += "\n\n*" + where + "*"
	}
	if doc := renderDoc(h); doc != "" {
		md += "\n\n---\n\n" + doc
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: md}, Range: &rng}
}

// analyzerDefinition finds a library declaration with the analyzer: in
// its sources jar, extracted for the editor to open.
func (s *Server) analyzerDefinition(ctx context.Context, path string, pos protocol.Position) []protocol.Location {
	h, _, _ := s.analyzerResolve(ctx, path, pos)
	if h == nil || h.Source == nil {
		return nil
	}
	file, err := analyzer.SourceFile(h.Source)
	if err != nil {
		s.log.Debug("analyzer: definition", "err", err)
		return nil
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	off := textutil.UTF16ToByte(content, h.Source.Offset)
	rng, err := protocol.NewMapper(content, s.session.Encoding()).OffsetRange(off, off)
	if err != nil {
		return nil
	}
	return []protocol.Location{{URI: protocol.URIFromPath(file), Range: rng}}
}

// analyzerResolve asks the analyzer what the reference at pos in the file
// at path is, in the file's current text (returned with its mapper).
func (s *Server) analyzerResolve(ctx context.Context, path string, pos protocol.Position) (*analyzer.HoverInfo, []byte, *protocol.Mapper) {
	c := s.analyzerClient()
	if c == nil || !cache.IsKotlinFile(path) {
		return nil, nil, nil
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
		return nil, nil, nil
	}
	off := mapper.PositionOffset(pos)
	ctx, cancel := context.WithTimeout(ctx, hoverTimeout)
	defer cancel()
	h, err := c.Hover(ctx, path, string(text), textutil.UTF16Len(text[:min(off, len(text))]))
	if err != nil || h == nil || h.Signature == "" {
		if err != nil {
			s.log.Debug("analyzer: resolve", "err", err)
		}
		return nil, nil, nil
	}
	return h, text, mapper
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
