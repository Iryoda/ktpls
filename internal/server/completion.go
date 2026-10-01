package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Iryoda/ktpls/internal/analyzer"
	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// completionTimeout bounds how long completion waits for the analyzer's
// candidates; past it, the index's are returned alone, marked incomplete
// so the client asks again.
const completionTimeout = 2 * time.Second

func (s *Server) Completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	ext, complete := s.analyzerCompletion(ctx, path, params.Position)
	var list *protocol.CompletionList
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		list = kotlin.Complete(f, sn.Index(), f.Mapper.PositionOffset(params.Position), ext...)
	})
	if list != nil && !complete {
		list.IsIncomplete = true
	}
	return list, err
}

// analyzerCompletion asks the analyzer for the candidates the index can't
// know (library members and extensions, default imports). It reports
// false if the analyzer was asked and didn't answer in time.
func (s *Server) analyzerCompletion(ctx context.Context, path string, pos protocol.Position) ([]kotlin.External, bool) {
	c := s.analyzerClient()
	if c == nil || !cache.IsKotlinFile(path) {
		return nil, true
	}
	var text []byte
	var start, offset int
	ok := false
	s.session.Read(func(sn *cache.Snapshot) {
		f, release, err := sn.Parse(path)
		if err != nil {
			return
		}
		defer release()
		text, offset = f.Content, f.Mapper.PositionOffset(pos)
		start, ok = kotlin.CompletionStart(f, offset)
	})
	if !ok {
		return nil, true
	}
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	cands, err := c.Complete(ctx, path, string(text), textutil.UTF16Len(text[:offset]), string(text[start:offset]))
	if err != nil {
		s.log.Debug("analyzer: complete", "err", err)
		return nil, false
	}
	ext := make([]kotlin.External, len(cands))
	for i, k := range cands {
		ext[i] = kotlin.External{Name: k.Name, Kind: k.Kind, Signature: k.Signature, Receiver: k.Receiver, Container: k.Container, Member: k.Member, ID: k.ID}
	}
	return ext, true
}

// ResolveCompletionItem adds the docs of a compiler candidate (a library
// declaration's, from its sources jar), as hover shows them. The index's
// items come with theirs.
func (s *Server) ResolveCompletionItem(ctx context.Context, item *protocol.CompletionItem) (*protocol.CompletionItem, error) {
	var data kotlin.ExternalData
	if item.Documentation != nil || len(item.Data) == 0 || json.Unmarshal(item.Data, &data) != nil || data.Analyzer == "" {
		return item, nil
	}
	c := s.analyzerClient()
	if c == nil {
		return item, nil
	}
	ctx, cancel := context.WithTimeout(ctx, hoverTimeout)
	defer cancel()
	d, err := c.CompletionDoc(ctx, data.Analyzer)
	if err != nil {
		s.log.Debug("analyzer: completion doc", "err", err)
	}
	h := &analyzer.HoverInfo{Signature: item.Detail, Container: data.Container}
	if d != nil {
		h.Doc, h.DocLanguage = d.Doc, d.DocLanguage
	}
	item.Documentation = &protocol.MarkupContent{Kind: protocol.Markdown, Value: hoverMarkdown(h)}
	return item, nil
}
