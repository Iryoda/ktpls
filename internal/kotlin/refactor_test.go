package kotlin

import (
	"slices"
	"strings"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// applyTitled runs the action titled title at the "|" in src and returns
// the edited source, or "" if the action isn't offered.
func applyTitled(t *testing.T, src, title string, extra ...string) string {
	t.Helper()
	i := strings.Index(src, "|")
	src = src[:i] + src[i+1:]
	ix := NewIndex()
	for j, e := range extra {
		ef, _ := parseOne(t, "/w/extra/E"+string(rune('0'+j))+".kt", e)
		ix.Update(ef.Summary)
	}
	f, _ := parseOne(t, "/w/app/App.kt", src)
	ix.Update(f.Summary)
	for _, a := range CodeActions(f, ix, i) {
		if a.Title != title {
			continue
		}
		edits := slices.Clone(a.Edits)
		slices.SortStableFunc(edits, func(x, y protocol.TextEdit) int { // back to front
			return f.Mapper.PositionOffset(y.Range.Start) - f.Mapper.PositionOffset(x.Range.Start)
		})
		out := src
		for _, e := range edits {
			s, en := f.Mapper.RangeOffsets(e.Range)
			out = out[:s] + e.NewText + out[en:]
		}
		// The result must still parse.
		g, _ := parseOne(t, "/w/app/Out.kt", out)
		if g.Tree.RootNode().HasError() {
			t.Errorf("%s produced invalid Kotlin:\n%s", title, out)
		}
		return out
	}
	return ""
}

type refactorCase struct {
	name, src, title, want string // want "" = not offered
}

func runRefactorCases(t *testing.T, cases []refactorCase, extra ...string) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := applyTitled(t, tt.src, tt.title, extra...)
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

const refactorLib = `package lib

class Money(val cents: Long)

class Box<T>(val item: T)

fun price(): Money = Money(1)

fun maybe(): Money? = null

fun log(s: String) {}
`

func TestConvertBody(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"return to expression", "package app\n\nfun |a(): Int {\n    return 1\n}\n", "Convert to expression body",
			"package app\n\nfun a(): Int = 1\n"},
		{"cursor on return", "package app\n\nfun a(): Int {\n    |return 1\n}\n", "Convert to expression body",
			"package app\n\nfun a(): Int = 1\n"},
		{"unit call keeps Unit", "package app\n\nfun |a() {\n    listOf(1).size\n}\n", "Convert to expression body", ""},
		{"known unit call", "package app\n\nfun |a() {\n    println(\"x\")\n}\n", "Convert to expression body",
			"package app\n\nfun a() = println(\"x\")\n"},
		{"unknown call pins Unit", "package app\n\nfun |a() {\n    mystery()\n}\n", "Convert to expression body",
			"package app\n\nfun a(): Unit = mystery()\n"},
		{"two statements", "package app\n\nfun |a(): Int {\n    val x = 1\n    return x\n}\n", "Convert to expression body", ""},
		{"comment kept: not offered", "package app\n\nfun |a(): Int {\n    // why\n    return 1\n}\n", "Convert to expression body", ""},
		{"cursor deep in body", "package app\n\nfun a(): Int {\n    return |1\n}\n", "Convert to expression body", ""},
		{"expression to block", "package app\n\nfun |a(): Int = 1\n", "Convert to block body",
			"package app\n\nfun a(): Int {\n    return 1\n}\n"},
		{"indented member", "package app\n\nclass C {\n    fun |a(): Int = 1\n}\n", "Convert to block body",
			"package app\n\nclass C {\n    fun a(): Int {\n        return 1\n    }\n}\n"},
		{"inferred type written out", "package app\n\nfun |a() = \"x\"\n", "Convert to block body",
			"package app\n\nfun a(): String {\n    return \"x\"\n}\n"},
		{"inferred type imported", "package app\n\nimport lib.price\n\nfun |a() = price()\n", "Convert to block body",
			"package app\n\nimport lib.price\nimport lib.Money\n\nfun a(): Money {\n    return price()\n}\n"},
		{"unknown type: not offered", "package app\n\nfun |a() = mystery()\n", "Convert to block body", ""},
		{"declared Unit", "package app\n\nfun |a(): Unit = println(\"x\")\n", "Convert to block body",
			"package app\n\nfun a(): Unit {\n    println(\"x\")\n}\n"},
	}, refactorLib)
}

func TestSpecifyType(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"literal", "package app\n\nval |x = 1\n", "Specify type explicitly", "package app\n\nval x: Int = 1\n"},
		{"import added", "package app\n\nimport lib.price\n\nfun f() {\n    val |m = price()\n}\n", "Specify type explicitly",
			"package app\n\nimport lib.price\nimport lib.Money\n\nfun f() {\n    val m: Money = price()\n}\n"},
		{"declared nullable", "package app\n\nimport lib.maybe\n\nval |m = maybe()\n", "Specify type explicitly",
			"package app\n\nimport lib.maybe\nimport lib.Money\n\nval m: Money? = maybe()\n"},
		{"firstOrNull is nullable", "package app\n\nimport lib.Money\n\nfun f(xs: List<Money>) {\n    val |m = xs.firstOrNull()\n}\n", "Specify type explicitly",
			"package app\n\nimport lib.Money\n\nfun f(xs: List<Money>) {\n    val m: Money? = xs.firstOrNull()\n}\n"},
		{"safe call is nullable", "package app\n\nimport lib.Money\n\nfun f(m: Money?) {\n    val |c = m?.cents\n}\n", "Specify type explicitly",
			"package app\n\nimport lib.Money\n\nfun f(m: Money?) {\n    val c: Long? = m?.cents\n}\n"},
		{"!! is not", "package app\n\nimport lib.Money\n\nfun f(xs: List<Money>) {\n    val |m = xs.firstOrNull()!!\n}\n", "Specify type explicitly",
			"package app\n\nimport lib.Money\n\nfun f(xs: List<Money>) {\n    val m: Money = xs.firstOrNull()!!\n}\n"},
		{"map get is nullable", "package app\n\nfun f(m: Map<String, Int>) {\n    val |v = m[\"k\"]\n}\n", "Specify type explicitly",
			"package app\n\nfun f(m: Map<String, Int>) {\n    val v: Int? = m[\"k\"]\n}\n"},
		{"generic without arguments: not offered", "package app\n\nimport lib.Box\n\nval |b = Box(1)\n", "Specify type explicitly", ""},
		{"already typed", "package app\n\nval |x: Int = 1\n", "Specify type explicitly", ""},
		{"unknown", "package app\n\nval |x = mystery()\n", "Specify type explicitly", ""},
		{"function return", "package app\n\nfun |f() = 1L\n", "Specify return type explicitly", "package app\n\nfun f(): Long = 1L\n"},
	}, refactorLib)
}

func TestBraces(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"add to if/else", "package app\n\nfun f(x: Boolean) {\n    |if (x) a() else b()\n}\n", "Add braces to `if` statement",
			"package app\n\nfun f(x: Boolean) {\n    if (x) {\n        a()\n    } else {\n        b()\n    }\n}\n"},
		{"else if kept", "package app\n\nfun f(x: Int) {\n    |if (x == 1) a() else if (x == 2) b()\n}\n", "Add braces to `if` statement",
			"package app\n\nfun f(x: Int) {\n    if (x == 1) {\n        a()\n    } else if (x == 2) b()\n}\n"},
		{"remove from if/else", "package app\n\nfun f(x: Boolean) {\n    |if (x) {\n        a()\n    } else {\n        b()\n    }\n}\n", "Remove braces from `if` statement",
			"package app\n\nfun f(x: Boolean) {\n    if (x) a() else b()\n}\n"},
		{"dangling else guard", "package app\n\nfun f(x: Boolean, y: Boolean) {\n    |if (x) {\n        if (y) a()\n    } else {\n        b()\n    }\n}\n", "Remove braces from `if` statement",
			"package app\n\nfun f(x: Boolean, y: Boolean) {\n    if (x) {\n        if (y) a()\n    } else b()\n}\n"},
		{"declaration keeps braces", "package app\n\nfun f(x: Boolean) {\n    |if (x) {\n        val y = 1\n    }\n}\n", "Remove braces from `if` statement", ""},
		{"for loop", "package app\n\nfun f() {\n    |for (i in 0..3) println(i)\n}\n", "Add braces to `for` loop",
			"package app\n\nfun f() {\n    for (i in 0..3) {\n        println(i)\n    }\n}\n"},
		{"when branch", "package app\n\nfun f(x: Int) = when (x) {\n    |1 -> { \"one\" }\n    else -> \"many\"\n}\n", "Remove braces from `when` branch",
			"package app\n\nfun f(x: Int) = when (x) {\n    1 -> \"one\"\n    else -> \"many\"\n}\n"},
	})
}

func TestStringTemplate(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"identifiers and expressions", "package app\n\nfun f(name: String, n: Int) = |\"Hi \" + name + \"! You have \" + n + \" items, \" + name.length\n", "Convert concatenation to string template",
			"package app\n\nfun f(name: String, n: Int) = \"Hi $name! You have $n items, ${name.length}\"\n"},
		{"identifier before letters", "package app\n\nfun f(a: String) = |\"x\" + a + \"b\"\n", "Convert concatenation to string template",
			"package app\n\nfun f(a: String) = \"x${a}b\"\n"},
		{"digits inlined", "package app\n\nval s = |\"v\" + 2\n", "Convert concatenation to string template",
			"package app\n\nval s = \"v2\"\n"},
		{"trailing dollar escaped", "package app\n\nfun f(a: String) = |\"cost: $\" + a\n", "Convert concatenation to string template",
			"package app\n\nfun f(a: String) = \"cost: \\$$a\"\n"},
		{"string variable first", "package app\n\nfun f(a: String, b: Int) = |a + \" and \" + b\n", "Convert concatenation to string template",
			"package app\n\nfun f(a: String, b: Int) = \"$a and $b\"\n"},
		{"numbers first: not a string", "package app\n\nval s = |1 + 2 + \"x\"\n", "Convert concatenation to string template", ""},
		{"minus: not offered", "package app\n\nfun f(a: Int) = |\"x\" + a - 1\n", "Convert concatenation to string template", ""},
	})
}
