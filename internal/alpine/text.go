package alpine

import (
	"strconv"
	"unicode/utf8"

	"github.com/karl-tye/ktml/internal/ast"
)

// Text is the JavaScript of one attribute value, decoded from KTML source.
// Each KTML interpolation becomes one hole byte.
type Text struct {
	Value string
	// offsets maps each byte of Value, and its end, to a source offset.
	offsets []int
	// Holes are the interpolated paths, by their offset in Value.
	Holes map[int]*ast.PathExpression
}

// Source returns the source offset of a Value offset.
func (t *Text) Source(offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(t.offsets) {
		return t.offsets[len(t.offsets)-1]
	}
	return t.offsets[offset]
}

// SourceRange maps a half-open Value range to source.
func (t *Text) SourceRange(start, end int) (int, int) {
	if end > start && end <= len(t.Value) {
		// The end of a range is the end of its last byte.
		return t.Source(start), t.endOf(end - 1)
	}
	return t.Source(start), t.Source(end)
}

func (t *Text) endOf(offset int) int {
	if offset+1 < len(t.offsets) && t.offsets[offset+1] > t.offsets[offset] {
		return t.offsets[offset+1]
	}
	return t.offsets[offset] + 1
}

// Offset returns the Value offset of a source offset, or -1.
func (t *Text) Offset(source int) int {
	for index, offset := range t.offsets {
		if offset >= source {
			if offset > source && index > 0 {
				return index - 1
			}
			return index
		}
	}
	return -1
}

// AttributeText decodes an attribute's value. It returns false for a
// valueless attribute.
func AttributeText(source []byte, entry *ast.AttributeNode) (*Text, bool) {
	text := &Text{Holes: make(map[int]*ast.PathExpression)}
	if entry.Quoted == nil {
		if entry.Value == "" {
			return nil, false
		}
		start := entry.E - len(entry.Value)
		text.Value = entry.Value
		for index := 0; index <= len(entry.Value); index++ {
			text.offsets = append(text.offsets, start+index)
		}
		return text, true
	}
	quoted := entry.Quoted
	raw := quoted.P < len(source) && source[quoted.P] == '`'
	var value []byte
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			text.Holes[len(value)] = segment.Path
			value = append(value, holeByte)
			text.offsets = append(text.offsets, segment.P)
			continue
		}
		for offset := segment.P; offset < segment.E; {
			if raw || source[offset] != '\\' || offset+1 >= segment.E {
				value = append(value, source[offset])
				text.offsets = append(text.offsets, offset)
				offset++
				continue
			}
			decoded, width := decodeEscape(source[offset:segment.E])
			for range decoded {
				text.offsets = append(text.offsets, offset)
			}
			value = append(value, decoded...)
			offset += width
		}
	}
	text.offsets = append(text.offsets, quoted.E-1)
	text.Value = string(value)
	return text, true
}

// decodeEscape decodes one KTML string escape.
func decodeEscape(raw []byte) ([]byte, int) {
	switch raw[1] {
	case 'b':
		return []byte{'\b'}, 2
	case 'f':
		return []byte{'\f'}, 2
	case 'n':
		return []byte{'\n'}, 2
	case 'r':
		return []byte{'\r'}, 2
	case 't':
		return []byte{'\t'}, 2
	case 'u':
		if len(raw) >= 6 {
			if code, err := strconv.ParseUint(string(raw[2:6]), 16, 32); err == nil {
				return utf8.AppendRune(nil, rune(code)), 6
			}
		}
	}
	return []byte{raw[1]}, 2
}
