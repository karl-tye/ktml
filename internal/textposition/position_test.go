package textposition_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/textposition"
)

func TestUTF16_GIVENMultibyteText_WHENConvertingPositions_THENUsesLSPCodeUnits(t *testing.T) {
	source := []byte("a😀b\nç")
	line, character := textposition.Position(source, len("a😀"))
	if line != 0 || character != 3 {
		t.Errorf("Position() = (%d, %d), want (0, 3)", line, character)
	}
	if got, want := textposition.Offset(source, 0, 3), len("a😀"); got != want {
		t.Errorf("Offset() = %d, want %d", got, want)
	}
	if got, want := textposition.Offset(source, 1, 1), len("a😀b\nç"); got != want {
		t.Errorf("Offset() on second line = %d, want %d", got, want)
	}
}
