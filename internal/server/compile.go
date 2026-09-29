package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Iryoda/ktpls/internal/build"
	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/protocol"
	"github.com/Iryoda/ktpls/internal/util/textutil"
)

// Compiler diagnostics: the project's build (Gradle) compiles the Kotlin
// sources in the background after the workspace loads and after each
// save, and the compiler's errors and warnings are published alongside
// ktpls's own syntax diagnostics.

// initOptions are the client's initializationOptions.
type initOptions struct {
	Compile *struct {
		Enabled *bool             `json:"enabled"`
		Command []string          `json:"command"`
		Tasks   []string          `json:"tasks"`
		Env     map[string]string `json:"env"`
	} `json:"compile"`
}

// setupCompile creates the build runner, if the project has a build and
// the client didn't disable compilation.
func (s *Server) setupCompile(root string, raw json.RawMessage, progress bool) {
	cfg := build.Config{Enabled: true}
	var opts initOptions
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &opts); err != nil {
			s.log.Warn("invalid initializationOptions", "err", err)
		}
	}
	if c := opts.Compile; c != nil {
		if c.Enabled != nil {
			cfg.Enabled = *c.Enabled
		}
		cfg.Command, cfg.Tasks, cfg.Env = c.Command, c.Tasks, c.Env
	}
	s.progress = progress
	s.builder = build.NewRunner(root, cfg, s.log, s.buildStarted, s.buildDone)
	if s.builder == nil {
		s.log.Info("compiler diagnostics off", "enabled", cfg.Enabled)
	}
}

// requestBuild asks for a build (a no-op without a build).
func (s *Server) requestBuild() {
	if s.builder != nil {
		s.builder.Request(s.ctx)
	}
}

const progressToken = "ktpls/compile"

func (s *Server) buildStarted() {
	if !s.progress {
		return
	}
	if _, err := s.client.Request(s.ctx, "window/workDoneProgress/create", &protocol.WorkDoneProgressCreateParams{Token: progressToken}); err != nil {
		s.log.Debug("creating progress", "err", err)
		return
	}
	s.client.Notify("$/progress", &protocol.ProgressParams{Token: progressToken, Value: &protocol.WorkDoneProgressBegin{
		Kind: "begin", Title: "Compiling", Message: "Kotlin",
	}})
}

// buildDone updates the compiler diagnostics with a build's result.
//
// Kotlin compiles incrementally: only changed files (and dependents) are
// recompiled, and only they get messages. A file with errors stays dirty
// until fixed, so after a build that compiled, the errors are the
// complete set; warnings, though, are only re-reported for recompiled
// files, so they are replaced only for files mentioned in the output or
// saved since the last build.
func (s *Server) buildDone(res build.Result) {
	s.diagMu.Lock()
	changed := map[string]bool{}
	for p := range s.compileMsgs {
		changed[p] = true
	}
	if res.Compiled {
		mentioned := map[string]bool{}
		for _, m := range res.Messages {
			mentioned[m.Path] = true
		}
		next := map[string][]build.Message{}
		for p, msgs := range s.compileMsgs {
			if mentioned[p] || s.savedSinceBuild[p] {
				continue
			}
			for _, m := range msgs {
				if m.Severity == build.Warning {
					next[p] = append(next[p], m)
				}
			}
		}
		for _, m := range res.Messages {
			if m.Path != "" && m.Severity != build.Info {
				next[m.Path] = append(next[m.Path], m)
			}
		}
		s.compileMsgs = next
		s.savedSinceBuild = map[string]bool{}
	}
	errors, warnings := 0, 0
	for p, msgs := range s.compileMsgs {
		changed[p] = true
		for _, m := range msgs {
			if m.Severity == build.Error {
				errors++
			} else {
				warnings++
			}
		}
	}
	failure := res.Failure
	newFailure := failure != "" && failure != s.lastBuildFailure
	s.lastBuildFailure = failure
	s.diagMu.Unlock()

	for p := range changed {
		s.publishDiagnostics(p)
	}
	for _, m := range res.Messages {
		if m.Path == "" {
			s.logMessage(protocol.MessageWarning, "ktpls: compiler: %s", m.Text)
		}
	}
	summary := fmt.Sprintf("%d %s, %d %s", errors, textutil.Plural(errors, "error", "errors"), warnings, textutil.Plural(warnings, "warning", "warnings"))
	if failure != "" {
		summary = "build failed"
	}
	if s.progress {
		s.client.Notify("$/progress", &protocol.ProgressParams{Token: progressToken, Value: &protocol.WorkDoneProgressEnd{Kind: "end", Message: summary}})
	}
	if newFailure {
		msg := "ktpls: the build failed, so compiler diagnostics may be incomplete:\n" + textutil.Truncate(failure, 500)
		if strings.Contains(failure, "invalid source release") || strings.Contains(failure, "toolchain") || strings.Contains(failure, "JAVA_HOME") {
			msg += "\nThe build may need another JDK: set compile.env.JAVA_HOME in ktpls's init_options."
		}
		s.client.Notify("window/showMessage", &protocol.LogMessageParams{Type: protocol.MessageWarning, Message: msg})
	}
}

// compileDiagnostics renders the compiler messages for a file with the
// given content.
func (s *Server) compileDiagnostics(content []byte, msgs []build.Message) []protocol.Diagnostic {
	if len(msgs) == 0 {
		return nil
	}
	u16 := protocol.NewMapper(content, protocol.PositionEncodingUTF16)
	m := protocol.NewMapper(content, s.session.Encoding())
	var out []protocol.Diagnostic
	for _, msg := range msgs {
		// The compiler gives a 1-based line and column (in UTF-16 code
		// units); underline the identifier or token there.
		off := u16.PositionOffset(protocol.Position{Line: uint32(max(msg.Line-1, 0)), Character: uint32(max(msg.Column-1, 0))})
		off = skipAssignment(content, off)
		rng, _ := m.OffsetRange(off, tokenEnd(content, off))
		sev := protocol.SeverityError
		if msg.Severity == build.Warning {
			sev = protocol.SeverityWarning
		}
		out = append(out, protocol.Diagnostic{Range: rng, Severity: sev, Source: "kotlinc", Message: msg.Text})
	}
	return out
}

// fileContent returns the current content of path: the open buffer, or
// the file on disk.
func fileContent(sn *cache.Snapshot, path string) []byte {
	if f := sn.File(path); f != nil && f.Content != nil {
		return f.Content
	}
	content, _ := os.ReadFile(path)
	return content
}

// tokenEnd returns the end of what the compiler points at when it reports
// a position: an identifier, a string literal, or a bracketed expression
// (within the line), else one character.
func tokenEnd(src []byte, off int) int {
	if off >= len(src) || src[off] == '\n' || src[off] == '\r' {
		return off
	}
	lineEnd := off
	for lineEnd < len(src) && src[lineEnd] != '\n' {
		lineEnd++
	}
	r, size := utf8.DecodeRune(src[off:])
	switch {
	case textutil.IsIdentRune(r):
		end := off
		for end < lineEnd {
			r, size := utf8.DecodeRune(src[end:])
			if !textutil.IsIdentRune(r) {
				break
			}
			end += size
		}
		return end
	case r == '"':
		for i := off + 1; i < lineEnd; i++ {
			switch src[i] {
			case '\\':
				i++
			case '"':
				return i + 1
			}
		}
	case r == '(' || r == '[' || r == '{':
		closing := map[rune]byte{'(': ')', '[': ']', '{': '}'}[r]
		depth := 0
		for i := off; i < lineEnd; i++ {
			switch src[i] {
			case byte(r):
				depth++
			case closing:
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
	}
	return off + size
}

// skipAssignment moves an offset at `=` (where the compiler reports some
// initializer errors) to the value after it, on the same line.
func skipAssignment(src []byte, off int) int {
	if off >= len(src) || src[off] != '=' || off+1 < len(src) && src[off+1] == '=' {
		return off
	}
	i := off + 1
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i < len(src) && src[i] != '\n' && src[i] != '\r' {
		return i
	}
	return off
}
