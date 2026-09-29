package kotlin

import (
	"cmp"
	"fmt"
	"slices"
	"unicode"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/fuzzy"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// maxCompletions caps the number of items returned; when more match, the
// list is marked incomplete so the client asks again as the user types.
const maxCompletions = 200

// Completion tiers, most relevant first; they prefix sortText.
const (
	tierLocal     = iota // locals and parameters
	tierMember           // members of the receiver or enclosing classes
	tierInherit          // inherited members, extensions
	tierVisible          // same package and imported declarations
	tierKeyword          // Kotlin keywords
	tierWorkspace        // not yet imported: accepting adds the import
)

var keywords = []string{
	"abstract", "annotation", "as", "break", "by", "catch", "class", "companion", "const", "constructor",
	"continue", "crossinline", "data", "do", "else", "enum", "external", "false", "final", "finally",
	"for", "fun", "get", "if", "import", "in", "infix", "init", "inline", "inner", "interface",
	"internal", "is", "lateinit", "noinline", "null", "object", "open", "operator", "out", "override",
	"package", "private", "protected", "public", "reified", "return", "sealed", "set", "super",
	"suspend", "tailrec", "this", "throw", "true", "try", "typealias", "val", "value", "var", "vararg",
	"when", "where", "while",
}

// Complete returns completion candidates at offset in f.
func Complete(f *ParsedFile, ix *Index, offset int) *protocol.CompletionList {
	src := f.Content
	start := identStartBefore(src, offset)
	prefix := string(src[start:offset])
	if prefix != "" {
		if r, _ := utf8.DecodeRuneInString(prefix); r >= '0' && r <= '9' {
			return emptyList() // a number
		}
	}
	if inCommentOrString(f.Tree, offset) {
		return emptyList()
	}
	editRange, _ := f.Mapper.OffsetRange(start, offset)
	c := &completer{
		resolver: resolver{f: f, ix: ix, src: src},
		prefix:   prefix,
		edit:     editRange,
		seen:     map[string]bool{},
	}
	if recv := receiverBefore(f.Tree, src, start); recv != nil {
		c.members(recv)
	} else {
		c.scope(start)
	}
	return c.list()
}

// emptyList returns a list with no items. Items must be [] rather than
// null on the wire: LSP requires an array.
func emptyList() *protocol.CompletionList {
	return &protocol.CompletionList{Items: []protocol.CompletionItem{}}
}

type completer struct {
	resolver
	prefix string
	edit   protocol.Range
	items  []scored
	seen   map[string]bool // dedup key: label + detail
}

type scored struct {
	item  protocol.CompletionItem
	tier  int
	score int
	seq   int // insertion order: for locals, scope nearness
}

// identStartBefore returns the start of the identifier fragment ending at
// offset.
func identStartBefore(src []byte, offset int) int {
	i := offset
	for i > 0 {
		r, size := utf8.DecodeLastRune(src[:i])
		if r != '_' && !isLetterOrDigit(r) {
			break
		}
		i -= size
	}
	return i
}

func isLetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r >= utf8.RuneSelf
}

// inCommentOrString reports whether offset is inside a comment or the
// literal part of a string.
func inCommentOrString(tree *ts.Tree, offset int) bool {
	if offset == 0 {
		return false
	}
	n := tree.RootNode().DescendantForByteRange(uint(offset-1), uint(offset))
	for ; n != nil; n = n.Parent() {
		switch n.Kind() {
		case "line_comment", "multiline_comment", "string_content":
			return true
		case "interpolated_expression", "interpolated_identifier":
			return false
		case "string_literal":
			return int(n.EndByte()) > offset || !closedString(n)
		}
	}
	return false
}

// closedString reports whether a string literal node is terminated.
func closedString(n *ts.Node) bool {
	return n.ChildCount() > 0 && !n.Child(n.ChildCount()-1).IsMissing()
}

// receiverBefore returns the receiver expression of a member access whose
// name starts at start (`recv.na|` or `recv?.na|`), or nil.
func receiverBefore(tree *ts.Tree, src []byte, start int) *ts.Node {
	end := start
	if end == 0 || src[end-1] != '.' {
		return nil
	}
	end--
	if end > 0 && src[end-1] == '?' {
		end--
	}
	if end > 0 && src[end-1] == '.' {
		return nil // a range: 1..
	}
	if end == 0 {
		return nil
	}
	n := tree.RootNode().DescendantForByteRange(uint(end-1), uint(end))
	if n == nil || int(n.EndByte()) != end {
		return nil
	}
	// Climb to the whole receiver expression ending at the dot, through
	// the nodes a receiver can be made of: `a.b`, `f(x)`, `xs.map { }`,
	// `(x)`, `a[i]`, `x!!`, `this`.
	for p := n.Parent(); p != nil && int(p.EndByte()) == end && receiverParts[p.Kind()]; p = p.Parent() {
		n = p
	}
	return exprOrNil(n)
}

var receiverParts = map[string]bool{
	"navigation_expression":    true,
	"navigation_suffix":        true,
	"call_expression":          true,
	"call_suffix":              true,
	"value_arguments":          true,
	"annotated_lambda":         true,
	"lambda_literal":           true,
	"parenthesized_expression": true,
	"indexing_expression":      true,
	"indexing_suffix":          true,
	"postfix_expression":       true,
	"this_expression":          true,
	"super_expression":         true,
	"string_literal":           true,
}

func exprOrNil(n *ts.Node) *ts.Node {
	if n.IsNamed() {
		return n
	}
	if p := n.Parent(); p != nil { // the ")" of a call, "this", ...
		return p
	}
	return nil
}

// members offers the members of recv's type.
func (c *completer) members(recv *ts.Node) {
	if c.denotesType(recv) {
		// Foo.| offers what is reachable through the type name.
		for _, t := range c.typesOf(recv, 0) {
			for _, m := range c.ix.Members(t.FQName) {
				switch {
				case m.Kind == KindEnumEntry || m.Kind.IsType() && !m.Companion:
					c.addSymbol(m, tierMember, "")
				case m.Companion:
					for _, cm := range c.ix.Members(m.FQName) {
						c.addSymbol(cm, tierMember, "")
					}
				}
			}
		}
		return
	}
	types := c.typesOf(recv, 0)
	for _, t := range types {
		c.instanceMembers(t.FQName, tierMember, map[string]bool{})
	}
	accept := map[string]bool{}
	for _, t := range types {
		c.collectSupertypes(t.FQName, accept)
	}
	var libType string
	if len(types) == 0 {
		libType = lastSegment(c.knownTypeName(recv, 0))
		if libType == "" {
			return // unknown receiver: offer nothing rather than guess
		}
	}
	for ext := range c.ix.Extensions() {
		if libType != "" {
			if lastSegment(ext.Receiver) == libType {
				c.addSymbol(ext, tierInherit, "")
			}
			continue
		}
		for _, rt := range c.resolveTypeName(ext.Receiver, c.ix.File(ext.Path), ext.Container) {
			if accept[rt.FQName] {
				c.addSymbol(ext, tierInherit, "")
				break
			}
		}
	}
}

// denotesType reports whether expr names a class, interface, enum or type
// alias (rather than a value), as in `Color.RED` or `Foo.create()`.
// Objects are values: their members are offered as instance members.
func (c *completer) denotesType(expr *ts.Node) bool {
	var targets []target
	switch expr.Kind() {
	case "simple_identifier":
		targets = c.resolveName(expr, text(expr, c.src), false)
	case "navigation_expression":
		if sfx := child(expr, "navigation_suffix"); sfx != nil {
			if id := child(sfx, "simple_identifier"); id != nil {
				targets = c.resolveMember(sfx, text(id, c.src))
			}
		}
	}
	for _, t := range targets {
		if t.sym != nil && t.sym.Kind.IsType() && t.sym.Kind != KindObject {
			return true
		}
	}
	return false
}

// instanceMembers offers the members of container and, one tier lower,
// those it inherits from workspace supertypes.
func (c *completer) instanceMembers(container string, tier int, visited map[string]bool) {
	if visited[container] {
		return
	}
	visited[container] = true
	for _, m := range c.ix.Members(container) {
		if m.Kind != KindConstructor && !m.Companion && m.Kind != KindEnumEntry {
			c.addSymbol(m, tier, "")
		}
	}
	for _, st := range c.supertypes(container) {
		c.instanceMembers(st.FQName, tierInherit, visited)
	}
}

// scope offers everything visible without a receiver at offset.
func (c *completer) scope(offset int) {
	anchor := c.f.Tree.RootNode().NamedDescendantForByteRange(uint(offset), uint(offset))
	typesOnly := typePosition(c.src, offset-len(c.prefix))

	if !typesOnly {
		c.namedArguments(offset)
	}
	if anchor != nil && !typesOnly {
		visibleLocals(anchor, uint(offset), c.src, func(l local) bool {
			c.addLocal(l)
			return true
		})
	}
	if anchor != nil {
		for _, container := range c.enclosingContainers(anchor) {
			c.instanceMembers(container, tierMember, map[string]bool{})
			for _, m := range c.ix.Members(container) {
				if m.Companion {
					c.instanceMembers(m.FQName, tierMember, map[string]bool{})
				}
				if m.Kind.IsType() || m.Kind == KindEnumEntry {
					c.addSymbol(m, tierMember, "") // nested types
				}
			}
		}
	}

	// Same package and imports.
	sum := c.f.Summary
	visible := map[string]bool{}
	if sum != nil {
		for _, s := range c.ix.Package(sum.Package) {
			c.addVisible(s, typesOnly, "", visible)
		}
		for _, imp := range sum.Imports {
			if imp.Wildcard {
				for _, s := range c.ix.Package(imp.Path) {
					c.addVisible(s, typesOnly, "", visible)
				}
				for _, s := range c.ix.Members(imp.Path) {
					c.addVisible(s, typesOnly, "", visible)
				}
				continue
			}
			for _, s := range c.ix.ByFQName(imp.Path) {
				c.addVisible(s, typesOnly, imp.Alias, visible)
			}
		}
	}

	if c.prefix == "" {
		return // keywords and the whole workspace only once something is typed
	}
	if !typesOnly {
		for _, kw := range keywords {
			if !sameFirstRune(c.prefix, kw) {
				continue
			}
			if score, ok := fuzzy.Score(c.prefix, kw); ok {
				c.add(protocol.CompletionItem{Label: kw, Kind: protocol.CompletionKindKeyword}, tierKeyword, score)
			}
		}
	}
	c.unimported(sum, typesOnly, visible)
}

// namedArguments offers `name =` for the parameters of the call whose
// argument starts at offset: `f(a = 1, na|`. The call is found in the text
// (the innermost unclosed parenthesis), since the tree is usually broken
// while an argument list is being typed.
func (c *completer) namedArguments(offset int) {
	open := argumentListStart(c.src, offset)
	if open < 0 {
		return
	}
	id := IdentifierAt(c.f.Tree, open) // the callee name ends at the paren
	if id == nil || int(id.EndByte()) != open {
		return
	}
	for _, fn := range c.callables(c.resolve(id)) {
		for _, p := range fn.Params {
			score, ok := fuzzy.Score(c.prefix, p.Name)
			if !ok {
				continue
			}
			detail := p.Name
			if p.Type != "" {
				detail += ": " + p.Type
			}
			c.add(protocol.CompletionItem{
				Label:        p.Name + " =",
				Kind:         protocol.CompletionKindVariable,
				Detail:       detail,
				LabelDetails: &protocol.CompletionItemLabelDetails{Description: "parameter of " + fn.Name},
				TextEdit:     &protocol.TextEdit{Range: c.edit, NewText: p.Name + " = "},
			}, tierLocal, score)
		}
	}
}

// argumentListStart returns the offset of the "(" opening the argument
// list in which an argument starts at offset (just after "(" or ","), or
// -1.
func argumentListStart(src []byte, offset int) int {
	i := offset
	for i > 0 && (src[i-1] == ' ' || src[i-1] == '\t' || src[i-1] == '\n' || src[i-1] == '\r') {
		i--
	}
	if i == 0 || (src[i-1] != '(' && src[i-1] != ',') {
		return -1
	}
	depth := 0
	for j := i - 1; j >= 0; j-- {
		switch src[j] {
		case ')', ']', '}':
			depth++
		case '(':
			if depth == 0 {
				return j
			}
			depth--
		case '[', '{':
			if depth == 0 {
				return -1 // inside a lambda or index, not an argument list
			}
			depth--
		case '"':
			// Skip back over a string literal ("..."; escapes count).
			j--
			for j >= 0 && (src[j] != '"' || (j > 0 && src[j-1] == '\\')) {
				j--
			}
			if j < 0 {
				return -1
			}
		}
	}
	return -1
}

func (c *completer) addVisible(s *Symbol, typesOnly bool, alias string, visible map[string]bool) {
	if s.Receiver != "" || (typesOnly && !s.Kind.IsType()) {
		return
	}
	visible[s.FQName] = true
	c.addSymbol(s, tierVisible, alias)
}

// unimported offers top-level declarations of other packages, with an
// edit adding the import.
func (c *completer) unimported(sum *FileSummary, typesOnly bool, visible map[string]bool) {
	for name := range c.ix.Names() {
		if !sameFirstRune(c.prefix, name) {
			continue
		}
		score, ok := fuzzy.Score(c.prefix, name)
		if !ok {
			continue
		}
		for _, s := range c.ix.ByName(name) {
			if s.Container != "" || s.Receiver != "" || visible[s.FQName] || (typesOnly && !s.Kind.IsType()) {
				continue
			}
			if sum != nil && packageOf(c.ix, s) == sum.Package {
				continue
			}
			item := c.symbolItem(s, "")
			item.AdditionalTextEdits = []protocol.TextEdit{c.importEdit(s.FQName)}
			item.LabelDetails = &protocol.CompletionItemLabelDetails{Description: "import " + s.FQName}
			c.add(item, tierWorkspace, score)
		}
	}
}

// sameFirstRune reports whether a and b start with the same letter,
// ignoring case. The unimported and keyword tiers require it: matching
// anywhere would flood the list with scattered subsequence matches.
func sameFirstRune(a, b string) bool {
	ra, _ := utf8.DecodeRuneInString(a)
	rb, _ := utf8.DecodeRuneInString(b)
	return unicode.ToLower(ra) == unicode.ToLower(rb)
}

// importEdit returns an edit adding `import fq` to the file.
func (c *completer) importEdit(fq string) protocol.TextEdit { return importEdit(c.f, fq) }

// typePosition reports whether the identifier starting at start follows a
// colon (a type annotation or supertype list), where only types fit.
func typePosition(src []byte, start int) bool {
	i := start
	for i > 0 && (src[i-1] == ' ' || src[i-1] == '\t') {
		i--
	}
	return i > 0 && src[i-1] == ':' && (i < 2 || src[i-2] != ':')
}

func (c *completer) addLocal(l local) {
	name := text(l.name, c.src)
	score, ok := fuzzy.Score(c.prefix, name)
	if !ok {
		return
	}
	kind := protocol.CompletionKindVariable
	switch l.decl.Kind() {
	case "type_parameter":
		kind = protocol.CompletionKindTypeParameter
	case "function_declaration":
		kind = protocol.CompletionKindFunction
	case "class_declaration", "object_declaration":
		kind = protocol.CompletionKindClass
	}
	c.add(protocol.CompletionItem{
		Label:    name,
		Kind:     kind,
		Detail:   Signature(l.decl, c.src),
		TextEdit: &protocol.TextEdit{Range: c.edit, NewText: name},
	}, tierLocal, score)
}

func (c *completer) addSymbol(s *Symbol, tier int, alias string) {
	label := s.Name
	if alias != "" {
		label = alias
	}
	score, ok := fuzzy.Score(c.prefix, label)
	if !ok {
		return
	}
	c.add(c.symbolItem(s, alias), tier, score)
}

func (c *completer) symbolItem(s *Symbol, alias string) protocol.CompletionItem {
	label := s.Name
	if alias != "" {
		label = alias
	}
	item := protocol.CompletionItem{
		Label:    label,
		Kind:     completionKind(s),
		Detail:   s.Signature,
		TextEdit: &protocol.TextEdit{Range: c.edit, NewText: label},
	}
	if s.Doc != "" {
		item.Documentation = &protocol.MarkupContent{Kind: protocol.Markdown, Value: s.Doc}
	}
	switch {
	case s.Receiver != "":
		item.LabelDetails = &protocol.CompletionItemLabelDetails{Description: "ext " + s.Receiver}
	case s.Container != "":
		item.LabelDetails = &protocol.CompletionItemLabelDetails{Description: lastSegment(s.Container)}
	}
	return item
}

func completionKind(s *Symbol) protocol.CompletionItemKind {
	switch s.Kind {
	case KindClass, KindTypeAlias:
		return protocol.CompletionKindClass
	case KindInterface:
		return protocol.CompletionKindInterface
	case KindEnum:
		return protocol.CompletionKindEnum
	case KindObject:
		return protocol.CompletionKindModule
	case KindEnumEntry:
		return protocol.CompletionKindEnumMember
	case KindConstructor:
		return protocol.CompletionKindConstructor
	case KindFunction:
		if s.Container != "" {
			return protocol.CompletionKindMethod
		}
		return protocol.CompletionKindFunction
	case KindProperty:
		if s.Container != "" {
			return protocol.CompletionKindField
		}
		return protocol.CompletionKindProperty
	}
	return protocol.CompletionKindText
}

func (c *completer) add(item protocol.CompletionItem, tier, score int) {
	key := item.Label + "\x00" + item.Detail
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	item.FilterText = item.Label
	c.items = append(c.items, scored{item, tier, score, len(c.items)})
}

// list ranks the collected items: by tier, then match quality, then name.
func (c *completer) list() *protocol.CompletionList {
	slices.SortStableFunc(c.items, func(a, b scored) int {
		if a.tier != b.tier {
			return cmp.Compare(a.tier, b.tier)
		}
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		if a.tier == tierLocal {
			return cmp.Compare(a.seq, b.seq) // nearest scope first
		}
		return cmp.Compare(a.item.Label, b.item.Label)
	})
	list := emptyList()
	if len(c.items) > maxCompletions {
		c.items = c.items[:maxCompletions]
		list.IsIncomplete = true
	}
	for i, s := range c.items {
		s.item.SortText = fmt.Sprintf("%04d", i)
		list.Items = append(list.Items, s.item)
	}
	// While typing, the client re-filters; ask again when the workspace
	// tier is involved, since it depends on the prefix.
	if c.prefix != "" {
		list.IsIncomplete = true
	}
	return list
}
