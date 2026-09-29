package kotlin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// Marker tests: fixture files under testdata/<dir> annotate declarations
// with /*@def(label)*/ and uses with /*@ref(label)*/, placed immediately
// before the identifier. A marker may carry several comma-separated
// labels. Each ref must resolve to exactly the set of defs sharing one of
// its labels.
var markerRE = regexp.MustCompile(`/\*@(def|ref)\(([^)]*)\)\*/`)

type markerPos struct {
	path   string
	offset int
}

func (m markerPos) String() string { return fmt.Sprintf("%s@%d", filepath.Base(m.path), m.offset) }

type refMarker struct {
	markerPos
	labels []string
}

type workspace struct {
	files map[string]*ParsedFile // by path
	ix    *Index
	defs  map[string][]markerPos
	refs  []refMarker
}

func loadWorkspace(t *testing.T, dir string) *workspace {
	t.Helper()
	ws := &workspace{files: map[string]*ParsedFile{}, ix: NewIndex(), defs: map[string][]markerPos{}}
	dir, err := filepath.Abs(dir) // URIs need absolute paths
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".kt" {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree := Parse(src)
		t.Cleanup(tree.Close)
		if tree.RootNode().HasError() {
			t.Errorf("%s: fixture has syntax errors: %s", path, tree.RootNode().ToSexp())
		}
		m := protocol.NewMapper(src, protocol.PositionEncodingUTF16)
		f := &ParsedFile{Path: path, URI: protocol.URIFromPath(path), Content: src, Tree: tree, Mapper: m}
		f.Summary = Extract(path, src, tree, m)
		ws.files[path] = f
		ws.ix.Update(f.Summary)

		for _, loc := range markerRE.FindAllSubmatchIndex(src, -1) {
			kind := string(src[loc[2]:loc[3]])
			labels := strings.Split(string(src[loc[4]:loc[5]]), ",")
			pos := markerPos{path, identStart(src, loc[1])}
			if kind == "def" {
				for _, l := range labels {
					ws.defs[l] = append(ws.defs[l], pos)
				}
			} else {
				ws.refs = append(ws.refs, refMarker{pos, labels})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// identStart returns the offset of the identifier following a marker that
// ends at off, skipping further markers.
func identStart(src []byte, off int) int {
	for {
		rest := src[off:]
		if loc := markerRE.FindIndex(rest); loc != nil && loc[0] == 0 {
			off += loc[1]
			continue
		}
		return off
	}
}

func (ws *workspace) offsetOf(loc protocol.Location) markerPos {
	path, _ := loc.URI.Path()
	f := ws.files[path]
	if f == nil {
		return markerPos{path, -1}
	}
	return markerPos{path, f.Mapper.PositionOffset(loc.Range.Start)}
}

func TestDefinitionMarkers(t *testing.T) {
	ws := loadWorkspace(t, "testdata/definition")
	if len(ws.refs) == 0 {
		t.Fatal("no ref markers found")
	}
	for _, ref := range ws.refs {
		var want []markerPos
		for _, l := range ref.labels {
			if l == "" {
				continue // /*@ref()*/: expect no definition
			}
			if len(ws.defs[l]) == 0 {
				t.Errorf("%v: no def for label %q", ref, l)
			}
			want = append(want, ws.defs[l]...)
		}
		f := ws.files[ref.path]
		var got []markerPos
		for _, loc := range Definition(f, ws.ix, ref.offset) {
			got = append(got, ws.offsetOf(loc))
		}
		if !sameMarkers(got, want) {
			line, _ := f.Mapper.OffsetPosition(ref.offset)
			t.Errorf("ref %v (line %d, %q): got %v, want %v", ref.labels, line.Line+1, identAt(f, ref.offset), got, want)
		}
	}
}

func identAt(f *ParsedFile, off int) string {
	if n := IdentifierAt(f.Tree, off); n != nil {
		return text(n, f.Content)
	}
	return "<no identifier>"
}

// sameMarkers compares a and b as sets.
func sameMarkers(a, b []markerPos) bool {
	set := func(ms []markerPos) []string {
		out := make([]string, len(ms))
		for i, m := range ms {
			out[i] = m.String()
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(set(a), set(b))
}

// parseOne parses a single synthetic file for unit tests.
func parseOne(t *testing.T, path, src string) (*ParsedFile, *Index) {
	t.Helper()
	b := []byte(src)
	tree := Parse(b)
	t.Cleanup(tree.Close)
	m := protocol.NewMapper(b, protocol.PositionEncodingUTF16)
	f := &ParsedFile{Path: path, URI: protocol.URIFromPath(path), Content: b, Tree: tree, Mapper: m}
	f.Summary = Extract(path, b, tree, m)
	ix := NewIndex()
	ix.Update(f.Summary)
	return f, ix
}

// defAt resolves the identifier at the first occurrence of needle
// (offset by delta bytes) and returns the source text at each result.
func defAt(t *testing.T, f *ParsedFile, ix *Index, needle string, delta int) []string {
	t.Helper()
	i := strings.Index(string(f.Content), needle)
	if i < 0 {
		t.Fatalf("%q not found", needle)
	}
	var out []string
	for _, loc := range Definition(f, ix, i+delta) {
		start, end := f.Mapper.RangeOffsets(loc.Range)
		line, _ := f.Mapper.OffsetPosition(start)
		out = append(out, fmt.Sprintf("%d:%s", line.Line+1, f.Content[start:end]))
	}
	return out
}

func TestDefinitionStringTemplate(t *testing.T) {
	f, ix := parseOne(t, "/w/T.kt", `package p

fun greet(name: String): String {
    return "Hi $name, ${name.length}"
}
`)
	for _, needle := range []string{"$name", "${name"} {
		delta := strings.Index(needle, "n")
		if got := defAt(t, f, ix, needle, delta); !slices.Equal(got, []string{"3:name"}) {
			t.Errorf("%s: got %v", needle, got)
		}
	}
}

func TestDefinitionImportSegments(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "Lib.kt")
	use := filepath.Join(root, "Use.kt")
	ix := NewIndex()
	var files []*ParsedFile
	for path, src := range map[string]string{
		lib: "package a.b\n\nclass Thing {\n    fun act() {}\n}\n",
		use: "package c\n\nimport a.b.Thing\n\nfun f() = Thing().act()\n",
	} {
		b := []byte(src)
		tree := Parse(b)
		t.Cleanup(tree.Close)
		m := protocol.NewMapper(b, protocol.PositionEncodingUTF16)
		pf := &ParsedFile{Path: path, URI: protocol.URIFromPath(path), Content: b, Tree: tree, Mapper: m}
		pf.Summary = Extract(path, b, tree, m)
		ix.Update(pf.Summary)
		if path == use {
			files = append(files, pf)
		}
	}
	f := files[0]
	src := string(f.Content)
	check := func(off int, wantLine uint32) {
		t.Helper()
		locs := Definition(f, ix, off)
		if len(locs) != 1 || locs[0].URI != protocol.URIFromPath(lib) || locs[0].Range.Start.Line != wantLine {
			t.Errorf("offset %d (%q): got %v", off, src[off:off+5], locs)
		}
	}
	check(strings.Index(src, "Thing\n"), 2) // import segment -> class
	check(strings.Index(src, "Thing()"), 2) // constructor call -> class
	check(strings.Index(src, "act()"), 3)   // member via constructor-call receiver
	if locs := Definition(f, ix, strings.Index(src, "b.Thing")); len(locs) != 0 {
		t.Errorf("package segment: got %v, want none", locs)
	}
}

func TestExtractMisparsedOneLineObject(t *testing.T) {
	// Known grammar failure: one-line object bodies with a member parse as
	// an infix expression. Extraction recovers the object and its member.
	f, _ := parseOne(t, "/w/O.kt", "package p\n\nobject Registry { fun lookup() = 1 }\n")
	var got []string
	for _, s := range f.Summary.Symbols {
		got = append(got, s.Kind.String()+" "+s.FQName)
	}
	want := []string{"object p.Registry", "fun p.Registry.lookup"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractInsideErrorNodes(t *testing.T) {
	// One-line class bodies put members in ERROR nodes; they must still be
	// indexed with the right container.
	f, _ := parseOne(t, "/w/E.kt", "package p\n\nclass Holder { fun get() = 1 }\n\nfun after() {}\n")
	if !f.Tree.RootNode().HasError() {
		t.Log("grammar parsed the one-line body cleanly; test is moot but harmless")
	}
	var names []string
	for _, s := range f.Summary.Symbols {
		names = append(names, s.FQName)
	}
	for _, want := range []string{"p.Holder", "p.after"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %s in %v", want, names)
		}
	}
}

func TestExtractWhileTyping(t *testing.T) {
	// Half-typed code: recovery flattens broken classes into ERROR nodes.
	for _, tt := range []struct {
		src  string
		want []string
	}{
		{"package p\n\nclass Edited {", []string{"class p.Edited"}},
		{
			"package p\n\nclass A {\n    fun ok() = 1\n    fun broken( {\n}\n\nfun after() = 2\n",
			[]string{"class p.A", "fun p.A.ok", "fun p.A.broken", "fun p.after"},
		},
		{
			"package p\n\nclass A {\n    fun ok() = 1\n    val x = \n}\n\nfun after() = 2\n",
			[]string{"class p.A", "fun p.A.ok", "property p.A.x", "fun p.after"},
		},
	} {
		f, _ := parseOne(t, "/w/T.kt", tt.src)
		var got []string
		for _, s := range f.Summary.Symbols {
			got = append(got, s.Kind.String()+" "+s.FQName)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q:\n got  %v\n want %v", tt.src, got, tt.want)
		}
	}
}
