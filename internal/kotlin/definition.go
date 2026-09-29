package kotlin

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/kt-vibe-lsp/internal/protocol"
)

// maxFallback caps the number of by-name candidates returned when a name
// cannot be resolved precisely.
const maxFallback = 30

// Definition returns the declarations of the identifier at offset in f.
// Without a type checker, resolution is by scope and name: locals, then
// members of enclosing classes (including inherited ones), then imports,
// the current package and wildcard imports, and finally any workspace
// symbol with the same name.
func Definition(f *ParsedFile, ix *Index, offset int) []protocol.Location {
	id := IdentifierAt(f.Tree, offset)
	if id == nil {
		return nil
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	targets := r.resolve(id)
	var locs []protocol.Location
	for _, t := range targets {
		locs = append(locs, t.location(f))
	}
	return locs
}

// IdentifierAt returns the identifier node at offset, also accepting a
// cursor just past the end of an identifier.
//
// It queries the one-byte range [off, off+1) rather than an empty range:
// an empty range at the start of a token also matches a preceding token
// ending there (such as the grammar's hidden automatic semicolon, which
// can absorb a newline and a comment), and resolves to their parent.
func IdentifierAt(tree *ts.Tree, offset int) *ts.Node {
	for _, off := range []int{offset, offset - 1} {
		if off < 0 {
			continue
		}
		n := tree.RootNode().NamedDescendantForByteRange(uint(off), uint(off)+1)
		if n != nil && isIdentifier(n) {
			return n
		}
	}
	return nil
}

func isIdentifier(n *ts.Node) bool {
	switch n.Kind() {
	case "simple_identifier", "type_identifier", "interpolated_identifier":
		return true
	}
	return false
}

// A target is a resolved declaration: an indexed symbol, a local in the
// current file, or a parameter of an indexed function.
type target struct {
	sym   *Symbol
	local *local
	param *protocol.Location
	// For param targets: the parameter and the function declaring it.
	paramInfo  Param
	paramOwner *Symbol
}

func (t target) location(f *ParsedFile) protocol.Location {
	if t.sym != nil {
		return t.sym.Location()
	}
	if t.param != nil {
		return *t.param
	}
	rng, _ := f.Mapper.OffsetRange(int(t.local.name.StartByte()), int(t.local.name.EndByte()))
	return protocol.Location{URI: f.URI, Range: rng}
}

type resolver struct {
	f   *ParsedFile
	ix  *Index
	src []byte
}

func symbolTargets(syms []*Symbol) []target {
	out := make([]target, len(syms))
	for i, s := range syms {
		out[i] = target{sym: s}
	}
	return out
}

// resolve resolves the identifier node id.
func (r *resolver) resolve(id *ts.Node) []target {
	name := text(id, r.src)
	if id.Kind() == "interpolated_identifier" {
		name = trimDollar(name)
	}
	parent := id.Parent()
	if parent == nil {
		return nil
	}

	if isDeclarationName(id) {
		if s := r.symbolForDecl(parent); s != nil {
			return []target{{sym: s}}
		}
		return []target{{local: &local{name: id, decl: parent}}}
	}

	switch {
	case parent.Kind() == "identifier" && ancestorOfKind(parent, "package_header") != nil:
		return nil
	case parent.Kind() == "identifier" && ancestorOfKind(parent, "import_header") != nil:
		return r.resolveImportPart(parent, id)
	case parent.Kind() == "navigation_suffix":
		return r.resolveMember(parent, name)
	case parent.Kind() == "value_argument" && isNamedArgument(parent, id):
		return r.resolveNamedArgument(parent, name)
	case id.Kind() == "type_identifier":
		return r.resolveTypeUse(id, name)
	}
	return r.resolveName(id, name, false)
}

func trimDollar(s string) string {
	if len(s) > 0 && s[0] == '$' {
		return s[1:]
	}
	return s
}

// isDeclarationName reports whether id is the name of the declaration
// that is its parent.
func isDeclarationName(id *ts.Node) bool {
	p := id.Parent()
	switch p.Kind() {
	case "class_declaration", "object_declaration", "companion_object", "type_alias", "type_parameter":
		return id.Kind() == "type_identifier" && sameNode(child(p, "type_identifier"), id)
	case "function_declaration", "variable_declaration", "parameter", "class_parameter", "enum_entry":
		return id.Kind() == "simple_identifier" && sameNode(child(p, "simple_identifier"), id)
	}
	return false
}

// symbolForDecl returns the indexed symbol for declaration node decl in
// the current file, or nil if decl is not indexed (a local).
func (r *resolver) symbolForDecl(decl *ts.Node) *Symbol {
	if r.f.Summary == nil {
		return nil
	}
	if decl.Kind() == "variable_declaration" {
		decl = decl.Parent() // symbols record the property_declaration
		if decl != nil && decl.Kind() == "multi_variable_declaration" {
			decl = decl.Parent()
		}
	}
	if decl == nil {
		return nil
	}
	for _, s := range r.f.Summary.Symbols {
		if s.StartByte == decl.StartByte() && s.EndByte == decl.EndByte() {
			return s
		}
	}
	return nil
}

// resolveName resolves an unqualified name used in expression (or, with
// typesOnly, type) position.
func (r *resolver) resolveName(use *ts.Node, name string, typesOnly bool) []target {
	if l := findLocal(use, name, r.src); l != nil {
		return []target{{local: l}}
	}
	for _, c := range r.enclosingContainers(use) {
		if ms := r.membersNamed(c, name, typesOnly, map[string]bool{}); len(ms) > 0 {
			return symbolTargets(ms)
		}
	}
	if syms := r.resolveInFile(r.f.Summary, name, typesOnly); len(syms) > 0 {
		return symbolTargets(syms)
	}
	if r.inLibraryLambda(use) {
		// The implicit receiver is a library type (a DSL builder): no
		// workspace declaration can be the target.
		return nil
	}
	return symbolTargets(r.fallback(name, typesOnly))
}

// returnsReceiver are the standard library functions that return their
// receiver: `x.also { }` is x.
var returnsReceiver = map[string]bool{"also": true, "apply": true, "takeIf": true, "takeUnless": true}

// scopeFunctions are the standard library functions whose lambda argument
// has the call's receiver (or, for with, its first argument) as its
// implicit receiver.
var scopeFunctions = map[string]bool{"apply": true, "run": true, "with": true}

// lambdaOwner returns the call_expression that lambda is an argument of.
func lambdaOwner(lambda *ts.Node) *ts.Node {
	p := lambda.Parent()
	for p != nil {
		switch p.Kind() {
		case "annotated_lambda", "value_argument", "value_arguments", "call_suffix":
			p = p.Parent()
		case "call_expression":
			return p
		default:
			return nil
		}
	}
	return nil
}

// callee returns the called name node of a call and its receiver
// expression (nil for an unqualified call).
func callee(call *ts.Node) (name, recv *ts.Node) {
	c := call.NamedChild(0)
	switch {
	case c == nil:
		return nil, nil
	case c.Kind() == "simple_identifier":
		return c, nil
	case c.Kind() == "navigation_expression":
		if sfx := child(c, "navigation_suffix"); sfx != nil {
			return child(sfx, "simple_identifier"), c.NamedChild(0)
		}
	}
	return nil, nil
}

// inLibraryLambda reports whether n is inside a lambda passed to a
// function that isn't declared in the workspace (other than a scope
// function, whose receiver we model).
func (r *resolver) inLibraryLambda(n *ts.Node) bool {
	lambda := ancestorOfKind(n, "lambda_literal")
	if lambda == nil {
		return false
	}
	call := lambdaOwner(lambda)
	if call == nil {
		return false
	}
	name, _ := callee(call)
	if name == nil {
		return false
	}
	fn := text(name, r.src)
	if scopeFunctions[fn] {
		return false
	}
	return len(r.ix.ByName(fn)) == 0
}

// resolveInFile resolves a top-level name as seen from file sum: explicit
// imports, then the file's package, then wildcard imports.
func (r *resolver) resolveInFile(sum *FileSummary, name string, typesOnly bool) []*Symbol {
	if sum == nil {
		return nil
	}
	for _, imp := range sum.Imports {
		if imp.Name() == name {
			if syms := filterKinds(r.ix.ByFQName(imp.Path), typesOnly); len(syms) > 0 {
				return syms
			}
		}
	}
	if syms := filterKinds(r.ix.ByFQName(joinFQ(sum.Package, name)), typesOnly); len(syms) > 0 {
		return syms
	}
	var out []*Symbol
	for _, imp := range sum.Imports {
		if imp.Wildcard {
			out = append(out, filterKinds(r.ix.ByFQName(joinFQ(imp.Path, name)), typesOnly)...)
		}
	}
	return out
}

// fallback returns the workspace symbols a name could refer to when
// scope-based resolution fails, typically because the name is a member of
// an implicit receiver whose type we don't know (a lambda with receiver,
// or an inherited library supertype). A top-level declaration can't be
// the target: it would have to be imported or in the same package, and
// both were checked.
func (r *resolver) fallback(name string, typesOnly bool) []*Symbol {
	var syms []*Symbol
	for _, s := range filterKinds(r.ix.ByName(name), typesOnly) {
		if s.Container != "" || s.Receiver != "" {
			syms = append(syms, s)
		}
	}
	if len(syms) > maxFallback {
		syms = syms[:maxFallback]
	}
	return syms
}

func filterKinds(syms []*Symbol, typesOnly bool) []*Symbol {
	if !typesOnly {
		return syms
	}
	var out []*Symbol
	for _, s := range syms {
		if s.Kind.IsType() {
			out = append(out, s)
		}
	}
	return out
}

// enclosingContainers returns the FQNames of the classes and objects
// enclosing n, innermost first.
func (r *resolver) enclosingContainers(n *ts.Node) []string {
	var out []string
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case "class_declaration", "object_declaration", "companion_object", "infix_expression":
			if s := r.symbolForDecl(p); s != nil {
				out = append(out, s.FQName)
			}
		case "lambda_literal":
			// Inside x.apply { }, x.run { } and with(x) { }, x's members
			// are in scope.
			if call := lambdaOwner(p); call != nil {
				if name, recv := callee(call); name != nil && scopeFunctions[text(name, r.src)] {
					if text(name, r.src) == "with" {
						recv = firstArgument(call)
					}
					for _, t := range r.typesOf(recv, 1) {
						out = append(out, t.FQName)
					}
				}
			}
		case "function_declaration":
			// Inside `fun Foo.bar()`, Foo's members are in scope.
			if rt := receiverType(p, r.src); rt != "" {
				for _, t := range r.resolveTypeName(rt, r.f.Summary, "") {
					out = append(out, t.FQName)
				}
			}
		}
	}
	return out
}

// membersNamed returns the members of container (including inherited and
// companion members) with the given name.
func (r *resolver) membersNamed(container, name string, typesOnly bool, visited map[string]bool) []*Symbol {
	if visited[container] {
		return nil
	}
	visited[container] = true

	var out []*Symbol
	members := r.ix.Members(container)
	for _, m := range members {
		if m.Name == name && (!typesOnly || m.Kind.IsType()) {
			out = append(out, m)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, m := range members {
		if m.Companion {
			out = append(out, r.membersNamed(m.FQName, name, typesOnly, visited)...)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, super := range r.supertypes(container) {
		out = append(out, r.membersNamed(super.FQName, name, typesOnly, visited)...)
	}
	return out
}

// supertypes resolves the declared supertypes of the class(es) with the
// given FQName.
func (r *resolver) supertypes(fq string) []*Symbol {
	var out []*Symbol
	for _, cls := range r.ix.ByFQName(fq) {
		for _, st := range cls.Supertypes {
			out = append(out, r.resolveTypeName(st, r.ix.File(cls.Path), cls.Container)...)
		}
	}
	return out
}

// resolveTypeName resolves a (possibly dotted) type name as written in
// file sum, inside the given container ("" at top level).
func (r *resolver) resolveTypeName(name string, sum *FileSummary, container string) []*Symbol {
	if name == "" {
		return nil
	}
	if first, rest, dotted := cut(name); dotted {
		// Outer.Inner, or fully qualified a.b.C.
		for _, outer := range r.resolveTypeName(first, sum, container) {
			if syms := filterKinds(r.ix.ByFQName(joinFQ(outer.FQName, rest)), true); len(syms) > 0 {
				return syms
			}
		}
		return filterKinds(r.ix.ByFQName(name), true)
	}
	// Nested types of the enclosing containers.
	for c := container; c != ""; c = parentFQ(c) {
		if syms := filterKinds(r.ix.ByFQName(joinFQ(c, name)), true); len(syms) > 0 {
			return syms
		}
	}
	if syms := r.resolveInFile(sum, name, true); len(syms) > 0 {
		return syms
	}
	return nil
}

func cut(name string) (first, rest string, ok bool) {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return name[:i], name[i+1:], true
		}
	}
	return name, "", false
}

// parentFQ returns the qualifier of a dotted name ("" if none).
func parentFQ(fq string) string {
	for i := len(fq) - 1; i >= 0; i-- {
		if fq[i] == '.' {
			return fq[:i]
		}
	}
	return ""
}

// resolveTypeUse resolves a type_identifier in a type (user_type), where
// earlier identifiers qualify later ones: in `a.b.C`, C is looked up in a.b.
func (r *resolver) resolveTypeUse(id *ts.Node, name string) []target {
	parent := id.Parent()
	if parent.Kind() == "user_type" {
		var qual []string
		for _, t := range childrenOf(parent, "type_identifier") {
			if t.Equals(*id) {
				break
			}
			qual = append(qual, text(t, r.src))
		}
		if len(qual) > 0 {
			full := joinAll(append(qual, name))
			return symbolTargets(r.resolveTypeName(full, r.f.Summary, r.containerAt(id)))
		}
	}
	if l := findLocal(id, name, r.src); l != nil { // type parameters
		return []target{{local: l}}
	}
	if syms := r.resolveTypeName(name, r.f.Summary, r.containerAt(id)); len(syms) > 0 {
		return symbolTargets(syms)
	}
	// Inherited nested types, then anything with that name.
	for _, c := range r.enclosingContainers(id) {
		if ms := r.membersNamed(c, name, true, map[string]bool{}); len(ms) > 0 {
			return symbolTargets(ms)
		}
	}
	return symbolTargets(r.fallback(name, true))
}

func joinAll(parts []string) string {
	out := ""
	for _, p := range parts {
		out = joinFQ(out, p)
	}
	return out
}

// containerAt returns the FQName of the innermost container enclosing n.
func (r *resolver) containerAt(n *ts.Node) string {
	if cs := r.enclosingContainers(n); len(cs) > 0 {
		return cs[0]
	}
	return ""
}

// resolveImportPart resolves one segment of an import path: the prefix up
// to and including the segment names a declaration (or a package).
func (r *resolver) resolveImportPart(ident, id *ts.Node) []target {
	var parts []string
	for _, p := range childrenOf(ident, "simple_identifier") {
		parts = append(parts, text(p, r.src))
		if p.Equals(*id) {
			break
		}
	}
	return symbolTargets(r.ix.ByFQName(joinAll(parts)))
}

// resolveMember resolves `receiver.name`, where suffix is the
// navigation_suffix holding name.
func (r *resolver) resolveMember(suffix *ts.Node, name string) []target {
	nav := suffix.Parent()
	var receiver *ts.Node
	if nav != nil && nav.Kind() == "navigation_expression" {
		receiver = nav.NamedChild(0)
	}
	if receiver != nil && !sameNode(receiver, suffix) {
		types := r.typesOf(receiver, 0)
		for _, t := range types {
			if ms := r.membersNamed(t.FQName, name, false, map[string]bool{}); len(ms) > 0 {
				return symbolTargets(ms)
			}
		}
		if ext := r.extensionsOn(types, "", name); len(ext) > 0 {
			return symbolTargets(ext)
		}
		// A package-qualified name: a.b.Foo
		if q := r.qualifiedText(receiver); q != "" {
			if syms := r.ix.ByFQName(joinFQ(q, name)); len(syms) > 0 {
				return symbolTargets(syms)
			}
		}
		if len(types) > 0 {
			// A workspace type without such a member: the member comes
			// from a library supertype (or is generated, like copy()).
			return nil
		}
		if tn := r.knownTypeName(receiver, 0); tn != "" {
			// A library type (String, List, ...): only workspace
			// extensions on it can be the target.
			return symbolTargets(r.extensionsOn(nil, tn, name))
		}
	}
	// Unknown receiver: any member or extension with that name. A name
	// that only exists as a top-level declaration can't be the target.
	var cands []*Symbol
	for _, s := range r.ix.ByName(name) {
		if s.Container != "" || s.Receiver != "" {
			cands = append(cands, s)
		}
	}
	if len(cands) > maxFallback {
		cands = cands[:maxFallback]
	}
	return symbolTargets(cands)
}

// extensionsOn returns the extension functions and properties named name
// whose receiver is one of types (or a supertype of one), or, if types is
// empty, whose receiver type is named typeName.
func (r *resolver) extensionsOn(types []*Symbol, typeName, name string) []*Symbol {
	accept := map[string]bool{}
	for _, t := range types {
		r.collectSupertypes(t.FQName, accept)
	}
	var out []*Symbol
	for _, s := range r.ix.ByName(name) {
		if s.Receiver == "" {
			continue
		}
		if len(types) == 0 {
			if typeName != "" && lastSegment(s.Receiver) == lastSegment(typeName) {
				out = append(out, s)
			}
			continue
		}
		for _, rt := range r.resolveTypeName(s.Receiver, r.ix.File(s.Path), s.Container) {
			if accept[rt.FQName] {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// collectSupertypes adds fq and all its transitive workspace supertypes.
func (r *resolver) collectSupertypes(fq string, into map[string]bool) {
	if into[fq] {
		return
	}
	into[fq] = true
	for _, st := range r.supertypes(fq) {
		r.collectSupertypes(st.FQName, into)
	}
}

// knownTypeName returns the name of expr's type when it is known even if
// the type is not declared in the workspace, e.g. "String" for a
// parameter `s: String` or a string literal; "" if unknown.
func (r *resolver) knownTypeName(expr *ts.Node, depth int) string {
	if depth > maxTypeDepth || expr == nil {
		return ""
	}
	switch expr.Kind() {
	case "string_literal", "multiline_string_literal":
		return "String"
	case "integer_literal":
		return "Int"
	case "long_literal":
		return "Long"
	case "real_literal":
		return "Double"
	case "boolean_literal":
		return "Boolean"
	case "character_literal":
		return "Char"
	case "parenthesized_expression":
		return r.knownTypeName(expr.NamedChild(0), depth+1)
	case "this_expression":
		if fn := enclosingExtension(expr); fn != nil {
			return receiverType(fn, r.src)
		}
	case "simple_identifier":
		targets := r.resolveName(expr, text(expr, r.src), false)
		if len(targets) != 1 {
			return ""
		}
		t := targets[0]
		if t.sym != nil {
			return t.sym.Type
		}
		if t.local != nil {
			if tn := declaredType(t.local.decl, r.src); tn != "" {
				return tn
			}
			if init := initializer(t.local.decl); init != nil {
				return r.knownTypeName(init, depth+1)
			}
		}
	case "call_expression":
		if callee := expr.NamedChild(0); callee != nil && callee.Kind() == "simple_identifier" {
			targets := r.resolveName(callee, text(callee, r.src), false)
			if len(targets) == 1 && targets[0].sym != nil && targets[0].sym.Kind == KindFunction {
				return targets[0].sym.Type
			}
		}
	}
	return ""
}

// firstArgument returns the first value argument of a call, or nil.
func firstArgument(call *ts.Node) *ts.Node {
	sfx := child(call, "call_suffix")
	if sfx == nil {
		return nil
	}
	args := child(sfx, "value_arguments")
	if args == nil {
		return nil
	}
	if arg := child(args, "value_argument"); arg != nil {
		return arg.NamedChild(0)
	}
	return nil
}

// initializer returns the initializer expression of a local variable
// declaration (`val x = expr`), or nil.
func initializer(decl *ts.Node) *ts.Node {
	if decl.Kind() != "variable_declaration" {
		return nil
	}
	prop := decl.Parent()
	if prop == nil || prop.Kind() != "property_declaration" {
		return nil
	}
	seenEq := false
	for _, c := range children(prop) {
		if c.Kind() == "=" {
			seenEq = true
		} else if seenEq && c.IsNamed() && !c.IsExtra() {
			return c
		}
	}
	return nil
}

// enclosingExtension returns the innermost enclosing extension function
// (one with a receiver type), or nil.
func enclosingExtension(n *ts.Node) *ts.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case "function_declaration":
			if child(p, "receiver_type") != nil {
				return p
			}
		case "class_declaration", "object_declaration":
			return nil // `this` is the class
		}
	}
	return nil
}

// isNamedArgument reports whether id is the name in `name = value` inside
// argument arg.
func isNamedArgument(arg, id *ts.Node) bool {
	next := id.NextSibling()
	return sameNode(arg.NamedChild(0), id) && next != nil && next.Kind() == "="
}

// resolveNamedArgument resolves the parameter name of a named argument
// `f(name = value)` to the parameter of the called function or
// constructor.
func (r *resolver) resolveNamedArgument(arg *ts.Node, name string) []target {
	call := arg.Parent() // value_arguments
	for call != nil && call.Kind() != "call_expression" && call.Kind() != "constructor_invocation" {
		if call.Kind() == "statements" || call.Kind() == "source_file" {
			return nil
		}
		call = call.Parent()
	}
	if call == nil {
		return nil
	}
	var callees []target
	switch callee := call.NamedChild(0); {
	case callee == nil:
		return nil
	case callee.Kind() == "simple_identifier":
		callees = r.resolveName(callee, text(callee, r.src), false)
	case callee.Kind() == "navigation_expression":
		if suffix := child(callee, "navigation_suffix"); suffix != nil {
			if id := child(suffix, "simple_identifier"); id != nil {
				callees = r.resolveMember(suffix, text(id, r.src))
			}
		}
	case callee.Kind() == "user_type": // constructor_invocation (annotations, supertypes)
		callees = symbolTargets(r.resolveTypeName(typeName(callee, r.src), r.f.Summary, r.containerAt(callee)))
	}
	var out []target
	for _, fn := range r.callables(callees) {
		for _, p := range fn.Params {
			if p.Name == name {
				out = append(out, target{
					param:      &protocol.Location{URI: fn.URI, Range: p.SelectionRange},
					paramInfo:  p,
					paramOwner: fn,
				})
			}
		}
	}
	return out
}

// callables maps resolved callees to the declarations whose parameters a
// call binds: functions and constructors, including the primary and
// secondary constructors of a class being instantiated.
func (r *resolver) callables(callees []target) []*Symbol {
	var fns []*Symbol
	for _, c := range callees {
		if c.sym == nil {
			continue
		}
		fns = append(fns, c.sym)
		if c.sym.Kind.IsType() {
			for _, m := range r.ix.Members(c.sym.FQName) {
				if m.Kind == KindConstructor {
					fns = append(fns, m)
				}
			}
		}
	}
	return fns
}

// maxTypeDepth bounds receiver-type resolution through chains like a.b.c.
const maxTypeDepth = 8

// typesOf returns the type symbols that expression expr may evaluate to,
// or that it names (for `Foo.bar`, where Foo is a class or object).
func (r *resolver) typesOf(expr *ts.Node, depth int) []*Symbol {
	if depth > maxTypeDepth || expr == nil {
		return nil
	}
	switch expr.Kind() {
	case "this_expression":
		if fn := enclosingExtension(expr); fn != nil {
			return r.resolveTypeName(receiverType(fn, r.src), r.f.Summary, r.containerAt(fn))
		}
		if cs := r.enclosingContainers(expr); len(cs) > 0 {
			return filterKinds(r.ix.ByFQName(cs[0]), true)
		}
	case "parenthesized_expression":
		return r.typesOf(expr.NamedChild(0), depth+1)
	case "simple_identifier":
		return r.typesOfTargets(r.resolveName(expr, text(expr, r.src), false), depth)
	case "navigation_expression":
		suffix := child(expr, "navigation_suffix")
		if suffix == nil {
			return nil
		}
		name := child(suffix, "simple_identifier")
		if name == nil {
			return nil
		}
		var out []*Symbol
		for _, t := range r.typesOf(expr.NamedChild(0), depth+1) {
			for _, m := range r.membersNamed(t.FQName, text(name, r.src), false, map[string]bool{}) {
				out = append(out, r.typeOfSymbol(m)...)
			}
		}
		return out
	case "call_expression":
		// x.also { } returns x.
		if name, recv := callee(expr); name != nil && recv != nil && returnsReceiver[text(name, r.src)] {
			return r.typesOf(recv, depth+1)
		}
		// f() has f's return type; Foo() constructs a Foo.
		return r.typesOf(expr.NamedChild(0), depth+1)
	}
	return nil
}

// typesOfTargets maps resolved declarations to the types of their values.
func (r *resolver) typesOfTargets(targets []target, depth int) []*Symbol {
	var out []*Symbol
	for _, t := range targets {
		if t.sym != nil {
			out = append(out, r.typeOfSymbol(t.sym)...)
			continue
		}
		// A local: use its declared type, or infer it from the initializer.
		decl := t.local.decl
		if tn := declaredType(decl, r.src); tn != "" {
			out = append(out, r.resolveTypeName(tn, r.f.Summary, r.containerAt(decl))...)
		} else if init := initializer(decl); init != nil {
			out = append(out, r.typesOf(init, depth+1)...)
		}
	}
	return out
}

// typeOfSymbol returns the type of a symbol's value: a type names itself
// (Foo.bar accesses Foo's companion/static members), a property or
// function has its declared type.
func (r *resolver) typeOfSymbol(s *Symbol) []*Symbol {
	if s.Kind.IsType() || s.Kind == KindEnumEntry {
		if s.Kind == KindEnumEntry {
			return filterKinds(r.ix.ByFQName(s.Container), true)
		}
		return []*Symbol{s}
	}
	if s.Kind == KindConstructor {
		return filterKinds(r.ix.ByFQName(s.Container), true)
	}
	return r.resolveTypeName(s.Type, r.ix.File(s.Path), s.Container)
}

// qualifiedText returns the dotted text of a receiver made only of
// identifiers (a.b.c), or "".
func (r *resolver) qualifiedText(n *ts.Node) string {
	switch n.Kind() {
	case "simple_identifier":
		return text(n, r.src)
	case "navigation_expression":
		left := r.qualifiedText(n.NamedChild(0))
		suffix := child(n, "navigation_suffix")
		if left == "" || suffix == nil {
			return ""
		}
		if id := child(suffix, "simple_identifier"); id != nil {
			return left + "." + text(id, r.src)
		}
	}
	return ""
}

// ancestorOfKind returns the nearest ancestor of n with the given kind.
func ancestorOfKind(n *ts.Node, kind string) *ts.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == kind {
			return p
		}
	}
	return nil
}

// sameNode reports whether a and b are the same node; a may be nil.
func sameNode(a, b *ts.Node) bool { return a != nil && b != nil && a.Equals(*b) }
