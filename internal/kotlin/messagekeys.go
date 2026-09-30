package kotlin

import (
	"strconv"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// Message keys: string literals passed to parameters that take keys of the
// project's message bundles (messages.properties), like IntelliJ's check
// of @PropertyKey arguments. Which parameters take keys is declared
// (@PropertyKey) or learned: a parameter most of whose literal arguments
// across the workspace are keys takes keys, so an exception's message code
// is found without configuration.

// A StringArg is a plain string literal passed as a call argument.
type StringArg struct {
	Callee string // the called function's or class's simple name
	Pos    int    // the argument's position; -1 if named
	Name   string // the argument's name, if named
	Value  string
	Range  protocol.Range // the literal's content, without the quotes
}

// stringArguments collects the plain string literal arguments of every
// call in the tree.
func stringArguments(root *ts.Node, src []byte, m *protocol.Mapper) []StringArg {
	var out []StringArg
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n.Kind() == "call_expression" {
			out = append(out, callStringArgs(n, src, m)...)
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	return out
}

func callStringArgs(call *ts.Node, src []byte, m *protocol.Mapper) []StringArg {
	callee := calleeName(call.NamedChild(0), src)
	if callee == "" {
		return nil
	}
	args, ok := enclosingCallArgs(call)
	if !ok {
		return nil
	}
	var out []StringArg
	for i, a := range args {
		lit := a.NamedChild(a.NamedChildCount() - 1)
		value, ok := plainString(lit, src)
		if !ok {
			continue
		}
		arg := StringArg{Callee: callee, Pos: i, Value: value}
		if a.NamedChildCount() > 1 {
			if id := a.NamedChild(0); id.Kind() == "simple_identifier" {
				arg.Pos, arg.Name = -1, text(id, src)
			}
		}
		arg.Range, _ = m.OffsetRange(int(lit.StartByte())+1, int(lit.EndByte())-1)
		out = append(out, arg)
	}
	return out
}

// calleeName returns the simple name of a call's callee: f in f(), a.f()
// or Foo<T>().
func calleeName(n *ts.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "simple_identifier":
		return text(n, src)
	case "navigation_expression":
		if sfx := n.NamedChild(n.NamedChildCount() - 1); sfx != nil && sfx.Kind() == "navigation_suffix" {
			if id := child(sfx, "simple_identifier"); id != nil {
				return text(id, src)
			}
		}
	}
	return ""
}

// plainString returns the value of a one-line string literal without
// templates or escapes.
func plainString(n *ts.Node, src []byte) (string, bool) {
	if n == nil || n.Kind() != "string_literal" {
		return "", false
	}
	t := text(n, src)
	if len(t) < 2 || !strings.HasPrefix(t, `"`) || strings.HasPrefix(t, `"""`) || !strings.HasSuffix(t, `"`) {
		return "", false
	}
	v := t[1 : len(t)-1]
	if strings.ContainsAny(v, "$\\\n") {
		return "", false
	}
	return v, true
}

// A KeyParam identifies a parameter by its function or class's simple
// name and its name ("ValidationException(code)"), or its position when
// the name is unknown ("isEqualTo(#0)").
type KeyParam string

// ParamOf returns the parameter an argument is passed to.
func ParamOf(ix *Index, a StringArg) KeyParam {
	if a.Name != "" {
		return KeyParam(a.Callee + "(" + a.Name + ")")
	}
	name := ""
	for _, s := range ix.ByName(a.Callee) {
		if s.Kind != KindFunction && s.Kind != KindConstructor && !s.Kind.IsType() {
			continue
		}
		if a.Pos >= len(s.Params) {
			continue
		}
		switch p := s.Params[a.Pos].Name; {
		case name == "":
			name = p
		case name != p:
			return positional(a) // overloads disagree
		}
	}
	if name == "" {
		return positional(a)
	}
	return KeyParam(a.Callee + "(" + name + ")")
}

func positional(a StringArg) KeyParam {
	return KeyParam(a.Callee + "(#" + strconv.Itoa(a.Pos) + ")")
}

// Learning thresholds: a parameter takes keys if it has at least
// minKeyUses literal arguments and at least keyShare of them are keys.
const (
	minKeyUses = 3
	keyShare   = 0.8
)

// KeyParams returns the parameters taking message keys: those declared
// @PropertyKey, and those whose literal arguments in the workspace are
// mostly keys (isKey).
func KeyParams(ix *Index, isKey func(string) bool) map[KeyParam]bool {
	type count struct{ all, keys int }
	counts := map[KeyParam]*count{}
	out := map[KeyParam]bool{}
	for f := range ix.Files() {
		for _, a := range f.StringArgs {
			p := ParamOf(ix, a)
			c := counts[p]
			if c == nil {
				c = &count{}
				counts[p] = c
			}
			c.all++
			if isKey(a.Value) {
				c.keys++
			}
		}
		for _, s := range f.Symbols {
			for _, p := range s.Params {
				if p.PropertyKey {
					out[KeyParam(ownerName(s)+"("+p.Name+")")] = true
				}
			}
		}
	}
	for p, c := range counts {
		if c.all >= minKeyUses && float64(c.keys) >= keyShare*float64(c.all) {
			out[p] = true
		}
	}
	return out
}

// ownerName is the name calls use for a declaration's parameters: the
// class for a constructor.
func ownerName(s *Symbol) string {
	if s.Kind == KindConstructor {
		return lastSegment(s.Container)
	}
	return s.Name
}
