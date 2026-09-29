// Package build runs the project's build tool (Gradle) to compile Kotlin
// and turns the compiler's messages into diagnostics: the errors and
// warnings IntelliJ shows, computed by the same Kotlin compiler.
package build

import (
	"bufio"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Severity of a compiler message.
type Severity int

const (
	Error Severity = iota + 1
	Warning
	Info
)

// A Message is one compiler diagnostic. Line and Column are 1-based;
// Column counts UTF-16 code units, as the compiler (a JVM program) does.
// Path is empty for messages without a location.
type Message struct {
	Severity     Severity
	Path         string
	Line, Column int
	Text         string
}

var (
	// Kotlin 2.x: "e: file:///abs/Foo.kt:12:5 message"
	uriLocRE = regexp.MustCompile(`^([ewi]): (file://\S+?):(\d+):(\d+) (.*)$`)
	// Kotlin 1.x: "e: /abs/Foo.kt: (12, 5): message"
	pathLocRE = regexp.MustCompile(`^([ewi]): (/.+?\.kts?): \((\d+), (\d+)\): (.*)$`)
	// No location: "e: message"
	bareRE = regexp.MustCompile(`^([ewi]): (.*)$`)
)

var severities = map[string]Severity{"e": Error, "w": Warning, "i": Info}

// ParseOutput extracts the Kotlin compiler's messages from build output.
// Lines following a message that aren't build tool output (a message's
// continuation, like a multi-line type description) are appended to it.
func ParseOutput(out string) []Message {
	var msgs []Message
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	continuing := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m, ok := parseLine(line); ok {
			msgs = append(msgs, m)
			continuing = true
			continue
		}
		if continuing && isContinuation(line) {
			msgs[len(msgs)-1].Text += "\n" + line
			continue
		}
		continuing = false
	}
	return msgs
}

func parseLine(line string) (Message, bool) {
	if m := uriLocRE.FindStringSubmatch(line); m != nil {
		path, ok := uriPath(m[2])
		if ok {
			return located(m[1], path, m[3], m[4], m[5]), true
		}
	}
	if m := pathLocRE.FindStringSubmatch(line); m != nil {
		return located(m[1], filepath.Clean(m[2]), m[3], m[4], m[5]), true
	}
	if m := bareRE.FindStringSubmatch(line); m != nil {
		return Message{Severity: severities[m[1]], Text: m[2]}, true
	}
	return Message{}, false
}

func located(sev, path, line, col, text string) Message {
	l, _ := strconv.Atoi(line)
	c, _ := strconv.Atoi(col)
	return Message{Severity: severities[sev], Path: path, Line: l, Column: c, Text: text}
}

func uriPath(u string) (string, bool) {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(parsed.Path)), true
}

// isContinuation reports whether line may continue a compiler message
// rather than being build tool output.
func isContinuation(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	for _, p := range []string{"> Task ", "> ", "FAILURE:", "BUILD ", "* ", "What went wrong", "Execution failed",
		"Compilation error", "Run with", "Get more help", "Deprecated Gradle", "You can use", "For more on",
		"Configuration cache", "Reusing configuration", "Calculating task graph", "Starting a Gradle Daemon",
		"[Incubating]", "w: ", "e: ", "i: ", "actionable task"} {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}
