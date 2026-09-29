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
	for scope, inner := use.Parent(), use; scope != nil; scope, inner = scope.Parent(), scope {
		if l := searchScope(scope, inner, use, name, src); l != nil {
			return l
		}
	}
	return nil
}

// searchScope looks for name among the declarations that node scope
// introduces and that are visible at use. inner is the child of scope that
// contains use.
func searchScope(scope, inner, use *ts.Node, name string, src []byte) *local {
	match := func(id, decl *ts.Node) *local {
		if id != nil && text(id, src) == name {
			return &local{name: id, decl: decl}
		}
		return nil
	}
	switch scope.Kind() {
	case "function_declaration", "secondary_constructor", "anonymous_function", "setter":
		if params := child(scope, "function_value_parameters", "parameter_with_optional_type"); params != nil {
			for _, p := range childrenOf(params, "parameter") {
				if l := match(child(p, "simple_identifier"), p); l != nil {
					return l
				}
			}
		}
		if l := typeParameter(scope, name, src); l != nil {
			return l
		}
		// Setters: `set(value) { ... }` has a bare parameter.
		if scope.Kind() == "setter" {
			if p := child(scope, "parameter_with_optional_type"); p != nil {
				return match(child(p, "simple_identifier"), p)
			}
		}

	case "lambda_literal":
		if params := child(scope, "lambda_parameters"); params != nil {
			if l := searchVariables(params, name, src); l != nil {
				return l
			}
		}

	case "for_statement":
		if l := searchVariables(scope, name, src); l != nil {
			return l
		}

	case "catch_block":
		// catch (e: Exception) { ... }
		return match(child(scope, "simple_identifier"), scope)

	case "when_expression":
		if subj := child(scope, "when_subject"); subj != nil {
			if l := searchVariables(subj, name, src); l != nil {
				return l
			}
		}

	case "class_declaration":
		// Constructor parameters are visible in initializers and property
		// initializers (val/var parameters are also members).
		if pc := child(scope, "primary_constructor"); pc != nil {
			for _, p := range childrenOf(pc, "class_parameter") {
				if l := match(child(p, "simple_identifier"), p); l != nil {
					return l
				}
			}
		}
		if l := typeParameter(scope, name, src); l != nil {
			return l
		}

	case "statements", "source_file":
		// Declarations earlier in the block. Search backwards so the
		// nearest (shadowing) declaration wins. Skip the statement that
		// contains the use: in `val x = x + 1` the right-hand x is outer.
		if scope.Kind() == "source_file" {
			return nil // top-level declarations are in the index
		}
		for i := int(scope.ChildCount()) - 1; i >= 0; i-- {
			stmt := scope.Child(uint(i))
			if stmt.StartByte() >= use.StartByte() || stmt.Equals(*inner) {
				continue
			}
			switch stmt.Kind() {
			case "property_declaration":
				if l := searchVariables(stmt, name, src); l != nil {
					return l
				}
			case "function_declaration":
				if l := match(child(stmt, "simple_identifier"), stmt); l != nil {
					return l
				}
			case "class_declaration", "object_declaration":
				if l := match(child(stmt, "type_identifier"), stmt); l != nil {
					return l
				}
			}
		}
	}
	return nil
}

// searchVariables finds name among variable_declaration children of n,
// including destructuring declarations.
func searchVariables(n *ts.Node, name string, src []byte) *local {
	vars := childrenOf(n, "variable_declaration")
	if mv := child(n, "multi_variable_declaration"); mv != nil {
		vars = append(vars, childrenOf(mv, "variable_declaration")...)
	}
	for _, v := range vars {
		if id := child(v, "simple_identifier"); id != nil && text(id, src) == name {
			return &local{name: id, decl: v}
		}
	}
	return nil
}

// typeParameter finds a type parameter <name> declared by decl.
func typeParameter(decl *ts.Node, name string, src []byte) *local {
	tps := child(decl, "type_parameters")
	if tps == nil {
		return nil
	}
	for _, tp := range childrenOf(tps, "type_parameter") {
		if id := child(tp, "type_identifier"); id != nil && text(id, src) == name {
			return &local{name: id, decl: tp}
		}
	}
	return nil
}
