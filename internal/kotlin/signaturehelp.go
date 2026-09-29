package kotlin

import (
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// SignatureHelp describes the call whose argument list encloses offset:
// the callee's signatures (overloads included) and the parameter being
// typed. The call is found in the text, since the tree is usually broken
// while arguments are being typed.
func SignatureHelp(f *ParsedFile, ix *Index, offset int) *protocol.SignatureHelp {
	open, arg, name := enclosingArgument(f.Content, offset)
	if open < 0 {
		return nil
	}
	id := IdentifierAt(f.Tree, open)
	if id == nil || int(id.EndByte()) != open {
		return nil
	}
	r := &resolver{f: f, ix: ix, src: f.Content}
	fns := r.callables(r.resolve(id))
	if len(fns) == 0 {
		return nil
	}
	help := &protocol.SignatureHelp{}
	active := -1
	for _, fn := range fns {
		if fn.Kind.IsType() && len(fn.Params) == 0 && hasConstructors(ix, fn) {
			continue // a class with only secondary constructors
		}
		sig, paramIndex := signatureInfo(fn, arg, name)
		help.Signatures = append(help.Signatures, sig)
		if active < 0 && paramIndex >= 0 {
			active = len(help.Signatures) - 1
			help.ActiveParameter = uint32(paramIndex)
		}
	}
	if len(help.Signatures) == 0 {
		return nil
	}
	if active < 0 {
		active = 0
	}
	help.ActiveSignature = uint32(active)
	return help
}

func hasConstructors(ix *Index, class *Symbol) bool {
	for _, m := range ix.Members(class.FQName) {
		if m.Kind == KindConstructor {
			return true
		}
	}
	return false
}

// signatureInfo renders fn as "name(a: A, b: B): R" and returns the index
// of the parameter matching argument number arg (or the named argument),
// or -1 if fn doesn't take it.
func signatureInfo(fn *Symbol, arg int, named string) (protocol.SignatureInformation, int) {
	name := fn.Name
	if fn.Kind.IsType() || fn.Kind == KindConstructor {
		name = lastSegment(fn.FQName)
		if fn.Kind == KindConstructor {
			name = lastSegment(fn.Container)
		}
	}
	var b strings.Builder
	b.WriteString(name + "(")
	info := protocol.SignatureInformation{Parameters: []protocol.ParameterInformation{}}
	active := -1
	for i, p := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		start := utf16Len(b.String())
		if p.Vararg {
			b.WriteString("vararg ")
		}
		b.WriteString(p.Name)
		if p.Type != "" {
			b.WriteString(": " + p.Type)
		}
		info.Parameters = append(info.Parameters, protocol.ParameterInformation{Label: [2]uint32{start, utf16Len(b.String())}})
		switch {
		case named != "" && p.Name == named:
			active = i
		case named == "" && (i == arg || p.Vararg && arg >= i && active < 0):
			active = i
		}
	}
	b.WriteString(")")
	if fn.Kind == KindFunction && fn.Type != "" {
		b.WriteString(": " + fn.Type)
	}
	info.Label = b.String()
	if fn.Doc != "" {
		info.Documentation = &protocol.MarkupContent{Kind: protocol.Markdown, Value: fn.Doc}
	}
	if active >= 0 {
		a := uint32(active)
		info.ActiveParameter = &a
	}
	return info, active
}

func utf16Len(s string) uint32 { return uint32(len(utf16.Encode([]rune(s)))) }

var namedArgRE = regexp.MustCompile(`^\s*([\p{L}_][\p{L}\p{Nd}_]*)\s*=[^=]`)

// enclosingArgument finds the argument list around offset: the offset of
// its "(", the index of the argument being typed, and that argument's
// name if it is named (`f(a, name = |`). open is -1 outside any argument
// list (or inside a lambda or index within it).
func enclosingArgument(src []byte, offset int) (open, arg int, name string) {
	depth := 0
	for j := offset - 1; j >= 0; j-- {
		switch src[j] {
		case ')', ']', '}':
			depth++
		case '[', '{':
			if depth == 0 {
				return -1, 0, ""
			}
			depth--
		case '(':
			if depth == 0 {
				arg, start := 0, j+1
				d := 0
				for k := j + 1; k < offset; k++ {
					switch src[k] {
					case '(', '[', '{':
						d++
					case ')', ']', '}':
						d--
					case '"':
						k = skipString(src, k, offset)
					case ',':
						if d == 0 {
							arg++
							start = k + 1
						}
					}
				}
				if m := namedArgRE.FindSubmatch(src[start:min(offset+1, len(src))]); m != nil {
					name = string(m[1])
				}
				return j, arg, name
			}
			depth--
		case '"':
			// Skip back over a string literal.
			j--
			for j >= 0 && (src[j] != '"' || (j > 0 && src[j-1] == '\\')) {
				j--
			}
			if j < 0 {
				return -1, 0, ""
			}
		case '\n':
			// Arguments may span lines; keep going.
		}
	}
	return -1, 0, ""
}

// skipString returns the offset of the quote closing the string opened at
// src[i], or limit.
func skipString(src []byte, i, limit int) int {
	for k := i + 1; k < limit; k++ {
		switch src[k] {
		case '\\':
			k++
		case '"':
			return k
		}
	}
	return limit
}
