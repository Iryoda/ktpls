package kotlin

import (
	ts "github.com/tree-sitter/go-tree-sitter"
)

// A local is a declaration found in the syntax tree of the current file
// rather than in the index: a parameter, local variable, lambda parameter,
// loop variable, or type parameter.
type local struct {
	name *ts.Node // the declaring identifier
	decl *ts.Node // the declaring node (parameter, variable_declaration, ...)
}

// findLocal searches the lexical scopes enclosing use, innermost first, for
// a declaration of name.
func findLocal(use *ts.Node, name string, src []byte) *local {
	var found *local
	visibleLocals(use, use.StartByte(), func(l local) bool {
		if text(l.name, src) == name {
			found = &l
			return false
		}
		return true
	})
	return found
}

// visibleLocals calls yield for each local declaration visible at offset
// pos, which lies in node n (n itself may be a scope), innermost
// (shadowing) first, until yield returns false.
func visibleLocals(n *ts.Node, pos uint, yield func(local) bool) {
	for scope := n; scope != nil; scope = scope.Parent() {
		if !scopeLocals(scope, pos, yield) {
			return
		}
	}
}

// contains reports whether offset pos lies within n.
func contains(n *ts.Node, pos uint) bool { return n.StartByte() <= pos && pos < n.EndByte() }

// scopeLocals yields the declarations that node scope introduces and that
// are visible at pos, nearest first. It returns false if yield stopped the
// iteration.
func scopeLocals(scope *ts.Node, pos uint, yield func(local) bool) bool {
	emit := func(id, decl *ts.Node) bool {
		if id == nil {
			return true
		}
		return yield(local{name: id, decl: decl})
	}
	switch scope.Kind() {
	case "function_declaration", "secondary_constructor", "anonymous_function":
		if params := child(scope, "function_value_parameters"); params != nil {
			for _, p := range childrenOf(params, "parameter") {
				if !emit(child(p, "simple_identifier"), p) {
					return false
				}
			}
		}
		return typeParameters(scope, yield)

	case "setter":
		// set(value) { ... }
		if p := child(scope, "parameter_with_optional_type"); p != nil {
			return emit(child(p, "simple_identifier"), p)
		}

	case "lambda_literal":
		if params := child(scope, "lambda_parameters"); params != nil {
			return variables(params, yield)
		}

	case "for_statement":
		return variables(scope, yield)

	case "catch_block":
		// catch (e: Exception) { ... }
		return emit(child(scope, "simple_identifier"), scope)

	case "when_expression":
		if subj := child(scope, "when_subject"); subj != nil {
			return variables(subj, yield)
		}

	case "class_declaration":
		// Constructor parameters are visible in initializers and property
		// initializers (val/var parameters are also members).
		if pc := child(scope, "primary_constructor"); pc != nil {
			for _, p := range childrenOf(pc, "class_parameter") {
				if !emit(child(p, "simple_identifier"), p) {
					return false
				}
			}
		}
		return typeParameters(scope, yield)

	case "statements", "ERROR":
		// Declarations earlier in the block, nearest first. Skip the
		// statement that contains pos: in `val x = x + 1` the right-hand
		// x is outer.
		//
		// An ERROR node is a scope too: recovery may flatten a function
		// into it (`fun f(s: String) = s.` with the parameters as a
		// sibling of the use).
		for i := int(scope.ChildCount()) - 1; i >= 0; i-- {
			stmt := scope.Child(uint(i))
			if stmt.StartByte() >= pos || contains(stmt, pos) {
				continue
			}
			if scope.Kind() == "ERROR" && stmt.Kind() == "function_value_parameters" {
				for _, p := range childrenOf(stmt, "parameter") {
					if !emit(child(p, "simple_identifier"), p) {
						return false
					}
				}
				continue
			}
			switch stmt.Kind() {
			case "property_declaration":
				if !variables(stmt, yield) {
					return false
				}
			case "function_declaration":
				if !emit(child(stmt, "simple_identifier"), stmt) {
					return false
				}
			case "class_declaration", "object_declaration":
				if !emit(child(stmt, "type_identifier"), stmt) {
					return false
				}
			}
		}
	}
	return true
}

// variables yields the variable_declaration children of n, including
// destructuring declarations.
func variables(n *ts.Node, yield func(local) bool) bool {
	vars := childrenOf(n, "variable_declaration")
	if mv := child(n, "multi_variable_declaration"); mv != nil {
		vars = append(vars, childrenOf(mv, "variable_declaration")...)
	}
	for _, v := range vars {
		if id := child(v, "simple_identifier"); id != nil {
			if !yield(local{name: id, decl: v}) {
				return false
			}
		}
	}
	return true
}

// typeParameters yields the type parameters <T, ...> declared by decl.
func typeParameters(decl *ts.Node, yield func(local) bool) bool {
	tps := child(decl, "type_parameters")
	if tps == nil {
		return true
	}
	for _, tp := range childrenOf(tps, "type_parameter") {
		if id := child(tp, "type_identifier"); id != nil {
			if !yield(local{name: id, decl: tp}) {
				return false
			}
		}
	}
	return true
}
