package formatter_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/formatter"
)

func TestFormat_GIVENDefaultedInput_THENPreservesCanonicalDefaultSyntax(t *testing.T) {
	result := compiler.CompileSyntax("button.ktml", []byte(`Button[disabled:boolean=false]{button}`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	formatted := formatter.Format(result.Document)
	if formatted != "Button [disabled: boolean = false] {\n\tbutton\n}\n" {
		t.Errorf("Format() = %q", formatted)
	}
}
