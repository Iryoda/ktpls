// Package messages reads a project's message bundles: the
// messages.properties files (and their locale variants, such as
// messages_pt_BR.properties) that Spring's MessageSource and Java's
// ResourceBundle resolve message keys against.
package messages

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// An Entry is a key's definition in one bundle file.
type Entry struct {
	Path   string
	Locale string // "" for the default bundle, e.g. "pt_BR"
	Line   int    // 0-based
	Value  string
}

// Bundles are a project's message bundles.
type Bundles struct {
	Keys  map[string][]Entry // key -> its definitions, default bundle first
	Files []string

	stamps map[string]time.Time // Files' modification times when read
}

// Has reports whether key is defined in any bundle.
func (b *Bundles) Has(key string) bool {
	_, ok := b.Keys[key]
	return ok
}

// Stale reports whether a bundle file changed since the bundles were read.
func (b *Bundles) Stale() bool {
	for p, t := range b.stamps {
		info, err := os.Stat(p)
		if err != nil || !info.ModTime().Equal(t) {
			return true
		}
	}
	return false
}

// skipDirs are directories never holding the project's bundles.
var skipDirs = map[string]bool{".git": true, ".gradle": true, ".idea": true, "build": true, "node_modules": true, "out": true, "target": true}

// Load reads the message bundles under root: the files named after the
// bundle basenames (spring.messages.basename, default "messages") in the
// resources directories of the main source sets.
func Load(root string) *Bundles {
	b := &Bundles{Keys: map[string][]Entry{}, stamps: map[string]time.Time{}}
	if root == "" {
		return b
	}
	var resources []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		// src/main/resources, not test resources.
		if d.Name() == "resources" && filepath.Base(filepath.Dir(path)) == "main" {
			resources = append(resources, path)
			return filepath.SkipDir
		}
		return nil
	})
	for _, dir := range resources {
		for _, base := range basenames(dir) {
			files, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(base)+"*.properties"))
			slices.Sort(files) // messages.properties before messages_xx.properties
			for _, f := range files {
				locale, ok := localeOf(filepath.Base(f), filepath.Base(base))
				if !ok {
					continue
				}
				b.read(f, locale)
			}
		}
	}
	return b
}

func (b *Bundles) read(path, locale string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	b.Files = append(b.Files, path)
	b.stamps[path] = info.ModTime()
	for _, p := range Parse(data) {
		b.Keys[p.Key] = append(b.Keys[p.Key], Entry{Path: path, Locale: locale, Line: p.Line, Value: p.Value})
	}
}

var localeSuffix = regexp.MustCompile(`^(?:_[A-Za-z]{2,3}(?:_[A-Za-z0-9]{2,8})*)?$`)

// localeOf returns the locale of a bundle file named after base:
// "messages_pt_BR.properties" is "pt_BR", "messages.properties" "".
func localeOf(name, base string) (string, bool) {
	rest, ok := strings.CutSuffix(strings.TrimPrefix(name, base), ".properties")
	if !ok || !strings.HasPrefix(name, base) || !localeSuffix.MatchString(rest) {
		return "", false
	}
	return strings.TrimPrefix(rest, "_"), true
}

// basenames returns the bundle basenames configured in a resources
// directory's application.properties or application.yml
// (spring.messages.basename, comma separated), else "messages".
func basenames(dir string) []string {
	var value string
	if data, err := os.ReadFile(filepath.Join(dir, "application.properties")); err == nil {
		for _, p := range Parse(data) {
			if p.Key == "spring.messages.basename" {
				value = p.Value
			}
		}
	}
	for _, name := range []string{"application.yml", "application.yaml"} {
		if value != "" {
			break
		}
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			value = yamlValue(data, []string{"spring", "messages", "basename"})
		}
	}
	if value == "" {
		return []string{"messages"}
	}
	var out []string
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		v = strings.TrimPrefix(strings.TrimPrefix(v, "classpath:"), "/")
		v = strings.ReplaceAll(v, ".", "/") // Spring accepts dotted names
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// yamlValue finds a scalar in simple YAML: nested keys by indentation, or
// a dotted key ("spring.messages.basename: x").
func yamlValue(data []byte, path []string) string {
	type level struct {
		indent int
		key    string
	}
	var stack []level
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			if trimmed == "---" {
				stack = nil
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.HasPrefix(trimmed, "-") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, level{indent, strings.TrimSpace(key)})
		var full []string
		for _, l := range stack {
			full = append(full, strings.Split(l.key, ".")...)
		}
		if slices.Equal(full, path) {
			v := strings.TrimSpace(value)
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// A Property is a key and value of a .properties file.
type Property struct {
	Key, Value string
	Line       int // 0-based, of the key
}

// Parse parses a .properties file: key=value, key:value or key value
// lines, # and ! comments, backslash line continuations and escapes.
func Parse(data []byte) []Property {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out []Property
	for i := 0; i < len(lines); i++ {
		start := i
		line := strings.TrimLeft(lines[i], " \t\f")
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		// Join continuation lines: an odd number of trailing backslashes.
		for continued(line) && i+1 < len(lines) {
			i++
			line = line[:len(line)-1] + strings.TrimLeft(lines[i], " \t\f")
		}
		key, value := splitProperty(line)
		out = append(out, Property{Key: unescape(key), Value: unescape(value), Line: start})
	}
	return out
}

func continued(line string) bool {
	n := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// splitProperty splits a logical line at the first unescaped =, : or
// whitespace.
func splitProperty(line string) (key, value string) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '=', ':', ' ', '\t', '\f':
			key = line[:i]
			rest := strings.TrimLeft(line[i:], " \t\f")
			if line[i] == ' ' || line[i] == '\t' || line[i] == '\f' {
				if rest != "" && (rest[0] == '=' || rest[0] == ':') {
					rest = strings.TrimLeft(rest[1:], " \t\f")
				}
			} else {
				rest = strings.TrimLeft(line[i+1:], " \t\f")
			}
			return key, rest
		}
	}
	return line, ""
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+4 < len(s) {
				if r, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(r))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
