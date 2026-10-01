package server

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/springconfig"
)

// Configuration properties (see kotlin.Placeholders): the key of a
// ${key} placeholder in a string must be a property of the project's
// application.yml (or .properties, any profile), unless it has a default.

type propertiesState struct {
	mu     sync.Mutex
	config *springconfig.Config
}

// springConfig returns the project's configuration, reread if a file
// changed; nil if there is none or the check is off.
func (s *Server) springConfig() *springconfig.Config {
	if o := s.opts.Properties; o != nil && o.Enabled != nil && !*o.Enabled {
		return nil
	}
	s.props.mu.Lock()
	defer s.props.mu.Unlock()
	if s.props.config == nil || s.props.config.Stale(s.root) {
		s.props.config = springconfig.Load(s.root)
	}
	if len(s.props.config.Files) == 0 {
		return nil
	}
	return s.props.config
}

// propertyKey matches the keys checked: dotted names and indexes, not
// expressions or other sources' names (sm@secret, sm://secret).
var propertyKey = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+|\[[^\]]+\])*$`)

// envStyle matches an environment variable's name, which Spring resolves
// from the environment: ${PORT}, ${DB_HOST}.
var envStyle = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// runtimeSources are prefixes of properties set at run time, never in the
// project's files: random values, JVM system properties, the server's
// actual port.
var runtimeSources = []string{"random.", "java.", "user.", "os.", "file.", "line.", "path.", "local."}

// checked reports whether a placeholder's key must be in the project's
// configuration.
func (s *Server) checked(p kotlin.Placeholder) bool {
	if p.HasDefault || !propertyKey.MatchString(p.Key) || envStyle.MatchString(p.Key) {
		return false
	}
	ignore := runtimeSources
	if o := s.opts.Properties; o != nil {
		ignore = append(slices.Clip(ignore), o.Ignore...)
	}
	for _, prefix := range ignore {
		if strings.HasPrefix(p.Key, prefix) || p.Key == strings.TrimSuffix(prefix, ".") {
			return false
		}
	}
	return true
}

// propertyDiagnostics reports the placeholders in a file whose keys aren't
// configured.
func (s *Server) propertyDiagnostics(pf *kotlin.ParsedFile) []protocol.Diagnostic {
	c := s.springConfig()
	if c == nil {
		return nil
	}
	test := isTestPath(pf.Path)
	var out []protocol.Diagnostic
	for _, p := range kotlin.Placeholders(pf) {
		if !s.checked(p) {
			continue
		}
		entries, section := c.Lookup(p.Key, test)
		if len(entries) > 0 {
			continue
		}
		var msg string
		if section {
			msg = fmt.Sprintf("Property %q is a section of %s, not a value", p.Key, configNames(c, test))
		} else {
			msg = fmt.Sprintf("Unknown property %q: not in %s", p.Key, configNames(c, test))
			if near := nearestProperty(c, p.Key, test); near != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", near)
			}
		}
		severity := protocol.SeverityWarning
		if ext := externalImports(c); len(ext) > 0 {
			// It may well come from there.
			severity = protocol.SeverityInformation
			msg += "; it may come from " + strings.Join(ext, ", ")
		}
		out = append(out, protocol.Diagnostic{Range: p.Range, Severity: severity, Source: "ktpls", Message: msg})
	}
	return out
}

// externalImports returns the imported locations that may define any
// property: a config server, Vault, Consul... A secret manager's (sm@,
// sm://) only define their own keys, which placeholders name as such.
func externalImports(c *springconfig.Config) []string {
	var out []string
	for _, loc := range c.Imports {
		if !strings.HasPrefix(loc, "sm@") && !strings.HasPrefix(loc, "sm://") {
			out = append(out, loc)
		}
	}
	return out
}

// isTestPath reports whether a file is test code, which also sees the
// test resources' configuration.
func isTestPath(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "src" && strings.Contains(strings.ToLower(parts[i+1]), "test") {
			return true
		}
	}
	return false
}

// configNames lists the configuration files visible to (test) code by
// name.
func configNames(c *springconfig.Config, test bool) string {
	var names []string
	for _, f := range c.Files {
		if !test && isTestPath(f) {
			continue
		}
		if name := filepath.Base(f); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) > 3 {
		return names[0] + " and its profiles"
	}
	return strings.Join(names, ", ")
}

// nearestProperty returns the configured key closest to a mistyped one.
func nearestProperty(c *springconfig.Config, key string, test bool) string {
	written := map[string]string{} // canonical -> as first written
	for k, entries := range c.Keys {
		for _, e := range entries {
			if test || !e.Test {
				written[k] = e.Key
				break
			}
		}
	}
	near := nearest(func(yield func(string) bool) {
		for k := range written {
			if !yield(k) {
				return
			}
		}
	}, springconfig.Canonical(key))
	return written[near]
}

// propertyHover shows a placeholder's value in each profile.
func (s *Server) propertyHover(f *kotlin.ParsedFile, offset int) *protocol.Hover {
	c := s.springConfig()
	p := kotlin.PlaceholderAt(f, offset)
	if c == nil || p == nil {
		return nil
	}
	entries, section := c.Lookup(p.Key, isTestPath(f.Path))
	var b strings.Builder
	b.WriteString("```properties\n")
	b.WriteString(p.Key)
	b.WriteString("\n```\n\n*configuration property*\n")
	for _, e := range entries {
		profile := e.Profile
		if profile == "" {
			profile = "default"
		}
		fmt.Fprintf(&b, "\n- **%s** (%s): %s", profile, filepath.Base(e.Path), inlineCode(e.Value))
	}
	switch {
	case p.HasDefault:
		fmt.Fprintf(&b, "\n- **if unset**: %s", inlineCode(p.Default))
	case section:
		b.WriteString("\n\nA section, not a value: properties are nested under it.")
	case len(entries) == 0:
		b.WriteString("\n\nNot in the project's configuration files.")
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: b.String()}, Range: &p.Range}
}

func inlineCode(s string) string {
	if s == "" {
		return "*(empty)*"
	}
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	return fence + s + fence
}

// propertyDefinition locates a placeholder's definitions.
func (s *Server) propertyDefinition(f *kotlin.ParsedFile, offset int) []protocol.Location {
	c := s.springConfig()
	p := kotlin.PlaceholderAt(f, offset)
	if c == nil || p == nil {
		return nil
	}
	entries, _ := c.Lookup(p.Key, isTestPath(f.Path))
	var out []protocol.Location
	for _, e := range entries {
		pos := protocol.Position{Line: uint32(e.Line)}
		out = append(out, protocol.Location{URI: protocol.URIFromPath(e.Path), Range: protocol.Range{Start: pos, End: pos}})
	}
	return out
}
