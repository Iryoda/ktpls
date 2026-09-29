package kotlin

import (
	"unicode"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// Kind classifies a declaration.
type Kind int

const (
	KindClass Kind = iota + 1
	KindInterface
	KindEnum
	KindObject
	KindFunction
	KindProperty
	KindConstructor
	KindTypeAlias
	KindEnumEntry
)

var kindNames = map[Kind]string{
	KindClass: "class", KindInterface: "interface", KindEnum: "enum class", KindObject: "object",
	KindFunction: "fun", KindProperty: "property", KindConstructor: "constructor",
	KindTypeAlias: "typealias", KindEnumEntry: "enum entry",
}

func (k Kind) String() string { return kindNames[k] }

// kindKeyword returns the Kotlin keyword that declares kind.
func kindKeyword(k Kind) string {
	switch k {
	case KindInterface:
		return "interface"
	case KindObject:
		return "object"
	case KindFunction:
		return "fun"
	case KindProperty:
		return "val"
	}
	return "class"
}

// IsType reports whether a symbol of kind k names a type.
func (k Kind) IsType() bool {
	switch k {
	case KindClass, KindInterface, KindEnum, KindObject, KindTypeAlias:
		return true
	}
	return false
}

// A Symbol is a declaration visible outside its own body: a top-level or
// member declaration. (Locals are resolved from the syntax tree instead.)
type Symbol struct {
	Name      string
	Kind      Kind
	FQName    string // package + containers + name, e.g. "com.example.Circle.area"
	Container string // FQName of the enclosing class/object; "" if top-level
	Companion bool   // a companion object

	// Type is the declared type of a property, the return type of a
	// function, or a cheaply inferred type (`val x = Foo()`), as written
	// with generic arguments ("List<Account>"); "" if unknown.
	Type string
	// Supertypes lists a class or object's supertypes as written
	// (e.g. "Shape", "a.b.Base").
	Supertypes []string
	// Receiver is the receiver type of an extension function or property
	// (`fun String.shout()` has Receiver "String"); "" otherwise.
	Receiver string
	// Params lists the parameters of a function or constructor, or the
	// primary constructor parameters of a class.
	Params []Param

	// Signature is the declaration as Kotlin source without its body,
	// e.g. "override fun area(): Double". Doc is its KDoc as markdown.
	Signature string
	Doc       string

	Path           string
	URI            protocol.DocumentURI
	Range          protocol.Range // the whole declaration
	SelectionRange protocol.Range // the name

	// Byte extent of the declaration in the file version it was extracted
	// from; used to match syntax nodes in open files to their symbols.
	StartByte, EndByte uint
}

// Location returns the symbol's name location.
func (s *Symbol) Location() protocol.Location {
	return protocol.Location{URI: s.URI, Range: s.SelectionRange}
}

// A Param is a function or constructor parameter.
type Param struct {
	Name           string
	Type           string
	SelectionRange protocol.Range
}

// An Import is one import directive.
type Import struct {
	Path     string // imported name, e.g. "com.example.Foo" (or package for wildcards)
	Alias    string // `as` alias, or ""
	Wildcard bool   // import com.example.*
}

// Name returns the simple name an import makes visible ("" for wildcards).
func (i Import) Name() string {
	if i.Wildcard {
		return ""
	}
	if i.Alias != "" {
		return i.Alias
	}
	return lastSegment(i.Path)
}

// A FileSummary is everything the index needs to know about a file.
type FileSummary struct {
	Path    string
	URI     protocol.DocumentURI
	Package string
	Imports []Import
	Symbols []*Symbol
}

// Extract returns the package, imports and declarations of a parsed file.
func Extract(path string, src []byte, tree *ts.Tree, m *protocol.Mapper) *FileSummary {
	x := &extractor{
		src: src,
		m:   m,
		sum: &FileSummary{Path: path, URI: protocol.URIFromPath(path)},
	}
	root := tree.RootNode()
	// The package header precedes all declarations; find it first so FQ
	// names are right.
	if h := child(root, "package_header"); h != nil {
		x.sum.Package = dotted(child(h, "identifier"), src)
	}
	x.visit(root, x.sum.Package, "")
	return x.sum
}

type extractor struct {
	src []byte
	m   *protocol.Mapper
	sum *FileSummary
}

// skipKinds are nodes whose contents never hold non-local declarations.
var skipKinds = map[string]bool{
	"function_body":         true,
	"anonymous_initializer": true,
	"getter":                true,
	"setter":                true,
	"lambda_literal":        true,
	"annotation":            true,
	"value_arguments":       true,
	"call_expression":       true,
	"string_literal":        true,
	"line_comment":          true,
	"multiline_comment":     true,
}

// visit extracts declarations under n. qual is the FQ prefix for names
// declared here (the package, or the enclosing container's FQName) and
// container is the enclosing container's FQName ("" at top level).
func (x *extractor) visit(n *ts.Node, qual, container string) {
	switch n.Kind() {
	case "package_header":
		return
	case "import_header":
		x.addImport(n)
		return
	case "class_declaration":
		x.classLike(n, container, qual)
		return
	case "object_declaration", "companion_object":
		x.classLike(n, container, qual)
		return
	case "function_declaration":
		if name := child(n, "simple_identifier"); name != nil {
			s := x.add(n, name, KindFunction, qual, container)
			s.Type = declaredTypeText(n, x.src)
			s.Receiver = receiverType(n, x.src)
			s.Params = x.params(child(n, "function_value_parameters"), "parameter")
		}
		return
	case "property_declaration":
		x.property(n, qual, container)
		return
	case "secondary_constructor":
		if container != "" {
			kw := child(n, "constructor")
			if kw == nil {
				kw = n
			}
			s := x.addNamed(n, kw, lastSegment(container), KindConstructor, qual, container)
			s.Params = x.params(child(n, "function_value_parameters"), "parameter")
		}
		return
	case "type_alias":
		if name := child(n, "type_identifier"); name != nil {
			s := x.add(n, name, KindTypeAlias, qual, container)
			s.Type = declaredType(n, x.src)
		}
		return
	case "enum_entry":
		if name := child(n, "simple_identifier"); name != nil {
			x.add(n, name, KindEnumEntry, qual, container)
		}
		return
	case "infix_expression":
		if x.misparsedObject(n, qual, container) {
			return
		}
	case "ERROR":
		x.visitError(n, qual, container)
		return
	}
	if skipKinds[n.Kind()] {
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		x.visit(n.Child(i), qual, container)
	}
}

// classLike extracts a class, interface, enum, object or companion object
// and its members.
func (x *extractor) classLike(n *ts.Node, container, qual string) {
	kind := KindClass
	switch {
	case n.Kind() == "object_declaration" || n.Kind() == "companion_object":
		kind = KindObject
	case hasToken(n, "interface"):
		kind = KindInterface
	case hasToken(n, "enum"):
		kind = KindEnum
	}

	var s *Symbol
	name := child(n, "type_identifier")
	switch {
	case name != nil:
		s = x.add(n, name, kind, qual, container)
	case n.Kind() == "companion_object":
		s = x.addNamed(n, n, "Companion", kind, qual, container)
	default:
		// Recovery produced a nameless declaration; still look inside.
		for _, c := range children(n) {
			x.visit(c, qual, container)
		}
		return
	}
	s.Companion = n.Kind() == "companion_object"

	for _, d := range childrenOf(n, "delegation_specifier") {
		if st := typeName(child(d, "user_type", "constructor_invocation", "explicit_delegation"), x.src); st != "" {
			s.Supertypes = append(s.Supertypes, st)
		} else if ed := child(d, "explicit_delegation"); ed != nil {
			if st := typeName(child(ed, "user_type"), x.src); st != "" {
				s.Supertypes = append(s.Supertypes, st)
			}
		}
	}
	if pc := child(n, "primary_constructor"); pc != nil {
		s.Params = x.params(pc, "class_parameter")
		for _, p := range childrenOf(pc, "class_parameter") {
			if child(p, "binding_pattern_kind") == nil {
				continue // plain constructor parameter, not a property
			}
			if pname := child(p, "simple_identifier"); pname != nil {
				ps := x.add(p, pname, KindProperty, s.FQName, s.FQName)
				ps.Type = declaredTypeText(p, x.src)
			}
		}
	}
	if body := child(n, "class_body", "enum_class_body"); body != nil {
		for _, c := range children(body) {
			x.visit(c, s.FQName, s.FQName)
		}
	}
}

// misparsedObject handles a known grammar failure: a one-line object
// with a member, `object X { fun f() {} }`, parses silently as
// infix_expression(object_literal, simple_identifier, lambda_literal).
func (x *extractor) misparsedObject(n *ts.Node, qual, container string) bool {
	if n.NamedChildCount() != 3 {
		return false
	}
	obj, name, body := n.NamedChild(0), n.NamedChild(1), n.NamedChild(2)
	if obj.Kind() != "object_literal" || obj.NamedChildCount() != 0 || name.Kind() != "simple_identifier" || body.Kind() != "lambda_literal" {
		return false
	}
	s := x.add(n, name, KindObject, qual, container)
	if stmts := child(body, "statements"); stmts != nil {
		for _, c := range children(stmts) {
			x.visit(c, s.FQName, s.FQName)
		}
	}
	return true
}

// visitError extracts declarations from an ERROR node. When a class body
// fails to parse (typically while the user is typing), recovery flattens
// the class into the ERROR node: the keyword, the name, "{", the members
// as siblings, and maybe "}". Rebuild that structure by tracking braces.
// When a broken member swallows the closing brace, indentation decides:
// a declaration starting at or left of the class keyword's column is not
// one of its members.
func (x *extractor) visitError(n *ts.Node, qual, container string) {
	type frame struct {
		qual, container string
		column          int // column of the declaring keyword; -1 for plain blocks
	}
	stack := []frame{{qual, container, -1}}
	top := func() frame { return stack[len(stack)-1] }
	var pending *Symbol // class-like declared just before an opening brace
	pendingColumn := 0

	kids := children(n)
	for i := 0; i < len(kids); i++ {
		c := kids[i]
		switch c.Kind() {
		case "class", "interface", "object":
			if i+1 < len(kids) && (kids[i+1].Kind() == "simple_identifier" || kids[i+1].Kind() == "type_identifier") {
				kind := map[string]Kind{"class": KindClass, "interface": KindInterface, "object": KindObject}[c.Kind()]
				f := top()
				pending = x.add(n, kids[i+1], kind, f.qual, f.container)
				pending.StartByte = c.StartByte()
				pending.Range, _ = x.m.OffsetRange(int(c.StartByte()), int(n.EndByte()))
				if doc := KDocBefore(x.src, c.StartByte()); doc != "" {
					pending.Doc = RenderKDoc(doc)
				}
				pendingColumn = x.column(c)
				i++
				continue
			}
		case "{":
			if pending != nil {
				stack = append(stack, frame{pending.FQName, pending.FQName, pendingColumn})
			} else {
				f := top() // some other block
				f.column = -1
				stack = append(stack, f)
			}
			pending = nil
			continue
		case "}":
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			pending = nil
			continue
		}
		for len(stack) > 1 && top().column >= 0 && c.IsNamed() && x.column(c) <= top().column {
			stack = stack[:len(stack)-1]
		}
		f := top()
		x.visit(c, f.qual, f.container)
	}
}

// column returns the byte column of n's start within its line.
func (x *extractor) column(n *ts.Node) int {
	start := int(n.StartByte())
	i := start
	for i > 0 && x.src[i-1] != '\n' {
		i--
	}
	return start - i
}

func (x *extractor) property(n *ts.Node, qual, container string) {
	vars := childrenOf(n, "variable_declaration")
	if mv := child(n, "multi_variable_declaration"); mv != nil {
		vars = append(vars, childrenOf(mv, "variable_declaration")...)
	}
	for _, v := range vars {
		name := child(v, "simple_identifier")
		if name == nil {
			continue
		}
		s := x.add(n, name, KindProperty, qual, container)
		s.Receiver = receiverType(n, x.src)
		s.Type = declaredTypeText(v, x.src)
		if s.Type == "" {
			s.Type = inferredType(n, x.src)
		}
	}
}

// params extracts the parameters (children of the given kind) of a
// parameter list node.
func (x *extractor) params(list *ts.Node, kind string) []Param {
	if list == nil {
		return nil
	}
	var out []Param
	for _, p := range childrenOf(list, kind) {
		name := child(p, "simple_identifier")
		if name == nil {
			continue
		}
		rng, _ := x.m.OffsetRange(int(name.StartByte()), int(name.EndByte()))
		out = append(out, Param{Name: text(name, x.src), Type: declaredTypeText(p, x.src), SelectionRange: rng})
	}
	return out
}

// receiverType returns the receiver type of an extension declaration.
func receiverType(decl *ts.Node, src []byte) string {
	if r := child(decl, "receiver_type"); r != nil {
		return declaredType(r, src)
	}
	return ""
}

// inferredType cheaply infers the type of `val x = Foo(...)`: a call of a
// capitalized name is assumed to be a constructor call.
func inferredType(prop *ts.Node, src []byte) string {
	call := child(prop, "call_expression")
	if call == nil {
		return ""
	}
	callee := call.NamedChild(0)
	if callee == nil || callee.Kind() != "simple_identifier" {
		return ""
	}
	name := text(callee, src)
	if r := []rune(name); len(r) > 0 && unicode.IsUpper(r[0]) {
		return name
	}
	return ""
}

func (x *extractor) addImport(n *ts.Node) {
	imp := Import{Path: dotted(child(n, "identifier"), x.src)}
	if imp.Path == "" {
		return
	}
	if a := child(n, "import_alias"); a != nil {
		if id := child(a, "type_identifier", "simple_identifier"); id != nil {
			imp.Alias = text(id, x.src)
		}
	}
	imp.Wildcard = hasToken(n, "wildcard_import")
	x.sum.Imports = append(x.sum.Imports, imp)
}

func (x *extractor) add(decl, name *ts.Node, kind Kind, qual, container string) *Symbol {
	return x.addNamed(decl, name, text(name, x.src), kind, qual, container)
}

func (x *extractor) addNamed(decl, nameNode *ts.Node, name string, kind Kind, qual, container string) *Symbol {
	s := &Symbol{
		Name:      name,
		Kind:      kind,
		FQName:    joinFQ(qual, name),
		Container: container,
		Path:      x.sum.Path,
		URI:       x.sum.URI,
		StartByte: decl.StartByte(),
		EndByte:   decl.EndByte(),
	}
	s.Range, _ = x.m.OffsetRange(int(decl.StartByte()), int(decl.EndByte()))
	s.SelectionRange, _ = x.m.OffsetRange(int(nameNode.StartByte()), int(nameNode.EndByte()))
	if decl.Kind() == "ERROR" || decl.Kind() == "infix_expression" {
		// Recovered declarations: the node spans more than the header.
		s.Signature = kindKeyword(kind) + " " + name
	} else {
		s.Signature = Signature(decl, x.src)
	}
	if doc := KDocBefore(x.src, decl.StartByte()); doc != "" {
		s.Doc = RenderKDoc(doc)
	}
	x.sum.Symbols = append(x.sum.Symbols, s)
	return s
}
