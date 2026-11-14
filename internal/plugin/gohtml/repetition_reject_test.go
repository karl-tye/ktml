package gohtml

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerateChecked_GIVENKeyedRepetition_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	tests := []struct {
		name, source string
	}{
		{name: "component body", source: `Card [items: string] { for item in items key item { "{item}" } }`},
		{name: "element body", source: `Card [items: string] { ul { li { for item in items key item {} } } }`},
		{name: "component call body", source: "Box { div { children } }\nCard [items: string] { Box { for item in items key item {} } }"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := compiler.CompileSyntax("card.ktml", []byte(tc.source))
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			wrote := false
			output, err := GenerateChecked(result.Document, false, "card.ktml", func(string, []byte) { wrote = true })
			if err == nil || !strings.Contains(err.Error(), "keyed repetition over items") || !strings.Contains(err.Error(), "gohtml") {
				t.Fatalf("GenerateChecked() error = %v, want a gohtml keyed repetition error", err)
			}
			if output != "" || wrote {
				t.Fatalf("GenerateChecked() produced output = %q, wrote = %t", output, wrote)
			}
		})
	}
}
