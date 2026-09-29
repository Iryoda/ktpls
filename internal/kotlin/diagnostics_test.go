package kotlin

import (
	"strings"
	"testing"
)

func TestSyntaxErrors(t *testing.T) {
	f, _ := parseOne(t, "/w/E.kt", "package p\n\nfun ok() = 1\n\nfun broken( {\n    val x = 1\n}\n")
	errs := SyntaxErrors(f)
	if len(errs) == 0 {
		t.Fatal("no errors reported")
	}
	for _, e := range errs {
		if e.Range.Start.Line != e.Range.End.Line {
			t.Errorf("error spans lines: %+v", e)
		}
		if !strings.HasPrefix(e.Message, "syntax error") {
			t.Errorf("message %q", e.Message)
		}
	}
	clean, _ := parseOne(t, "/w/C.kt", "package p\n\nclass C {\n    fun a() = 1\n}\n")
	if errs := SyntaxErrors(clean); len(errs) != 0 {
		t.Errorf("clean file: %+v", errs)
	}
}

func TestNewSyntaxErrors(t *testing.T) {
	// An error present when the file was opened (standing in for code the
	// grammar can't parse) is suppressed; an error typed later is not,
	// even though the edit moves the old one down.
	opened := "package p\n\nfun old( {\n}\n"
	f, _ := parseOne(t, "/w/B.kt", opened)
	baseline := ErrorKeys(SyntaxErrors(f))
	if len(baseline) == 0 {
		t.Fatal("baseline has no errors")
	}
	if again := NewSyntaxErrors(SyntaxErrors(f), baseline); len(again) != 0 {
		t.Errorf("unchanged file reported %+v", again)
	}
	edited := "package p\n\nval typing = listOf(1,\n\nfun old( {\n}\n"
	g, _ := parseOne(t, "/w/B.kt", edited)
	got := NewSyntaxErrors(SyntaxErrors(g), baseline)
	if len(got) == 0 {
		t.Fatal("new error suppressed")
	}
}

func TestOneLineBodyGapHasNoVisibleError(t *testing.T) {
	// The grammar fails on one-line class bodies (valid Kotlin) through a
	// hidden missing token: no diagnostic may be reported for it.
	f, _ := parseOne(t, "/w/B.kt", "package p\n\nclass Holder { fun get() = 1 }\n")
	if errs := SyntaxErrors(f); len(errs) != 0 {
		t.Errorf("reported %+v", errs)
	}
}
