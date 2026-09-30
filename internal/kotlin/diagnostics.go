package kotlin

import (
	"fmt"
	"maps"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// A SyntaxError is a region the parser couldn't make sense of.
type SyntaxError struct {
	Range   protocol.Range
	Message string
	// Key identifies the error independently of its position (kind and
	// text), so an error can be matched across edits that move it.
	Key string
}

// maxKeyText bounds the source text included in an error's key.
const maxKeyText = 120

// SyntaxErrors returns the syntax errors in f's tree. An ERROR region is
// reported from its start to the end of its first line, so that recovery
// wrapping a whole declaration doesn't underline all of it; a missing
// token is reported where it was expected.
func SyntaxErrors(f *ParsedFile) []SyntaxError {
	var out []SyntaxError
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if !n.HasError() {
			return
		}
		switch {
		case n.IsMissing():
			off := int(n.StartByte())
			pos, _ := f.Mapper.OffsetPosition(off)
			out = append(out, SyntaxError{
				Range:   protocol.Range{Start: pos, End: pos},
				Message: fmt.Sprintf("syntax error: missing `%s`", n.Kind()),
				Key:     "missing:" + n.Kind() + ":" + contextKey(f.Content, off),
			})
			return
		case n.IsError():
			start, end := int(n.StartByte()), int(n.EndByte())
			lineEnd := start
			for lineEnd < end && f.Content[lineEnd] != '\n' {
				lineEnd++
			}
			rng, _ := f.Mapper.OffsetRange(start, max(lineEnd, start))
			msg := "syntax error"
			if first := firstToken(n); first != nil && first.EndByte() > first.StartByte() {
				msg = fmt.Sprintf("syntax error near `%s`", textutil.Truncate(textutil.CollapseSpace(text(first, f.Content)), 30))
			}
			out = append(out, SyntaxError{
				Range:   rng,
				Message: msg,
				Key:     "error:" + textutil.Truncate(textutil.CollapseSpace(string(f.Content[start:end])), maxKeyText),
			})
			return // one report per region
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(f.Tree.RootNode())
	return out
}

// contextKey describes the surroundings of a zero-width error.
func contextKey(src []byte, off int) string {
	lo, hi := max(0, off-40), min(len(src), off+40)
	return textutil.CollapseSpace(string(src[lo:hi]))
}

func firstToken(n *ts.Node) *ts.Node {
	for n.ChildCount() > 0 {
		n = n.Child(0)
	}
	return n
}

// NewSyntaxErrors returns the errors in current that are not in baseline
// (as counted by key): the ones introduced since the baseline was taken.
func NewSyntaxErrors(current []SyntaxError, baseline map[string]int) []SyntaxError {
	left := make(map[string]int, len(baseline))
	maps.Copy(left, baseline)
	var out []SyntaxError
	for _, e := range current {
		if left[e.Key] > 0 {
			left[e.Key]--
			continue
		}
		out = append(out, e)
	}
	return out
}

// ErrorKeys counts the keys of errs.
func ErrorKeys(errs []SyntaxError) map[string]int {
	m := map[string]int{}
	for _, e := range errs {
		m[e.Key]++
	}
	return m
}
