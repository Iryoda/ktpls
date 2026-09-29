package textutil

import "testing"

func TestContainsWord(t *testing.T) {
	for _, tt := range []struct {
		content, name string
		want          bool
	}{
		{"val id = 1", "id", true},
		{"valid", "id", false},
		{"idx id", "id", true},
		{"x.id()", "id", true},
		{"_id", "id", false},
		{"id", "id", true},
		{"", "id", false},
	} {
		if got := ContainsWord([]byte(tt.content), tt.name); got != tt.want {
			t.Errorf("ContainsWord(%q, %q) = %v", tt.content, tt.name, got)
		}
	}
}

func TestUTF16Len(t *testing.T) {
	for s, want := range map[string]int{"": 0, "abc": 3, "é": 1, "中": 1, "😀": 2, "a😀b": 4} {
		if got := UTF16Len(s); got != want {
			t.Errorf("UTF16Len(%q) = %d, want %d", s, got, want)
		}
		if got := UTF16Len([]byte(s)); got != want {
			t.Errorf("UTF16Len([]byte(%q)) = %d, want %d", s, got, want)
		}
	}
}

func TestIndent(t *testing.T) {
	if got := IndentLines("a\nb\n\nc", "  "); got != "a\n  b\n\n  c" {
		t.Errorf("IndentLines: %q", got)
	}
	if got := DedentLines("a\n    b\nc", "    "); got != "a\nb\nc" {
		t.Errorf("DedentLines: %q", got)
	}
	src := []byte("x\n    y = 1\n")
	if got := LineIndent(src, 9); got != "    " {
		t.Errorf("LineIndent: %q", got)
	}
}
