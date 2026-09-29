package kotlin

import (
	"fmt"
	"strings"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// A HoverResult is the markdown shown for an identifier.
type HoverResult struct {
	Markdown string
	Range    protocol.Range // the hovered identifier
}

// Hover describes the declaration of the identifier at offset in f: its
// signature, where it is declared, and its KDoc.
func Hover(f *ParsedFile, ix *Index, offset int) *HoverResult {
	id := IdentifierAt(f.Tree, offset)
	if id == nil {
		return nil
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	targets := r.resolve(id)
	if len(targets) == 0 {
		// The implicit lambda parameter: show its inferred type.
		if text(id, f.Content) == "it" {
			if t := r.typeOf(id, 0); t.text != "" {
				rng, _ := f.Mapper.OffsetRange(int(id.StartByte()), int(id.EndByte()))
				return &HoverResult{Markdown: "```kotlin\nit: " + t.text + "\n```\n\n*implicit lambda parameter*", Range: rng}
			}
		}
		return nil
	}
	md := r.describe(targets[0])
	if n := len(targets) - 1; n > 0 {
		md += fmt.Sprintf("\n\n_+%d other %s_", n, plural(n, "candidate", "candidates"))
	}
	rng, _ := f.Mapper.OffsetRange(int(id.StartByte()), int(id.EndByte()))
	return &HoverResult{Markdown: md, Range: rng}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// describe renders a target as markdown.
func (r *resolver) describe(t target) string {
	var sig, where, doc string
	switch {
	case t.sym != nil:
		s := t.sym
		sig = s.Signature
		if sig == "" {
			sig = kindKeyword(s.Kind) + " " + s.Name
		}
		switch {
		case s.Receiver != "":
			where = "extension on `" + s.Receiver + "`"
			if pkg := packageOf(r.ix, s); pkg != "" {
				where += " in `" + pkg + "`"
			}
		case s.Container != "":
			where = "in `" + s.Container + "`"
		default:
			if pkg := packageOf(r.ix, s); pkg != "" {
				where = "package `" + pkg + "`"
			}
		}
		doc = s.Doc
	case t.param != nil:
		sig = t.paramInfo.Name
		if t.paramInfo.Type != "" {
			sig += ": " + t.paramInfo.Type
		}
		where = "parameter of `" + t.paramOwner.Name + "`"
	case t.local != nil:
		decl := t.local.decl
		sig = Signature(decl, r.src)
		where = localKind(decl.Kind())
		if lambda, _ := lambdaParameterIndex(decl); lambda != nil {
			where = "lambda parameter"
			if declaredTypeText(decl, r.src) == "" {
				if tt := r.typeOf(t.local.name, 0); tt.text != "" {
					sig += ": " + tt.text
				}
			}
		}
		docNode := decl
		if decl.Kind() == "variable_declaration" && decl.Parent() != nil && decl.Parent().Kind() == "property_declaration" {
			docNode = decl.Parent()
		}
		if c := KDocBefore(r.src, docNode.StartByte()); c != "" {
			doc = RenderKDoc(c)
		}
	}

	var b strings.Builder
	b.WriteString("```kotlin\n" + sig + "\n```")
	if where != "" {
		b.WriteString("\n\n*" + where + "*")
	}
	if doc != "" {
		b.WriteString("\n\n---\n\n" + doc)
	}
	return b.String()
}

func localKind(kind string) string {
	switch kind {
	case "parameter", "class_parameter":
		return "parameter"
	case "type_parameter":
		return "type parameter"
	}
	return "local"
}

func packageOf(ix *Index, s *Symbol) string {
	if f := ix.File(s.Path); f != nil {
		return f.Package
	}
	return ""
}
