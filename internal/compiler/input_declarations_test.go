package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

func TestCompileSyntax_GIVENCanonicalInputModes_WHENCompiled_THENReturnsExplicitRangedDeclarations(t *testing.T) {
	source := `Card [title: string, subtitle?: string, enabled: boolean = false, count: number = 0, label: string = ""] { div }`
	result := compiler.CompileSyntax("card.ktml", []byte(source))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	declaration := result.Document.Nodes[0].(*ast.ElementDecl)
	if len(declaration.Attributes) != 5 {
		t.Fatalf("input count = %d, want 5", len(declaration.Attributes))
	}

	tests := []struct {
		index       int
		name        string
		mode        ast.InputMode
		nameStart   int
		nameEnd     int
		markerStart int
		markerEnd   int
		literalKind ast.ScalarLiteralKind
		literalRaw  string
	}{
		{index: 0, name: "title", mode: ast.RequiredInput, nameStart: 6, nameEnd: 11},
		{index: 1, name: "subtitle", mode: ast.OptionalInput, nameStart: 21, nameEnd: 29, markerStart: 29, markerEnd: 30},
		{index: 2, name: "enabled", mode: ast.DefaultedInput, nameStart: 40, nameEnd: 47, markerStart: 57, markerEnd: 58, literalKind: ast.BooleanLiteral, literalRaw: "false"},
		{index: 3, name: "count", mode: ast.DefaultedInput, nameStart: 66, nameEnd: 71, markerStart: 80, markerEnd: 81, literalKind: ast.NumberLiteral, literalRaw: "0"},
		{index: 4, name: "label", mode: ast.DefaultedInput, nameStart: 85, nameEnd: 90, markerStart: 99, markerEnd: 100, literalKind: ast.StringLiteral, literalRaw: `""`},
	}
	for _, tc := range tests {
		input := declaration.Attributes[tc.index]
		if input.Name != tc.name || input.Mode != tc.mode {
			t.Errorf("input[%d] = (%q, %q), want (%q, %q)", tc.index, input.Name, input.Mode, tc.name, tc.mode)
		}
		if input.NameRange.Start != tc.nameStart || input.NameRange.End != tc.nameEnd {
			t.Errorf("%s name range = %#v, want %d-%d", tc.name, input.NameRange, tc.nameStart, tc.nameEnd)
		}
		if tc.mode == ast.OptionalInput {
			if input.OptionalMarker == nil || input.OptionalMarker.Start != tc.markerStart || input.OptionalMarker.End != tc.markerEnd {
				t.Errorf("%s optional marker = %#v, want %d-%d", tc.name, input.OptionalMarker, tc.markerStart, tc.markerEnd)
			}
		}
		if tc.mode == ast.DefaultedInput {
			if input.DefaultOperator == nil || input.DefaultOperator.Start != tc.markerStart || input.DefaultOperator.End != tc.markerEnd {
				t.Errorf("%s default operator = %#v, want %d-%d", tc.name, input.DefaultOperator, tc.markerStart, tc.markerEnd)
			}
			if input.Default == nil || input.Default.Kind != tc.literalKind || input.Default.Raw != tc.literalRaw {
				t.Errorf("%s default = %#v, want (%q, %q)", tc.name, input.Default, tc.literalKind, tc.literalRaw)
			}
		}
	}
}

func TestWorkspace_GIVENCanonicalInputModes_WHENAnalyzed_THENPreservesTypedDefaultsAndOptionalAbsence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.ktml")
	source := `Button [title: string, note?: string, enabled: boolean = false, count: number = 0, label: string = ""] { button }
Page { Button title="Save" }`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("diagnostics = %#v", snapshot.Diagnostics(path))
	}
	button, ok := snapshot.Resolve(path, "Button")
	if !ok || len(button.Inputs) != 5 {
		t.Fatalf("Button inputs = %#v", button.Inputs)
	}
	if input := button.Inputs[1]; input.Mode != compiler.OptionalInput || input.Default != nil {
		t.Errorf("optional input = %#v", input)
	}
	if input := button.Inputs[2]; input.Mode != compiler.DefaultedInput || input.Default == nil || input.Default.Kind != compiler.BooleanLiteral || input.Default.Boolean {
		t.Errorf("false default = %#v", input)
	}
	if input := button.Inputs[3]; input.Default == nil || input.Default.Kind != compiler.NumberLiteral || input.Default.Number != 0 {
		t.Errorf("zero default = %#v", input)
	}
	if input := button.Inputs[4]; input.Default == nil || input.Default.Kind != compiler.StringLiteral || input.Default.String != "" {
		t.Errorf("empty string default = %#v", input)
	}
}

func TestWorkspace_GIVENInvalidDefaultsAndGeneratedFieldCollisions_WHENAnalyzed_THENReportsExactDeclarationRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.ktml")
	source := `Card [first-name: string, firstName: string, enabled: boolean = 0, count: number = false] { div }`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	diagnostics := snapshot.Diagnostics(path)
	want := map[string][][2]int{
		"generated-field-collision": {{26, 35}},
		"default-type-mismatch":     {{64, 65}, {83, 88}},
	}
	for _, diagnostic := range diagnostics {
		ranges, ok := want[diagnostic.Code]
		if !ok {
			t.Errorf("unexpected diagnostic = %#v", diagnostic)
			continue
		}
		matched := false
		for _, expected := range ranges {
			if diagnostic.Start == expected[0] && diagnostic.End == expected[1] {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s range = %d-%d, want one of %#v", diagnostic.Code, diagnostic.Start, diagnostic.End, ranges)
		}
	}
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics = %#v, want 3", diagnostics)
	}
}

func TestWorkspace_GIVENOptionalInputReadWithoutNarrowing_WHENCallsAreValidated_THENPreservesAbsenceAndRejectsTheRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "optional.ktml")
	source := `Label [text?: string] { span }
Page [note?: string] { Label text="{note}" }`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	diagnostics := snapshot.Diagnostics(path)
	if len(diagnostics) != 1 || diagnostics[0].Code != "optional-value-access" || diagnostics[0].Start != 67 || diagnostics[0].End != 71 {
		t.Fatalf("diagnostics = %#v, want optional-value-access at 67-71", diagnostics)
	}
}

func TestWorkspace_GIVENInputWithoutExportedGoField_WHENAnalyzed_THENReportsTheKTMLNameRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid-name.ktml")
	if err := os.WriteFile(path, []byte(`Card [_label: string] { div }`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	diagnostics := snapshot.Diagnostics(path)
	if len(diagnostics) != 1 || diagnostics[0].Code != "invalid-generated-field" || diagnostics[0].Start != 6 || diagnostics[0].End != 12 {
		t.Fatalf("diagnostics = %#v, want invalid-generated-field at 6-12", diagnostics)
	}
}

func TestCompileSyntax_GIVENInvalidOrInterpolatedStringDefault_WHENCompiled_THENRejectsTheLiteral(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "invalid escape", source: `Card [label: string = "\q"] { div }`},
		{name: "interpolation", source: `Card [label: string = "{other}"] { div }`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := compiler.CompileSyntax("invalid.ktml", []byte(tc.source))
			if !result.HasErrors() || result.Document != nil {
				t.Fatalf("CompileSyntax() = %#v, want fatal string default diagnostic", result)
			}
		})
	}
}
