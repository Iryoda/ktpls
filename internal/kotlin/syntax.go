// Package kotlin implements Kotlin language features (definition, hover,
// completion, ...) on top of tree-sitter syntax trees.
//
// Node kinds follow the fwcd/tree-sitter-kotlin grammar; see "Grammar
// notes" in PLAN.md. That grammar uses no field names, so children are
// found by kind.
package kotlin

import (
	"strings"

	tskotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

var language = ts.NewLanguage(tskotlin.Language())

// Parse parses Kotlin source into a syntax tree. The caller must Close the
// returned tree. Parse is safe for concurrent use: each call uses its own
// parser, since tree-sitter parsers are not goroutine-safe (and are cheap).
func Parse(src []byte) *ts.Tree {
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(language); err != nil {
		// Only possible on a grammar/runtime ABI mismatch: a build problem.
		panic("kotlin: incompatible tree-sitter grammar: " + err.Error())
	}
	return p.Parse(src, nil)
}

// A ParsedFile is one version of a source file with its syntax tree.
type ParsedFile struct {
	Path    string
	URI     protocol.DocumentURI
	Content []byte
	Tree    *ts.Tree
	Mapper  *protocol.Mapper
	Summary *FileSummary // symbols extracted from this same version
}

// text returns the source text of n.
func text(n *ts.Node, src []byte) string {
	return string(src[n.StartByte():n.EndByte()])
}

// children returns the children of n, including anonymous tokens.
func children(n *ts.Node) []*ts.Node {
	out := make([]*ts.Node, 0, n.ChildCount())
	for i := uint(0); i < n.ChildCount(); i++ {
		out = append(out, n.Child(i))
	}
	return out
}

// child returns the first direct child of n with one of the given kinds.
func child(n *ts.Node, kinds ...string) *ts.Node {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		for _, k := range kinds {
			if c.Kind() == k {
				return c
			}
		}
	}
	return nil
}

// childrenOf returns the direct children of n with the given kind.
func childrenOf(n *ts.Node, kind string) []*ts.Node {
	var out []*ts.Node
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == kind {
			out = append(out, c)
		}
	}
	return out
}

// hasToken reports whether n has a direct child of the given kind (for
// anonymous tokens the kind is the token text, e.g. "interface").
func hasToken(n *ts.Node, kind string) bool { return child(n, kind) != nil }

// dotted returns the dotted name of an `identifier` node
// (identifier > simple_identifier ("." simple_identifier)*), ignoring
// whitespace and comments between the parts.
func dotted(n *ts.Node, src []byte) string {
	if n == nil {
		return ""
	}
	parts := childrenOf(n, "simple_identifier")
	if len(parts) == 0 {
		return text(n, src)
	}
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = text(p, src)
	}
	return strings.Join(names, ".")
}

// typeName returns the name of the type denoted by a type node, e.g.
// "String" for `String?` or "a.b.C" for `a.b.C<T>`, or "" if n is not a
// named type (function types, etc.).
func typeName(n *ts.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "user_type":
		var names []string
		for _, id := range childrenOf(n, "type_identifier") {
			names = append(names, text(id, src))
		}
		return strings.Join(names, ".")
	case "nullable_type", "parenthesized_type", "type_reference", "constructor_invocation":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if name := typeName(n.NamedChild(i), src); name != "" {
				return name
			}
		}
	}
	return ""
}

// declaredType returns the type annotation among n's direct children
// (the `: T` of a parameter, property or function), or "".
func declaredType(n *ts.Node, src []byte) string {
	return typeName(child(n, "user_type", "nullable_type", "parenthesized_type"), src)
}

// joinFQ joins a qualifier and a name with a dot.
func joinFQ(qual, name string) string {
	if qual == "" {
		return name
	}
	return qual + "." + name
}

// lastSegment returns the part of a dotted name after its last dot.
func lastSegment(fq string) string {
	return fq[strings.LastIndexByte(fq, '.')+1:]
}
