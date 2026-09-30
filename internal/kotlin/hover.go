package kotlin

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// A HoverResult is the markdown shown for an identifier.
type HoverResult struct {
	Markdown string
	Range    protocol.Range // the hovered identifier

	// Local is set for a local variable or lambda parameter, whose type
	// is inferred from the syntax alone (a guess, or missing); Where is
	// what it is ("lambda parameter", ...).
	Local bool
	Where string
	// Guess is set when the declaration was found by name alone (the
	// receiver's type is unknown): maybe not the one referred to.
	Guess bool
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
				return &HoverResult{Markdown: CodeBlock("it: "+t.text) + "\n\n*implicit lambda parameter*", Range: rng,
					Local: true, Where: "implicit lambda parameter"}
			}
		}
		return nil
	}
	md, where := r.describe(targets[0])
	if n := len(targets) - 1; n > 0 {
		md += fmt.Sprintf("\n\n_+%d other %s_", n, textutil.Plural(n, "candidate", "candidates"))
	}
	rng, _ := f.Mapper.OffsetRange(int(id.StartByte()), int(id.EndByte()))
	return &HoverResult{Markdown: md, Range: rng, Local: targets[0].local != nil, Where: where, Guess: targets[0].guess}
}

// describe renders a target as markdown, and says what it is.
func (r *resolver) describe(t target) (md, where string) {
	var sig, doc string
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
	b.WriteString(CodeBlock(sig))
	if where != "" {
		b.WriteString("\n\n*")
		b.WriteString(where)
		b.WriteString("*")
	}
	if doc != "" {
		b.WriteString("\n\n---\n\n")
		b.WriteString(doc)
	}
	return b.String(), where
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

// CodeBlock renders a declaration as a Kotlin code block. A parameter
// (`name: Type`) is not Kotlin on its own, and editors highlight the
// block by parsing it, so it is shown as the val it is.
func CodeBlock(sig string) string {
	if paramLike.MatchString(sig) {
		sig = "val " + sig
	}
	return "```kotlin\n" + sig + "\n```"
}

// paramLike matches `name: Type`.
var paramLike = regexp.MustCompile("^(?:[\\p{L}_][\\p{L}\\p{N}_]*|`[^`]+`)\\s*:")
