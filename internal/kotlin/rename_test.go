package kotlin

import (
	"slices"
	"strings"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

var renameFiles = map[string]string{
	"/w/shapes/Shape.kt": `package shapes

interface Shape {
    fun area(): Double
}

class Square(val side: Double) : Shape {
    override fun area() = side * side
}

class Circle(val r: Double) : Shape {
    override fun area() = 3.14 * r * r
}

fun format(label: String, value: Double) = "$label: ${value}"
`,
	"/w/app/App.kt": `package app

import shapes.Circle
import shapes.Shape
import shapes.Square
import shapes.format as fmt

fun total(shapes: List<Shape>): Double {
    val sq = Square(2.0)
    val sum = shapes.sumOf { it.area() } + sq.area() + Circle(1.0).area()
    println(fmt(label = "total", value = sum))
    return sum
}
`,
}

// renameWorkspace parses renameFiles and returns them with an index.
func renameWorkspace(t *testing.T) (map[string]*ParsedFile, *Index) {
	t.Helper()
	files := map[string]*ParsedFile{}
	ix := NewIndex()
	for path, src := range renameFiles {
		f, _ := parseOne(t, path, src)
		files[path] = f
		ix.Update(f.Summary)
	}
	return files, ix
}

// doRename renames the identifier at needle in path and returns the
// edited sources.
func doRename(t *testing.T, path, needle, newName string) (map[string]string, error) {
	t.Helper()
	files, ix := renameWorkspace(t)
	f := files[path]
	i := strings.Index(string(f.Content), needle)
	if i < 0 {
		t.Fatalf("%q not found", needle)
	}
	changes, err := Rename(f, ix, i, newName, filesOf(files))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for p, pf := range files {
		src := string(pf.Content)
		edits := slices.Clone(changes[pf.URI])
		slices.SortFunc(edits, func(a, b protocol.TextEdit) int {
			return pf.Mapper.PositionOffset(b.Range.Start) - pf.Mapper.PositionOffset(a.Range.Start)
		})
		for _, e := range edits {
			s, en := pf.Mapper.RangeOffsets(e.Range)
			src = src[:s] + e.NewText + src[en:]
		}
		out[p] = src
	}
	return out, nil
}

func TestRenameOverrideFamily(t *testing.T) {
	// From the interface, an override, or a call site: the whole family.
	for _, start := range []struct{ path, needle string }{
		{"/w/shapes/Shape.kt", "area(): Double"},
		{"/w/shapes/Shape.kt", "area() = side"},
		{"/w/app/App.kt", "area() }"},
	} {
		got, err := doRename(t, start.path, start.needle, "surface")
		if err != nil {
			t.Fatalf("%v: %v", start, err)
		}
		if strings.Contains(got["/w/shapes/Shape.kt"], "area") || strings.Contains(got["/w/app/App.kt"], "area") {
			t.Errorf("from %q: area left behind:\n%s\n%s", start.needle, got["/w/shapes/Shape.kt"], got["/w/app/App.kt"])
		}
		if n := strings.Count(got["/w/shapes/Shape.kt"], "surface()"); n != 3 {
			t.Errorf("from %q: %d declarations renamed, want 3", start.needle, n)
		}
		if n := strings.Count(got["/w/app/App.kt"], "surface()"); n != 3 {
			t.Errorf("from %q: %d uses renamed, want 3", start.needle, n)
		}
	}
}

func TestRenameKeepsImportAlias(t *testing.T) {
	got, err := doRename(t, "/w/shapes/Shape.kt", "format(", "render")
	if err != nil {
		t.Fatal(err)
	}
	app := got["/w/app/App.kt"]
	if !strings.Contains(app, "import shapes.render as fmt") || !strings.Contains(app, "println(fmt(") {
		t.Errorf("alias not kept:\n%s", app)
	}
	if !strings.Contains(got["/w/shapes/Shape.kt"], "fun render(") {
		t.Errorf("declaration not renamed")
	}
}

func TestRenameParameterAndNamedArguments(t *testing.T) {
	got, err := doRename(t, "/w/shapes/Shape.kt", "label: String", "title")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got["/w/shapes/Shape.kt"], `fun format(title: String, value: Double) = "$title: ${value}"`) {
		t.Errorf("declaration and template:\n%s", got["/w/shapes/Shape.kt"])
	}
	if !strings.Contains(got["/w/app/App.kt"], `fmt(title = "total", value = sum)`) {
		t.Errorf("named argument:\n%s", got["/w/app/App.kt"])
	}
}

func TestRenameLocal(t *testing.T) {
	got, err := doRename(t, "/w/app/App.kt", "sum =", "grand")
	if err != nil {
		t.Fatal(err)
	}
	app := got["/w/app/App.kt"]
	if strings.Contains(app, "sum ") || strings.Count(app, "grand") != 3 || !strings.Contains(app, "shapes.sumOf") {
		t.Errorf("local rename:\n%s", app)
	}
}

func TestRenameRefusals(t *testing.T) {
	for _, tt := range []struct{ needle, newName, wantErr string }{
		{"Square(2.0)", "class", "not a valid"},
		{"Square(2.0)", "2x", "not a valid"},
		{"println", "log", "library"},
		{"it.area", "x", "can't be renamed"},
	} {
		_, err := doRename(t, "/w/app/App.kt", tt.needle, tt.newName)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s -> %s: got %v, want error containing %q", tt.needle, tt.newName, err, tt.wantErr)
		}
	}
	// A class rename also renames its constructor calls and imports.
	got, err := doRename(t, "/w/shapes/Shape.kt", "Square(val", "Box")
	if err != nil {
		t.Fatal(err)
	}
	if app := got["/w/app/App.kt"]; !strings.Contains(app, "import shapes.Box") || !strings.Contains(app, "Box(2.0)") {
		t.Errorf("class rename:\n%s", app)
	}
}

func TestPrepareRename(t *testing.T) {
	files, ix := renameWorkspace(t)
	f := files["/w/app/App.kt"]
	src := string(f.Content)
	rng, err := PrepareRename(f, ix, strings.Index(src, "sq.area")+1)
	if err != nil {
		t.Fatal(err)
	}
	if s, e := f.Mapper.RangeOffsets(rng); src[s:e] != "sq" {
		t.Errorf("range covers %q", src[s:e])
	}
	if _, err := PrepareRename(f, ix, strings.Index(src, "println")); err == nil {
		t.Error("library function: expected refusal")
	}
}
