package gogen

import "testing"

func TestLineIndex_GIVENOffsets_WHENLocated_THENMatchesCountingFromTheStart(t *testing.T) {
	for _, source := range []string{"", "a", "\n", "ab\ncd\n\nef", "\n\nx\n"} {
		index := newLineIndex([]byte(source))
		for offset := -1; offset <= len(source)+1; offset++ {
			wantLine, wantColumn := 1, 1
			for position := 0; position < offset && position < len(source); position++ {
				if source[position] == '\n' {
					wantLine, wantColumn = wantLine+1, 1
				} else {
					wantColumn++
				}
			}
			if line, column := index.lineColumn(offset); line != wantLine || column != wantColumn {
				t.Errorf("lineColumn(%q, %d) = %d:%d, want %d:%d", source, offset, line, column, wantLine, wantColumn)
			}
		}
	}
}
