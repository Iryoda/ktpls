package kotlin

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// Generated code often uses a type written somewhere else: an interface
// member's parameter types in the interface's file, the inferred type of
// a call in the callee's file. A type name means what its file's imports
// say, so writing it into another file may require an import, or may not
// be possible at all. A porter renders types into a target file and
// collects the imports they need.

// A typeCtx is where a type text was written: its file, the container it
// was written in, the type-parameter substitution in effect (for members
// inherited through generic supertypes), and type parameters that stay
// as they are (a generic function's own).
type typeCtx struct {
	sum       *FileSummary // nil: the text is a fully qualified workspace type
	container string
	subst     map[string]substRef
	params    map[string]bool
}

// A substRef is the type a type parameter stands for, as written in its
// own context.
type substRef struct {
	text string
	ctx  typeCtx
}

func ctxOf(t typeRef) typeCtx { return typeCtx{sum: t.sum, container: t.container} }

// defaultTypes are types visible in every Kotlin file (default imports).
var defaultTypes = map[string]bool{}

func init() {
	for t := range strings.FieldsSeq(`Any Unit Nothing String CharSequence Int Long Short Byte Double
		Float Boolean Char Number Comparable Throwable Exception RuntimeException Error
		IllegalArgumentException IllegalStateException UnsupportedOperationException
		IndexOutOfBoundsException NoSuchElementException NullPointerException ArithmeticException
		ClassCastException Array IntArray LongArray ShortArray ByteArray DoubleArray FloatArray
		BooleanArray CharArray Pair Triple Lazy Result Function Enum Annotation List MutableList
		ArrayList Set MutableSet HashSet LinkedHashSet Map MutableMap HashMap LinkedHashMap
		Collection MutableCollection Iterable MutableIterable Iterator MutableIterator ListIterator
		Sequence IntRange LongRange CharRange Regex StringBuilder Appendable UInt ULong UByte UShort
		Comparator KClass Deprecated Suppress JvmStatic JvmOverloads Volatile Synchronized`) {
		defaultTypes[t] = true
	}
}

var typeTokenRE = regexp.MustCompile(`[\p{L}_][\p{L}\p{Nd}_]*(?:\.[\p{L}_][\p{L}\p{Nd}_]*)*`)

// A porter renders types into a target file.
type porter struct {
	r               *resolver
	target          *FileSummary
	targetContainer string
	imports         []string // fully qualified names to import into target
}

func (r *resolver) newPorter(target *FileSummary, container string) *porter {
	return &porter{r: r, target: target, targetContainer: container}
}

// render rewrites a type text from ctx for the target file. It reports
// false if some name in it can't be made to mean the same thing there.
func (p *porter) render(text string, ctx typeCtx) (string, bool) {
	ok := true
	out := typeTokenRE.ReplaceAllStringFunc(text, func(tok string) string {
		if !ok {
			return tok
		}
		res, good := p.token(tok, ctx)
		ok = ok && good
		return res
	})
	return out, ok
}

func (p *porter) token(tok string, ctx typeCtx) (string, bool) {
	switch tok {
	case "in", "out", "suspend", "reified":
		return tok, true
	}
	if v, ok := ctx.subst[tok]; ok {
		return p.render(v.text, v.ctx)
	}
	if ctx.params[tok] {
		return tok, true
	}
	if ctx.sum == nil { // a fully qualified workspace type
		if syms := filterKinds(p.r.ix.ByFQName(baseType(tok)), true); len(syms) > 0 {
			return p.symbol(syms[0])
		}
		return tok, false
	}
	if r, _ := utf8.DecodeRuneInString(tok); strings.Contains(tok, ".") && unicode.IsLower(r) {
		return tok, true // already fully qualified: a.b.C
	}
	if origin := p.r.resolveTypeName(tok, ctx.sum, ctx.container); len(origin) > 0 {
		return p.symbol(origin[0])
	}
	// A library type, or a type parameter of the context.
	first, _, _ := strings.Cut(tok, ".")
	if defaultTypes[first] {
		return tok, true
	}
	for _, imp := range ctx.sum.Imports {
		if !imp.Wildcard && imp.Name() == first {
			if imp.Alias != "" {
				return tok, false
			}
			p.need(imp.Path)
			return tok, true
		}
	}
	// Whatever resolved where it was written resolves the same way when
	// written in the same file.
	return tok, ctx.sum == p.target
}

// symbol returns how to refer to a workspace type from the target file,
// importing it if needed.
func (p *porter) symbol(s *Symbol) (string, bool) {
	pkg := packageOf(p.r.ix, s)
	rel := s.FQName
	if pkg != "" {
		rel = strings.TrimPrefix(s.FQName, pkg+".")
	}
	for _, name := range []string{s.Name, rel} {
		if here := p.r.resolveTypeName(name, p.target, p.targetContainer); len(here) > 0 && here[0] == s {
			return name, true
		}
	}
	top, _, _ := strings.Cut(rel, ".")
	if here := p.r.resolveTypeName(top, p.target, p.targetContainer); len(here) > 0 {
		return s.FQName, true // the name means something else here
	}
	if pkg != "" && (p.target == nil || pkg != p.target.Package) {
		p.need(joinFQ(pkg, top))
	}
	return rel, true
}

func (p *porter) need(fq string) {
	if !slices.Contains(p.imports, fq) {
		p.imports = append(p.imports, fq)
	}
}

// importsEdit returns one edit adding imports of fqs to f (skipping those
// already visible), or nil if none are needed.
func importsEdit(f *ParsedFile, fqs []string) *protocol.TextEdit {
	var add []string
	for _, fq := range fqs {
		if !importVisible(f.Summary, fq) && !slices.Contains(add, fq) {
			add = append(add, fq)
		}
	}
	if len(add) == 0 {
		return nil
	}
	slices.Sort(add)
	e := importEdit(f, add[0])
	for _, fq := range add[1:] {
		if strings.HasSuffix(e.NewText, "\n") {
			e.NewText += "import " + fq + "\n"
		} else {
			e.NewText += "\nimport " + fq
		}
	}
	return &e
}

// importVisible reports whether fq is already visible in the file: same
// package, imported, or covered by a wildcard import.
func importVisible(sum *FileSummary, fq string) bool {
	if sum == nil {
		return false
	}
	pkg := parentFQ(fq)
	if pkg == sum.Package {
		return true
	}
	for _, imp := range sum.Imports {
		if imp.Path == fq && imp.Alias == "" || imp.Wildcard && imp.Path == pkg {
			return true
		}
	}
	return false
}
