package kotlin

import (
	"slices"
	"strings"
	"testing"
)

func TestTypeDefinition(t *testing.T) {
	f, ix := parseOne(t, "/w/T.kt", `package p

class Account(val owner: Owner)
class Owner(val name: String)

class Bank(val accounts: List<Account>, val index: Map<String, Owner>) {
    fun primary(): Account = accounts.first()
}

fun use(bank: Bank) {
    val acc = bank.primary()
    acc.owner.name
    bank.accounts.forEach { it.owner }
    println(bank)
}
`)
	src := string(f.Content)
	lines := func(needle string, delta int) []int {
		var out []int
		for _, l := range TypeDefinition(f, ix, strings.Index(src, needle)+delta) {
			out = append(out, int(l.Range.Start.Line)+1)
		}
		slices.Sort(out)
		return out
	}
	for _, tt := range []struct {
		needle string
		delta  int
		want   []int
	}{
		{"bank: Bank", 0, []int{6}},         // parameter
		{"acc = bank", 0, []int{3}},         // local inferred from a call
		{"acc.owner.name", 4, []int{4}},     // property
		{"acc.owner.name", 10, nil},         // String: a library type
		{"accounts.forEach", 0, []int{3}},   // List<Account> -> Account
		{"it.owner", 0, []int{3}},           // implicit lambda parameter
		{"index: Map", 0, []int{4}},         // Map<String, Owner> -> Owner
		{"primary(): Account", 0, []int{3}}, // function: its return type
		{"Owner(val", 0, []int{4}},          // a type names itself
	} {
		if got := lines(tt.needle, tt.delta); !slices.Equal(got, tt.want) {
			t.Errorf("%q+%d: got %v, want %v", tt.needle, tt.delta, got, tt.want)
		}
	}
}
