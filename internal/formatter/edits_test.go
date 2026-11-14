package formatter

import (
	"strings"
	"testing"
)

func TestLineEdits_GIVENASourceAndItsFormat_WHENApplied_THENGivesTheFormatWithOnlyTheChangedLines(t *testing.T) {
	tests := []struct {
		name, source, formatted string
		edits                   int
	}{
		{"unchanged", "a\nb\n", "a\nb\n", 0},
		{"one line", "a\n b\nc\n", "a\n\tb\nc\n", 1},
		{"two places", "a\n b\nc\nd\n e\nf\n", "a\n\tb\nc\nd\n\te\nf\n", 2},
		{"inserted", "a\nc\n", "a\nb\nc\n", 1},
		{"removed", "a\n\n\nb\n", "a\n\nb\n", 1},
		{"no final newline", "a\n b", "a\n\tb\n", 1},
		{"everything", "x", "y\nz\n", 1},
		{"empty source", "", "a\n", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			edits := LineEdits(test.source, test.formatted)
			if got := Apply(test.source, edits); got != test.formatted {
				t.Errorf("Apply(LineEdits) = %q, want %q (edits %+v)", got, test.formatted, edits)
			}
			if len(edits) != test.edits {
				t.Errorf("edits = %+v, want %d", edits, test.edits)
			}
		})
	}
}

func TestLineEdits_GIVENAFormattedDocument_WHENEveryLineIsMisindented_THENStillRoundTrips(t *testing.T) {
	var source, formatted strings.Builder
	for i := 0; i < 500; i++ {
		source.WriteString("  line\n")
		formatted.WriteString("\tline\n")
		if i%7 == 0 {
			formatted.WriteString("\n")
		}
	}
	if got := Apply(source.String(), LineEdits(source.String(), formatted.String())); got != formatted.String() {
		t.Error("a large rewrite did not round-trip")
	}
}
