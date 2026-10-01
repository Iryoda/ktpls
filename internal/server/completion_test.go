package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

func TestResolveCompletionItemWithoutAnalyzer(t *testing.T) {
	s := &Server{}
	for _, in := range []protocol.CompletionItem{
		{Label: "deposit", Detail: "fun deposit()", Documentation: &protocol.MarkupContent{Kind: protocol.Markdown, Value: "from the index"}},
		{Label: "plain"},
		{Label: "flatMap", Detail: "fun flatMap()", Data: json.RawMessage(`{"analyzer":"1:0"}`)},
		{Label: "bad", Data: json.RawMessage(`not json`)},
	} {
		item := in
		got, err := s.ResolveCompletionItem(context.Background(), &item)
		if err != nil {
			t.Errorf("%s: %v", in.Label, err)
			continue
		}
		// Unchanged: the index's docs are kept, and without the analyzer
		// there are none to add.
		if got.Label != in.Label || got.Documentation != in.Documentation {
			t.Errorf("%s: resolved to %+v", in.Label, got)
		}
	}
}
