package kotlin

import (
	"regexp"
	"strings"
)

// RenderJavadoc converts a Javadoc comment to markdown: its inline tags
// and HTML become markdown, then block tags (@param, @return, ...) are
// rendered as in KDoc.
func RenderJavadoc(comment string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(comment, "/**"), "*/")
	var lines []string
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		if after, ok := strings.CutPrefix(trimmed, "*"); ok {
			line = strings.TrimPrefix(after, " ")
		} else {
			line = trimmed
		}
		lines = append(lines, line)
	}
	text := javadocHTML(javadocInline(strings.Join(lines, "\n")))
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		out = append(out, " * "+line)
	}
	return RenderKDoc("/**\n" + strings.Join(out, "\n") + "\n */")
}

// javadocInline replaces inline tags: {@code x} and {@literal x} by code
// spans, and {@link A#b label} by the label or the target as code.
func javadocInline(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "{@")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		end := matchingBrace(s, i)
		if end < 0 {
			b.WriteString(s[i:])
			return b.String()
		}
		inner := s[i+2 : end]
		tag, arg, _ := strings.Cut(inner, " ")
		if tag != inner {
			arg = strings.TrimSpace(arg)
		} else {
			arg = ""
		}
		switch tag {
		case "code", "literal":
			b.WriteString("`")
			b.WriteString(escapeHTML(arg))
			b.WriteString("`") // kept through the HTML pass
		case "link", "linkplain":
			target, label, _ := strings.Cut(arg, " ")
			if label = strings.TrimSpace(label); label != "" {
				b.WriteString(label)
			} else {
				b.WriteString("`")
				b.WriteString(strings.ReplaceAll(strings.TrimPrefix(target, "#"), "#", "."))
				b.WriteString("`")
			}
		case "value":
			b.WriteString("`")
			b.WriteString(escapeHTML(arg))
			b.WriteString("`")
		default: // {@inheritDoc} and others
			b.WriteString(arg)
		}
		s = s[end+1:]
	}
}

// matchingBrace returns the index of the brace closing the one at open.
func matchingBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

var (
	htmlPre     = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	htmlCode    = regexp.MustCompile(`(?is)<(?:code|tt)>(.*?)</(?:code|tt)>`)
	htmlBold    = regexp.MustCompile(`(?is)<(?:b|strong)>(.*?)</(?:b|strong)>`)
	htmlItalic  = regexp.MustCompile(`(?is)<(?:i|em)>(.*?)</(?:i|em)>`)
	htmlPara    = regexp.MustCompile(`(?i)\s*<p\s*/?>\s*`)
	htmlBreak   = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlItem    = regexp.MustCompile(`(?i)\s*<li>\s*`)
	htmlLink    = regexp.MustCompile(`(?is)<a\s[^>]*>(.*?)</a>`)
	htmlTag     = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	blankLines  = regexp.MustCompile(`\n{3,}`)
	htmlEntites = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ", "&amp;", "&")
)

// javadocHTML converts the HTML Javadoc uses into markdown.
func javadocHTML(s string) string {
	s = htmlPre.ReplaceAllStringFunc(s, func(m string) string {
		code := htmlPre.FindStringSubmatch(m)[1]
		code = strings.Trim(htmlTag.ReplaceAllString(code, ""), "\n")
		return "\n```java\n" + code + "\n```\n"
	})
	s = htmlCode.ReplaceAllString(s, "`$1`")
	s = htmlBold.ReplaceAllString(s, "**$1**")
	s = htmlItalic.ReplaceAllString(s, "*$1*")
	s = htmlLink.ReplaceAllString(s, "$1")
	s = htmlPara.ReplaceAllString(s, "\n\n")
	s = htmlBreak.ReplaceAllString(s, "\n")
	s = htmlItem.ReplaceAllString(s, "\n- ")
	s = htmlTag.ReplaceAllString(s, "")
	s = htmlEntites.Replace(s)
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }
