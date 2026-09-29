package kotlin

import "strings"

// Helpers for dotted Kotlin names: a.b.C.

// joinFQ joins a qualifier and a name with a dot.
func joinFQ(qual, name string) string {
	if qual == "" {
		return name
	}
	return qual + "." + name
}

// lastSegment returns the part of a dotted name after its last dot.
func lastSegment(fq string) string {
	return fq[strings.LastIndexByte(fq, '.')+1:]
}

func cut(name string) (first, rest string, ok bool) {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return name[:i], name[i+1:], true
		}
	}
	return name, "", false
}

// parentFQ returns the qualifier of a dotted name ("" if none).
func parentFQ(fq string) string {
	for i := len(fq) - 1; i >= 0; i-- {
		if fq[i] == '.' {
			return fq[:i]
		}
	}
	return ""
}

func joinAll(parts []string) string {
	out := ""
	for _, p := range parts {
		out = joinFQ(out, p)
	}
	return out
}
