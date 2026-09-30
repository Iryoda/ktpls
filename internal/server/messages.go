package server

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/messages"
	"github.com/Iryoda/ktpls/internal/protocol"
)

// Message keys (see kotlin.KeyParams): a literal passed to a parameter
// taking message keys must be a key of the project's message bundles.

// keyParamsTTL is how long the learned key parameters are reused: they
// change slowly, and learning them reads every file's call arguments.
const keyParamsTTL = 10 * time.Second

type messagesState struct {
	mu        sync.Mutex
	bundles   *messages.Bundles
	params    map[kotlin.KeyParam]bool
	learnedAt time.Time
}

// messageBundles returns the project's bundles, reread if a file changed.
func (s *Server) messageBundles() *messages.Bundles {
	if !s.messagesEnabled() {
		return nil
	}
	s.msg.mu.Lock()
	defer s.msg.mu.Unlock()
	if s.msg.bundles == nil || s.msg.bundles.Stale() {
		s.msg.bundles = messages.Load(s.root)
		s.msg.params = nil
	}
	if len(s.msg.bundles.Keys) == 0 {
		return nil
	}
	return s.msg.bundles
}

func (s *Server) messagesEnabled() bool {
	o := s.opts.Messages
	return o == nil || o.Enabled == nil || *o.Enabled
}

// keyParams returns the parameters taking message keys.
func (s *Server) keyParams(sn *cache.Snapshot, b *messages.Bundles) map[kotlin.KeyParam]bool {
	s.msg.mu.Lock()
	defer s.msg.mu.Unlock()
	if s.msg.params == nil || time.Since(s.msg.learnedAt) > keyParamsTTL {
		s.msg.params = kotlin.KeyParams(sn.Index(), b.Has)
		if o := s.opts.Messages; o != nil {
			for _, p := range o.KeyParameters {
				s.msg.params[kotlin.KeyParam(p)] = true
			}
		}
		s.msg.learnedAt = time.Now()
	}
	return s.msg.params
}

// messageDiagnostics reports the unknown message keys in a file.
func (s *Server) messageDiagnostics(sn *cache.Snapshot, path string) []protocol.Diagnostic {
	b := s.messageBundles()
	sum := sn.Index().File(path)
	if b == nil || sum == nil || len(sum.StringArgs) == 0 {
		return nil
	}
	params := s.keyParams(sn, b)
	var out []protocol.Diagnostic
	for _, a := range sum.StringArgs {
		if b.Has(a.Value) || !params[kotlin.ParamOf(sn.Index(), a)] {
			continue
		}
		msg := fmt.Sprintf("Unknown message key %q: not in %s", a.Value, bundleNames(b))
		if near := nearestKey(b, a.Value); near != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", near)
		}
		out = append(out, protocol.Diagnostic{Range: a.Range, Severity: protocol.SeverityWarning, Source: "ktpls", Message: msg})
	}
	return out
}

func bundleNames(b *messages.Bundles) string {
	seen := map[string]bool{}
	var names []string
	for _, f := range b.Files {
		name := f[strings.LastIndexByte(f, '/')+1:]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) > 2 {
		return names[0] + " and its locales"
	}
	return strings.Join(names, ", ")
}

// nearestKey returns the key closest to a mistyped one, if close enough.
func nearestKey(b *messages.Bundles, key string) string {
	if key == "" || strings.ContainsRune(key, ' ') {
		return "" // a message, not a mistyped key
	}
	best, bestDist := "", len(key)/4+2
	for k := range b.Keys {
		if abs(len(k)-len(key)) >= bestDist {
			continue
		}
		d := editDistance(k, key, bestDist)
		if d < bestDist || d == bestDist && best != "" && closer(k, best, key) {
			best, bestDist = k, d
		}
	}
	return best
}

// closer breaks a tie between keys as far from key: a typo usually keeps
// the key's start, so the longer common prefix wins, then the first.
func closer(a, b, key string) bool {
	pa, pb := commonPrefix(a, key), commonPrefix(b, key)
	return pa > pb || pa == pb && a < b
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// editDistance is the Levenshtein distance of a and b, or max if at least
// that.
func editDistance(a, b string, max int) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		low := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			low = min(low, cur[j])
		}
		if low >= max {
			return max
		}
		prev, cur = cur, prev
	}
	return min(prev[len(b)], max)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// messageKeyAt returns the string argument at pos in a file if it is a
// known message key.
func (s *Server) messageKeyAt(sn *cache.Snapshot, path string, pos protocol.Position) (string, *protocol.Range) {
	b := s.messageBundles()
	sum := sn.Index().File(path)
	if b == nil || sum == nil {
		return "", nil
	}
	for i := range sum.StringArgs {
		a := &sum.StringArgs[i]
		if within(pos, a.Range) && b.Has(a.Value) {
			return a.Value, &a.Range
		}
	}
	return "", nil
}

func within(p protocol.Position, r protocol.Range) bool {
	after := p.Line > r.Start.Line || p.Line == r.Start.Line && p.Character >= r.Start.Character
	before := p.Line < r.End.Line || p.Line == r.End.Line && p.Character <= r.End.Character
	return after && before
}

// messageHover shows a message key's value in each locale.
func (s *Server) messageHover(sn *cache.Snapshot, path string, pos protocol.Position) *protocol.Hover {
	key, rng := s.messageKeyAt(sn, path, pos)
	if key == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("```properties\n" + key + "\n```\n\n*message key*\n")
	for _, e := range s.messageBundles().Keys[key] {
		locale := e.Locale
		if locale == "" {
			locale = "default"
		}
		b.WriteString("\n- **" + locale + "**: " + e.Value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: b.String()}, Range: rng}
}

// messageDefinition locates a message key's definitions.
func (s *Server) messageDefinition(sn *cache.Snapshot, path string, pos protocol.Position) []protocol.Location {
	key, _ := s.messageKeyAt(sn, path, pos)
	if key == "" {
		return nil
	}
	var out []protocol.Location
	for _, e := range s.messageBundles().Keys[key] {
		p := protocol.Position{Line: uint32(e.Line)}
		out = append(out, protocol.Location{URI: protocol.URIFromPath(e.Path), Range: protocol.Range{Start: p, End: p}})
	}
	return out
}
