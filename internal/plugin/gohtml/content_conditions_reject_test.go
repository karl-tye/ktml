package gohtml

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerateChecked_GIVENContentInputsAndConditions_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{name: "declared children", source: `Card [children: content] { div { children } }`, want: "content input children"},
		{name: "named slot", source: `Card [actions?: content] { div { actions } }`, want: "content input actions"},
		{name: "slot fill", source: "Box { div { children } }\nCard [] { Box { slot actions {} } }", want: "slot fill actions"},
		{name: "condition in a component body", source: `Card [on: boolean] { if on { p "x" } }`, want: "condition on"},
		{name: "condition in an element body", source: `Card [on: boolean] { div { if !on {} else { p } } }`, want: "condition !on"},
		{name: "condition in a call body", source: "Box { div { children } }\nCard [on: boolean] { Box { if on {} } }", want: "condition on"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := compiler.CompileSyntax("card.ktml", []byte(tc.source))
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			wrote := false
			output, err := GenerateChecked(result.Document, false, "card.ktml", func(string, []byte) { wrote = true })
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "gohtml") {
				t.Fatalf("GenerateChecked() error = %v, want a gohtml error about %s", err, tc.want)
			}
			if output != "" || wrote {
				t.Fatalf("GenerateChecked() produced output = %q, wrote = %t", output, wrote)
			}
		})
	}
}
