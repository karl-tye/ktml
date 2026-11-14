package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type segmentExpectation struct {
	Kind    string `json:"kind"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Literal string `json:"literal,omitempty"`
	Path    string `json:"path,omitempty"`
}

func TestConformanceCorpus_GIVENQuotedSegments_WHENNormalized_THENMatchesKindsValuesAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		Cases []struct {
			Name     string               `json:"name"`
			Source   string               `json:"source"`
			Segments []segmentExpectation `json:"segments"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, tc := range manifest.Cases {
		if tc.Segments == nil {
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
			if got := collectSegments(result.Document); !reflect.DeepEqual(got, tc.Segments) {
				t.Fatalf("segments = %#v, want %#v", got, tc.Segments)
			}
		})
	}
}

func collectSegments(document *ast.Document) []segmentExpectation {
	var result []segmentExpectation
	visitQuoted := func(quoted *ast.QuotedValue) {
		if quoted == nil {
			return
		}
		for _, segment := range quoted.Segments {
			item := segmentExpectation{Start: segment.P, End: segment.E}
			switch segment.Kind {
			case ast.LiteralSegment:
				item.Kind = "literal"
				item.Literal = segment.Literal
			case ast.PathSegment:
				item.Kind = "path"
				item.Path = segment.Path.String()
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
