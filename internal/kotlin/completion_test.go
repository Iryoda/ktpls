package kotlin

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Iryoda/ktpls/internal/protocol"
)

// completionLib is a second file in every completion test's workspace.
const completionLib = `package acme.lib

class Account(val id: String, val owner: String) {
    fun deposit(amount: Long) {}
    fun withdraw(amount: Long) {}
    companion object {
        fun create(): Account = Account("", "")
    }
}

enum class Status { ACTIVE, CLOSED }

fun formatMoney(amount: Long): String = ""

fun String.shout(): String = this
`

// complete returns the completion labels at the "|" in src.
func complete(t *testing.T, src string) (*protocol.CompletionList, []string) {
	t.Helper()
	i := strings.Index(src, "|")
	if i < 0 {
		t.Fatal("no cursor marker")
	}
	src = src[:i] + src[i+1:]
	ix := NewIndex()
	lib, _ := parseOne(t, "/w/acme/lib/Lib.kt", completionLib)
	ix.Update(lib.Summary)
	f, _ := parseOne(t, "/w/acme/app/App.kt", src)
	ix.Update(f.Summary)
	list := Complete(f, ix, i)
	var labels []string
	for _, it := range list.Items {
		labels = append(labels, it.Label)
	}
	return list, labels
}

func item(list *protocol.CompletionList, label string) *protocol.CompletionItem {
	for i := range list.Items {
		if list.Items[i].Label == label {
			return &list.Items[i]
		}
	}
	return nil
}

func TestCompletion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		src     string
		want    []string // present, in this relative order
		notWant []string
	}{
		{
			name: "locals before members before package",
			src: `package acme.app

import acme.lib.Account

val appName = "x"

class Service(private val account: Account) {
    private val limit = 10

    fun run(amountParam: Long) {
        val amountLocal = 1
        a|
    }
}
`,
			want:    []string{"amountLocal", "amountParam", "account", "appName"},
			notWant: []string{"deposit"}, // a member of Account, not in scope
		},
		{
			name: "members of a typed receiver",
			src: `package acme.app

import acme.lib.Account

fun f(acc: Account) {
    acc.|
}
`,
			want:    []string{"deposit", "id", "owner", "withdraw"},
			notWant: []string{"create", "formatMoney", "Account"},
		},
		{
			name: "receiver typed by initializer, with prefix",
			src: `package acme.app

import acme.lib.Account

fun f() {
    val acc = Account("1", "me")
    acc.wi|
}
`,
			want:    []string{"withdraw"},
			notWant: []string{"deposit"},
		},
		{
			name: "companion members through the class name",
			src: `package acme.app

import acme.lib.Account

fun f() = Account.|
`,
			want:    []string{"create"},
			notWant: []string{"deposit", "id"},
		},
		{
			name: "enum entries",
			src: `package acme.app

import acme.lib.Status

fun f() = Status.|
`,
			want: []string{"ACTIVE", "CLOSED"},
		},
		{
			name: "extensions on a library type",
			src: `package acme.app

import acme.lib.shout

fun f(s: String) = s.|
`,
			want: []string{"shout"},
		},
		{
			name: "unknown receiver offers nothing",
			src: `package acme.app

fun f(x: Any) = x.|
`,
			notWant: []string{"deposit", "shout", "formatMoney"},
		},
		{
			name: "type position offers only types",
			src: `package acme.app

import acme.lib.*

val x: A|
`,
			want:    []string{"Account"},
			notWant: []string{"abstract", "appName"},
		},
		{
			name: "keywords",
			src: `package acme.app

fun f() {
    whe|
}
`,
			want: []string{"when"},
		},
		{
			name: "nothing inside comments or strings",
			src: `package acme.app

// acc|
`,
			notWant: []string{"Account", "abstract"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, labels := complete(t, tt.src)
			last := -1
			for _, w := range tt.want {
				i := slices.Index(labels, w)
				if i < 0 {
					t.Errorf("missing %q in %v", w, labels)
					continue
				}
				if i < last {
					t.Errorf("%q ranked before its predecessor in want list: %v", w, labels)
				}
				last = i
			}
			for _, nw := range tt.notWant {
				if slices.Contains(labels, nw) {
					t.Errorf("unexpected %q in %v", nw, labels)
				}
			}
		})
	}
}

func TestCompletionAutoImport(t *testing.T) {
	list, _ := complete(t, `package acme.app

import acme.lib.Status

fun f() {
    formatMo|
}
`)
	it := item(list, "formatMoney")
	if it == nil {
		t.Fatal("formatMoney not offered")
	}
	if len(it.AdditionalTextEdits) != 1 {
		t.Fatalf("additional edits = %v", it.AdditionalTextEdits)
	}
	e := it.AdditionalTextEdits[0]
	// Inserted at the end of the last import (line 2, after "import acme.lib.Status").
	if e.NewText != "\nimport acme.lib.formatMoney" || e.Range.Start != (protocol.Position{Line: 2, Character: 22}) {
		t.Errorf("import edit = %+v", e)
	}
	if it.TextEdit == nil || it.TextEdit.NewText != "formatMoney" || it.TextEdit.Range.Start.Character != 4 {
		t.Errorf("text edit = %+v", it.TextEdit)
	}

	// Already imported: no edit.
	list, _ = complete(t, "package acme.app\n\nimport acme.lib.Status\n\nval s = Sta|\n")
	if it := item(list, "Status"); it == nil || len(it.AdditionalTextEdits) != 0 {
		t.Errorf("imported Status: %+v", it)
	}

	// No imports yet: goes after the package header.
	list, _ = complete(t, "package acme.app\n\nval s = Acc|\n")
	if it := item(list, "Account"); it == nil || len(it.AdditionalTextEdits) != 1 || it.AdditionalTextEdits[0].NewText != "\n\nimport acme.lib.Account" {
		t.Errorf("Account: %+v", it)
	}
}

func TestCompletionWhileTyping(t *testing.T) {
	// A member access at the end of a line: the parser may attach the next
	// line's identifier as the member name.
	_, labels := complete(t, `package acme.app

import acme.lib.Account

fun f(acc: Account) {
    acc.|
    println("x")
}
`)
	if !slices.Contains(labels, "deposit") {
		t.Errorf("missing deposit in %v", labels)
	}
}

func TestCompletionNamedArguments(t *testing.T) {
	for _, src := range []string{
		"package acme.app\n\nimport acme.lib.Account\n\nfun f() = Account(ow|",
		"package acme.app\n\nimport acme.lib.Account\n\nfun f() = Account(id = \"1\", ow|)\n",
		"package acme.app\n\nimport acme.lib.Account\n\nfun f(a: Account) {\n    a.deposit(\n        am|\n",
	} {
		list, labels := complete(t, src)
		want := "owner ="
		if strings.Contains(src, "deposit") {
			want = "amount ="
		}
		it := item(list, want)
		if it == nil {
			t.Errorf("%q: missing %q in %v", src, want, labels)
			continue
		}
		if labels[0] != want || it.TextEdit.NewText != strings.TrimSuffix(want, " =")+" = " {
			t.Errorf("%q: first=%q edit=%+v", src, labels[0], it.TextEdit)
		}
	}
	// Not at the start of an argument: no parameter names.
	if list, _ := complete(t, "package acme.app\n\nimport acme.lib.Account\n\nfun f() = Account(\"x\" + ow|"); item(list, "owner =") != nil {
		t.Error("offered a parameter name mid-expression")
	}
}

func TestArgumentListStart(t *testing.T) {
	for _, tt := range []struct {
		src  string // "|" marks the offset, "^" the expected paren
		want bool
	}{
		{`f^(|`, true},
		{`f^(a, |`, true},
		{`f^(x = "a, (b", |`, true},
		{`f^(x = "say \"hi\" (", |`, true},
		{`f^(g(1), |`, true},
		{`f(g^(|`, true},
		{`f(a + |`, false},
		{`list.map { |`, false},
	} {
		want := strings.Index(tt.src, "^")
		src := strings.Replace(tt.src, "^", "", 1)
		off := strings.Index(src, "|")
		src = strings.Replace(src, "|", "", 1)
		got := argumentListStart([]byte(src), off)
		if tt.want && got != want || !tt.want && got != -1 {
			t.Errorf("%s: got %d, want %d (ok=%v)", tt.src, got, want, tt.want)
		}
	}
}

func TestCompletionReceiverShapes(t *testing.T) {
	for _, src := range []string{
		// Inside a multi-line argument list.
		"package acme.app\n\nimport acme.lib.Account\n\nfun f(acc: Account) = listOf(\n    x = acc.|\n)\n",
		// Receiver is a call, or a call with a trailing lambda.
		"package acme.app\n\nimport acme.lib.Account\n\nfun f() = Account.create().|\n",
		"package acme.app\n\nimport acme.lib.Account\n\nfun f(acc: Account) = acc.also { }.|\n",
		// Parenthesized.
		"package acme.app\n\nimport acme.lib.Account\n\nfun f(acc: Account) = (acc).|\n",
		// Safe call.
		"package acme.app\n\nimport acme.lib.Account\n\nfun f(acc: Account?) = acc?.|\n",
	} {
		if _, labels := complete(t, src); !slices.Contains(labels, "deposit") {
			t.Errorf("%q: missing deposit in %v", src, labels)
		}
	}
}

func TestCompletionEmptyIsArray(t *testing.T) {
	for _, src := range []string{"package p\n\n// in a comment|\n", "package p\n\nval n = 12|\n"} {
		list, _ := complete(t, src)
		b, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"items":[]`) {
			t.Errorf("%q: got %s, want items []", src, b)
		}
	}
}

const filledSrc = `package acme.app

fun generateKey(modelBrand: String, modelName: String, storageName: String, colorName: String, memoryName: String?): String = ""

data class Phone(val brand: String, val model: String, val color: String)

fun use(modelBrand: String, modelName: String) {
    CALL
}
`

func namedLabels(t *testing.T, call string) []string {
	t.Helper()
	_, labels := complete(t, strings.Replace(filledSrc, "CALL", call, 1))
	var out []string
	for _, l := range labels {
		if before, ok := strings.CutSuffix(l, " ="); ok {
			out = append(out, before)
		}
	}
	slices.Sort(out)
	return out
}

func TestCompletionSkipsFilledArguments(t *testing.T) {
	for _, tt := range []struct {
		call string
		want []string
	}{
		// The user's case: two named, one being typed after them.
		{"generateKey(\n        modelBrand = modelBrand,\n        modelName = modelName,\n        |\n    )", []string{"colorName", "memoryName", "storageName"}},
		// Filled arguments after the cursor count too.
		{"generateKey(\n        |\n        modelName = modelName,\n        colorName = \"c\",\n    )", []string{"memoryName", "modelBrand", "storageName"}},
		// Positional arguments fill the leading parameters.
		{"generateKey(modelBrand, modelName, |)", []string{"colorName", "memoryName", "storageName"}},
		// The argument being typed doesn't hide its own name.
		{"generateKey(modelBrand = modelBrand, sto|)", []string{"storageName"}},
		// Constructors: data class properties.
		{"Phone(brand = \"b\", |)", []string{"color", "model"}},
		// Strings containing commas or parentheses don't confuse it.
		{"Phone(brand = \"a, (b\", |)", []string{"color", "model"}},
	} {
		if got := namedLabels(t, tt.call); !slices.Equal(got, tt.want) {
			t.Errorf("%s:\n got  %v\n want %v", tt.call, got, tt.want)
		}
	}
}

func TestCompletionArgumentsAreFocused(t *testing.T) {
	// At an argument start: parameter names first; no keyword or
	// unimported-workspace flood.
	_, labels := complete(t, strings.Replace(filledSrc, "CALL", "Phone(brand = \"b\", mo|)", 1))
	if len(labels) == 0 || labels[0] != "model =" {
		t.Fatalf("first suggestion: %v", labels)
	}
	for _, l := range labels {
		if slices.Contains(keywords, l) && l != "null" && l != "true" && l != "false" && l != "this" {
			t.Errorf("keyword %q offered inside arguments: %v", l, labels)
		}
		if l == "Money" || l == "formatMoney" {
			t.Errorf("unimported symbol %q offered inside arguments", l)
		}
	}
	// Locals still come right after the parameter names.
	_, labels = complete(t, strings.Replace(filledSrc, "CALL", "generateKey(mod|)", 1))
	if !slices.Contains(labels, "modelBrand") || slices.Index(labels, "modelBrand =") > slices.Index(labels, "modelBrand") {
		t.Errorf("order: %v", labels)
	}
}

func TestCompletionMergesOverloads(t *testing.T) {
	list, labels := complete(t, strings.Replace(filledSrc, "CALL", "gen|", 1)+"\nfun generateKey(x: Int) = \"\"\n")
	n := 0
	for _, l := range labels {
		if l == "generateKey" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("generateKey listed %d times: %v", n, labels)
	}
	if it := item(list, "generateKey"); it.LabelDetails == nil || it.LabelDetails.Detail != " (+1 overload)" {
		t.Errorf("label details: %+v", it.LabelDetails)
	}
}
