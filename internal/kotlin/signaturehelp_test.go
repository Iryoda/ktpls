package kotlin

import (
	"strings"
	"testing"
)

const sigSrc = `package p

/** Sends a message. */
fun send(to: String, body: String, retries: Int = 3): Boolean = true

fun log(level: Int, vararg parts: String) {}

fun pick(a: Int) {}
fun pick(a: Int, b: Int) {}

class Box(val width: Int, val height: Int)

fun use(x: String) {
    CURSOR
}
`

// help returns the label of the active signature with the active
// parameter bracketed, e.g. "send(to: String, [body: String], ...)".
func help(t *testing.T, call string) string {
	t.Helper()
	src := strings.Replace(sigSrc, "CURSOR", call, 1)
	i := strings.Index(src, "|")
	src = src[:i] + src[i+1:]
	f, ix := parseOne(t, "/w/S.kt", src)
	h := SignatureHelp(f, ix, i)
	if h == nil {
		return "<none>"
	}
	sig := h.Signatures[h.ActiveSignature]
	if int(h.ActiveParameter) >= len(sig.Parameters) {
		return sig.Label
	}
	p := sig.Parameters[h.ActiveParameter].Label // ASCII: UTF-16 offsets = bytes
	return sig.Label[:p[0]] + "[" + sig.Label[p[0]:p[1]] + "]" + sig.Label[p[1]:]
}

func TestSignatureHelp(t *testing.T) {
	for _, tt := range []struct{ call, want string }{
		{`send(|`, `send([to: String], body: String, retries: Int): Boolean`},
		{`send("a", |`, `send(to: String, [body: String], retries: Int): Boolean`},
		{`send("a, b", "c", |)`, `send(to: String, body: String, [retries: Int]): Boolean`},
		{`send(to = "a", retries = |`, `send(to: String, body: String, [retries: Int]): Boolean`},
		{`send(listOf(1, 2).size.toString(), |`, `send(to: String, [body: String], retries: Int): Boolean`},
		{"send(\n        \"a\",\n        |", `send(to: String, [body: String], retries: Int): Boolean`},
		{`log(1, "a", "b", |`, `log(level: Int, [vararg parts: String])`},
		{`pick(1, |`, `pick(a: Int, [b: Int])`}, // the overload that fits
		{`Box(1, |`, `Box(width: Int, [height: Int])`},
		{`send("a") { |`, `<none>`}, // inside a lambda
		{`x.length|`, `<none>`},
	} {
		if got := help(t, tt.call); got != tt.want {
			t.Errorf("%s:\n got  %s\n want %s", tt.call, got, tt.want)
		}
	}
}

func TestSignatureHelpDocAndOverloads(t *testing.T) {
	src := strings.Replace(sigSrc, "CURSOR", "pick(", 1)
	f, ix := parseOne(t, "/w/S.kt", src)
	h := SignatureHelp(f, ix, strings.Index(src, "pick(\n")+5)
	if h == nil || len(h.Signatures) != 2 {
		t.Fatalf("pick overloads: %+v", h)
	}
	src = strings.Replace(sigSrc, "CURSOR", "send(", 1)
	f, ix = parseOne(t, "/w/S.kt", src)
	h = SignatureHelp(f, ix, strings.Index(src, "send(\n")+5)
	if h == nil || h.Signatures[0].Documentation == nil || h.Signatures[0].Documentation.Value != "Sends a message." {
		t.Errorf("doc: %+v", h)
	}
}
