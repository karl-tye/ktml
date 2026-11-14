// Package textposition converts between UTF-8 byte offsets and LSP UTF-16
// positions.
package textposition

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Position converts a byte offset into a zero-based line and UTF-16 character.
func Position(source []byte, offset int) (line, character int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	for index := 0; index < offset; {
		if source[index] == '\n' {
			line++
			character = 0
			index++
			continue
		}
		r, size := utf8.DecodeRune(source[index:])
		if size == 0 || index+size > offset {
			break
		}
		character += utf16.RuneLen(r)
		index += size
	}
	return line, character
}

// Offset converts a zero-based line and UTF-16 character into a byte offset.
func Offset(source []byte, targetLine, targetCharacter int) int {
	if targetLine < 0 || targetCharacter < 0 {
		return 0
	}
	line := 0
	index := 0
	for index < len(source) && line < targetLine {
		if source[index] == '\n' {
			line++
		}
		index++
	}
	if line < targetLine {
		return len(source)
	}
	character := 0
	for index < len(source) && source[index] != '\n' {
		if character >= targetCharacter {
			return index
		}
		r, size := utf8.DecodeRune(source[index:])
		width := utf16.RuneLen(r)
		if character+width > targetCharacter {
			return index
		}
		character += width
		index += size
	}
	return index
}
