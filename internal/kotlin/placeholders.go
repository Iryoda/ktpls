package kotlin

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// Property placeholders: Spring's ${key} (or ${key:default}) in string
// literals, as in @Value("\${server.port}") or a Feign client's url,
// resolved against the project's application.yml. In Kotlin source the
// dollar is escaped: "\${key}", or ${'$'}{key} in a raw string.

// A Placeholder is a ${key} in a string literal.
type Placeholder struct {
	Key        string
	Default    string // after the colon, as written
	HasDefault bool
	Range      protocol.Range // the key
}

// Placeholders returns the property placeholders in f's string literals.
func Placeholders(f *ParsedFile) []Placeholder {
	var out []Placeholder
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n.Kind() == "string_literal" {
			out = append(out, literalPlaceholders(n, f.Content, f.Mapper)...)
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(f.Tree.RootNode())
	return out
}

// PlaceholderAt returns the placeholder whose key is at offset, if any.
func PlaceholderAt(f *ParsedFile, offset int) *Placeholder {
	n := f.Tree.RootNode().DescendantForByteRange(uint(offset), uint(offset))
	for ; n != nil && n.Kind() != "string_literal"; n = n.Parent() {
	}
	if n == nil {
		return nil
	}
	pos, err := f.Mapper.OffsetPosition(offset)
	if err != nil {
		return nil
	}
	for _, p := range literalPlaceholders(n, f.Content, f.Mapper) {
		if within(pos, p.Range) {
			return &p
		}
	}
	return nil
}

func within(p protocol.Position, r protocol.Range) bool {
	after := p.Line > r.Start.Line || p.Line == r.Start.Line && p.Character >= r.Start.Character
	before := p.Line < r.End.Line || p.Line == r.End.Line && p.Character <= r.End.Character
	return after && before
}

func literalPlaceholders(lit *ts.Node, src []byte, m *protocol.Mapper) []Placeholder {
	raw := strings.HasPrefix(text(lit, src), `"""`)
	var out []Placeholder
	add := func(open int) { // src[open] is the "{" after the dollar
		if p, ok := placeholder(src, open, int(lit.EndByte()), m); ok {
			out = append(out, p)
		}
	}
	for i := uint(0); i < lit.ChildCount(); i++ {
		c := lit.Child(i)
		switch {
		case !raw && c.Kind() == "string_content":
			// "\${": a dollar escaped by an odd run of backslashes.
			s := text(c, src)
			for j := strings.Index(s, "${"); j >= 0; j = nextIndex(s, "${", j+2) {
				if escapedDollar(s, j) {
					add(int(c.StartByte()) + j + 1)
				}
			}
		case raw && c.Kind() == "interpolated_expression":
			// ${'$'}{key}: the escaped dollar is an interpolation.
			if e := strings.TrimSpace(text(c, src)); e != `'$'` && e != `"$"` {
				continue
			}
			if i+2 < lit.ChildCount() {
				if next := lit.Child(i + 2); next.Kind() == "string_content" && strings.HasPrefix(text(next, src), "{") {
					add(int(next.StartByte()))
				}
			}
		}
	}
	return out
}

func nextIndex(s, sub string, from int) int {
	if from > len(s) {
		return -1
	}
	if i := strings.Index(s[from:], sub); i >= 0 {
		return from + i
	}
	return -1
}

func escapedDollar(s string, dollar int) bool {
	n := 0
	for k := dollar - 1; k >= 0 && s[k] == '\\'; k-- {
		n++
	}
	return n%2 == 1
}

// placeholder parses the placeholder whose "{" is at src[open], up to its
// closing brace before end.
func placeholder(src []byte, open, end int, m *protocol.Mapper) (Placeholder, bool) {
	keyStart := open + 1
	k := keyStart
	for k < end && src[k] != '}' && src[k] != ':' && src[k] != '"' && src[k] != '\n' {
		k++
	}
	if k == end || src[k] == '"' || src[k] == '\n' {
		return Placeholder{}, false // not closed in this literal
	}
	p := Placeholder{Key: string(src[keyStart:k])}
	if src[k] == ':' {
		// The default runs to the matching brace (it may hold placeholders).
		depth, d := 0, k+1
		for ; d < end; d++ {
			if src[d] == '{' {
				depth++
			} else if src[d] == '}' {
				if depth == 0 {
					break
				}
				depth--
			}
		}
		if d == end {
			return Placeholder{}, false
		}
		p.HasDefault, p.Default = true, string(src[k+1:d])
	}
	var err error
	if p.Range, err = m.OffsetRange(keyStart, k); err != nil {
		return Placeholder{}, false
	}
	return p, true
}
