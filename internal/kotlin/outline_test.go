package kotlin

import (
	"slices"
	"strings"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

func TestDocumentSymbols(t *testing.T) {
	f, _ := parseOne(t, "/w/O.kt", `package p

class Shape(val sides: Int) {
    fun area(): Double = 0.0

    companion object {
        fun unit() = Shape(1)
    }

    enum class Kind { A, B }
}

fun top() {}
`)
	var render func(syms []protocol.DocumentSymbol, depth int) []string
	render = func(syms []protocol.DocumentSymbol, depth int) []string {
		var out []string
		for _, s := range syms {
			out = append(out, strings.Repeat("  ", depth)+s.Name)
			out = append(out, render(s.Children, depth+1)...)
		}
		return out
	}
	got := render(DocumentSymbols(f.Summary), 0)
	want := []string{"Shape", "  sides", "  area", "  Companion", "    unit", "  Kind", "    A", "    B", "top"}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWorkspaceSymbols(t *testing.T) {
	_, ix := parseOne(t, "/w/W.kt", "package p\n\nclass UserService {\n    fun findUser() {}\n}\n\nclass Unrelated\n")
	var got []string
	for _, s := range WorkspaceSymbols(ix, "us") {
		got = append(got, s.Name+"@"+s.ContainerName)
	}
	if len(got) == 0 || got[0] != "UserService@p" || !slices.Contains(got, "findUser@p.UserService") || slices.Contains(got, "Unrelated@p") {
		t.Errorf("got %v", got)
	}
	// Empty query: types only.
	for _, s := range WorkspaceSymbols(ix, "") {
		if s.Name == "findUser" {
			t.Errorf("empty query listed a function")
		}
	}
}
