package kotlin

import (
	"strings"
	"testing"
)

const hoverSrc = `package acme.greet

/**
 * Greets [name] politely.
 *
 * Uses a [Formatter](https://example.com/fmt) when set.
 *
 * @param name who to greet
 * @param times how many times;
 *   defaults to once
 * @return the greeting
 * @throws IllegalArgumentException if [name] is blank
 * @since 1.2
 */
@Deprecated("use hello")
suspend fun greet(name: String, times: Int = 1): String = name.repeat(times)

/** A box. */


class Detached

/** Holds an [item]. */
data class Box<T : Any>(val item: T, private val tag: String = "none") : Comparable<Box<T>> {
    /** How many. */
    var count = 0

    val label: String
        get() = tag

    override fun compareTo(other: Box<T>): Int = 0
}

fun String.shout(): String = uppercase()

fun configure(host: String, port: Int = 8080, secure: Boolean = false, retries: Int = 3, timeoutMillis: Long = 1000) {}

fun use(box: Box<String>) {
    val total = box.count + 1
    greet(name = "x")
    "a".shout()
    configure("h")
    println(total)
    val d = Detached()
}
`

func TestHover(t *testing.T) {
	f, ix := parseOne(t, "/w/acme/greet/Greet.kt", hoverSrc)
	for _, tt := range []struct {
		name   string
		needle string // hover at the first occurrence of needle...
		delta  int    // ...plus delta bytes
		want   string
	}{
		{
			name:   "function with KDoc tags",
			needle: "greet(name = ",
			want: "```kotlin\nsuspend fun greet(name: String, times: Int = …): String\n```\n\n*package `acme.greet`*\n\n---\n\n" +
				"Greets `name` politely.\n\nUses a [Formatter](https://example.com/fmt) when set.\n\n" +
				"**Parameters**\n- `name` — who to greet\n- `times` — how many times; defaults to once\n\n" +
				"**Returns**\n- the greeting\n\n" +
				"**Throws**\n- `IllegalArgumentException` — if `name` is blank\n\n" +
				"- *@since* 1.2",
		},
		{
			name:   "named argument",
			needle: "name = \"x\"",
			want:   "```kotlin\nname: String\n```\n\n*parameter of `greet`*",
		},
		{
			name:   "KDoc separated by two blank lines does not attach",
			needle: "Detached()",
			want:   "```kotlin\nclass Detached\n```\n\n*package `acme.greet`*",
		},
		{
			name:   "generic data class",
			needle: "Box<String>",
			want: "```kotlin\ndata class Box<T : Any>(val item: T, private val tag: String = …) : Comparable<Box<T>>\n```\n\n" +
				"*package `acme.greet`*\n\n---\n\nHolds an `item`.",
		},
		{
			name:   "member property with initializer",
			needle: "count + 1",
			want:   "```kotlin\nvar count = 0\n```\n\n*in `acme.greet.Box`*\n\n---\n\nHow many.",
		},
		{
			name:   "property with getter",
			needle: "label: String",
			want:   "```kotlin\nval label: String\n```\n\n*in `acme.greet.Box`*",
		},
		{
			name:   "extension function",
			needle: "shout()\n",
			want:   "```kotlin\nfun String.shout(): String\n```\n\n*extension on `String` in `acme.greet`*",
		},
		{
			name:   "long parameter list wraps",
			needle: "configure(\"h\")",
			want: "```kotlin\nfun configure(\n    host: String,\n    port: Int = …,\n    secure: Boolean = …,\n    retries: Int = …,\n    timeoutMillis: Long = …,\n)\n```\n\n" +
				"*package `acme.greet`*",
		},
		{
			name:   "local with inferred type",
			needle: "println(total)",
			delta:  len("println("),
			want:   "```kotlin\nval total = box.count + 1\n```\n\n*local*",
		},
		{
			name:   "parameter",
			needle: "box.count",
			want:   "```kotlin\nbox: Box<String>\n```\n\n*parameter*",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := strings.Index(hoverSrc, tt.needle)
			if i < 0 {
				t.Fatalf("%q not found", tt.needle)
			}
			h := Hover(f, ix, i+tt.delta)
			if h == nil {
				t.Fatal("no hover")
			}
			if h.Markdown != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", h.Markdown, tt.want)
			}
		})
	}
}

func TestHoverNothing(t *testing.T) {
	f, ix := parseOne(t, "/w/X.kt", "package p\n\nfun f() = println(\"x\")\n")
	if h := Hover(f, ix, strings.Index(string(f.Content), "println")); h != nil {
		t.Errorf("library function: got %q, want no hover", h.Markdown)
	}
}

func TestHoverAmbiguous(t *testing.T) {
	f, ix := parseOne(t, "/w/A.kt", `package p

class A {
    fun run() {}
}

class B {
    fun run() {}
}

fun f(xs: List<A>) = xs.forEach { it.run() }
`)
	h := Hover(f, ix, strings.LastIndex(string(f.Content), "run"))
	if h == nil || !strings.HasSuffix(h.Markdown, "_+1 other candidate_") {
		t.Errorf("got %v", h)
	}
}

func TestRenderKDocCodeFence(t *testing.T) {
	got := RenderKDoc("/**\n * Example:\n * ```\n * @Foo val x = [a]\n * ```\n */")
	want := "Example:\n```\n@Foo val x = [a]\n```"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
