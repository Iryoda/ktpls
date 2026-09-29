package fuzzy

import "testing"

func TestScore(t *testing.T) {
	for _, tt := range []struct {
		pattern, cand string
		ok            bool
	}{
		{"", "anything", true},
		{"get", "getName", true},
		{"gn", "getName", true},
		{"GN", "getName", true},
		{"ng", "getName", false},
		{"xyz", "getName", false},
		{"cfg", "configure", true},
	} {
		if _, ok := Score(tt.pattern, tt.cand); ok != tt.ok {
			t.Errorf("Score(%q, %q) ok = %v, want %v", tt.pattern, tt.cand, ok, tt.ok)
		}
	}

	// Ranking: prefix > camelCase humps > scattered.
	better := func(p, a, b string) {
		t.Helper()
		sa, _ := Score(p, a)
		sb, _ := Score(p, b)
		if sa <= sb {
			t.Errorf("Score(%q): %q (%d) should beat %q (%d)", p, a, sa, b, sb)
		}
	}
	better("get", "getName", "targetName")
	better("gN", "getName", "gardenNames")
	better("us", "userService", "focus")
	better("name", "name", "nameOrNull")
}
