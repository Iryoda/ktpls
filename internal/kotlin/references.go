package kotlin

import (
	"bytes"
	"cmp"
	"slices"
	"sync"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// A FileSource calls fn with each parsed workspace file whose text
// contains name as a whole word (a cheap prefilter for reference search).
// It may call fn concurrently; each file is valid only during its call.
type FileSource func(name string, fn func(*ParsedFile))

// References returns the uses of the declaration named at offset in f,
// across the workspace, and the declaration itself if includeDecl.
//
// A use is an identifier with the same name that resolves, by the same
// rules as Definition, to the same declaration. Declarations are compared
// by the location of their name, which identifies symbols, locals and
// parameters alike.
func References(f *ParsedFile, ix *Index, offset int, includeDecl bool, files FileSource) []protocol.Location {
	id := IdentifierAt(f.Tree, offset)
	if id == nil {
		return nil
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	targets := r.resolve(id)
	if len(targets) == 0 {
		return nil
	}
	want := map[protocol.Location]bool{}
	for _, t := range targets {
		want[t.location(f)] = true
	}
	// The names a use may have: the identifier, the declaration's own
	// name, and any import alias of it (`import a.format as fmt`).
	names := map[string]bool{trimDollar(text(id, f.Content)): true}
	for _, t := range targets {
		if t.sym == nil {
			continue
		}
		names[t.sym.Name] = true
		for sum := range ix.Files() {
			for _, imp := range sum.Imports {
				if imp.Alias != "" && imp.Path == t.sym.FQName {
					names[imp.Alias] = true
				}
			}
		}
	}

	var (
		mu   sync.Mutex
		locs []protocol.Location
		seen = map[protocol.Location]bool{}
	)
	for name := range names {
		files(name, func(pf *ParsedFile) {
			pr := &resolver{f: pf, ix: ix, src: pf.Content}
			var found []protocol.Location
			walkIdentifiers(pf.Tree.RootNode(), func(n *ts.Node) {
				if trimDollar(text(n, pf.Content)) != name {
					return
				}
				for _, t := range pr.resolve(n) {
					if want[t.location(pf)] {
						if loc := nodeLocation(pf, n); includeDecl || !want[loc] {
							found = append(found, loc)
						}
						return
					}
				}
			})
			mu.Lock()
			defer mu.Unlock()
			for _, loc := range found {
				if !seen[loc] {
					seen[loc] = true
					locs = append(locs, loc)
				}
			}
		})
	}
	slices.SortFunc(locs, func(a, b protocol.Location) int {
		return cmp.Or(
			cmp.Compare(a.URI, b.URI),
			cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
			cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
		)
	})
	return locs
}

// ContainsWord reports whether name occurs in content as a whole
// identifier (not as part of a longer one).
func ContainsWord(content []byte, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; ; {
		j := bytes.Index(content[i:], []byte(name))
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		before, _ := utf8.DecodeLastRune(content[:start])
		after, _ := utf8.DecodeRune(content[end:])
		if (start == 0 || !isIdentRune(before)) && (end == len(content) || !isIdentRune(after)) {
			return true
		}
		i = start + 1
	}
}

func isIdentRune(r rune) bool { return r == '_' || isLetterOrDigit(r) }

func walkIdentifiers(n *ts.Node, fn func(*ts.Node)) {
	if isIdentifier(n) {
		fn(n)
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		walkIdentifiers(n.Child(i), fn)
	}
}

func nodeLocation(f *ParsedFile, n *ts.Node) protocol.Location {
	rng, _ := f.Mapper.OffsetRange(int(n.StartByte()), int(n.EndByte()))
	return protocol.Location{URI: f.URI, Range: rng}
}
