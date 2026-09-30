package kotlin

import (
	"slices"
	"strings"
	"testing"
)

func TestKeyParams(t *testing.T) {
	ix := NewIndex()
	add := func(path, src string) {
		f, _ := parseOne(t, path, src)
		ix.Update(f.Summary)
	}
	add("/p/Errors.kt", `package p
class AppException(val code: String, val args: List<String> = emptyList()) : Exception()
fun label(@PropertyKey(resourceBundle = "messages") key: String) = key
`)
	add("/p/Use.kt", `package p
fun f(id: String) {
    throw AppException("user.not-found", listOf(id))
}
fun g() = AppException(code = "user.disabled")
fun h() = AppException("order.not-found")
fun i() = AppException("user.nt-found")
fun j() = label("anything")
fun k() = println("user.not-found")
fun l() = println("hello")
fun m() = println("world")
fun n() = println("again")
`)
	keys := map[string]bool{"user.not-found": true, "user.disabled": true, "order.not-found": true}
	got := KeyParams(ix, func(k string) bool { return keys[k] })
	var names []string
	for p := range got {
		names = append(names, string(p))
	}
	slices.Sort(names)
	// AppException(code) is learned (3 of 4 literals are keys, 75% < 80%:
	// so not yet); label(key) is declared.
	if strings.Join(names, " ") != "label(key)" {
		t.Errorf("key params: %v", names)
	}
	keys["user.nt-found"] = true // now 4 of 4
	got = KeyParams(ix, func(k string) bool { return keys[k] })
	if !got["AppException(code)"] || got["println(#0)"] {
		t.Errorf("key params: %v", got)
	}
}
