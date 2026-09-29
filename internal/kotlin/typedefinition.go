package kotlin

import (
	"github.com/Iryoda/ktpls/internal/protocol"
)

// TypeDefinition returns the declaration of the type of the value named at
// offset: for `account` (an Account), the class Account. For a library
// type wrapping workspace types, such as List<Account>, it returns the
// workspace types among its arguments.
func TypeDefinition(f *ParsedFile, ix *Index, offset int) []protocol.Location {
	id := IdentifierAt(f.Tree, offset)
	if id == nil {
		return nil
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	var refs []typeRef
	targets := r.resolve(id)
	for _, t := range targets {
		switch {
		case t.sym != nil && (t.sym.Kind.IsType() || t.sym.Kind == KindEnumEntry || t.sym.Kind == KindConstructor):
			refs = append(refs, symbolsType([]target{t}, ix))
		case t.sym != nil:
			refs = append(refs, typeRef{t.sym.Type, ix.File(t.sym.Path), t.sym.Container})
		case t.param != nil:
			refs = append(refs, typeRef{t.paramInfo.Type, ix.File(t.paramOwner.Path), t.paramOwner.Container})
		case t.local != nil:
			refs = append(refs, r.typeOfLocal(t.local.decl, 0))
		}
	}
	if len(targets) == 0 { // `it`
		refs = append(refs, r.typeOf(id, 0))
	}
	var locs []protocol.Location
	seen := map[*Symbol]bool{}
	for _, ref := range refs {
		for _, s := range r.workspaceTypesIn(ref, 0) {
			if !seen[s] {
				seen[s] = true
				locs = append(locs, s.Location())
			}
		}
	}
	return locs
}

// workspaceTypesIn resolves t to workspace types; a library type (List,
// Map, ...) yields the workspace types among its type arguments.
func (r *resolver) workspaceTypesIn(t typeRef, depth int) []*Symbol {
	if t.text == "" || depth > 4 {
		return nil
	}
	if syms := r.resolveRef(t); len(syms) > 0 {
		return syms
	}
	var out []*Symbol
	for i := range typeArgs(t.text) {
		out = append(out, r.workspaceTypesIn(t.arg(i), depth+1)...)
	}
	return out
}
