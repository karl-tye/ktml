package gohtml

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerateChecked_GIVENHostSchemaFeatures_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{name: "record input", source: `Card [user: User] { div }`, want: "unsupported input type User"},
		{name: "collection input", source: `Card [tags: [string]] { div }`, want: "unsupported input type [string]"},
		{name: "integer input", source: `Card [count: integer] { div }`, want: "unsupported input type integer"},
		{name: "dotted text path", source: `Card [user: string] { "{user.name}" }`, want: "dotted path user.name"},
		{name: "dotted attribute path", source: `Card [user: string] { div title="{user.name}" }`, want: "dotted path user.name"},
		{name: "schema import", source: `import { User } from "./user.ktmlschema"
Card { div }`, want: "schema import"},
		{name: "import alias", source: `import { Button as B } from "./button.ktml"
Card { div }`, want: "import alias"},
		{name: "record declaration", source: `record User []`, want: "record declaration"},
		{name: "state declaration", source: `state S { A }`, want: "state declaration S"},
		{name: "bodyless signature", source: "Card [on: boolean]\nCard [on = true] { div }", want: "component signature Card"},
		{name: "default signature", source: "Card [on: boolean] default { div }\nCard [on = true] { div }", want: "component signature Card"},
		{name: "render clause", source: "Card [on = true] { div }", want: "render clause for Card"},
		{name: "URL input", source: `Card [href: URL] { a href="{href}" }`, want: "unsupported input type URL"},
		{name: "TrustedURL input", source: `Card [href: TrustedURL] { a href="{href}" }`, want: "unsupported input type TrustedURL"},
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
				t.Fatalf("GenerateChecked() error = %v, want gohtml error containing %q", err, tc.want)
			}
			if output != "" || wrote {
				t.Fatalf("GenerateChecked() produced output = %q, wrote = %t", output, wrote)
			}
		})
	}
}
