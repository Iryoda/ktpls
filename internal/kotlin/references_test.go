package kotlin

import (
	"slices"
	"strings"
	"testing"
)

// filesOf serves a fixed set of parsed files as a FileSource.
func filesOf(files map[string]*ParsedFile) FileSource {
	return func(name string, fn func(*ParsedFile)) {
		for _, f := range files {
			if ContainsWord(f.Content, name) {
				fn(f)
			}
		}
	}
}

func TestContainsWord(t *testing.T) {
	for _, tt := range []struct {
		content, name string
		want          bool
	}{
		{"val id = 1", "id", true},
		{"valid", "id", false},
		{"idx id", "id", true},
		{"x.id()", "id", true},
		{"_id", "id", false},
		{"id", "id", true},
		{"", "id", false},
	} {
		if got := ContainsWord([]byte(tt.content), tt.name); got != tt.want {
			t.Errorf("ContainsWord(%q, %q) = %v", tt.content, tt.name, got)
		}
	}
}

// TestReferencesMarkers checks, on the definition fixtures, that the
// references of each declaration include every use marked as resolving
// to it alone.
func TestReferencesMarkers(t *testing.T) {
	ws := loadWorkspace(t, "testdata/definition")
	src := filesOf(ws.files)
	checked := 0
	for label, defs := range ws.defs {
		if len(defs) != 1 {
			continue
		}
		def := defs[0]
		var got []markerPos
		for _, loc := range References(ws.files[def.path], ws.ix, def.offset, true, src) {
			got = append(got, ws.offsetOf(loc))
		}
		if !slices.Contains(got, def) {
			t.Errorf("%s: declaration missing from %v", label, got)
		}
		for _, ref := range ws.refs {
			if len(ref.labels) == 1 && ref.labels[0] == label {
				checked++
				if !slices.Contains(got, ref.markerPos) {
					t.Errorf("%s: use %v missing from %v", label, ref.markerPos, got)
				}
			}
		}
	}
	if checked < 30 {
		t.Errorf("only %d uses checked", checked)
	}
}

func TestReferences(t *testing.T) {
	f, ix := parseOne(t, "/w/R.kt", `package p

class Counter(var count: Int) {
    fun bump() { count++ }
}

fun count(c: Counter): Int {
    val count = c.count
    c.bump()
    return count + c.count
}

fun other(count: Int) = "$count"
`)
	src := filesOf(map[string]*ParsedFile{f.Path: f})
	lines := func(off int, decl bool) []int {
		var out []int
		for _, l := range References(f, ix, off, decl, src) {
			out = append(out, int(l.Range.Start.Line)+1)
		}
		slices.Sort(out)
		return out
	}
	s := string(f.Content)
	// The property: its declaration, the use in bump, and both c.count.
	if got := lines(strings.Index(s, "count: Int)"), true); !slices.Equal(got, []int{3, 4, 8, 10}) {
		t.Errorf("property: %v", got)
	}
	if got := lines(strings.Index(s, "count: Int)"), false); !slices.Equal(got, []int{4, 8, 10}) {
		t.Errorf("property without declaration: %v", got)
	}
	// The local val: declaration and the use in return, not the property.
	if got := lines(strings.Index(s, "count = c"), true); !slices.Equal(got, []int{8, 10}) {
		t.Errorf("local: %v", got)
	}
	// The top-level function: only its declaration.
	if got := lines(strings.Index(s, "count(c"), true); !slices.Equal(got, []int{7}) {
		t.Errorf("function: %v", got)
	}
	// A parameter used in a string template.
	if got := lines(strings.Index(s, "count: Int) ="), true); !slices.Equal(got, []int{13, 13}) {
		t.Errorf("template parameter: %v", got)
	}
}
