package kotlin

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// maxTestTargets caps the go-to-test actions offered.
const maxTestTargets = 5

// testSuffixes and testPrefixes are the naming conventions for test
// classes: FooTest, FooTests, FooIT, FooIntegrationTest, FooSpec, TestFoo.
var (
	testSuffixes = []string{"IntegrationTest", "Tests", "Test", "IT", "Spec"}
	testPrefixes = []string{"Test"}
)

// testNavigation offers to go from a class to its tests, or from a test
// class to the class it tests. The class is the top-level class around
// offset, or else the one named like the file.
func (r *resolver) testNavigation(offset int) []Action {
	subject := r.subjectClass(offset)
	if subject == nil {
		return nil
	}
	var targets []*Symbol
	var title string
	if tested := testedName(subject.Name); tested != "" && isTestPath(subject.Path) {
		targets = r.rankTestTargets(subject, []string{tested}, false)
		title = "Go to tested class `%s`"
	} else {
		var names []string
		for _, s := range testSuffixes {
			names = append(names, subject.Name+s)
		}
		for _, p := range testPrefixes {
			names = append(names, p+subject.Name)
		}
		targets = r.rankTestTargets(subject, names, true)
		title = "Go to test `%s`"
	}
	var out []Action
	for _, t := range targets[:min(len(targets), maxTestTargets)] {
		name := t.Name
		if len(targets) > 1 {
			name = t.FQName
		}
		loc := t.Location()
		out = append(out, Action{Title: fmt.Sprintf(title, name), Kind: protocol.Source, Open: &loc})
	}
	return out
}

// subjectClass returns the top-level class or object around offset, or
// the file's class named like the file.
func (r *resolver) subjectClass(offset int) *Symbol {
	if r.f.Summary == nil {
		return nil
	}
	pos, err := r.f.Mapper.OffsetPosition(offset)
	if err != nil {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(r.f.Path), filepath.Ext(r.f.Path))
	var named, first *Symbol
	for _, s := range r.f.Summary.Symbols {
		if s.Container != "" || !s.Kind.IsType() || s.Kind == KindTypeAlias {
			continue
		}
		if rangeContains(s.Range, pos) {
			return s
		}
		if s.Name == base && named == nil {
			named = s
		}
		if first == nil {
			first = s
		}
	}
	if named != nil {
		return named
	}
	return first
}

func rangeContains(r protocol.Range, p protocol.Position) bool {
	before := func(a, b protocol.Position) bool {
		return a.Line < b.Line || a.Line == b.Line && a.Character <= b.Character
	}
	return before(r.Start, p) && before(p, r.End)
}

// testedName returns the class name a test class name refers to, or "".
func testedName(name string) string {
	for _, s := range testSuffixes {
		if t, ok := strings.CutSuffix(name, s); ok && t != "" {
			return t
		}
	}
	for _, p := range testPrefixes {
		if t, ok := strings.CutPrefix(name, p); ok && t != "" && strings.ToUpper(t[:1]) == t[:1] {
			return t
		}
	}
	return ""
}

// isTestPath reports whether path is in a test source set or named like
// a test.
func isTestPath(path string) bool {
	p := filepath.ToSlash(path)
	for _, dir := range []string{"/test/", "/androidTest/", "/testFixtures/", "/integrationTest/", "/it/", "Test/kotlin/"} {
		if strings.Contains(p, dir) {
			return true
		}
	}
	return testedName(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))) != ""
}

// rankTestTargets returns the top-level types with one of names, best
// first: same package, then in (or, wantTests false, outside) a test
// source set, then by name order and path.
func (r *resolver) rankTestTargets(subject *Symbol, names []string, wantTests bool) []*Symbol {
	pkg := packageOf(r.ix, subject)
	type cand struct {
		s     *Symbol
		score int
	}
	var cands []cand
	for i, n := range names {
		for _, s := range r.ix.ByName(n) {
			if s.Container != "" || !s.Kind.IsType() || s == subject {
				continue
			}
			score := i
			if packageOf(r.ix, s) != pkg {
				score += 100
			}
			if isTestPath(s.Path) != wantTests {
				score += 1000
			}
			cands = append(cands, cand{s, score})
		}
	}
	slices.SortFunc(cands, func(a, b cand) int {
		return cmp.Or(cmp.Compare(a.score, b.score), cmp.Compare(a.s.Path, b.s.Path))
	})
	out := make([]*Symbol, len(cands))
	for i, c := range cands {
		out[i] = c.s
	}
	return out
}
