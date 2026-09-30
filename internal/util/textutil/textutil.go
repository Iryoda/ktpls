// Package textutil holds small text helpers shared across ktpls:
// line and indentation handling, identifier characters, and UTF-16
// lengths.
package textutil

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

// LineStart returns the offset of the start of the line containing off.
func LineStart(src []byte, off int) int {
	for off > 0 && src[off-1] != '\n' {
		off--
	}
	return off
}

// LineIndent returns the leading whitespace of the line containing off.
func LineIndent(src []byte, off int) string {
	start := LineStart(src, off)
	end := start
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[start:end])
}

// IndentLines prefixes every non-empty line of s but the first.
func IndentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			lines[i] = prefix + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// DedentLines removes prefix from every line of s but the first.
func DedentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimPrefix(lines[i], prefix)
	}
	return strings.Join(lines, "\n")
}

// CollapseSpace replaces each run of whitespace in s with one space.
func CollapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Truncate shortens s to n bytes, marking the cut with "...".
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Plural returns one if n is 1, else many.
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// IsDigits reports whether s is a non-empty run of ASCII digits.
func IsDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// IsLetterOrDigit reports whether r is an ASCII letter or digit, or any
// non-ASCII rune (identifiers may contain Unicode letters).
func IsLetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r >= utf8.RuneSelf
}

// IsIdentRune reports whether r may appear in an identifier.
func IsIdentRune(r rune) bool { return r == '_' || IsLetterOrDigit(r) }

// SameFirstRune reports whether a and b start with the same letter,
// ignoring case.
func SameFirstRune(a, b string) bool {
	ra, _ := utf8.DecodeRuneInString(a)
	rb, _ := utf8.DecodeRuneInString(b)
	return unicode.ToLower(ra) == unicode.ToLower(rb)
}

// ContainsWord reports whether name occurs in content as a whole
// identifier (not as part of a longer one).
func ContainsWord(content []byte, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; ; {
		j := bytes.Index(content[i:], []byte(name))
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		before, _ := utf8.DecodeLastRune(content[:start])
		after, _ := utf8.DecodeRune(content[end:])
		if (start == 0 || !IsIdentRune(before)) && (end == len(content) || !IsIdentRune(after)) {
			return true
		}
		i = start + 1
	}
}

// UTF16Len returns the number of UTF-16 code units encoding s. Invalid
// UTF-8 bytes count as one unit each (U+FFFD).
func UTF16Len[T ~string | ~[]byte](s T) int {
	n := 0
	for _, r := range string(s) {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// UTF16ToByte converts an offset into src in UTF-16 code units (as Java
// and the JVM count) into a byte offset.
func UTF16ToByte(src []byte, units int) int {
	off := 0
	for off < len(src) && units > 0 {
		r, size := utf8.DecodeRune(src[off:])
		if r >= 0x10000 {
			units -= 2
		} else {
			units--
		}
		off += size
	}
	return off
}
