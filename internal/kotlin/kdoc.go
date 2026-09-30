package kotlin

import (
	"bytes"
	"regexp"
	"strings"
)

// KDocBefore returns the KDoc comment (`/** ... */`) immediately preceding
// the declaration starting at byte offset start, or "". At most one blank
// line may separate them.
//
// The comment is found by scanning the source text rather than the syntax
// tree, because the grammar sometimes folds comments into hidden tokens.
func KDocBefore(src []byte, start uint) string {
	end := int(start)
	newlines := 0
	for end > 0 {
		c := src[end-1]
		if c == '\n' {
			newlines++
		} else if c != ' ' && c != '\t' && c != '\r' {
			break
		}
		end--
	}
	if newlines > 2 || !bytes.HasSuffix(src[:end], []byte("*/")) {
		return ""
	}
	begin := bytes.LastIndex(src[:end-2], []byte("/*"))
	if begin < 0 || !bytes.HasPrefix(src[begin:], []byte("/**")) || bytes.HasPrefix(src[begin:], []byte("/**/")) {
		return ""
	}
	return string(src[begin:end])
}

var (
	kdocTagRE  = regexp.MustCompile(`^@(\w+)\s*(.*)$`)
	kdocLinkRE = regexp.MustCompile(`\[([A-Za-z_][\w.]*)\]`)
)

// RenderKDoc converts a KDoc comment to markdown: the leading `*` gutter is
// stripped, [links] become code spans, and block tags become sections.
func RenderKDoc(comment string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(comment, "/**"), "*/")
	var desc []string
	type tag struct{ name, arg, text string }
	var tags []tag
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "*") {
			line = strings.TrimPrefix(trimmed, "*")
			line = strings.TrimPrefix(line, " ")
		} else {
			line = trimmed
		}
		fenceLine := strings.HasPrefix(strings.TrimSpace(line), "```")
		if fenceLine {
			inFence = !inFence
		}
		if !inFence && !fenceLine {
			if m := kdocTagRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				t := tag{name: m[1]}
				switch t.name {
				case "param", "property", "throws", "exception", "see", "sample":
					arg, rest, _ := strings.Cut(m[2], " ")
					t.arg, t.text = strings.Trim(arg, "[]"), strings.TrimSpace(rest)
				default:
					t.text = m[2]
				}
				tags = append(tags, t)
				continue
			}
			if len(tags) > 0 && strings.TrimSpace(line) != "" {
				// Continuation of the previous tag.
				tags[len(tags)-1].text += " " + strings.TrimSpace(line)
				continue
			}
		}
		if len(tags) == 0 {
			if !inFence && !fenceLine {
				line = links(line)
			}
			desc = append(desc, line)
		}
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(strings.Join(desc, "\n")))
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if title != "" {
			b.WriteString("**" + title + "**\n")
		}
		for i, it := range items {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("- " + it)
		}
	}
	item := func(t tag) string {
		s := ""
		if t.arg != "" {
			s = "`" + t.arg + "`"
		}
		if t.text != "" {
			if s != "" {
				s += " — "
			}
			s += links(t.text)
		}
		return s
	}
	groups := []struct {
		title string
		names []string
	}{
		{"Parameters", []string{"param"}},
		{"Properties", []string{"property"}},
		{"Returns", []string{"return"}},
		{"Throws", []string{"throws", "exception"}},
		{"See also", []string{"see"}},
	}
	used := map[string]bool{}
	for _, g := range groups {
		var items []string
		for _, t := range tags {
			for _, n := range g.names {
				if t.name == n {
					items = append(items, item(t))
					used[n] = true
				}
			}
		}
		section(g.title, items)
	}
	var other []string
	for _, t := range tags {
		if !used[t.name] {
			other = append(other, "*@"+t.name+"* "+item(t))
		}
	}
	section("", other)
	return b.String()
}

// links renders KDoc [name] links as code spans, and [text][name] links
// as their text, leaving markdown links [text](url) alone.
func links(s string) string {
	var b strings.Builder
	last := 0
	ms := kdocLinkRE.FindAllStringSubmatchIndex(s, -1)
	for i := 0; i < len(ms); i++ {
		m := ms[i]
		if m[0] < last || m[1] < len(s) && s[m[1]] == '(' {
			continue
		}
		b.WriteString(s[last:m[0]])
		if i+1 < len(ms) && ms[i+1][0] == m[1] { // [text][name]
			b.WriteString(s[m[2]:m[3]])
			last = ms[i+1][1]
			i++
			continue
		}
		b.WriteString("`" + s[m[2]:m[3]] + "`")
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
