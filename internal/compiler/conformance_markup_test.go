package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type normalizedMarkup struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Name  string `json:"name,omitempty"`
	Text  string `json:"text,omitempty"`
}

func TestConformanceCorpus_GIVENDoctypesAndHTMLComments_WHENParsed_THENMatchesNormalizedMarkup(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		Cases []struct {
			Name   string             `json:"name"`
			Source string             `json:"source"`
			Markup []normalizedMarkup `json:"markup"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Markup == nil {
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
			if got, want := mustJSON(t, normalizeGoMarkup(result.Document)), mustJSON(t, fixture.Markup); got != want {
				t.Errorf("markup = %s, want %s", got, want)
			}
		})
	}
}

// normalizeGoMarkup lists every doctype and HTML comment in source pre-order.
func normalizeGoMarkup(document *ast.Document) []normalizedMarkup {
	markup := []normalizedMarkup{}
	var walk func(nodes []ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch node := node.(type) {
			case *ast.DoctypeNode:
				markup = append(markup, normalizedMarkup{Kind: "doctype", Start: node.P, End: node.E, Name: node.Name})
			case *ast.HTMLCommentNode:
				markup = append(markup, normalizedMarkup{Kind: "comment", Start: node.P, End: node.E, Text: node.Text})
			case *ast.RawTextNode:
				markup = append(markup, normalizedMarkup{Kind: "raw-text", Start: node.P, End: node.E, Text: node.Text})
			case *ast.ElementDecl:
				walk(node.Body)
			case *ast.RenderClause:
				walk(node.Body)
			case *ast.ElementNode:
				walk(node.Children)
			case *ast.RepetitionNode:
				walk(node.Body)
			case *ast.SlotNode:
				walk(node.Body)
			case *ast.ConditionNode:
				walk(node.Then)
				walk(node.Else)
			}
		}
	}
	walk(document.Nodes)
	return markup
}
