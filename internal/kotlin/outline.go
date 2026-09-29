package kotlin

import (
	"cmp"
	"slices"

	"github.com/Iryoda/ktpls/internal/fuzzy"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// maxWorkspaceSymbols caps workspace symbol search results.
const maxWorkspaceSymbols = 200

// DocumentSymbols returns the outline of a file: its declarations, with
// members nested under their classes and objects.
func DocumentSymbols(sum *FileSummary) []protocol.DocumentSymbol {
	if sum == nil {
		return nil
	}
	type node struct {
		sym      protocol.DocumentSymbol
		children []*node
	}
	byFQ := map[string]*node{} // containers of this file
	var roots []*node
	for _, s := range sum.Symbols {
		n := &node{sym: protocol.DocumentSymbol{
			Name:           s.Name,
			Detail:         s.Signature,
			Kind:           symbolKind(s),
			Range:          s.Range,
			SelectionRange: s.SelectionRange,
		}}
		if parent := byFQ[s.Container]; s.Container != "" && parent != nil {
			parent.children = append(parent.children, n)
		} else {
			roots = append(roots, n)
		}
		if s.Kind.IsType() && byFQ[s.FQName] == nil {
			byFQ[s.FQName] = n
		}
	}
	var build func([]*node) []protocol.DocumentSymbol
	build = func(ns []*node) []protocol.DocumentSymbol {
		out := make([]protocol.DocumentSymbol, len(ns))
		for i, n := range ns {
			out[i] = n.sym
			out[i].Children = build(n.children)
		}
		return out
	}
	return build(roots)
}

// WorkspaceSymbols returns the workspace declarations whose names match
// query, best matches first.
func WorkspaceSymbols(ix *Index, query string) []protocol.SymbolInformation {
	type match struct {
		s     *Symbol
		score int
	}
	var matches []match
	for name := range ix.Names() {
		score, ok := fuzzy.Score(query, name)
		if !ok {
			continue
		}
		for _, s := range ix.ByName(name) {
			if s.Kind == KindConstructor {
				continue
			}
			if query == "" && !s.Kind.IsType() {
				continue // without a query, list types only
			}
			matches = append(matches, match{s, score})
		}
	}
	slices.SortFunc(matches, func(a, b match) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		return cmp.Compare(a.s.FQName, b.s.FQName)
	})
	if len(matches) > maxWorkspaceSymbols {
		matches = matches[:maxWorkspaceSymbols]
	}
	out := make([]protocol.SymbolInformation, len(matches))
	for i, m := range matches {
		container := m.s.Container
		if container == "" {
			container = packageOf(ix, m.s)
		}
		out[i] = protocol.SymbolInformation{Name: m.s.Name, Kind: symbolKind(m.s), Location: m.s.Location(), ContainerName: container}
	}
	return out
}

func symbolKind(s *Symbol) protocol.SymbolKind {
	switch s.Kind {
	case KindClass, KindTypeAlias:
		return protocol.SymbolKindClass
	case KindInterface:
		return protocol.SymbolKindInterface
	case KindEnum:
		return protocol.SymbolKindEnum
	case KindObject:
		return protocol.SymbolKindObject
	case KindEnumEntry:
		return protocol.SymbolKindEnumMember
	case KindConstructor:
		return protocol.SymbolKindConstructor
	case KindFunction:
		if s.Container != "" {
			return protocol.SymbolKindMethod
		}
		return protocol.SymbolKindFunction
	case KindProperty:
		return protocol.SymbolKindProperty
	}
	return protocol.SymbolKindVariable
}
