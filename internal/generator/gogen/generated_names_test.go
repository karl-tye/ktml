package gogen_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENComponentsWithOneGoName_WHENGenerated_THENFails(t *testing.T) {
	result := compiler.CompileSyntax("rows.ktml", []byte("Row { div }\nrow { span }\n"))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	_, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err == nil || !strings.Contains(err.Error(), "Go name Row") {
		t.Fatalf("Generate() error = %v, want a Go name collision", err)
	}
}
