package kotlin

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// hardKeywords can't be used as identifiers (without backticks).
var hardKeywords = []string{
	"as", "break", "class", "continue", "do", "else", "false", "for", "fun", "if", "in",
	"interface", "is", "null", "object", "package", "return", "super", "this", "throw",
	"true", "try", "typealias", "typeof", "val", "var", "when", "while",
}

var identifierRE = regexp.MustCompile(`^[\p{L}_][\p{L}\p{Nd}_]*$`)

// PrepareRename returns the range of the identifier at offset if it can be
// renamed: it must resolve to exactly one declaration in the workspace.
func PrepareRename(f *ParsedFile, ix *Index, offset int) (protocol.Range, error) {
	r := &resolver{f: f, ix: ix, src: f.Content}
	id, _, err := r.renameTarget(offset)
	if err != nil {
		return protocol.Range{}, err
	}
	return nodeLocation(f, id).Range, nil
}

// Rename returns the edits renaming the declaration named at offset, and
// every use of it, to newName. Renaming a member renames the whole
// override family: the members it overrides, their overrides, and all of
// their uses. Uses through an import alias keep the alias.
func Rename(f *ParsedFile, ix *Index, offset int, newName string, files FileSource) (map[protocol.DocumentURI][]protocol.TextEdit, error) {
	if !identifierRE.MatchString(newName) || slices.Contains(hardKeywords, newName) {
		return nil, fmt.Errorf("%q is not a valid Kotlin identifier", newName)
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	id, targets, err := r.renameTarget(offset)
	if err != nil {
		return nil, err
	}
	oldName := trimDollar(text(id, f.Content))
	if oldName == newName {
		return map[protocol.DocumentURI][]protocol.TextEdit{}, nil
	}
	family := r.overrideFamily(targets)
	edits := map[protocol.DocumentURI][]protocol.TextEdit{}
	for _, loc := range r.findUses(family, map[string]bool{oldName: true}, true, files) {
		edits[loc.URI] = append(edits[loc.URI], protocol.TextEdit{Range: loc.Range, NewText: newName})
	}
	return edits, nil
}

// renameTarget resolves the identifier to rename.
func (r *resolver) renameTarget(offset int) (id *ts.Node, targets []target, err error) {
	id = IdentifierAt(r.f.Tree, offset)
	if id == nil {
		return nil, nil, errors.New("no identifier here")
	}
	if name := trimDollar(text(id, r.src)); name == "it" || slices.Contains(hardKeywords, name) {
		return nil, nil, fmt.Errorf("%q can't be renamed", name)
	}
	targets = r.resolve(id)
	switch {
	case len(targets) == 0:
		return nil, nil, errors.New("no declaration found in the workspace (library symbols can't be renamed)")
	case len(targets) > 1:
		return nil, nil, fmt.Errorf("ambiguous: %d possible declarations; rename from the declaration instead", len(targets))
	}
	return id, targets, nil
}

// overrideFamily extends member targets with the members they override
// (transitively up the workspace supertypes) and all overrides of those.
func (r *resolver) overrideFamily(targets []target) []target {
	var family []*Symbol
	seen := map[*Symbol]bool{}
	add := func(s *Symbol) bool {
		if seen[s] {
			return false
		}
		seen[s] = true
		family = append(family, s)
		return true
	}
	out := slices.Clone(targets)
	for _, t := range targets {
		s := t.sym
		if s == nil || s.Container == "" || (s.Kind != KindFunction && s.Kind != KindProperty) {
			continue
		}
		add(s)
	}
	// Grow to a fixpoint: up to overridden members, down to overrides.
	for i := 0; i < len(family); i++ {
		s := family[i]
		visited := map[string]bool{}
		var up func(container string)
		up = func(container string) {
			for _, super := range r.supertypes(container) {
				if visited[super.FQName] {
					continue
				}
				visited[super.FQName] = true
				for _, m := range r.ix.Members(super.FQName) {
					if m.Name == s.Name && m.Kind == s.Kind {
						add(m)
					}
				}
				up(super.FQName)
			}
		}
		up(s.Container)
		for _, impl := range r.implementationSymbols([]target{{sym: s}}) {
			add(impl)
		}
	}
	for _, s := range family {
		if !slices.ContainsFunc(out, func(t target) bool { return t.sym == s }) {
			out = append(out, target{sym: s})
		}
	}
	return out
}
