package kotlin

import (
	"fmt"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// An Action is a code action: a titled set of edits to one file.
type Action struct {
	Title string
	Kind  string
	Edits []protocol.TextEdit
}

// CodeActions returns the code actions available at offset in f.
func CodeActions(f *ParsedFile, ix *Index, offset int) []Action {
	r := &resolver{f: f, ix: ix, src: f.Content}
	return r.nameArguments(offset)
}

// nameArguments offers to add parameter names to the positional arguments
// of the call enclosing offset: f(a, b) becomes f(x = a, y = b).
// Arguments already named are kept; naming stops at a spread argument or a
// vararg parameter, and a trailing lambda is left outside the parentheses.
// Overloads that would name the arguments differently each get an action.
func (r *resolver) nameArguments(offset int) []Action {
	call, args := enclosingCall(r.f.Tree, uint(offset))
	if call == nil {
		return nil
	}
	var fns []*Symbol
	switch call.Kind() {
	case "call_expression":
		if name, _ := callee(call); name != nil {
			fns = r.callables(r.resolve(name))
		}
	case "constructor_invocation": // supertype and annotation constructors
		fns = r.callables(symbolTargets(r.resolveTypeName(typeName(child(call, "user_type"), r.src), r.f.Summary, r.containerAt(call))))
	}

	var actions []Action
	var seen []string
	for _, fn := range fns {
		edits, ok := r.argumentNameEdits(fn, args)
		if !ok || len(edits) == 0 {
			continue
		}
		key := fmt.Sprint(edits)
		if slices.Contains(seen, key) {
			continue // another overload naming the arguments the same way
		}
		seen = append(seen, key)
		actions = append(actions, Action{Kind: protocol.RefactorRewrite, Edits: edits})
	}
	for i := range actions {
		name := fns[0].Name
		if fns[0].Kind == KindConstructor || fns[0].Kind.IsType() {
			name = lastSegment(fns[0].FQName)
		}
		actions[i].Title = fmt.Sprintf("Add parameter names to `%s` arguments", name)
		if len(actions) > 1 {
			actions[i].Title += fmt.Sprintf(" (%s)", paramList(fns[i]))
		}
	}
	return actions
}

// argumentNameEdits returns the edits naming args by fn's parameters, or
// false if the arguments don't fit fn.
func (r *resolver) argumentNameEdits(fn *Symbol, args []*ts.Node) ([]protocol.TextEdit, bool) {
	named := map[string]bool{}
	for _, a := range args {
		if name := argumentName(a, r.src); name != "" {
			named[name] = true
		}
	}
	var edits []protocol.TextEdit
	for i, a := range args {
		if child(a, "spread_expression") != nil {
			break // *array: positional by nature
		}
		if name := argumentName(a, r.src); name != "" {
			if !slices.ContainsFunc(fn.Params, func(p Param) bool { return p.Name == name }) {
				return nil, false // names a parameter fn doesn't have
			}
			continue
		}
		if i >= len(fn.Params) {
			return nil, false // too many arguments for fn
		}
		p := fn.Params[i]
		if p.Vararg {
			break
		}
		if named[p.Name] {
			return nil, false // that parameter is already passed by name
		}
		pos, _ := r.f.Mapper.OffsetPosition(int(a.StartByte()))
		edits = append(edits, protocol.TextEdit{Range: protocol.Range{Start: pos, End: pos}, NewText: p.Name + " = "})
	}
	return edits, true
}

// argumentName returns the name of a named argument (`name = value`), or "".
func argumentName(arg *ts.Node, src []byte) string {
	if id := arg.NamedChild(0); id != nil && id.Kind() == "simple_identifier" && isNamedArgument(arg, id) {
		return text(id, src)
	}
	return ""
}

// enclosingCall returns the innermost call around offset that has
// parenthesized arguments, and those arguments.
func enclosingCall(tree *ts.Tree, offset uint) (*ts.Node, []*ts.Node) {
	n := tree.RootNode().NamedDescendantForByteRange(offset, offset)
	for ; n != nil; n = n.Parent() {
		var args *ts.Node
		switch n.Kind() {
		case "call_expression":
			if sfx := child(n, "call_suffix"); sfx != nil {
				args = child(sfx, "value_arguments")
			}
		case "constructor_invocation":
			args = child(n, "value_arguments")
		}
		if args != nil {
			if list := childrenOf(args, "value_argument"); len(list) > 0 {
				return n, list
			}
		}
	}
	return nil, nil
}

// paramList renders fn's parameters for disambiguating titles.
func paramList(fn *Symbol) string {
	parts := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		parts[i] = p.Name
		if p.Type != "" {
			parts[i] += ": " + p.Type
		}
	}
	return strings.Join(parts, ", ")
}
