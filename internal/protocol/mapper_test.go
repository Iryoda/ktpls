package protocol

import "testing"

func TestMapper(t *testing.T) {
	type pos = Position
	tests := []struct {
		name    string
		content string
		enc     PositionEncodingKind
		offset  int // byte offset
		want    Position
	}{
		{"start", "abc", PositionEncodingUTF16, 0, pos{0, 0}},
		{"ascii", "abc\ndef", PositionEncodingUTF16, 5, pos{1, 1}},
		{"eof", "abc\ndef", PositionEncodingUTF16, 7, pos{1, 3}},
		{"after trailing newline", "abc\n", PositionEncodingUTF16, 4, pos{1, 0}},
		{"crlf", "ab\r\ncd", PositionEncodingUTF16, 5, pos{1, 1}},
		{"lone cr", "ab\rcd", PositionEncodingUTF16, 4, pos{1, 1}},
		// "é" is 2 bytes in UTF-8, 1 UTF-16 unit.
		{"2-byte utf16", "é=x", PositionEncodingUTF16, 3, pos{0, 2}},
		{"2-byte utf8", "é=x", PositionEncodingUTF8, 3, pos{0, 3}},
		// "中" is 3 bytes, 1 unit.
		{"3-byte utf16", "中x", PositionEncodingUTF16, 3, pos{0, 1}},
		// "😀" is 4 bytes, 2 UTF-16 units (surrogate pair).
		{"emoji utf16", "😀x", PositionEncodingUTF16, 4, pos{0, 2}},
		{"emoji utf8", "😀x", PositionEncodingUTF8, 4, pos{0, 4}},
		{"emoji second line", "a\n😀😀b", PositionEncodingUTF16, 10, pos{1, 4}},
		// Combining characters are separate code points: "é" = 1+2 bytes, 2 units.
		{"combining", "éx", PositionEncodingUTF16, 3, pos{0, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMapper([]byte(tt.content), tt.enc)
			got, err := m.OffsetPosition(tt.offset)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("OffsetPosition(%d) = %v, want %v", tt.offset, got, tt.want)
			}
			if back := m.PositionOffset(tt.want); back != tt.offset {
				t.Errorf("PositionOffset(%v) = %d, want %d", tt.want, back, tt.offset)
			}
		})
	}
}

func TestMapperClamping(t *testing.T) {
	m := NewMapper([]byte("ab\r\ncd\n"), PositionEncodingUTF16)
	tests := []struct {
		p    Position
		want int
	}{
		{Position{0, 99}, 2}, // past end of line: clamp before "\r\n"
		{Position{1, 99}, 6}, // clamp before "\n"
		{Position{2, 5}, 7},  // empty last line
		{Position{50, 0}, 7}, // past last line: end of content
	}
	for _, tt := range tests {
		if got := m.PositionOffset(tt.p); got != tt.want {
			t.Errorf("PositionOffset(%v) = %d, want %d", tt.p, got, tt.want)
		}
	}

	// A position inside a surrogate pair maps to the start of the rune.
	m = NewMapper([]byte("😀x"), PositionEncodingUTF16)
	if got := m.PositionOffset(Position{0, 1}); got != 0 {
		t.Errorf("mid-surrogate: got %d, want 0", got)
	}

	if _, err := m.OffsetPosition(99); err == nil {
		t.Error("OffsetPosition out of range: expected error")
	}
}
