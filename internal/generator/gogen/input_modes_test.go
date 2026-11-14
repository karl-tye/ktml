package gogen_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENOptionalAndDefaultedInputs_WHENRendered_THENPublicPropsPreserveAbsenceAndDefaults(t *testing.T) {
	result := compiler.CompileSyntax("badge.ktml", []byte(`Badge [title: string, note?: string, enabled: boolean = false, label: string = ""] { button disabled="{enabled}" { "{title}{label}" } }
Unused [count: number = 08] { div }
Page { Badge title="Save" enabled=true }
NumericPage { Unused count=08 }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !regexp.MustCompile(`(?m)^\s*Note\s+\*string$`).Match(generated) || !regexp.MustCompile(`(?m)^\s*Enabled\s+\*bool$`).Match(generated) || !regexp.MustCompile(`(?m)^\s*Label\s+\*string$`).Match(generated) {
		t.Fatalf("generated props do not preserve absence:\n%s", generated)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedInputModes(t *testing.T) {
	var omitted strings.Builder
	if err := (Badge{Title: "Save"}).Render(context.Background(), &omitted); err != nil { t.Fatal(err) }
	if got := omitted.String(); got != "<button>Save</button>" { t.Fatalf("omitted defaults = %q", got) }

	falseValue := false
	emptyValue := ""
	var explicitZero strings.Builder
	if err := (Badge{Title: "Save", Enabled: &falseValue, Label: &emptyValue}).Render(context.Background(), &explicitZero); err != nil { t.Fatal(err) }
	if got := explicitZero.String(); got != "<button>Save</button>" { t.Fatalf("explicit zero values = %q", got) }

	var nested strings.Builder
	if err := (Page{}).Render(context.Background(), &nested); err != nil { t.Fatal(err) }
	if got := nested.String(); got != "<button disabled>Save</button>" { t.Fatalf("nested defaults = %q", got) }

	var numeric strings.Builder
	if err := (NumericPage{}).Render(context.Background(), &numeric); err != nil { t.Fatal(err) }
	if got := numeric.String(); got != "<div></div>" { t.Fatalf("numeric call = %q", got) }
}

`)
}

func TestGenerate_GIVENPrototypeOverride_WHENDirectGoIsRequested_THENFailsWithoutOutput(t *testing.T) {
	result := compiler.CompileSyntax("button.ktml", []byte(`Button [disabled="true"] { button disabled }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err == nil || !strings.Contains(err.Error(), "declaration overrides") {
		t.Fatalf("Generate() error = %v, want fatal declaration override diagnostic", err)
	}
	if generated != nil {
		t.Fatalf("Generate() output = %q, want nil", generated)
	}
}
