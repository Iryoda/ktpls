package kotlin

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// Rewrites of the code around the cursor that only need the syntax tree
// and the type inference: body forms, explicit types, braces, string
// templates.

const indentUnit = "    "

func (r *resolver) editAt(start, end int, newText string) protocol.TextEdit {
	rng, _ := r.f.Mapper.OffsetRange(start, end)
	return protocol.TextEdit{Range: rng, NewText: newText}
}

func (r *resolver) withImports(a Action, imports []string) Action {
	if e := importsEdit(r.f, imports); e != nil {
		a.Edits = append(a.Edits, *e)
	}
	return a
}

// --- Expression body / block body ---

// unitFunctions are standard library functions returning Unit.
var unitFunctions = map[string]bool{"println": true, "print": true, "require": true, "check": true, "assert": true}

// returnsUnit reports whether call is known to return Unit.
func (r *resolver) returnsUnit(call *ts.Node) bool {
	name, recv := callee(call)
	if name == nil {
		return false
	}
	if recv == nil && unitFunctions[text(name, r.src)] {
		return true
	}
	targets := r.resolve(name)
	if len(targets) != 1 || targets[0].sym == nil || targets[0].sym.Kind != KindFunction {
		return false
	}
	t := targets[0].sym.Type
	return t == "" || t == "Unit"
}

// convertBody offers to turn a single-expression block body into an
// expression body, and back. The cursor must be on the function's
// header (or its `return`/`=`), not deep in the body.
func (r *resolver) convertBody(off int) []Action {
	fn := innermost(r.f.Tree, off, "function_declaration")
	if fn == nil {
		return nil
	}
	body := child(fn, "function_body")
	if body == nil {
		return nil
	}
	params := child(fn, "function_value_parameters")
	if params == nil {
		return nil
	}
	declared := declaredTypeText(fn, r.src)

	if hasToken(body, "=") { // expression body -> block body
		kids, ok := namedChildren(body)
		if !ok || len(kids) != 1 || off >= int(kids[0].StartByte()) {
			return nil
		}
		expr := kids[0]
		indent := textutil.LineIndent(r.src, int(fn.StartByte()))
		stmt := "return " + textutil.IndentLines(text(expr, r.src), indentUnit)
		var imports []string
		var edits []protocol.TextEdit
		switch {
		case declared == "Unit":
			stmt = textutil.IndentLines(text(expr, r.src), indentUnit)
		case declared != "":
		case expr.Kind() == "call_expression" && r.returnsUnit(expr):
			stmt = textutil.IndentLines(text(expr, r.src), indentUnit)
		default:
			// Without a declared type, a block body would return Unit:
			// the inferred type must be written out.
			p := r.newPorter(r.f.Summary, r.containerAt(fn))
			t, ok := r.exprTypeText(expr, p)
			if !ok {
				return nil
			}
			imports = p.imports
			edits = append(edits, r.editAt(int(params.EndByte()), int(params.EndByte()), ": "+t))
		}
		edits = append(edits, r.editAt(int(body.StartByte()), int(body.EndByte()),
			"{\n"+indent+indentUnit+stmt+"\n"+indent+"}"))
		return []Action{r.withImports(Action{Title: "Convert to block body", Kind: protocol.RefactorRewrite, Edits: edits}, imports)}
	}

	// block body -> expression body
	stmts := child(body, "statements")
	if stmts == nil {
		return nil
	}
	if kids, ok := namedChildren(body); !ok || len(kids) != 1 {
		return nil // comments around the statements
	}
	kids, ok := namedChildren(stmts)
	if !ok || len(kids) != 1 {
		return nil
	}
	st := kids[0]
	var expr *ts.Node
	needUnit := false
	switch {
	case st.Kind() == "jump_expression" && hasToken(st, "return") && !strings.HasPrefix(text(st, r.src), "return@"):
		expr = st.NamedChild(0)
	case st.Kind() == "call_expression" && (declared == "" || declared == "Unit"):
		expr = st
		needUnit = declared == "" && !r.returnsUnit(st)
	}
	if expr == nil || off >= int(expr.StartByte()) {
		return nil
	}
	edits := []protocol.TextEdit{r.editAt(int(body.StartByte()), int(body.EndByte()), "= "+textutil.DedentLines(text(expr, r.src), indentUnit))}
	if needUnit {
		// Keep the function returning Unit whatever the call returns.
		edits = append(edits, r.editAt(int(params.EndByte()), int(params.EndByte()), ": Unit"))
	}
	return []Action{{Title: "Convert to expression body", Kind: protocol.RefactorRewrite, Edits: edits}}
}

// --- Specify type explicitly ---

// nullableResults are standard library calls returning a nullable value.
var nullableResults = map[string]bool{
	"firstOrNull": true, "lastOrNull": true, "singleOrNull": true, "find": true, "findLast": true,
	"getOrNull": true, "elementAtOrNull": true, "randomOrNull": true, "maxByOrNull": true,
	"minByOrNull": true, "maxOrNull": true, "minOrNull": true, "maxOfOrNull": true, "minOfOrNull": true,
	"removeFirstOrNull": true, "removeLastOrNull": true, "firstNotNullOfOrNull": true,
	"toIntOrNull": true, "toLongOrNull": true, "toDoubleOrNull": true, "takeIf": true, "takeUnless": true,
}

// nullable reports whether expression e may be null beyond what its
// inferred type text says (declared types carry their own `?`). ok is
// false if that can't be told.
func (r *resolver) nullable(e *ts.Node, depth int) (nullable, ok bool) {
	if e == nil || depth > maxTypeDepth {
		return false, false
	}
	switch e.Kind() {
	case "postfix_expression":
		if strings.HasSuffix(strings.TrimSpace(text(e, r.src)), "!!") {
			return false, true
		}
		return r.nullable(e.NamedChild(0), depth+1)
	case "parenthesized_expression":
		return r.nullable(e.NamedChild(0), depth+1)
	case "navigation_expression":
		if sfx := child(e, "navigation_suffix"); sfx != nil && strings.HasPrefix(strings.TrimSpace(text(sfx, r.src)), "?.") {
			return true, true
		}
		return r.nullable(e.NamedChild(0), depth+1)
	case "call_expression":
		if name, recv := callee(e); name != nil && recv != nil && nullableResults[text(name, r.src)] {
			return true, true
		}
		return r.nullable(e.NamedChild(0), depth+1)
	case "indexing_expression":
		if mapTypes[lastSegment(baseType(r.typeOf(e.NamedChild(0), depth+1).text))] {
			return true, true // map[key]
		}
		return r.nullable(e.NamedChild(0), depth+1)
	case "simple_identifier":
		for _, t := range r.resolveName(e, text(e, r.src), false) {
			if t.local != nil && declaredTypeText(t.local.decl, r.src) == "" {
				if init := initializer(t.local.decl); init != nil {
					return r.nullable(init, depth+1)
				}
			}
		}
	}
	return false, true
}

// exprTypeText returns the type of expr written for p's target file, or
// false if it isn't known precisely enough to be written down.
func (r *resolver) exprTypeText(expr *ts.Node, p *porter) (string, bool) {
	t := r.typeOf(expr, 0)
	if t.text == "" {
		return "", false
	}
	nullable, ok := r.nullable(expr, 0)
	if !ok {
		return "", false
	}
	s, ok := p.render(t.text, ctxOf(t))
	if !ok {
		return "", false
	}
	if !strings.Contains(s, "<") {
		// A generic class written without its type arguments.
		for _, sym := range r.resolveRef(t) {
			if len(sym.TypeParams) > 0 {
				return "", false
			}
		}
	}
	if nullable && !strings.HasSuffix(s, "?") {
		if strings.Contains(s, "->") {
			s = "(" + s + ")"
		}
		s += "?"
	}
	return s, true
}

// specifyType offers to write out the inferred type of a property or of a
// function with an expression body. The cursor must be before the `=`.
func (r *resolver) specifyType(off int) []Action {
	if prop := innermost(r.f.Tree, off, "property_declaration"); prop != nil {
		vars := childrenOf(prop, "variable_declaration")
		init := propertyInitializer(prop)
		if len(vars) != 1 || declaredTypeText(vars[0], r.src) != "" || init == nil || off >= int(init.StartByte()) {
			return nil
		}
		p := r.newPorter(r.f.Summary, r.containerAt(prop))
		t, ok := r.exprTypeText(init, p)
		if !ok {
			return nil
		}
		at := int(vars[0].EndByte())
		return []Action{r.withImports(Action{Title: "Specify type explicitly", Kind: protocol.RefactorRewrite,
			Edits: []protocol.TextEdit{r.editAt(at, at, ": "+t)}}, p.imports)}
	}
	fn := innermost(r.f.Tree, off, "function_declaration")
	if fn == nil || declaredTypeText(fn, r.src) != "" {
		return nil
	}
	body, params := child(fn, "function_body"), child(fn, "function_value_parameters")
	if body == nil || params == nil || !hasToken(body, "=") {
		return nil
	}
	kids, ok := namedChildren(body)
	if !ok || len(kids) != 1 || off >= int(kids[0].StartByte()) {
		return nil
	}
	p := r.newPorter(r.f.Summary, r.containerAt(fn))
	t, ok := r.exprTypeText(kids[0], p)
	if !ok {
		return nil
	}
	at := int(params.EndByte())
	return []Action{r.withImports(Action{Title: "Specify return type explicitly", Kind: protocol.RefactorRewrite,
		Edits: []protocol.TextEdit{r.editAt(at, at, ": "+t)}}, p.imports)}
}

// --- Braces ---

var braceTitles = map[string]string{
	"if_expression":   "`if` statement",
	"for_statement":   "`for` loop",
	"while_statement": "`while` loop",
	"when_entry":      "`when` branch",
}

// declarationKinds can't be the body of a control structure without
// braces.
var declarationKinds = map[string]bool{
	"property_declaration": true, "function_declaration": true, "class_declaration": true,
	"object_declaration": true, "type_alias": true,
}

// braces offers to add or remove the braces of the bodies of the
// innermost if/for/while/when-branch around the cursor.
func (r *resolver) braces(off int) []Action {
	n := innermost(r.f.Tree, off, "if_expression", "for_statement", "while_statement", "when_entry")
	if n == nil {
		return nil
	}
	bodies := childrenOf(n, "control_structure_body")
	for _, b := range bodies {
		if hasToken(b, "{") && int(b.StartByte()) < off && off < int(b.EndByte()) {
			return nil // inside a braced body: the statement there is what's at hand
		}
	}
	indent := textutil.LineIndent(r.src, int(n.StartByte()))
	hasElse := n.Kind() == "if_expression" && len(bodies) > 1
	var add, remove []protocol.TextEdit
	for i, b := range bodies {
		prev := b.PrevSibling()
		if prev == nil {
			continue
		}
		if !hasToken(b, "{") {
			kids, ok := namedChildren(b)
			if !ok || len(kids) != 1 {
				continue
			}
			if n.Kind() == "if_expression" && i == 1 && kids[0].Kind() == "if_expression" {
				continue // else if
			}
			add = append(add, r.editAt(int(prev.EndByte()), int(b.EndByte()),
				" {\n"+indent+indentUnit+textutil.IndentLines(text(kids[0], r.src), indentUnit)+"\n"+indent+"}"))
			continue
		}
		if hasToken(b, "{") && hasToken(b, "}") {
			if kids, ok := namedChildren(b); !ok || len(kids) != 1 {
				continue
			}
			stmts := child(b, "statements")
			if stmts == nil {
				continue
			}
			kids, ok := namedChildren(stmts)
			if !ok || len(kids) != 1 || declarationKinds[kids[0].Kind()] {
				continue
			}
			if hasElse && i == 0 && (kids[0].Kind() == "if_expression" || kids[0].Kind() == "when_expression") {
				continue // `if (a) if (b) x else y`: the else would move
			}
			remove = append(remove, r.editAt(int(prev.EndByte()), int(b.EndByte()), " "+textutil.DedentLines(text(kids[0], r.src), indentUnit)))
		}
	}
	what := braceTitles[n.Kind()]
	var out []Action
	if len(add) > 0 {
		out = append(out, Action{Title: "Add braces to " + what, Kind: protocol.RefactorRewrite, Edits: add})
	}
	if len(remove) > 0 {
		out = append(out, Action{Title: "Remove braces from " + what, Kind: protocol.RefactorRewrite, Edits: remove})
	}
	return out
}

// --- String template ---

func isRegularString(n *ts.Node, src []byte) bool {
	t := text(n, src)
	return n.Kind() == "string_literal" && strings.HasPrefix(t, `"`) && !strings.HasPrefix(t, `"""`)
}

// stringTemplate offers to turn a string concatenation into a template:
// "a" + b + "c" becomes "a${b}c" (or "a$b" before non-identifier text).
func (r *resolver) stringTemplate(off int) []Action {
	n := innermost(r.f.Tree, off, "additive_expression")
	if n == nil {
		return nil
	}
	for p := n.Parent(); p != nil && p.Kind() == "additive_expression"; p = p.Parent() {
		n = p
	}
	var operands []*ts.Node
	var flatten func(e *ts.Node) bool
	flatten = func(e *ts.Node) bool {
		if e.Kind() != "additive_expression" {
			operands = append(operands, e)
			return true
		}
		kids, ok := namedChildren(e)
		if !ok || len(kids) != 2 || !hasToken(e, "+") {
			return false // a minus, or a comment
		}
		return flatten(kids[0]) && flatten(kids[1])
	}
	if !flatten(n) {
		return nil
	}
	// The first operand decides what + means: it must be a String.
	first := operands[0]
	if !isRegularString(first, r.src) && r.typeOf(first, 0).text != "String" {
		return nil
	}
	literals := 0
	for _, o := range operands {
		t := text(o, r.src)
		if isRegularString(o, r.src) {
			literals++
		} else if strings.HasPrefix(t, `"`) {
			return nil // a raw string
		}
	}
	if literals == 0 {
		return nil
	}
	// startsIdent reports whether operand i renders starting with an
	// identifier character (which would extend a preceding $name).
	startsIdent := func(i int) bool {
		if i >= len(operands) {
			return false
		}
		o := operands[i]
		t := text(o, r.src)
		switch {
		case isRegularString(o, r.src):
			inner := t[1 : len(t)-1]
			return inner != "" && (textutil.IsIdentRune(rune(inner[0])) || inner[0] >= 0x80)
		case o.Kind() == "integer_literal" && textutil.IsDigits(t):
			return true
		}
		return false
	}
	var b strings.Builder
	for i, o := range operands {
		t := text(o, r.src)
		switch {
		case isRegularString(o, r.src):
			inner := t[1 : len(t)-1]
			if i+1 < len(operands) && strings.HasSuffix(inner, "$") && !strings.HasSuffix(inner, `\$`) {
				inner = inner[:len(inner)-1] + `\$` // "a$" + b must not become "a$b"
			}
			b.WriteString(inner)
		case o.Kind() == "integer_literal" && textutil.IsDigits(t):
			b.WriteString(t)
		case o.Kind() == "simple_identifier" && !startsIdent(i+1):
			b.WriteString("$" + t)
		default:
			b.WriteString("${" + t + "}")
		}
	}
	return []Action{{Title: "Convert concatenation to string template", Kind: protocol.RefactorRewrite,
		Edits: []protocol.TextEdit{r.editAt(int(n.StartByte()), int(n.EndByte()), `"`+b.String()+`"`)}}}
}
