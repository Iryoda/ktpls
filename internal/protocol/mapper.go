package protocol

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// A Mapper converts between byte offsets in a document's content and LSP
// positions, whose Character unit depends on the negotiated encoding
// (UTF-8 bytes or UTF-16 code units).
//
// Lines end at "\n", "\r\n", or a lone "\r", as the LSP specification
// requires. Positions past the end of a line clamp to the line's end, and
// lines past the end of the document clamp to the end of the content.
type Mapper struct {
	content   []byte
	encoding  PositionEncodingKind
	lineStart []int // byte offset of the start of each line
}

// NewMapper returns a Mapper for content. The content must not be modified
// afterwards.
func NewMapper(content []byte, enc PositionEncodingKind) *Mapper {
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '\n':
			starts = append(starts, i+1)
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				continue // the '\n' ends the line
			}
			starts = append(starts, i+1)
		}
	}
	return &Mapper{content: content, encoding: enc, lineStart: starts}
}

// lineEnd returns the byte offset of the end of line l, excluding its line
// terminator.
func (m *Mapper) lineEnd(l int) int {
	if l+1 >= len(m.lineStart) {
		return len(m.content)
	}
	end := m.lineStart[l+1]
	if end > 0 && m.content[end-1] == '\n' {
		end--
	}
	if end > m.lineStart[l] && m.content[end-1] == '\r' {
		end--
	}
	return end
}

// OffsetPosition returns the position of byte offset off.
func (m *Mapper) OffsetPosition(off int) (Position, error) {
	if off < 0 || off > len(m.content) {
		return Position{}, fmt.Errorf("offset %d out of range [0, %d]", off, len(m.content))
	}
	line := sort.Search(len(m.lineStart), func(i int) bool { return m.lineStart[i] > off }) - 1
	start := m.lineStart[line]
	var char int
	if m.encoding == PositionEncodingUTF8 {
		char = off - start
	} else {
		char = utf16Len(m.content[start:off])
	}
	return Position{Line: uint32(line), Character: uint32(char)}, nil
}

// PositionOffset returns the byte offset of position p.
func (m *Mapper) PositionOffset(p Position) int {
	line := int(p.Line)
	if line >= len(m.lineStart) {
		return len(m.content)
	}
	start, end := m.lineStart[line], m.lineEnd(line)
	want := int(p.Character)
	if m.encoding == PositionEncodingUTF8 {
		return min(start+want, end)
	}
	off, units := start, 0
	for off < end {
		r, size := utf8.DecodeRune(m.content[off:end])
		n := 1
		if r >= 0x10000 {
			n = 2 // surrogate pair
		}
		if units+n > want {
			break // want is before this rune (or inside a surrogate pair)
		}
		units += n
		off += size
	}
	return off
}

// OffsetRange returns the range spanning byte offsets [start, end).
func (m *Mapper) OffsetRange(start, end int) (Range, error) {
	s, err := m.OffsetPosition(start)
	if err != nil {
		return Range{}, err
	}
	e, err := m.OffsetPosition(end)
	if err != nil {
		return Range{}, err
	}
	return Range{Start: s, End: e}, nil
}

// RangeOffsets returns the byte offsets of range r.
func (m *Mapper) RangeOffsets(r Range) (start, end int) {
	start, end = m.PositionOffset(r.Start), m.PositionOffset(r.End)
	if end < start {
		end = start
	}
	return start, end
}

// utf16Len returns the number of UTF-16 code units needed to encode b.
// Invalid UTF-8 bytes count as one unit each (U+FFFD).
func utf16Len(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		b = b[size:]
	}
	return n
}
