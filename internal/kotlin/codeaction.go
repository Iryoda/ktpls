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
	// Open, if set, makes the action a navigation: selecting it opens
	// this location (through the OpenCommand command).
	Open *protocol.Location
	// Other holds edits to files other than the current one.
	Other map[protocol.DocumentURI][]protocol.TextEdit
}

// OpenCommand is the command of navigation actions. Its argument is a
// protocol.Location; the server asks the client to show it.
const OpenCommand = "ktpls.open"

// CodeActions returns the code actions available at offset in f.
func CodeActions(f *ParsedFile, ix *Index, offset int) []Action {
	return CodeActionsWith(f, ix, offset, nil)
}

// CodeActionsWith is CodeActions with a reader of other workspace files'
// current content, for actions editing them.
func CodeActionsWith(f *ParsedFile, ix *Index, offset int, read func(path string) []byte) []Action {
	r := &resolver{f: f, ix: ix, src: f.Content, read: read}
	var out []Action
	for _, fn := range []func(int) []Action{
		// Quick fixes first.
		r.addImports, r.createFunction, r.implementMembers, r.whenBranches,
		r.nameArguments, r.testNavigation,
		r.convertBody, r.specifyType, r.braces, r.stringTemplate,
	} {
		out = append(out, fn(offset)...)
	}
	return out
}

// addImports offers imports for an unresolved name at offset that is
// declared at top level in another workspace package: a class or
// function, or an extension when the name follows a dot.
func (r *resolver) addImports(offset int) []Action {
	id := IdentifierAt(r.f.Tree, offset)
	if id == nil || isDeclarationName(id) {
		return nil
	}
	name := text(id, r.src)
	member := id.Parent() != nil && id.Parent().Kind() == "navigation_suffix"
	if r.visiblyResolved(id, member) {
		return nil
	}
	pkg := ""
	if r.f.Summary != nil {
		pkg = r.f.Summary.Package
	}
	var actions []Action
	seen := map[string]bool{}
	for _, s := range r.ix.ByName(name) {
		if s.Container != "" || seen[s.FQName] || packageOf(r.ix, s) == pkg {
			continue
		}
		if member != (s.Receiver != "") {
			continue // after a dot only extensions fit; elsewhere, not them
		}
		seen[s.FQName] = true
		actions = append(actions, Action{
			Title: "Import `" + s.FQName + "`",
			Kind:  protocol.QuickFix,
			Edits: []protocol.TextEdit{importEdit(r.f, s.FQName)},
		})
	}
	slices.SortFunc(actions, func(a, b Action) int { return strings.Compare(a.Title, b.Title) })
	return actions
}

// visiblyResolved reports whether id resolves to a declaration visible
// from this file without a new import: a local, a member of the receiver
// or of an enclosing class, or a top-level declaration of the same package
// or already imported. (Resolution may also reach declarations the file
// can't see, like an unimported extension or a by-name guess.)
func (r *resolver) visiblyResolved(id *ts.Node, member bool) bool {
	enclosing := map[string]bool{}
	for _, c := range r.enclosingContainers(id) {
		r.collectSupertypes(c, enclosing)
	}
	for _, t := range r.resolve(id) {
		switch s := t.sym; {
		case s == nil:
			return true // a local or parameter
		case s.Container != "" && s.Receiver == "":
			if member || enclosing[s.Container] {
				return true
			}
		case r.importedOrLocal(s):
			return true
		}
	}
	return false
}

// importedOrLocal reports whether a top-level declaration is visible in
// this file: same package, or imported (explicitly, by wildcard or alias).
func (r *resolver) importedOrLocal(s *Symbol) bool {
	sum := r.f.Summary
	if sum == nil {
		return false
	}
	pkg := packageOf(r.ix, s)
	if pkg == sum.Package {
		return true
	}
	for _, imp := range sum.Imports {
		if imp.Path == s.FQName || imp.Wildcard && imp.Path == pkg {
			return true
		}
	}
	return false
}

// importEdit returns an edit adding `import fq` after the file's last
// import (or its package header, or at the top).
func importEdit(f *ParsedFile, fq string) protocol.TextEdit {
	root := f.Tree.RootNode()
	var at int
	newText := "import " + fq + "\n"
	if list := child(root, "import_list"); list != nil {
		headers := childrenOf(list, "import_header")
		if len(headers) > 0 {
			at, newText = int(headers[len(headers)-1].EndByte()), "\nimport "+fq
		}
	} else if pkg := child(root, "package_header"); pkg != nil {
		at, newText = int(pkg.EndByte()), "\n\nimport "+fq
	}
	pos, _ := f.Mapper.OffsetPosition(at)
	return protocol.TextEdit{Range: protocol.Range{Start: pos, End: pos}, NewText: newText}
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
