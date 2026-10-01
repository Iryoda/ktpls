// Package springconfig reads a Spring Boot project's configuration
// properties: the application.yml, application.properties and
// bootstrap files (and their profile variants, such as
// application-production.yml) that placeholders like ${server.port}
// resolve against.
package springconfig

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Iryoda/ktpls/internal/messages"
)

// An Entry is a property's definition in one file.
type Entry struct {
	Path    string
	Profile string // "" for the default profile, e.g. "production"
	Test    bool   // in test resources
	Key     string // as written, dotted: "services-mapping.dojizap.host"
	Line    int    // 0-based
	Value   string
}

// A Config is a project's configuration properties.
type Config struct {
	Keys  map[string][]Entry // canonical key -> its definitions, default profile first
	Files []string
	// Imports are spring.config.import locations that aren't files of the
	// project (a config server, a secret manager...): properties may come
	// from them.
	Imports []string

	sections map[string]bool      // canonical keys with nested properties
	stamps   map[string]time.Time // Files' modification times when read
}

// Lookup returns the definitions of key, visible from test code if test,
// and whether key names a section (properties nested under it) instead.
func (c *Config) Lookup(key string, test bool) (entries []Entry, section bool) {
	k := Canonical(key)
	for _, e := range c.Keys[k] {
		if test || !e.Test {
			entries = append(entries, e)
		}
	}
	return entries, len(entries) == 0 && c.sections[k]
}

// Stale reports whether a file changed since the config was read, or a
// new one appeared.
func (c *Config) Stale(root string) bool {
	for p, t := range c.stamps {
		info, err := os.Stat(p)
		if err != nil || !info.ModTime().Equal(t) {
			return true
		}
	}
	return !slices.Equal(configFiles(root), c.Files)
}

// Canonical returns the form keys are compared in, as Spring's relaxed
// binding does: lower case, without dashes and underscores outside
// brackets ("servicesMapping" and "services-mapping" are one key).
func Canonical(key string) string {
	var b strings.Builder
	depth := 0
	for _, r := range key {
		switch {
		case r == '[':
			depth++
		case r == ']':
			depth--
		case depth == 0 && (r == '-' || r == '_'):
			continue
		}
		if depth == 0 {
			b.WriteString(strings.ToLower(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// skipDirs are directories never holding the project's configuration.
var skipDirs = map[string]bool{".git": true, ".gradle": true, ".idea": true, "build": true, "node_modules": true, "out": true, "target": true}

var configName = regexp.MustCompile(`^(application|bootstrap)(?:-([A-Za-z0-9_.-]+))?\.(ya?ml|properties)$`)

// configFiles returns the configuration files under root's resources
// directories (src/main/resources, src/test/resources...), sorted.
func configFiles(root string) []string {
	if root == "" {
		return nil
	}
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.Name() != "resources" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "src" {
			return nil
		}
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if !e.IsDir() && configName.MatchString(e.Name()) {
				out = append(out, filepath.Join(path, e.Name()))
			}
		}
		return filepath.SkipDir
	})
	// application.yml before application-x.yml, main before test.
	slices.SortFunc(out, func(a, b string) int {
		ta, tb := isTest(a), isTest(b)
		if ta != tb {
			if ta {
				return 1
			}
			return -1
		}
		pa, pb := profileOf(a) != "", profileOf(b) != ""
		if pa != pb {
			if pa {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	})
	return out
}

func isTest(path string) bool {
	set := filepath.Base(filepath.Dir(filepath.Dir(path))) // src/<set>/resources
	return strings.Contains(strings.ToLower(set), "test")
}

func profileOf(path string) string {
	m := configName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return ""
	}
	return m[2]
}

// Load reads the configuration files under root.
func Load(root string) *Config {
	c := &Config{Keys: map[string][]Entry{}, sections: map[string]bool{}, stamps: map[string]time.Time{}}
	for _, path := range configFiles(root) {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		c.Files = append(c.Files, path)
		c.stamps[path] = info.ModTime()
		var props []Property
		if strings.HasSuffix(path, ".properties") {
			for _, p := range messages.Parse(data) {
				props = append(props, Property{Key: p.Key, Value: p.Value, Line: p.Line})
			}
		} else {
			props = ParseYAML(data)
		}
		for _, p := range props {
			e := Entry{Path: path, Profile: profileOf(path), Test: isTest(path), Key: p.Key, Line: p.Line, Value: p.Value}
			if p.Profile != "" {
				e.Profile = p.Profile
			}
			k := Canonical(p.Key)
			c.Keys[k] = append(c.Keys[k], e)
			for i := strings.LastIndexAny(k, ".["); i > 0; i = strings.LastIndexAny(k[:i], ".[") {
				c.sections[k[:i]] = true
			}
			if Canonical(p.Key) == "spring.config.import" {
				c.addImports(p.Value)
			}
		}
	}
	return c
}

// addImports records the imported locations that aren't files.
func (c *Config) addImports(value string) {
	for loc := range strings.SplitSeq(value, ",") {
		loc = strings.TrimPrefix(strings.TrimSpace(loc), "optional:")
		if loc == "" || strings.HasPrefix(loc, "classpath:") || strings.HasPrefix(loc, "file:") {
			continue
		}
		if !slices.Contains(c.Imports, loc) {
			c.Imports = append(c.Imports, loc)
		}
	}
}

// A Property is a key, as a dotted path, and its value.
type Property struct {
	Key, Value string
	Line       int    // 0-based, of the key
	Profile    string // the document's spring.config.activate.on-profile
}

// ParseYAML flattens YAML configuration into dotted properties, as
// Spring does: nested maps join with dots, list items are indexed
// (servers[0].host). A key with nothing nested is a property with an
// empty value. Block scalars (| and >) are skipped, flow collections kept
// as their text; documents (---) may set their profile with
// spring.config.activate.on-profile (or spring.profiles).
func ParseYAML(data []byte) []Property {
	type frame struct {
		indent int
		path   string // the key path this frame's children extend
		list   bool   // a list item
	}
	var out []Property
	var stack []frame
	docStart := 0
	counts := map[string]int{} // list path -> items so far
	blockIndent := -1          // inside a block scalar more indented than this
	hasChildren := map[int]bool{}
	pendingKey := -1 // index in out of the last key with no value yet

	endDoc := func() {
		profile := ""
		for _, p := range out[docStart:] {
			if k := Canonical(p.Key); k == "spring.config.activate.onprofile" || k == "spring.profiles" {
				profile = p.Value
			}
		}
		if profile != "" {
			for i := docStart; i < len(out); i++ {
				out[i].Profile = profile
			}
		}
		docStart = len(out)
		stack = nil
		counts = map[string]int{}
		pendingKey = -1
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for n, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if blockIndent >= 0 {
			if trimmed == "" || indent > blockIndent {
				continue
			}
			blockIndent = -1
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "---" || strings.HasPrefix(trimmed, "--- ") || trimmed == "..." {
			endDoc()
			continue
		}
		if strings.HasPrefix(trimmed, "%") { // a directive
			continue
		}
		rest := line[indent:]
		for {
			dash := rest == "-" || strings.HasPrefix(rest, "- ")
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				// A list may be as indented as its key: "a:\n- x".
				if top.indent < indent || dash && top.indent == indent && !top.list {
					break
				}
				stack = stack[:len(stack)-1]
			}
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1].path
			}
			if dash {
				// A list item of the parent key: its content starts after
				// the dash, as if indented there.
				i := counts[parent]
				counts[parent]++
				item := parent + "[" + strconv.Itoa(i) + "]"
				if pendingKey >= 0 && out[pendingKey].Key == parent {
					hasChildren[pendingKey] = true
				}
				body := strings.TrimLeft(strings.TrimPrefix(rest, "-"), " ")
				stack = append(stack, frame{indent, item, true})
				if body == "" || strings.HasPrefix(body, "#") {
					break
				}
				if _, _, ok := splitKey(body); !ok {
					out = append(out, Property{Key: item, Value: scalar(body), Line: n})
					break
				}
				indent += len(rest) - len(body)
				rest = body
				continue
			}
			key, value, ok := splitKey(rest)
			if !ok {
				break // a continuation of a multi-line scalar
			}
			full := joinKey(parent, key)
			if pendingKey >= 0 && nested(full, out[pendingKey].Key) {
				hasChildren[pendingKey] = true
			}
			v := strings.TrimSpace(value)
			switch {
			case v == "" || strings.HasPrefix(v, "#"):
				out = append(out, Property{Key: full, Line: n})
				pendingKey = len(out) - 1
				stack = append(stack, frame{indent, full, false})
			case strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">"):
				out = append(out, Property{Key: full, Value: "(multi-line text)", Line: n})
				blockIndent = indent
			default:
				out = append(out, Property{Key: full, Value: scalar(v), Line: n})
			}
			break
		}
	}
	endDoc()
	// Keys with nested properties aren't properties themselves.
	kept := out[:0]
	for i, p := range out {
		if !hasChildren[i] {
			kept = append(kept, p)
		}
	}
	return kept
}

// nested reports whether key is under section.
func nested(key, section string) bool {
	rest, ok := strings.CutPrefix(key, section)
	return ok && rest != "" && (rest[0] == '.' || rest[0] == '[')
}

// splitKey splits "key: value" (the key possibly quoted); ok is false for
// a line that isn't a mapping entry.
func splitKey(s string) (key, value string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if q := s[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(s[1:], q)
		if end < 0 {
			return "", "", false
		}
		key, after := s[1:end+1], s[end+2:]
		after = strings.TrimLeft(after, " ")
		if !strings.HasPrefix(after, ":") {
			return "", "", false
		}
		return key, after[1:], true
	}
	if s[0] == '[' || s[0] == '{' {
		return "", "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return strings.TrimSpace(s[:i]), s[i+1:], true
		}
		if s[i] == ' ' && i+1 < len(s) && s[i+1] == '#' {
			return "", "", false
		}
	}
	return "", "", false
}

// joinKey appends a YAML key to a path; a bracketed key ("[a.b]") is one
// segment, kept with its brackets.
func joinKey(parent, key string) string {
	if parent == "" {
		return key
	}
	if strings.HasPrefix(key, "[") {
		return parent + key
	}
	return parent + "." + key
}

// scalar returns a value without its quotes or trailing comment.
func scalar(v string) string {
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if end := strings.LastIndexByte(v, q); end > 0 {
			return v[1:end]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
