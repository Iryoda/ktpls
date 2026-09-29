package kotlin

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// maxSignatureLine is the width beyond which a signature's parameter list
// is broken one parameter per line.
const maxSignatureLine = 90

// maxInitializer bounds the initializer shown in a property signature.
const maxInitializer = 40

// headerStop lists the nodes where a declaration's header ends: bodies
// and accessors are not part of a signature.
var headerStop = map[string]bool{
	"class_body":        true,
	"enum_class_body":   true,
	"function_body":     true,
	"getter":            true,
	"setter":            true,
	"property_delegate": true,
}

// headerSkip lists nodes omitted from signatures.
var headerSkip = map[string]bool{
	"annotation":        true,
	"line_comment":      true,
	"multiline_comment": true,
}

// Signature returns a one-declaration summary of decl as Kotlin source,
// e.g. `override fun area(): Double` or `data class Circle(val r: Double)`,
// built from the syntax tree: annotations and bodies are dropped, default
// values are elided, and whitespace is normalized.
func Signature(decl *ts.Node, src []byte) string {
	switch decl.Kind() {
	case "enum_entry":
		if name := child(decl, "simple_identifier"); name != nil {
			return text(name, src)
		}
	case "variable_declaration":
		// A local declared by a lambda, loop or destructuring: show the
		// enclosing property when there is one.
		if p := decl.Parent(); p != nil && p.Kind() == "property_declaration" {
			return Signature(p, src)
		}
	}
	var w sigWriter
	w.src = src
	w.header(decl, true)
	sig := w.String()
	if decl.Kind() == "property_declaration" {
		if init := propertyInitializer(decl); init != nil {
			if t := collapseSpace(text(init, src)); len(t) <= maxInitializer && !strings.Contains(text(init, src), "\n") {
				sig += " = " + t
			}
		}
		if d := child(decl, "property_delegate"); d != nil {
			sig += " by …"
		}
	}
	return wrapParams(sig)
}

// propertyInitializer returns the initializer expression of a property
// declaration, or nil.
func propertyInitializer(prop *ts.Node) *ts.Node {
	seenEq := false
	for _, c := range children(prop) {
		switch {
		case c.Kind() == "=":
			seenEq = true
		case seenEq && c.IsNamed() && !headerSkip[c.Kind()]:
			return c
		}
	}
	return nil
}

// sigWriter accumulates the tokens of a declaration header, separating
// tokens by one space where the source had any gap between them.
type sigWriter struct {
	src     []byte
	b       strings.Builder
	lastEnd uint
	started bool
}

func (w *sigWriter) token(n *ts.Node) {
	w.emit(text(n, w.src), n.StartByte(), n.EndByte())
}

func (w *sigWriter) emit(s string, start, end uint) {
	if w.started && start > w.lastEnd {
		w.b.WriteByte(' ')
	}
	w.b.WriteString(s)
	w.lastEnd = end
	w.started = true
}

// header writes the tokens of n, stopping at a body. top is true for the
// declaration node itself, where a top-level `=` starts the initializer
// (handled by the caller) rather than a default value.
func (w *sigWriter) header(n *ts.Node, top bool) bool {
	if n.ChildCount() == 0 {
		if n.Kind() != "" && !n.IsMissing() {
			w.token(n)
		}
		return true
	}
	kids := children(n)
	for i := 0; i < len(kids); i++ {
		c := kids[i]
		switch {
		case headerStop[c.Kind()]:
			return false
		case headerSkip[c.Kind()]:
			continue
		case c.Kind() == "=" && top:
			return false // property initializer / expression body
		case c.Kind() == "=" && (n.Kind() == "function_value_parameters" || n.Kind() == "class_parameter" || n.Kind() == "parameter"):
			// A default value: `= …`, skipping the expression.
			w.token(c)
			j := i + 1
			for j < len(kids) && !kids[j].IsNamed() {
				j++
			}
			if j < len(kids) {
				w.emit("…", kids[j].StartByte(), kids[j].EndByte())
				i = j
			}
			continue
		case c.Kind() == "modifiers":
			w.modifiers(c)
			continue
		}
		if !w.header(c, false) {
			return false
		}
	}
	return true
}

// modifiers writes the non-annotation modifiers.
func (w *sigWriter) modifiers(n *ts.Node) {
	for _, c := range children(n) {
		if !headerSkip[c.Kind()] {
			w.header(c, false)
		}
	}
}

func (w *sigWriter) String() string {
	s := w.b.String()
	for _, r := range []struct{ old, new string }{
		{"( ", "("}, {" )", ")"}, {" ,", ","}, {",)", ")"}, {"< ", "<"}, {" >", ">"}, {" ?", "?"},
	} {
		s = strings.ReplaceAll(s, r.old, r.new)
	}
	return s
}

// collapseSpace replaces each run of whitespace in s with one space.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// wrapParams breaks the first parenthesized parameter list of a long
// signature one parameter per line.
func wrapParams(sig string) string {
	if len(sig) <= maxSignatureLine {
		return sig
	}
	open := strings.IndexByte(sig, '(')
	if open < 0 {
		return sig
	}
	depth := 0
	var parts []string
	start := open + 1
	for i := open; i < len(sig); i++ {
		switch sig[i] {
		case '(', '<', '[', '{':
			depth++
		case ')', '>', ']', '}':
			if sig[i] == '>' && i > 0 && sig[i-1] == '-' {
				continue // the arrow of a function type
			}
			depth--
			if depth == 0 {
				if p := strings.TrimSpace(sig[start:i]); p != "" {
					parts = append(parts, p)
				}
				if len(parts) < 2 {
					return sig
				}
				return sig[:open+1] + "\n    " + strings.Join(parts, ",\n    ") + ",\n" + sig[i:]
			}
		case ',':
			if depth == 1 {
				parts = append(parts, strings.TrimSpace(sig[start:i]))
				start = i + 1
			}
		}
	}
	return sig
}
