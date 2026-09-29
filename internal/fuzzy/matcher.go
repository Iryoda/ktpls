// Package fuzzy scores completion candidates against a typed pattern.
package fuzzy

import (
	"unicode"
	"unicode/utf8"
)

// Score reports whether pattern matches candidate as a case-insensitive
// subsequence, and how well: higher is better. Matches at the start of the
// candidate, at word boundaries (camelCase humps, after '_') and in
// consecutive runs score higher; skipped characters cost a little. An
// empty pattern matches everything with score 0.
func Score(pattern, candidate string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	score, pi := 0, 0
	pr, psize := utf8.DecodeRuneInString(pattern)
	prevMatched := false
	var prev rune
	for ci, c := range candidate {
		if pi >= len(pattern) {
			break
		}
		if unicode.ToLower(c) == unicode.ToLower(pr) {
			score += 10
			switch {
			case ci == 0:
				score += 15
			case isBoundary(prev, c):
				score += 10
			}
			if prevMatched {
				score += 5
			}
			if c == pr {
				score++ // same case
			}
			prevMatched = true
			pi += psize
			if pi < len(pattern) {
				pr, psize = utf8.DecodeRuneInString(pattern[pi:])
			}
		} else {
			if pi > 0 {
				score-- // gap inside the match
			}
			prevMatched = false
		}
		prev = c
	}
	if pi < len(pattern) {
		return 0, false
	}
	if len(candidate) >= len(pattern) && equalFoldPrefix(candidate, pattern) {
		score += 30
		if len(candidate) == len(pattern) {
			score += 20 // exact match
		}
	}
	return score, true
}

func isBoundary(prev, c rune) bool {
	return prev == '_' || (unicode.IsLower(prev) && unicode.IsUpper(c)) || (unicode.IsLetter(c) && !unicode.IsLetter(prev) && prev != 0)
}

func equalFoldPrefix(s, prefix string) bool {
	for _, p := range prefix {
		c, size := utf8.DecodeRuneInString(s)
		if size == 0 || unicode.ToLower(c) != unicode.ToLower(p) {
			return false
		}
		s = s[size:]
	}
	return true
}
