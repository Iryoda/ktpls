package kotlin

import (
	"slices"
	"strings"
	"testing"
)

func TestGoToTest(t *testing.T) {
	ix := NewIndex()
	files := map[string]*ParsedFile{}
	for path, src := range map[string]string{
		"/w/src/main/kotlin/shop/PriceService.kt":      "package shop\n\nclass PriceService {\n    fun price() = 1\n}\n\nclass Helper\n",
		"/w/src/test/kotlin/shop/PriceServiceTest.kt":  "package shop\n\nclass PriceServiceTest {\n    fun works() {}\n}\n",
		"/w/src/test/kotlin/shop/PriceServiceIT.kt":    "package shop\n\nclass PriceServiceIT\n",
		"/w/src/test/kotlin/other/PriceServiceTest.kt": "package other\n\nclass PriceServiceTest\n",
		"/w/src/main/kotlin/shop/Untested.kt":          "package shop\n\nclass Untested\n",
	} {
		f, _ := parseOne(t, path, src)
		files[path] = f
		ix.Update(f.Summary)
	}
	titles := func(path, needle string) []string {
		f := files[path]
		var out []string
		for _, a := range CodeActions(f, ix, strings.Index(string(f.Content), needle)) {
			if a.Open != nil {
				out = append(out, a.Title)
			}
		}
		return out
	}
	// Same package first, integration tests too, other packages last.
	got := titles("/w/src/main/kotlin/shop/PriceService.kt", "price()")
	want := []string{
		"Go to test `shop.PriceServiceTest`",
		"Go to test `shop.PriceServiceIT`",
		"Go to test `other.PriceServiceTest`",
	}
	if !slices.Equal(got, want) {
		t.Errorf("from the class:\n got  %v\n want %v", got, want)
	}
	// Back from the test to the tested class.
	if got := titles("/w/src/test/kotlin/shop/PriceServiceTest.kt", "works"); !slices.Equal(got, []string{"Go to tested class `PriceService`"}) {
		t.Errorf("from the test: %v", got)
	}
	// A second class in the file: its own tests (none).
	if got := titles("/w/src/main/kotlin/shop/PriceService.kt", "Helper"); len(got) != 0 {
		t.Errorf("Helper: %v", got)
	}
	if got := titles("/w/src/main/kotlin/shop/Untested.kt", "class"); len(got) != 0 {
		t.Errorf("no tests: %v", got)
	}
	// The action opens the test class's name.
	f := files["/w/src/main/kotlin/shop/PriceService.kt"]
	for _, a := range CodeActions(f, ix, strings.Index(string(f.Content), "price()")) {
		if a.Open != nil {
			if path, _ := a.Open.URI.Path(); path != "/w/src/test/kotlin/shop/PriceServiceTest.kt" || a.Open.Range.Start.Line != 2 {
				t.Errorf("opens %s:%d", path, a.Open.Range.Start.Line)
			}
			break
		}
	}
}
