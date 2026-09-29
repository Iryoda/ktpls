package kotlin

import (
	"slices"
	"strings"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

const codeActionSrc = `package p

fun somethingFunction(name: String, other1: List<String>) {}

fun log(level: Int, vararg parts: String) {}

fun overloaded(a: Int) {}
fun overloaded(b: String) {}

fun run(times: Int, block: () -> Unit) {}

class Box(val width: Int, val height: Int)

fun use(anyName: String, otherThings: List<String>, arr: Array<String>) {
    somethingFunction(anyName, otherThings)
    somethingFunction(anyName, other1 = otherThings)
    somethingFunction(name = anyName, other1 = otherThings)
    log(1, "a", "b")
    log(1, *arr)
    overloaded(1)
    run(3) { println() }
    Box(1, 2)
    somethingFunction(anyName, otherThings, 3)
}
`

// apply applies the edits of the first action at the call containing
// needle and returns the edited line.
func applyAction(t *testing.T, f *ParsedFile, ix *Index, needle string) (string, []Action) {
	t.Helper()
	i := strings.Index(string(f.Content), needle)
	if i < 0 {
		t.Fatalf("%q not found", needle)
	}
	actions := CodeActions(f, ix, i+1)
	if len(actions) == 0 {
		return "", nil
	}
	src := string(f.Content)
	edits := slices.Clone(actions[0].Edits)
	slices.SortFunc(edits, func(a, b protocol.TextEdit) int { // apply back to front
		return f.Mapper.PositionOffset(b.Range.Start) - f.Mapper.PositionOffset(a.Range.Start)
	})
	for _, e := range edits {
		at := f.Mapper.PositionOffset(e.Range.Start)
		src = src[:at] + e.NewText + src[at:]
	}
	line := src[strings.LastIndex(src[:i], "\n")+1:]
	return strings.TrimSpace(line[:strings.IndexByte(line, '\n')]), actions
}

func TestNameArguments(t *testing.T) {
	f, ix := parseOne(t, "/w/C.kt", codeActionSrc)
	for _, tt := range []struct {
		needle string
		want   string // "" = no action
	}{
		{"somethingFunction(anyName, otherThings)", "somethingFunction(name = anyName, other1 = otherThings)"},
		{"somethingFunction(anyName, other1", "somethingFunction(name = anyName, other1 = otherThings)"},
		{"somethingFunction(name = anyName", ""},         // all named already
		{"log(1, \"a\"", "log(level = 1, \"a\", \"b\")"}, // stops at vararg
		{"log(1, *arr)", "log(level = 1, *arr)"},
		{"run(3)", "run(times = 3) { println() }"}, // trailing lambda untouched
		{"Box(1, 2)", "Box(width = 1, height = 2)"},
		{"otherThings, 3)", ""}, // too many arguments
	} {
		got, _ := applyAction(t, f, ix, tt.needle)
		if got != tt.want {
			t.Errorf("%s:\n got  %q\n want %q", tt.needle, got, tt.want)
		}
	}
}

func TestNameArgumentsOverloads(t *testing.T) {
	f, ix := parseOne(t, "/w/C.kt", codeActionSrc)
	_, actions := applyAction(t, f, ix, "overloaded(1)")
	// Two overloads, two namings: one action each, told apart by signature.
	if len(actions) != 2 {
		t.Fatalf("got %d actions", len(actions))
	}
	for _, a := range actions {
		if !strings.Contains(a.Title, "(a: Int)") && !strings.Contains(a.Title, "(b: String)") {
			t.Errorf("title %q", a.Title)
		}
	}
}

func TestNameArgumentsInnermostCall(t *testing.T) {
	f, ix := parseOne(t, "/w/C.kt", "package p\n\nfun outer(x: Int) = x\nfun inner(y: Int) = y\n\nval v = outer(inner(1))\n")
	got, _ := applyAction(t, f, ix, "inner(1)")
	if got != "val v = outer(inner(y = 1))" {
		t.Errorf("got %q", got)
	}
	got, _ = applyAction(t, f, ix, "outer(inner")
	if got != "val v = outer(x = inner(1))" {
		t.Errorf("on outer: got %q", got)
	}
}

func TestNameArgumentsMemberCalls(t *testing.T) {
	src := `package p

class Service {
    fun somethingFunction(name: String, other1: List<String>) {}
    fun self() = this
}

class Controller(private val service: Service) {
    fun a(s: Service?, x: String, y: List<String>) {
        s!!.somethingFunction(x, y)
        service.somethingFunction(x, y)
        this.service.somethingFunction(x, y)
        s?.somethingFunction(x, y)
        s!!
            .somethingFunction(x, y)
        service.self().somethingFunction(x, y)
        unknownThing().somethingFunction(x, y)
    }
}
`
	f, ix := parseOne(t, "/w/M.kt", src)
	const want = ".somethingFunction(name = x, other1 = y)"
	lines := strings.Split(src, "\n")
	off, checked := 0, 0
	for i, line := range lines {
		if j := strings.Index(line, ".somethingFunction(x"); j >= 0 {
			// The cursor may be on the dot, on the name, or in the arguments.
			for _, delta := range []int{j, j + 1, strings.Index(line, "(x") + 1} {
				actions := CodeActions(f, ix, off+delta)
				if len(actions) != 1 {
					t.Errorf("line %d, column %d: %d actions", i+1, delta, len(actions))
					continue
				}
				edited := line
				for k := len(actions[0].Edits) - 1; k >= 0; k-- {
					at := int(actions[0].Edits[k].Range.Start.Character)
					edited = edited[:at] + actions[0].Edits[k].NewText + edited[at:]
				}
				if !strings.HasSuffix(edited, want) {
					t.Errorf("line %d: got %q", i+1, strings.TrimSpace(edited))
				}
				checked++
			}
		}
		off += len(line) + 1
	}
	if checked != 21 {
		t.Errorf("checked %d cursor positions, want 21", checked)
	}
}
