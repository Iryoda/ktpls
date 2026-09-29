package kotlin

import (
	"slices"
	"strings"
	"testing"
)

const typesSrc = `package p

class Account(val id: String) {
    fun deposit(amount: Long) {}
}

class Bank(val accounts: List<Account>, val byId: Map<String, Account>) {
    fun primary(): Account? = accounts.firstOrNull()
    fun each(block: (Account) -> Unit) = accounts.forEach(block)
}

fun String.shout() = this

fun use(bank: Bank, names: Set<String>) {
    bank.accounts.forEach { it.deposit(1) }
    bank.accounts.map { acc -> acc.deposit(2) }
    bank.accounts.forEachIndexed { i, acc -> acc.deposit(3) }
    bank.accounts.filter { it.id != "" }.sortedBy { it.id }.forEach { it.deposit(4) }
    bank.accounts.first().deposit(5)
    bank.accounts[0].deposit(6)
    bank.byId["x"]?.deposit(7)
    bank.primary()?.let { it.deposit(8) }
    bank.each { it.deposit(9) }
    names.forEach { it.shout() }
    for (a in bank.accounts) {
        a.deposit(10)
    }
    val found = bank.accounts.find { it.id == "a" }
    found?.deposit(11)
}
`

// TestInferredTypes checks definition through inferred receiver types:
// each deposit(N) must resolve to Account.deposit.
func TestInferredTypes(t *testing.T) {
	f, ix := parseOne(t, "/w/T.kt", typesSrc)
	src := string(f.Content)
	for n := 1; n <= 11; n++ {
		needle := "deposit(" + itoa(n-1) + ")"
		i := strings.Index(src, needle)
		if i < 0 {
			t.Fatalf("%q not found", needle)
		}
		locs := Definition(f, ix, i)
		if len(locs) != 1 || locs[0].Range.Start.Line != 3 {
			t.Errorf("%s: got %v, want Account.deposit (line 4)", needle, locs)
		}
	}
	// it: String gets the String extension.
	if locs := Definition(f, ix, strings.Index(src, "shout() }")); len(locs) != 1 || locs[0].Range.Start.Line != 11 {
		t.Errorf("shout: got %v", locs)
	}
}

func TestInferredTypesCompletionAndHover(t *testing.T) {
	src := strings.Replace(typesSrc, "it.deposit(1)", "it.", 1)
	i := strings.Index(src, "{ it. }") + len("{ it.")
	f, ix := parseOne(t, "/w/T.kt", src)
	var labels []string
	for _, it := range Complete(f, ix, i).Items {
		labels = append(labels, it.Label)
	}
	if !slices.Contains(labels, "deposit") || !slices.Contains(labels, "id") {
		t.Errorf("completion on it.: %v", labels)
	}

	f, ix = parseOne(t, "/w/T.kt", typesSrc)
	h := Hover(f, ix, strings.Index(typesSrc, "it.deposit(1)"))
	if h == nil || !strings.Contains(h.Markdown, "it: Account") {
		t.Errorf("hover on it: %v", h)
	}
	h = Hover(f, ix, strings.Index(typesSrc, "acc -> acc"))
	if h == nil || !strings.Contains(h.Markdown, "acc: Account") || !strings.Contains(h.Markdown, "lambda parameter") {
		t.Errorf("hover on lambda parameter: %v", h)
	}
}

func TestTypeText(t *testing.T) {
	if got := typeArgs("Map<String, List<Pair<A, B>>>"); !slices.Equal(got, []string{"String", "List<Pair<A, B>>"}) {
		t.Errorf("typeArgs: %v", got)
	}
	if got := typeArgs("List<out Account>"); !slices.Equal(got, []string{"Account"}) {
		t.Errorf("typeArgs variance: %v", got)
	}
	if got := baseType("a.b.List<Foo>?"); got != "a.b.List" {
		t.Errorf("baseType: %q", got)
	}
	for _, tt := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"(Account) -> Unit", []string{"Account"}, true},
		{"(a: Account, n: Int) -> Boolean", []string{"Account", "Int"}, true},
		{"((Map<String, Int>) -> Unit)?", []string{"Map<String, Int>"}, true},
		{"suspend (Int) -> Unit", []string{"Int"}, true},
		{"() -> Unit", nil, true},
		{"Account.() -> Unit", nil, false},
		{"List<Int>", nil, false},
	} {
		got, ok := functionTypeParams(tt.in)
		if ok != tt.ok || !slices.Equal(got, tt.want) {
			t.Errorf("functionTypeParams(%q) = %v, %v", tt.in, got, ok)
		}
	}
}
