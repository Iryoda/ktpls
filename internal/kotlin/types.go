package kotlin

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// This file infers the types of expressions that the declaration-based
// resolver can't: implicit lambda parameters (`it`), untyped lambda
// parameters, collection elements, and results of common standard library
// calls. Types are handled as text ("List<Account>") together with the
// file and container they were written in, since a type name means what
// the declaring file's imports say, and are resolved to symbols at the end.

// A typeRef is a type as written somewhere, e.g. "List<Account>" in file sum.
type typeRef struct {
	text      string
	sum       *FileSummary
	container string
}

func (t typeRef) arg(i int) typeRef {
	args := typeArgs(t.text)
	if i >= len(args) {
		return typeRef{}
	}
	return typeRef{args[i], t.sum, t.container}
}

// Collection-like types, whose first type argument is the element type.
var collectionTypes = map[string]bool{
	"Iterable": true, "Collection": true, "MutableCollection": true,
	"List": true, "MutableList": true, "ArrayList": true,
	"Set": true, "MutableSet": true, "HashSet": true, "LinkedHashSet": true, "SortedSet": true, "TreeSet": true,
	"Sequence": true, "Array": true, "Flow": true, "StateFlow": true, "SharedFlow": true, "Stream": true,
}

// Map-like types: Map<K, V>.
var mapTypes = map[string]bool{
	"Map": true, "MutableMap": true, "HashMap": true, "LinkedHashMap": true, "SortedMap": true, "TreeMap": true,
}

// elementLambdas are the standard library functions on collections whose
// lambda receives an element, mapped to the element parameter's index
// (the *Indexed variants pass the index first).
var elementLambdas = map[string]int{
	"forEach": 0, "onEach": 0, "map": 0, "mapNotNull": 0, "flatMap": 0, "filter": 0, "filterNot": 0,
	"any": 0, "all": 0, "none": 0, "count": 0, "first": 0, "firstOrNull": 0, "last": 0, "lastOrNull": 0,
	"find": 0, "findLast": 0, "single": 0, "singleOrNull": 0, "sumOf": 0, "sortedBy": 0,
	"sortedByDescending": 0, "groupBy": 0, "associateBy": 0, "associateWith": 0, "associate": 0,
	"partition": 0, "maxBy": 0, "minBy": 0, "maxByOrNull": 0, "minByOrNull": 0, "maxOf": 0, "minOf": 0,
	"distinctBy": 0, "takeWhile": 0, "dropWhile": 0, "indexOfFirst": 0, "indexOfLast": 0,
	"collect": 0, "mapTo": 0, "filterTo": 0,
	"forEachIndexed": 1, "mapIndexed": 1, "filterIndexed": 1, "mapIndexedNotNull": 1, "onEachIndexed": 1,
}

// itIsReceiver are the scope functions whose lambda receives the receiver
// as `it`.
var itIsReceiver = map[string]bool{"let": true, "also": true, "takeIf": true, "takeUnless": true}

// elementResults are collection functions returning one element.
var elementResults = map[string]bool{
	"first": true, "firstOrNull": true, "last": true, "lastOrNull": true, "single": true,
	"singleOrNull": true, "find": true, "findLast": true, "get": true, "getOrNull": true,
	"elementAt": true, "elementAtOrNull": true, "random": true, "randomOrNull": true,
	"maxBy": true, "minBy": true, "maxByOrNull": true, "minByOrNull": true, "removeAt": true,
	"removeFirst": true, "removeLast": true,
}

// sameCollection are collection functions returning a collection of the
// same element type.
var sameCollection = map[string]bool{
	"filter": true, "filterNot": true, "filterNotNull": true, "sorted": true, "sortedBy": true,
	"sortedByDescending": true, "sortedDescending": true, "sortedWith": true, "distinct": true,
	"distinctBy": true, "reversed": true, "take": true, "takeLast": true, "drop": true, "dropLast": true,
	"takeWhile": true, "dropWhile": true, "toList": true, "toMutableList": true, "toSet": true,
	"toMutableSet": true, "asSequence": true, "asIterable": true, "onEach": true, "shuffled": true,
	"plus": true, "minus": true, "orEmpty": true,
}

// typeOf infers the type of expression expr, as written somewhere.
func (r *resolver) typeOf(expr *ts.Node, depth int) typeRef {
	if expr == nil || depth > maxTypeDepth {
		return typeRef{}
	}
	if t, ok := r.typeMemo[expr.Id()]; ok {
		return t
	}
	if r.typeMemo == nil {
		r.typeMemo = map[uintptr]typeRef{}
	}
	r.typeMemo[expr.Id()] = typeRef{}
	t := r.typeOfUncached(expr, depth)
	r.typeMemo[expr.Id()] = t
	return t
}

func (r *resolver) typeOfUncached(expr *ts.Node, depth int) typeRef {
	none := typeRef{}
	here := func(text string) typeRef { return typeRef{text, r.f.Summary, r.containerAt(expr)} }
	switch expr.Kind() {
	case "string_literal", "multiline_string_literal":
		return here("String")
	case "integer_literal":
		return here("Int")
	case "long_literal":
		return here("Long")
	case "real_literal":
		return here("Double")
	case "boolean_literal":
		return here("Boolean")
	case "parenthesized_expression":
		return r.typeOf(expr.NamedChild(0), depth+1)
	case "postfix_expression": // x!!
		return r.typeOf(expr.NamedChild(0), depth+1)
	case "this_expression":
		if fn := enclosingExtension(expr); fn != nil {
			return typeRef{declaredTypeText(child(fn, "receiver_type"), r.src), r.f.Summary, r.containerAt(fn)}
		}
		if cs := r.enclosingContainers(expr); len(cs) > 0 {
			return typeRef{cs[0], nil, ""}
		}
	case "indexing_expression":
		t := r.typeOf(expr.NamedChild(0), depth+1)
		if mapTypes[lastSegment(baseType(t.text))] {
			return t.arg(1)
		}
		return r.elementOf(t)
	case "simple_identifier":
		return r.typeOfName(expr, depth)
	case "navigation_expression":
		if sfx := child(expr, "navigation_suffix"); sfx != nil {
			if id := child(sfx, "simple_identifier"); id != nil {
				return symbolsType(r.resolveMember(sfx, text(id, r.src)), r.ix)
			}
		}
	case "call_expression":
		return r.typeOfCall(expr, depth)
	}
	return none
}

// typeOfName infers the type of a name: a declaration's type, or an
// implicit or untyped lambda parameter's.
func (r *resolver) typeOfName(id *ts.Node, depth int) typeRef {
	name := text(id, r.src)
	targets := r.resolveName(id, name, false)
	if len(targets) == 0 && name == "it" {
		if lambda := enclosingItLambda(id); lambda != nil {
			return r.lambdaParamType(lambda, 0, depth+1)
		}
	}
	if len(targets) != 1 {
		return symbolsType(targets, r.ix)
	}
	t := targets[0]
	switch {
	case t.sym != nil:
		return symbolsType(targets, r.ix)
	case t.param != nil:
		return typeRef{t.paramInfo.Type, r.ix.File(t.paramOwner.Path), t.paramOwner.Container}
	}
	return r.typeOfLocal(t.local.decl, depth)
}

// typeOfLocal infers the type of a local declaration: its annotation, its
// lambda's parameter type, its initializer, or its loop's element type.
func (r *resolver) typeOfLocal(decl *ts.Node, depth int) typeRef {
	if tt := declaredTypeText(decl, r.src); tt != "" {
		return typeRef{tt, r.f.Summary, r.containerAt(decl)}
	}
	if lambda, i := lambdaParameterIndex(decl); lambda != nil {
		return r.lambdaParamType(lambda, i, depth+1)
	}
	if init := initializer(decl); init != nil {
		return r.typeOf(init, depth+1)
	}
	if decl.Kind() == "variable_declaration" && decl.Parent() != nil && decl.Parent().Kind() == "for_statement" {
		// for (x in xs): the element type of xs.
		for _, c := range children(decl.Parent()) {
			if c.StartByte() > decl.EndByte() && c.IsNamed() && c.Kind() != "control_structure_body" {
				return r.elementOf(r.typeOf(c, depth+1))
			}
		}
	}
	return typeRef{}
}

// symbolsType returns the type of the values the targets denote, when
// they agree on one.
func symbolsType(targets []target, ix *Index) typeRef {
	for _, t := range targets {
		s := t.sym
		if s == nil {
			continue
		}
		switch {
		case s.Kind.IsType():
			return typeRef{s.FQName, nil, ""}
		case s.Kind == KindEnumEntry || s.Kind == KindConstructor:
			return typeRef{s.Container, nil, ""}
		case s.Type != "":
			return typeRef{s.Type, ix.File(s.Path), s.Container}
		}
	}
	return typeRef{}
}

// typeOfCall infers the result type of a call.
func (r *resolver) typeOfCall(call *ts.Node, depth int) typeRef {
	name, recv := callee(call)
	if name == nil {
		return typeRef{}
	}
	fn := text(name, r.src)
	if recv != nil {
		switch {
		case returnsReceiver[fn]:
			return r.typeOf(recv, depth+1)
		case elementResults[fn]:
			if t := r.typeOf(recv, depth+1); t.text != "" {
				if mapTypes[lastSegment(baseType(t.text))] && (fn == "get" || fn == "getOrNull") {
					return t.arg(1)
				}
				if e := r.elementOf(t); e.text != "" {
					return e
				}
			}
		case sameCollection[fn]:
			if t := r.typeOf(recv, depth+1); collectionTypes[lastSegment(baseType(t.text))] {
				return t
			}
		}
	}
	return symbolsType(r.resolve(name), r.ix)
}

// elementOf returns the element type of a collection type.
func (r *resolver) elementOf(t typeRef) typeRef {
	if collectionTypes[lastSegment(baseType(t.text))] {
		return t.arg(0)
	}
	return typeRef{}
}

// lambdaParamType infers the type of parameter i of a lambda from the
// call receiving it.
func (r *resolver) lambdaParamType(lambda *ts.Node, i, depth int) typeRef {
	call := lambdaOwner(lambda)
	if call == nil || depth > maxTypeDepth {
		return typeRef{}
	}
	name, recv := callee(call)
	if name == nil {
		return typeRef{}
	}
	fn := text(name, r.src)
	if recv != nil {
		if itIsReceiver[fn] && i == 0 {
			return r.typeOf(recv, depth+1)
		}
		if idx, ok := elementLambdas[fn]; ok {
			if i == idx {
				return r.elementOf(r.typeOf(recv, depth+1))
			}
			if idx == 1 && i == 0 {
				return typeRef{"Int", r.f.Summary, ""}
			}
		}
	}
	// A workspace function with a function-typed parameter:
	// fun each(block: (Account) -> Unit).
	for _, f := range r.callables(r.resolve(name)) {
		for j := len(f.Params) - 1; j >= 0; j-- {
			if params, ok := functionTypeParams(f.Params[j].Type); ok {
				if i < len(params) {
					return typeRef{params[i], r.ix.File(f.Path), f.Container}
				}
				break
			}
		}
	}
	return typeRef{}
}

// enclosingItLambda returns the innermost enclosing lambda without
// declared parameters, the one `it` refers to.
func enclosingItLambda(n *ts.Node) *ts.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "lambda_literal" && child(p, "lambda_parameters") == nil {
			return p
		}
	}
	return nil
}

// lambdaParameterIndex returns the lambda declaring decl as a parameter,
// and its position, or nil.
func lambdaParameterIndex(decl *ts.Node) (*ts.Node, int) {
	params := decl.Parent()
	if params == nil || params.Kind() != "lambda_parameters" {
		return nil, 0
	}
	for i, v := range childrenOf(params, "variable_declaration") {
		if v.Equals(*decl) {
			return params.Parent(), i
		}
	}
	return nil, 0
}

// baseType returns the name of a type without generic arguments or
// nullability: "a.b.List" for "a.b.List<Foo>?".
func baseType(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimSuffix(t, "?")
	if i := strings.IndexByte(t, '<'); i >= 0 {
		t = t[:i]
	}
	if strings.ContainsAny(t, "()") {
		return "" // a function type
	}
	return strings.TrimSpace(t)
}

// typeArgs returns the top-level generic arguments of a type:
// ["String", "List<Int>"] for "Map<String, List<Int>>". Variance
// modifiers are dropped.
func typeArgs(t string) []string {
	open := strings.IndexByte(t, '<')
	if open < 0 {
		return nil
	}
	var args []string
	depth, start := 0, open+1
	for i := open; i < len(t); i++ {
		switch t[i] {
		case '<', '(':
			depth++
		case '>', ')':
			if t[i] == '>' && i > 0 && t[i-1] == '-' {
				continue // the arrow of a function type
			}
			depth--
			if depth == 0 {
				args = append(args, cleanArg(t[start:i]))
				return args
			}
		case ',':
			if depth == 1 {
				args = append(args, cleanArg(t[start:i]))
				start = i + 1
			}
		}
	}
	return args
}

func cleanArg(a string) string {
	a = strings.TrimSpace(a)
	for _, v := range []string{"out ", "in "} {
		a = strings.TrimPrefix(a, v)
	}
	return a
}

// functionTypeParams returns the parameter types of a function type
// "(A, B) -> R". Function types with a receiver ("A.() -> R") report
// false: their lambdas have no `it`.
func functionTypeParams(t string) ([]string, bool) {
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "?"))
	t = strings.TrimPrefix(t, "suspend ")
	if strings.HasPrefix(t, "(") && matchingParen(t, 0) == len(t)-1 {
		t = strings.TrimSpace(t[1 : len(t)-1]) // parenthesized: ((A) -> B)?
	}
	if !strings.HasPrefix(t, "(") {
		return nil, false
	}
	depth := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '(', '<':
			depth++
		case ')', '>':
			depth--
			if depth == 0 && t[i] == ')' {
				if !strings.HasPrefix(strings.TrimSpace(t[i+1:]), "->") {
					return nil, false
				}
				inner := t[1:i]
				if strings.TrimSpace(inner) == "" {
					return nil, true
				}
				var params []string
				d, start := 0, 0
				for j := 0; j < len(inner); j++ {
					switch inner[j] {
					case '(', '<':
						d++
					case ')', '>':
						d--
					case ',':
						if d == 0 {
							params = append(params, paramType(inner[start:j]))
							start = j + 1
						}
					}
				}
				return append(params, paramType(inner[start:])), true
			}
		}
	}
	return nil, false
}

// matchingParen returns the index of the parenthesis closing the one at
// open, or -1.
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// paramType strips an optional name from a function type parameter:
// "(account: Account) -> Unit" names its parameter.
func paramType(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, ':'); i >= 0 && !strings.ContainsAny(p[:i], "<(") {
		return strings.TrimSpace(p[i+1:])
	}
	return p
}
