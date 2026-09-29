package build

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseOutput(t *testing.T) {
	out := `> Task :compileKotlin FAILED
e: file:///home/u/proj/src/main/kotlin/a/Foo.kt:12:5 Unresolved reference 'pirce'.
w: file:///home/u/proj/src/main/kotlin/a/Bar.kt:3:9 Variable 'x' is never used.
e: file:///home/u/my%20proj/B%C3%A9.kt:1:1 Argument type mismatch: actual type is 'Int', but 'String' was expected.
e: /home/u/old/Legacy.kt: (7, 14): Type mismatch: inferred type is String but Int was expected
e: java.lang.IllegalStateException: something without a location
w: file:///home/u/proj/src/main/kotlin/a/Baz.kt:4:1 This declaration needs opt-in:
    Its usage must be marked with '@ExperimentalApi'
> Task :compileTestKotlin SKIPPED

FAILURE: Build failed with an exception.
`
	want := []Message{
		{Error, "/home/u/proj/src/main/kotlin/a/Foo.kt", 12, 5, "Unresolved reference 'pirce'."},
		{Warning, "/home/u/proj/src/main/kotlin/a/Bar.kt", 3, 9, "Variable 'x' is never used."},
		{Error, "/home/u/my proj/Bé.kt", 1, 1, "Argument type mismatch: actual type is 'Int', but 'String' was expected."},
		{Error, "/home/u/old/Legacy.kt", 7, 14, "Type mismatch: inferred type is String but Int was expected"},
		{Error, "", 0, 0, "java.lang.IllegalStateException: something without a location"},
		{Warning, "/home/u/proj/src/main/kotlin/a/Baz.kt", 4, 1, "This declaration needs opt-in:\n    Its usage must be marked with '@ExperimentalApi'"},
	}
	if got := ParseOutput(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%#v\nwant\n%#v", got, want)
	}
}

func TestSummarize(t *testing.T) {
	compiled := "> Task :compileKotlin\nw: file:///p/A.kt:1:1 warn\n> Task :compileTestKotlin UP-TO-DATE\nBUILD SUCCESSFUL in 3s\n"
	if r := Summarize(compiled, nil); !r.Compiled || r.Failure != "" || len(r.Messages) != 1 {
		t.Errorf("compiled: %+v", r)
	}
	fromCache := "> Task :compileKotlin FROM-CACHE\n> Task :compileTestKotlin UP-TO-DATE\nBUILD SUCCESSFUL\n"
	if r := Summarize(fromCache, nil); !r.Compiled {
		t.Errorf("from cache not reported as compiled: errors would go stale")
	}
	upToDate := "> Task :compileKotlin UP-TO-DATE\n> Task :compileTestKotlin UP-TO-DATE\nBUILD SUCCESSFUL\n"
	if r := Summarize(upToDate, nil); r.Compiled {
		t.Errorf("up to date reported as compiled")
	}
	errs := "> Task :app:compileKotlin FAILED\ne: file:///p/A.kt:2:3 Unresolved reference 'x'.\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\nExecution failed for task ':app:compileKotlin'.\n> Compilation error. See log for more details\n\n* Try:\n"
	if r := Summarize(errs, errors.New("exit status 1")); !r.Compiled || r.Failure != "" || len(r.Messages) != 1 {
		t.Errorf("compile errors: %+v", r)
	}
	broken := "> Task :compileKotlin\n> Task :compileJava FAILED\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\nExecution failed for task ':compileJava'.\n> Java compilation initialization error\n    error: invalid source release: 25\n\n* Try:\n"
	r := Summarize(broken, errors.New("exit status 1"))
	if !r.Compiled || !strings.Contains(r.Failure, "invalid source release: 25") {
		t.Errorf("other failure: %+v", r)
	}
}
