package kotlin

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// Code actions generating declarations: members to implement, missing
// `when` branches, functions called but not declared yet.

const todoCall = `TODO("Not yet implemented")`

// memberInsertion returns where and what to insert to add members (each
// unindented, possibly multi-line) to the class spanning src[start:end].
func memberInsertion(src []byte, start, end int, members []string) (int, string) {
	indent := textutil.LineIndent(src, start)
	inner := indent + indentUnit
	blocks := make([]string, len(members))
	for i, m := range members {
		blocks[i] = inner + textutil.IndentLines(m, inner)
	}
	block := strings.Join(blocks, "\n\n")
	if end == 0 || src[end-1] != '}' {
		return end, " {\n" + block + "\n" + indent + "}" // no body yet
	}
	closeOff := end - 1
	prev := closeOff - 1
	for prev >= 0 && (src[prev] == ' ' || src[prev] == '\t' || src[prev] == '\n' || src[prev] == '\r') {
		prev--
	}
	empty := prev >= 0 && src[prev] == '{'
	if ls := textutil.LineStart(src, closeOff); strings.TrimSpace(string(src[ls:closeOff])) == "" {
		if empty {
			return ls, block + "\n"
		}
		return ls, "\n" + block + "\n"
	}
	return closeOff, "\n" + block + "\n" + indent // `{}` or `{ x }` on one line
}

// classInsertEdits returns the edits adding members to class node cls of
// the current file; an enum with entries gets the `;` its members need.
func (r *resolver) classInsertEdits(cls *ts.Node, members []string) []protocol.TextEdit {
	at, txt := memberInsertion(r.src, int(cls.StartByte()), int(cls.EndByte()), members)
	edits := []protocol.TextEdit{r.editAt(at, at, txt)}
	if body := child(cls, "enum_class_body"); body != nil && !hasToken(body, ";") {
		if entries := childrenOf(body, "enum_entry"); len(entries) > 0 {
			end := int(entries[len(entries)-1].EndByte())
			edits = append(edits, r.editAt(end, end, ";"))
		}
	}
	return edits
}

// --- Implement members ---

// An inherited member, with the context its types are written in.
type inherited struct {
	sym *Symbol
	ctx typeCtx
}

// inheritedMembers returns the functions and properties cls inherits from
// its workspace supertypes (transitively), with type-argument
// substitution: implementing Repo<Account> turns T into Account.
func (r *resolver) inheritedMembers(cls *Symbol) []inherited {
	var out []inherited
	visited := map[string]bool{cls.FQName: true}
	var walk func(s *Symbol, subst map[string]substRef)
	walk = func(s *Symbol, subst map[string]substRef) {
		sum := r.ix.File(s.Path)
		for i, st := range s.Supertypes {
			supers := r.resolveTypeName(st, sum, s.Container)
			if len(supers) == 0 || visited[supers[0].FQName] {
				continue
			}
			sup := supers[0]
			visited[sup.FQName] = true
			full := st
			if i < len(s.SupertypeTexts) {
				full = s.SupertypeTexts[i]
			}
			args := typeArgs(full)
			next := map[string]substRef{}
			for j, tp := range sup.TypeParams {
				if j < len(args) {
					next[tp] = substRef{args[j], typeCtx{sum: sum, container: s.Container, subst: subst}}
				}
			}
			ctx := typeCtx{sum: r.ix.File(sup.Path), container: sup.FQName, subst: next}
			for _, m := range r.ix.Members(sup.FQName) {
				if (m.Kind == KindFunction || m.Kind == KindProperty) && m.Receiver == "" {
					out = append(out, inherited{m, ctx})
				}
			}
			walk(sup, next)
		}
	}
	walk(cls, nil)
	return out
}

// memberKey identifies a member for override matching: a property by
// name, a function by name and parameter types.
func (r *resolver) memberKey(m *Symbol, ctx typeCtx, target *FileSummary, container string) string {
	if m.Kind == KindProperty {
		return "val " + m.Name
	}
	p := r.newPorter(target, container) // imports discarded
	ctx.params = setOf(m.TypeParams)
	var types []string
	for _, prm := range m.Params {
		t, _ := p.render(prm.Type, ctx)
		types = append(types, strings.ReplaceAll(t, " ", ""))
	}
	return "fun " + m.Name + "(" + strings.Join(types, ",") + ")"
}

func setOf(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// missingMembers returns the abstract members cls inherits and neither
// declares nor inherits an implementation of, in declaration order.
func (r *resolver) missingMembers(cls *Symbol) []inherited {
	sum := r.ix.File(cls.Path)
	implemented := map[string]bool{}
	for _, m := range r.ix.Members(cls.FQName) {
		implemented[r.memberKey(m, typeCtx{sum: sum, container: cls.FQName}, sum, cls.FQName)] = true
	}
	all := r.inheritedMembers(cls)
	for _, m := range all {
		if !m.sym.Abstract {
			implemented[r.memberKey(m.sym, m.ctx, sum, cls.FQName)] = true
		}
	}
	var out []inherited
	for _, m := range all {
		key := r.memberKey(m.sym, m.ctx, sum, cls.FQName)
		if m.sym.Abstract && !implemented[key] {
			implemented[key] = true // once per key
			out = append(out, m)
		}
	}
	return out
}

// overrideText renders an override stub for m.
func (r *resolver) overrideText(m inherited, p *porter) (string, bool) {
	s := m.sym
	ctx := m.ctx
	if s.Kind == KindProperty {
		if s.Type == "" {
			return "", false
		}
		t, ok := p.render(s.Type, ctx)
		if !ok {
			return "", false
		}
		kw := "val"
		if s.Mutable {
			kw = "var"
		}
		txt := "override " + kw + " " + s.Name + ": " + t + "\n" + indentUnit + "get() = " + todoCall
		if s.Mutable {
			txt += "\n" + indentUnit + "set(value) {}"
		}
		return txt, true
	}
	ctx.params = setOf(s.TypeParams)
	var b strings.Builder
	b.WriteString("override ")
	if s.Suspend {
		b.WriteString("suspend ")
	}
	b.WriteString("fun ")
	if tps := typeParamClause(s.Signature); tps != "" {
		b.WriteString(tps + " ")
	}
	b.WriteString(s.Name + "(")
	for i, prm := range s.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		t, ok := p.render(prm.Type, ctx)
		if !ok || prm.Type == "" {
			return "", false
		}
		if prm.Vararg {
			b.WriteString("vararg ")
		}
		b.WriteString(prm.Name + ": " + t)
	}
	b.WriteString(")")
	if s.Type != "" && s.Type != "Unit" {
		t, ok := p.render(s.Type, ctx)
		if !ok {
			return "", false
		}
		b.WriteString(": " + t)
	}
	b.WriteString(" {\n" + indentUnit + todoCall + "\n}")
	return b.String(), true
}

// typeParamClause returns the "<T : Bound>" clause of a function
// signature, or "".
func typeParamClause(sig string) string {
	i := strings.Index(sig, "fun <")
	if i < 0 {
		return ""
	}
	depth := 0
	for j := i + 4; j < len(sig); j++ {
		switch sig[j] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return sig[i+4 : j+1]
			}
		}
	}
	return ""
}

// implementMembers offers stubs for the abstract members a class or
// object inherits without implementing. The cursor must be on the
// declaration's header.
func (r *resolver) implementMembers(off int) []Action {
	cls := innermost(r.f.Tree, off, "class_declaration", "object_declaration")
	if cls == nil || hasToken(cls, "interface") {
		return nil
	}
	if body := child(cls, "class_body", "enum_class_body"); body != nil && off > int(body.StartByte()) {
		return nil
	}
	sym := r.symbolForDecl(cls)
	if sym == nil {
		return nil
	}
	missing := r.missingMembers(sym)
	p := r.newPorter(r.f.Summary, sym.FQName)
	var members, names []string
	for _, m := range missing {
		// The implementing class's own type parameters are fine as is.
		if txt, ok := r.overrideText(m, p); ok {
			members = append(members, txt)
			names = append(names, m.sym.Name)
		}
	}
	if len(members) == 0 {
		return nil
	}
	title := "Implement members: " + strings.Join(names[:min(3, len(names))], ", ")
	if len(names) > 3 {
		title += fmt.Sprintf(" (+%d)", len(names)-3)
	}
	a := Action{Title: title, Kind: protocol.QuickFix, Edits: r.classInsertEdits(cls, members)}
	return []Action{r.withImports(a, p.imports)}
}

// --- Add remaining when branches ---

// whenBranches offers the branches missing from a `when` over an enum or
// a sealed type. The cursor must be on the `when` header or a condition.
func (r *resolver) whenBranches(off int) []Action {
	w := innermost(r.f.Tree, off, "when_expression")
	if w == nil {
		return nil
	}
	subj, lb, rb := child(w, "when_subject"), child(w, "{"), child(w, "}")
	if subj == nil || lb == nil || rb == nil {
		return nil
	}
	if off > int(lb.EndByte()) && innermost(r.f.Tree, off, "when_condition") == nil {
		return nil
	}
	var expr *ts.Node
	for _, c := range children(subj) {
		if c.IsNamed() && c.Kind() != "variable_declaration" {
			expr = c
		}
	}
	if expr == nil {
		return nil
	}
	types := r.typesOf(expr, 0)
	if len(types) != 1 {
		return nil
	}
	t := types[0]

	entries := childrenOf(w, "when_entry")
	existing := map[string]bool{}
	bare := false // existing enum conditions written without the type
	for _, e := range entries {
		if hasToken(e, "else") {
			return nil // already exhaustive
		}
		for _, c := range childrenOf(e, "when_condition") {
			ct := c.NamedChild(0)
			switch {
			case ct == nil:
			case ct.Kind() == "type_test":
				existing[lastSegment(typeName(child(ct, "user_type", "nullable_type"), r.src))] = true
			case ct.Kind() == "navigation_expression":
				if sfx := child(ct, "navigation_suffix"); sfx != nil {
					if id := child(sfx, "simple_identifier"); id != nil {
						existing[text(id, r.src)] = true
					}
				}
			case ct.Kind() == "simple_identifier":
				existing[text(ct, r.src)] = true
				bare = true
			}
		}
	}

	p := r.newPorter(r.f.Summary, r.containerAt(w))
	var conds []string
	switch {
	case t.Kind == KindEnum:
		qual, ok := p.symbol(t)
		if !ok {
			return nil
		}
		for _, m := range r.ix.Members(t.FQName) {
			if m.Kind != KindEnumEntry || existing[m.Name] {
				continue
			}
			if bare {
				conds = append(conds, m.Name)
			} else {
				conds = append(conds, qual+"."+m.Name)
			}
		}
	case t.Sealed:
		subs := slices.Clone(r.subtypeMap()[t.FQName])
		slices.SortFunc(subs, func(a, b *Symbol) int {
			return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.StartByte, b.StartByte))
		})
		for _, s := range subs {
			if existing[s.Name] {
				continue
			}
			ref, ok := p.symbol(s)
			if !ok {
				return nil
			}
			if s.Kind == KindObject {
				conds = append(conds, ref)
			} else {
				conds = append(conds, "is "+ref)
			}
		}
	default:
		return nil
	}
	if len(conds) == 0 {
		return nil
	}
	indent := textutil.LineIndent(r.src, int(w.StartByte())) + indentUnit
	if len(entries) > 0 {
		indent = textutil.LineIndent(r.src, int(entries[0].StartByte()))
	}
	lines := make([]string, len(conds))
	for i, c := range conds {
		lines[i] = indent + c + " -> TODO()"
	}
	block := strings.Join(lines, "\n")
	closeOff := int(rb.StartByte())
	var edit protocol.TextEdit
	if ls := textutil.LineStart(r.src, closeOff); strings.TrimSpace(string(r.src[ls:closeOff])) == "" {
		edit = r.editAt(ls, ls, block+"\n")
	} else {
		edit = r.editAt(closeOff, closeOff, "\n"+block+"\n"+textutil.LineIndent(r.src, int(w.StartByte())))
	}
	title := "Add remaining branches"
	if len(conds) == 1 {
		title = "Add missing branch `" + conds[0] + "`"
	}
	return []Action{r.withImports(Action{Title: title, Kind: protocol.QuickFix, Edits: []protocol.TextEdit{edit}}, p.imports)}
}

// --- Create function from usage ---

// createFunction offers to declare a function called but not declared:
// top-level (and, inside a class, as a private member) for an unqualified
// call; as a member of the receiver's class for recv.f(...).
func (r *resolver) createFunction(off int) []Action {
	id := IdentifierAt(r.f.Tree, off)
	if id == nil || id.Parent() == nil {
		return nil
	}
	var call, recv *ts.Node
	switch p := id.Parent(); {
	case p.Kind() == "call_expression" && sameNode(p.NamedChild(0), id):
		call = p
	case p.Kind() == "navigation_suffix":
		nav := p.Parent()
		if nav != nil && nav.Kind() == "navigation_expression" && nav.Parent() != nil &&
			nav.Parent().Kind() == "call_expression" && sameNode(nav.Parent().NamedChild(0), nav) {
			call, recv = nav.Parent(), nav.NamedChild(0)
		}
	}
	if call == nil {
		return nil
	}
	name := text(id, r.src)
	if first, _ := utf8.DecodeRuneInString(name); !unicode.IsLower(first) {
		return nil // Foo(): a constructor call
	}
	sfx := child(call, "call_suffix")
	if sfx == nil || child(sfx, "annotated_lambda") != nil {
		return nil
	}
	args := child(sfx, "value_arguments")

	if recv != nil {
		types := r.typesOf(recv, 0)
		if len(types) != 1 || (types[0].Kind != KindClass && types[0].Kind != KindObject) {
			return nil
		}
		cls := types[0]
		if len(r.membersNamed(cls.FQName, name, false, map[string]bool{})) > 0 || len(r.extensionsOn(types, "", name)) > 0 ||
			implicitMembers[name] || r.hasLibrarySupertype(cls.FQName, map[string]bool{}) || r.mayBeLibraryFunction(call, name) {
			return nil
		}
		return r.createMember(cls, name, call, args)
	}
	if r.visiblyResolved(id, false) || r.mayBeLibraryFunction(call, name) {
		return nil
	}
	var out []Action
	// Top-level, after the declaration containing the call.
	top := call
	for top.Parent() != nil && top.Parent().Kind() != "source_file" {
		top = top.Parent()
	}
	p := r.newPorter(r.f.Summary, "")
	if fn, ok := r.newFunctionText("fun", name, call, args, p); ok {
		at := int(top.EndByte())
		out = append(out, r.withImports(Action{
			Title: "Create function `" + name + "`", Kind: protocol.QuickFix,
			Edits: []protocol.TextEdit{r.editAt(at, at, "\n\n"+fn)},
		}, p.imports))
	}
	// A private member of the enclosing class.
	if cls := innermost(r.f.Tree, int(call.StartByte()), "class_declaration", "object_declaration"); cls != nil && !hasToken(cls, "interface") {
		if sym := r.symbolForDecl(cls); sym != nil {
			p := r.newPorter(r.f.Summary, sym.FQName)
			if fn, ok := r.newFunctionText("private fun", name, call, args, p); ok {
				out = append(out, r.withImports(Action{
					Title: "Create member function `" + sym.Name + "." + name + "`", Kind: protocol.QuickFix,
					Edits: r.classInsertEdits(cls, []string{fn}),
				}, p.imports))
			}
		}
	}
	return out
}

// stdlibFunctions are common top-level functions of the Kotlin standard
// library (default imports), which the workspace index doesn't contain.
var stdlibFunctions = setOf(strings.Fields(`
	listOf listOfNotNull mutableListOf arrayListOf emptyList setOf mutableSetOf hashSetOf
	linkedSetOf sortedSetOf emptySet mapOf mutableMapOf hashMapOf linkedMapOf sortedMapOf emptyMap
	arrayOf arrayOfNulls emptyArray intArrayOf longArrayOf doubleArrayOf floatArrayOf booleanArrayOf
	charArrayOf byteArrayOf shortArrayOf sequenceOf emptySequence generateSequence sequence iterator
	buildList buildSet buildMap buildString pairOf println print readLine readln readlnOrNull
	require requireNotNull check checkNotNull error assert TODO run with apply also let takeIf
	takeUnless repeat lazy lazyOf synchronized maxOf minOf compareBy compareByDescending
	compareValues compareValuesBy naturalOrder reverseOrder nullsFirst nullsLast runCatching
	Result success failure measureTimeMillis measureNanoTime measureTime use to until downTo step
	coroutineScope supervisorScope withContext launch async runBlocking delay flow flowOf emptyFlow
	channelFlow callbackFlow suspendCoroutine suspendCancellableCoroutine CoroutineScope
	assertEquals assertNotEquals assertTrue assertFalse assertNull assertNotNull assertThrows
	assertIs assertContains assertFails assertFailsWith fail
`))

// mayBeLibraryFunction reports whether an unresolved call may be to a
// library function the index can't see: imported, available through a
// wildcard import of a non-workspace package, a standard library
// function, or called inside a lambda with a library-typed receiver.
func (r *resolver) mayBeLibraryFunction(call *ts.Node, name string) bool {
	if stdlibFunctions[name] || r.inLibraryLambda(call) {
		return true
	}
	// Inherited from a library superclass, or a member of a library
	// receiver type (inside `fun String.f()`).
	for p := call.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case "class_declaration", "object_declaration":
			if s := r.symbolForDecl(p); s != nil && r.hasLibrarySupertype(s.FQName, map[string]bool{}) {
				return true
			}
		case "function_declaration":
			if rt := receiverType(p, r.src); rt != "" && len(r.resolveTypeName(rt, r.f.Summary, r.containerAt(p))) == 0 {
				return true
			}
		}
	}
	if r.f.Summary == nil {
		return false
	}
	for _, imp := range r.f.Summary.Imports {
		switch {
		case !imp.Wildcard && imp.Name() == name:
			return true
		case imp.Wildcard && len(r.ix.Package(imp.Path)) == 0 && len(r.ix.Members(imp.Path)) == 0:
			return true // a library package: may declare anything
		}
	}
	return false
}

// implicitMembers are members every class has (Any, data classes, enums)
// or that the standard library adds to every type.
var implicitMembers = setOf(strings.Fields(`
	toString equals hashCode copy component1 component2 component3 component4 component5
	component6 component7 component8 component9 name ordinal compareTo values valueOf entries
	let run apply also takeIf takeUnless to javaClass
`))

// hasLibrarySupertype reports whether the class fq has, transitively, a
// supertype outside the workspace, which may declare any member.
func (r *resolver) hasLibrarySupertype(fq string, visited map[string]bool) bool {
	if visited[fq] {
		return false
	}
	visited[fq] = true
	for _, cls := range r.ix.ByFQName(fq) {
		for _, st := range cls.Supertypes {
			supers := r.resolveTypeName(st, r.ix.File(cls.Path), cls.Container)
			if len(supers) == 0 {
				return true
			}
			if r.hasLibrarySupertype(supers[0].FQName, visited) {
				return true
			}
		}
	}
	return false
}

// createMember declares fn as a member of cls, which may live in another
// file.
func (r *resolver) createMember(cls *Symbol, name string, call, args *ts.Node) []Action {
	var src []byte
	var target *FileSummary
	switch {
	case cls.Path == r.f.Path:
		src, target = r.src, r.f.Summary
	case r.read != nil:
		src, target = r.read(cls.Path), r.ix.File(cls.Path)
	}
	if src == nil || target == nil || int(cls.EndByte) > len(src) {
		return nil
	}
	p := r.newPorter(target, cls.FQName)
	fn, ok := r.newFunctionText("fun", name, call, args, p)
	if !ok {
		return nil
	}
	at, txt := memberInsertion(src, int(cls.StartByte), int(cls.EndByte), []string{fn})
	m := protocol.NewMapper(src, r.f.Mapper.Encoding())
	rng, _ := m.OffsetRange(at, at)
	edits := []protocol.TextEdit{{Range: rng, NewText: txt}}
	a := Action{Title: "Create member function `" + cls.Name + "." + name + "`", Kind: protocol.QuickFix}
	if cls.Path == r.f.Path {
		a.Edits = edits
		return []Action{r.withImports(a, p.imports)}
	}
	tf := &ParsedFile{Path: cls.Path, URI: cls.URI, Content: src, Tree: nil, Mapper: m, Summary: target}
	if e := importsEditNoTree(tf, p.imports); e != nil {
		edits = append(edits, *e)
	}
	a.Other = map[protocol.DocumentURI][]protocol.TextEdit{cls.URI: edits}
	return []Action{a}
}

// newFunctionText renders a stub `<kw> name(params): R { TODO(...) }` for
// call, with parameter names and types taken from the arguments and the
// return type from how the call's value is used.
func (r *resolver) newFunctionText(kw, name string, call, args *ts.Node, p *porter) (string, bool) {
	var params []string
	used := map[string]bool{}
	if args != nil {
		for _, a := range childrenOf(args, "value_argument") {
			if child(a, "spread_expression") != nil {
				return "", false
			}
			value := a.NamedChild(a.NamedChildCount() - 1)
			if value == nil || value.Kind() == "lambda_literal" || value.Kind() == "annotated_lambda" {
				return "", false
			}
			pname := argumentName(a, r.src)
			if pname == "" {
				pname = r.suggestName(value)
			}
			base := pname
			for i := 2; used[pname]; i++ {
				pname = fmt.Sprintf("%s%d", base, i)
			}
			used[pname] = true
			t, ok := r.exprTypeText(value, p)
			if !ok {
				t = "Any?"
			}
			params = append(params, pname+": "+t)
		}
	}
	sig := kw + " " + name + "(" + strings.Join(params, ", ") + ")"
	if ret := r.expectedType(call, p); ret != "" {
		sig += ": " + ret
	}
	return sig + " {\n" + indentUnit + todoCall + "\n}", true
}

// suggestName picks a parameter name for an argument: a variable's or
// property's name, else one derived from its type.
func (r *resolver) suggestName(e *ts.Node) string {
	switch e.Kind() {
	case "simple_identifier":
		return text(e, r.src)
	case "navigation_expression":
		if sfx := child(e, "navigation_suffix"); sfx != nil {
			if id := child(sfx, "simple_identifier"); id != nil {
				return text(id, r.src)
			}
		}
	}
	if t := baseType(r.typeOf(e, 0).text); t != "" {
		n := lastSegment(t)
		if name := strings.ToLower(n[:1]) + n[1:]; identifierRE.MatchString(name) && !slices.Contains(hardKeywords, name) {
			return name
		}
	}
	return "arg"
}

// expectedType returns the type the call's value must have where it is
// used, rendered by p: "" for a statement (Unit), "Boolean" for a
// condition, a declared variable's or parameter's type, or "Any".
func (r *resolver) expectedType(call *ts.Node, p *porter) string {
	parent := call.Parent()
	if parent == nil {
		return "Any"
	}
	here := typeCtx{sum: r.f.Summary, container: r.containerAt(call)}
	render := func(t string, ctx typeCtx) string {
		if s, ok := p.render(t, ctx); ok && t != "" {
			return s
		}
		return "Any"
	}
	switch parent.Kind() {
	case "statements", "control_structure_body":
		if parent.Kind() == "control_structure_body" && parent.Parent() != nil && parent.Parent().Kind() == "when_entry" {
			break // a when branch may be a value
		}
		return ""
	case "if_expression", "while_statement":
		if sameNode(parent.ChildByFieldName("condition"), call) {
			return "Boolean"
		}
	case "property_declaration":
		if vars := childrenOf(parent, "variable_declaration"); len(vars) == 1 {
			if t := declaredTypeText(vars[0], r.src); t != "" {
				return render(t, here)
			}
		}
	case "jump_expression":
		if fn := ancestorOfKind(call, "function_declaration"); fn != nil {
			if t := declaredTypeText(fn, r.src); t != "" {
				return render(t, here)
			}
		}
	case "value_argument":
		outer := ancestorOfKind(parent, "call_expression")
		if outer == nil {
			break
		}
		name, _ := callee(outer)
		if name == nil {
			break
		}
		args, _ := enclosingCallArgs(outer)
		idx := slices.IndexFunc(args, func(a *ts.Node) bool { return sameNode(a, parent) })
		pname := argumentName(parent, r.src)
		for _, fn := range r.callables(r.resolve(name)) {
			for i, prm := range fn.Params {
				if (pname != "" && prm.Name == pname) || (pname == "" && i == idx) {
					return render(prm.Type, typeCtx{sum: r.ix.File(fn.Path), container: fn.Container})
				}
			}
		}
	}
	if fn := ancestorOfKind(call, "function_declaration"); fn != nil && parent.Kind() == "function_body" {
		if t := declaredTypeText(fn, r.src); t != "" {
			return render(t, here)
		}
	}
	return "Any"
}

// enclosingCallArgs returns the arguments of call.
func enclosingCallArgs(call *ts.Node) ([]*ts.Node, bool) {
	sfx := child(call, "call_suffix")
	if sfx == nil {
		return nil, false
	}
	args := child(sfx, "value_arguments")
	if args == nil {
		return nil, false
	}
	return childrenOf(args, "value_argument"), true
}

// importsEditNoTree is importsEdit for a file known only by its summary
// and content (not parsed into a tree): imports go after the last
// import, else after the package line, else at the top.
func importsEditNoTree(f *ParsedFile, fqs []string) *protocol.TextEdit {
	var add []string
	for _, fq := range fqs {
		if !importVisible(f.Summary, fq) && !slices.Contains(add, fq) {
			add = append(add, fq)
		}
	}
	if len(add) == 0 {
		return nil
	}
	slices.Sort(add)
	src := string(f.Content)
	lines := make([]string, len(add))
	for i, fq := range add {
		lines[i] = "import " + fq
	}
	block := strings.Join(lines, "\n")
	at, txt := 0, block+"\n\n"
	if i := strings.LastIndex(src, "\nimport "); i >= 0 {
		at = i + 1 + strings.IndexByte(src[i+1:], '\n')
		txt = "\n" + block
	} else if strings.HasPrefix(src, "package ") || strings.Contains(src, "\npackage ") {
		j := strings.Index(src, "package ")
		at = j + strings.IndexByte(src[j:], '\n')
		txt = "\n\n" + block
	}
	rng, _ := f.Mapper.OffsetRange(at, at)
	return &protocol.TextEdit{Range: rng, NewText: txt}
}
