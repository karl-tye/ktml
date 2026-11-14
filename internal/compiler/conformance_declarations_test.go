package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type declarationManifest struct {
	Cases []struct {
		Name         string                  `json:"name"`
		Source       string                  `json:"source"`
		Declarations []normalizedDeclaration `json:"declarations"`
	} `json:"cases"`
}

type normalizedDeclaration struct {
	Name         string            `json:"name"`
	Start        int               `json:"start"`
	End          int               `json:"end"`
	Bodyless     bool              `json:"bodyless"`
	DefaultStart int               `json:"defaultStart,omitempty"`
	DefaultEnd   int               `json:"defaultEnd,omitempty"`
	Inputs       []normalizedInput `json:"inputs"`
}

type normalizedInput struct {
	Name          string             `json:"name"`
	Start         int                `json:"start"`
	End           int                `json:"end"`
	NameStart     int                `json:"nameStart"`
	NameEnd       int                `json:"nameEnd"`
	Type          string             `json:"type"`
	TypeStart     int                `json:"typeStart"`
	TypeEnd       int                `json:"typeEnd"`
	Mode          string             `json:"mode"`
	OptionalStart int                `json:"optionalStart,omitempty"`
	OptionalEnd   int                `json:"optionalEnd,omitempty"`
	OperatorStart int                `json:"operatorStart,omitempty"`
	OperatorEnd   int                `json:"operatorEnd,omitempty"`
	Default       *normalizedLiteral `json:"default,omitempty"`
}

type normalizedLiteral struct {
	Kind    string   `json:"kind"`
	Raw     string   `json:"raw"`
	Start   int      `json:"start"`
	End     int      `json:"end"`
	String  *string  `json:"string,omitempty"`
	Boolean *bool    `json:"boolean,omitempty"`
	Number  *float64 `json:"number,omitempty"`
}

func TestConformanceCorpus_GIVENInputDeclarations_WHENParsed_THENMatchesNormalizedShapeAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest declarationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Declarations == nil {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(corpusDir, filepath.FromSlash(fixture.Source)))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			result := compiler.CompileSyntax(fixture.Source, source)
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			got := normalizeGoDeclarations(result.Document)
			if encodedGot, _ := json.Marshal(got); string(encodedGot) != mustJSON(t, fixture.Declarations) {
				t.Errorf("declarations = %s, want %s", encodedGot, mustJSON(t, fixture.Declarations))
			}
		})
	}
}

func normalizeGoDeclarations(document *ast.Document) []normalizedDeclaration {
	var declarations []normalizedDeclaration
	for _, node := range document.Nodes {
		declaration, ok := node.(*ast.ElementDecl)
		if !ok {
			continue
		}
		normalized := normalizedDeclaration{Name: declaration.Name, Start: declaration.Pos(), End: declaration.Pos() + len(declaration.Name), Bodyless: declaration.Bodyless}
		if declaration.DefaultRange != nil {
			normalized.DefaultStart, normalized.DefaultEnd = declaration.DefaultRange.Start, declaration.DefaultRange.End
		}
		for _, input := range declaration.Attributes {
			if input.Kind != ast.InputDeclaration {
				continue
			}
			item := normalizedInput{
				Name: input.Name, Start: input.Pos(), End: input.End(), NameStart: input.NameRange.Start,
				NameEnd: input.NameRange.End, Type: input.TypeDecl, TypeStart: input.TypeRange.Start,
				TypeEnd: input.TypeRange.End, Mode: string(input.Mode),
			}
			if input.OptionalMarker != nil {
				item.OptionalStart, item.OptionalEnd = input.OptionalMarker.Start, input.OptionalMarker.End
			}
			if input.DefaultOperator != nil {
				item.OperatorStart, item.OperatorEnd = input.DefaultOperator.Start, input.DefaultOperator.End
			}
			if input.Default != nil {
				item.Default = &normalizedLiteral{Kind: string(input.Default.Kind), Raw: input.Default.Raw, Start: input.Default.P, End: input.Default.E}
				switch input.Default.Kind {
				case ast.StringLiteral:
					item.Default.String = &input.Default.String
				case ast.BooleanLiteral:
					item.Default.Boolean = &input.Default.Boolean
				case ast.NumberLiteral:
					item.Default.Number = &input.Default.Number
				}
			}
			normalized.Inputs = append(normalized.Inputs, item)
		}
		declarations = append(declarations, normalized)
	}
	return declarations
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal expectation: %v", err)
	}
	return string(data)
}
