package kotlin

import (
	"github.com/Iryoda/ktpls/internal/protocol"
)

// Implementation returns the implementations of the declaration named at
// offset in f: for a class or interface, the workspace types that inherit
// from it (transitively); for a member, the members with the same name
// in those types.
func Implementation(f *ParsedFile, ix *Index, offset int) []protocol.Location {
	id := IdentifierAt(f.Tree, offset)
	if id == nil {
		return nil
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	return r.implementations(r.resolve(id))
}

// implementations returns the implementations of the target declarations.
func (r *resolver) implementations(targets []target) []protocol.Location {
	var locs []protocol.Location
	for _, s := range r.implementationSymbols(targets) {
		locs = append(locs, s.Location())
	}
	return locs
}

// implementationSymbols returns the declarations implementing the targets.
func (r *resolver) implementationSymbols(targets []target) []*Symbol {
	ix := r.ix
	var subs map[string][]*Symbol // built lazily: it scans the whole index
	var out []*Symbol
	seen := map[*Symbol]bool{}
	for _, t := range targets {
		s := t.sym
		if s == nil {
			continue
		}
		if subs == nil {
			subs = r.subtypeMap()
		}
		switch {
		case s.Kind.IsType():
			for _, sub := range subtypesOf(s.FQName, subs) {
				if !seen[sub] {
					seen[sub] = true
					out = append(out, sub)
				}
			}
		case s.Container != "":
			for _, sub := range subtypesOf(s.Container, subs) {
				for _, m := range ix.Members(sub.FQName) {
					if m.Name == s.Name && m.Kind == s.Kind && !seen[m] {
						seen[m] = true
						out = append(out, m)
					}
				}
			}
		}
	}
	return out
}

// subtypeMap maps each workspace type's FQName to the types declaring it
// as a direct supertype.
func (r *resolver) subtypeMap() map[string][]*Symbol {
	subs := map[string][]*Symbol{}
	for sum := range r.ix.Files() {
		for _, s := range sum.Symbols {
			for _, st := range s.Supertypes {
				for _, super := range r.resolveTypeName(st, sum, s.Container) {
					subs[super.FQName] = append(subs[super.FQName], s)
				}
			}
		}
	}
	return subs
}

// subtypesOf returns the transitive subtypes of fq.
func subtypesOf(fq string, subs map[string][]*Symbol) []*Symbol {
	var out []*Symbol
	visited := map[string]bool{fq: true}
	queue := []string{fq}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, sub := range subs[cur] {
			if visited[sub.FQName] {
				continue
			}
			visited[sub.FQName] = true
			out = append(out, sub)
			queue = append(queue, sub.FQName)
		}
	}
	return out
}
