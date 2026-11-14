package gohtml

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerateChecked_GIVENOptionalOrDefaultedInputs_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "optional", source: `Card [label?: string] { div }`},
		{name: "defaulted", source: `Card [label: string = "fallback"] { div }`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := compiler.CompileSyntax("card.ktml", []byte(tc.source))
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			wrote := false
			output, err := GenerateChecked(result.Document, false, "card.ktml", func(string, []byte) { wrote = true })
			if err == nil || !strings.Contains(err.Error(), "unsupported input mode") {
				t.Fatalf("GenerateChecked() error = %v, want fatal unsupported input mode", err)
			}
			if output != "" || wrote {
				t.Fatalf("GenerateChecked() produced output = %q, wrote = %t", output, wrote)
			}
		})
	}
}
