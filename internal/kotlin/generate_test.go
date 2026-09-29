package kotlin

import (
	"strings"
	"testing"
)

const generateLib = `package lib

import java.math.BigDecimal

interface Repo<T> {
    fun save(item: T): T
    fun all(): List<T>
    val size: Int
    var name: String
    fun describe() = "repo"
}

abstract class Priced {
    abstract fun price(): BigDecimal
    open fun currency() = "BRL"
}

class Money(val cents: Long)

enum class Color { RED, GREEN, BLUE }

sealed interface Shape
object Dot : Shape
class Circle(val r: Double) : Shape
class Square(val side: Double) : Shape
`

func TestImplementMembers(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"generic interface", `package app

import lib.Money
import lib.Repo

class |MoneyRepo : Repo<Money> {
}
`, "Implement members: save, all, size (+1)", `package app

import lib.Money
import lib.Repo

class MoneyRepo : Repo<Money> {
    override fun save(item: Money): Money {
        TODO("Not yet implemented")
    }

    override fun all(): List<Money> {
        TODO("Not yet implemented")
    }

    override val size: Int
        get() = TODO("Not yet implemented")

    override var name: String
        get() = TODO("Not yet implemented")
        set(value) {}
}
`},
		{"library type imported from the supertype's file", `package app

import lib.Priced

class |Item : Priced() {
    fun other() = 1
}
`, "Implement members: price", `package app

import lib.Priced
import java.math.BigDecimal

class Item : Priced() {
    fun other() = 1

    override fun price(): BigDecimal {
        TODO("Not yet implemented")
    }
}
`},
		{"no body yet", "package app\n\nimport lib.Priced\n\nobject |Free : Priced()\n", "Implement members: price",
			"package app\n\nimport lib.Priced\nimport java.math.BigDecimal\n\nobject Free : Priced() {\n    override fun price(): BigDecimal {\n        TODO(\"Not yet implemented\")\n    }\n}\n"},
		{"already implemented: not offered", `package app

import lib.Priced
import java.math.BigDecimal

class |Item : Priced() {
    override fun price() = BigDecimal.ONE
}
`, "Implement members: price", ""},
	}, generateLib)
}

func TestWhenBranches(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"enum", `package app

import lib.Color

fun f(c: Color) = |when (c) {
    Color.RED -> 1
}
`, "Add remaining branches", `package app

import lib.Color

fun f(c: Color) = when (c) {
    Color.RED -> 1
    Color.GREEN -> TODO()
    Color.BLUE -> TODO()
}
`},
		{"sealed, type imported", `package app

fun f(s: lib.Shape) = |when (s) {
    is lib.Circle -> 1
}
`, "Add remaining branches", `package app

import lib.Dot
import lib.Square

fun f(s: lib.Shape) = when (s) {
    is lib.Circle -> 1
    Dot -> TODO()
    is Square -> TODO()
}
`},
		{"one missing", "package app\n\nimport lib.Color\n\nfun f(c: Color) = |when (c) {\n    Color.RED, Color.GREEN -> 1\n}\n", "Add missing branch `Color.BLUE`",
			"package app\n\nimport lib.Color\n\nfun f(c: Color) = when (c) {\n    Color.RED, Color.GREEN -> 1\n    Color.BLUE -> TODO()\n}\n"},
		{"else: not offered", "package app\n\nimport lib.Color\n\nfun f(c: Color) = |when (c) {\n    Color.RED -> 1\n    else -> 2\n}\n", "Add remaining branches", ""},
	}, generateLib)
}

func TestCreateFunction(t *testing.T) {
	runRefactorCases(t, []refactorCase{
		{"statement, typed arguments", `package app

import lib.Money

fun use(total: Money) {
    |record(total, "note", 3)
}
`, "Create function `record`", `package app

import lib.Money

fun use(total: Money) {
    record(total, "note", 3)
}

fun record(total: Money, string: String, int: Int) {
    TODO("Not yet implemented")
}
`},
		{"expected type from declaration", "package app\n\nval ok: Boolean = |check2(1)\n", "Create function `check2`",
			"package app\n\nval ok: Boolean = check2(1)\n\nfun check2(int: Int): Boolean {\n    TODO(\"Not yet implemented\")\n}\n"},
		{"condition is Boolean", "package app\n\nfun f(n: Int) {\n    if (|isBig(n)) println(n)\n}\n", "Create function `isBig`",
			"package app\n\nfun f(n: Int) {\n    if (isBig(n)) println(n)\n}\n\nfun isBig(n: Int): Boolean {\n    TODO(\"Not yet implemented\")\n}\n"},
		{"member in enclosing class", "package app\n\nclass Svc {\n    fun run() {\n        |helper(1)\n    }\n}\n", "Create member function `Svc.helper`",
			"package app\n\nclass Svc {\n    fun run() {\n        helper(1)\n    }\n\n    private fun helper(int: Int) {\n        TODO(\"Not yet implemented\")\n    }\n}\n"},
		{"member of receiver in this file", "package app\n\nclass Svc\n\nfun f(s: Svc) {\n    s.|start(true)\n}\n", "Create member function `Svc.start`",
			"package app\n\nclass Svc {\n    fun start(boolean: Boolean) {\n        TODO(\"Not yet implemented\")\n    }\n}\n\nfun f(s: Svc) {\n    s.start(true)\n}\n"},
		{"existing function: not offered", "package app\n\nfun g() = 1\n\nfun f() {\n    |g()\n}\n", "Create function `g`", ""},
	}, generateLib)
}

func TestCreateMemberInOtherFile(t *testing.T) {
	ix := NewIndex()
	libSrc := "package lib\n\nclass Store {\n    fun open() = 1\n}\n"
	lib, _ := parseOne(t, "/w/lib/Store.kt", libSrc)
	ix.Update(lib.Summary)
	app, _ := parseOne(t, "/w/app/App.kt", "package app\n\nimport lib.Store\nimport lib.Store as S\n\nfun f(s: Store) = s.close(2L)\n")
	ix.Update(app.Summary)
	read := func(path string) []byte {
		if path == lib.Path {
			return lib.Content
		}
		return nil
	}
	var got *Action
	for _, a := range CodeActionsWith(app, ix, strings.Index(string(app.Content), "close"), read) {
		if a.Title == "Create member function `Store.close`" {
			got = &a
		}
	}
	if got == nil || len(got.Edits) != 0 || len(got.Other[lib.URI]) != 1 {
		t.Fatalf("action: %+v", got)
	}
	e := got.Other[lib.URI][0]
	at := lib.Mapper.PositionOffset(e.Range.Start)
	out := libSrc[:at] + e.NewText + libSrc[at:]
	want := "package lib\n\nclass Store {\n    fun open() = 1\n\n    fun close(long: Long): Any {\n        TODO(\"Not yet implemented\")\n    }\n}\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
