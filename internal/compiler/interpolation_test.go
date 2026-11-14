package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestCompileSyntax_GIVENMalformedQuotedInterpolation_WHENCompiled_THENReportsExactExpressionRange(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		start      int
		end        int
	}{
		{name: "call", expression: "{title()}", end: len("{title()}")},
		{name: "empty path", expression: "{}", end: len("{}")},
		{name: "empty path part", expression: "{user..Name}", end: len("{user..Name}")},
		{name: "unclosed path", expression: "{title", end: len("{title")},
		{name: "unmatched close", expression: "title}", start: len("title"), end: len("title}")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := `Card [title: string] { "` + tc.expression + `" }`
			result := compiler.CompileSyntax("card.ktml", []byte(source))
			if !result.HasErrors() || result.Document != nil {
				t.Fatalf("CompileSyntax() = %#v, want fatal interpolation diagnostic", result)
			}
			if len(result.Diagnostics) != 1 {
				t.Fatalf("diagnostics = %#v, want one", result.Diagnostics)
			}
			diagnostic := result.Diagnostics[0]
			start := strings.Index(source, tc.expression)
			if diagnostic.Code != "invalid-interpolation" || diagnostic.Start != start+tc.start || diagnostic.End != start+tc.end {
				t.Errorf("diagnostic = %#v, want invalid-interpolation at %d-%d", diagnostic, start+tc.start, start+tc.end)
			}
		})
	}
}

func TestWorkspace_GIVENQuotedInterpolationPaths_WHENAnalyzed_THENResolvesRootsAgainstEnclosingInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "card.ktml")
	source := `Card [name: string] {
	div title="Hello {missingAttribute}" { "Hello {name}, from {missingText}" }
}`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	diagnostics := snapshot.Diagnostics(path)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v, want two unknown roots", diagnostics)
	}
	for i, name := range []string{"missingAttribute", "missingText"} {
		start := strings.Index(source, name)
		if got := diagnostics[i]; got.Code != "unknown-path-root" || got.Start != start || got.End != start+len(name) {
			t.Errorf("diagnostic[%d] = %#v, want unknown-path-root at %d-%d", i, got, start, start+len(name))
		}
	}
}

func TestWorkspace_GIVENDottedPathOnScalarInput_WHENAnalyzed_THENReportsScalarFieldAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "card.ktml")
	source := `Card [user: string] { "Hello {user.Name}" }`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	diagnostics := snapshot.Diagnostics(path)
	start := strings.Index(source, "Name}")
	if len(diagnostics) != 1 || diagnostics[0].Code != "scalar-field-access" || diagnostics[0].Start != start || diagnostics[0].End != start+len("Name") {
		t.Errorf("diagnostics = %#v, want scalar-field-access at %d-%d", diagnostics, start, start+len("Name"))
	}
}
