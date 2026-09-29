package kotlin

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestImplementation(t *testing.T) {
	f, ix := parseOne(t, "/w/I.kt", `package p

interface Shape {
    fun area(): Double
    val name: String
}

abstract class Polygon : Shape {
    override val name = "polygon"
}

class Square(val side: Double) : Polygon() {
    override fun area() = side * side
}

class Circle(override val name: String) : Shape {
    override fun area() = 3.14
}

object Nothing2 : Comparable<Int> {
    override fun compareTo(other: Int) = 0
}

fun use(s: Shape) = s.area()
`)
	for _, tt := range []struct {
		needle string
		want   []string
	}{
		{"Shape {", []string{"8:Polygon", "12:Square", "16:Circle"}}, // transitively
		{"area(): Double", []string{"13:area", "17:area"}},
		{"name: String\n}", []string{"9:name", "16:name"}},
		{"Polygon :", []string{"12:Square"}},
		{"area()\n", []string{"13:area", "17:area"}}, // from a use site
		{"Square(", nil},
	} {
		var got []string
		for _, loc := range defAtFunc(t, f, tt.needle, func(off int) []string {
			var out []string
			for _, l := range Implementation(f, ix, off) {
				start, end := f.Mapper.RangeOffsets(l.Range)
				line, _ := f.Mapper.OffsetPosition(start)
				out = append(out, itoa(int(line.Line))+":"+string(f.Content[start:end]))
			}
			return out
		}) {
			got = append(got, loc)
		}
		slices.Sort(got)
		want := slices.Clone(tt.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", tt.needle, got, want)
		}
	}
}

// defAtFunc runs fn at the first occurrence of needle.
func defAtFunc(t *testing.T, f *ParsedFile, needle string, fn func(int) []string) []string {
	t.Helper()
	i := strings.Index(string(f.Content), needle)
	if i < 0 {
		t.Fatalf("%q not found", needle)
	}
	return fn(i)
}

func itoa(n int) string { return strconv.Itoa(n + 1) }
