package gohtml

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerate_GIVENLiteralTrueAttribute_WHENGenerated_THENKeepsTheValue(t *testing.T) {
	result := compiler.CompileSyntax("link.ktml", []byte(`Link [] {
	a hx-push-url="true" aria-hidden="false" hidden
}`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	output := Generate(result.Document, false, "link.ktml", nil)
	for _, want := range []string{`hx-push-url="true"`, `aria-hidden="false"`, ` hidden`} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not contain %s:\n%s", want, output)
		}
	}
}
