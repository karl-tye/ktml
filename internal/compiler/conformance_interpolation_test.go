package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type interpolationExpectation struct {
	Start int                   `json:"start"`
	End   int                   `json:"end"`
	Path  string                `json:"path"`
	Parts []pathPartExpectation `json:"parts"`
}

type pathPartExpectation struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func TestConformanceCorpus_GIVENInterpolationShape_WHENNormalized_THENMatchesPathsAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		Cases []struct {
			Name           string                     `json:"name"`
			Source         string                     `json:"source"`
			Interpolations []interpolationExpectation `json:"interpolations"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, tc := range manifest.Cases {
		if tc.Interpolations == nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(corpusDir, filepath.FromSlash(tc.Source)))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			result := compiler.CompileSyntax(tc.Source, source)
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			got := collectInterpolations(result.Document)
			if len(got) != len(tc.Interpolations) {
				t.Fatalf("interpolations = %#v, want %#v", got, tc.Interpolations)
			}
			for i := range got {
				if got[i].Start != tc.Interpolations[i].Start || got[i].End != tc.Interpolations[i].End || got[i].Path != tc.Interpolations[i].Path {
					t.Errorf("interpolation[%d] = %#v, want %#v", i, got[i], tc.Interpolations[i])
				}
				if len(got[i].Parts) != len(tc.Interpolations[i].Parts) {
					t.Fatalf("interpolation[%d] parts = %#v, want %#v", i, got[i].Parts, tc.Interpolations[i].Parts)
				}
				for j := range got[i].Parts {
					if got[i].Parts[j] != tc.Interpolations[i].Parts[j] {
						t.Errorf("interpolation[%d].parts[%d] = %#v, want %#v", i, j, got[i].Parts[j], tc.Interpolations[i].Parts[j])
					}
				}
			}
		})
	}
}

func collectInterpolations(document *ast.Document) []interpolationExpectation {
	var result []interpolationExpectation
	visitQuoted := func(quoted *ast.QuotedValue) {
		if quoted == nil {
			return
		}
		for _, segment := range quoted.Segments {
			if segment.Kind != ast.PathSegment || segment.Path == nil {
				continue
			}
			item := interpolationExpectation{Start: segment.Path.P, End: segment.Path.E, Path: segment.Path.String()}
			for _, part := range segment.Path.Parts {
				item.Parts = append(item.Parts, pathPartExpectation{Name: part.Name, Start: part.P, End: part.E})
			}
			result = append(result, item)
		}
	}
	var visitNodes func([]ast.Node)
	visitNodes = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch node := node.(type) {
			case *ast.TextNode:
				visitQuoted(node.Quoted)
			case *ast.ElementNode:
				for _, attribute := range node.Attributes {
					visitQuoted(attribute.Quoted)
				}
				visitNodes(node.Children)
			case *ast.ElementDecl:
				visitNodes(node.Body)
			}
		}
	}
	visitNodes(document.Nodes)
	return result
}
