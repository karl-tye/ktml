package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type normalizedRepetition struct {
	Item            string                   `json:"item"`
	Start           int                      `json:"start"`
	End             int                      `json:"end"`
	KeywordStart    int                      `json:"keywordStart"`
	KeywordEnd      int                      `json:"keywordEnd"`
	ItemStart       int                      `json:"itemStart"`
	ItemEnd         int                      `json:"itemEnd"`
	InStart         int                      `json:"inStart"`
	InEnd           int                      `json:"inEnd"`
	Collection      normalizedRepetitionPath `json:"collection"`
	KeyKeywordStart int                      `json:"keyKeywordStart"`
	KeyKeywordEnd   int                      `json:"keyKeywordEnd"`
	Key             normalizedRepetitionPath `json:"key"`
}

type normalizedRepetitionPath struct {
	Name  string                         `json:"name"`
	Start int                            `json:"start"`
	End   int                            `json:"end"`
	Parts []normalizedRepetitionPathPart `json:"parts"`
}

type normalizedRepetitionPathPart struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type repetitionManifest struct {
	Cases []struct {
		Name        string                 `json:"name"`
		Source      string                 `json:"source"`
		Repetitions []normalizedRepetition `json:"repetitions"`
	} `json:"cases"`
}

func TestConformanceCorpus_GIVENRepetitions_WHENParsed_THENMatchesNormalizedShapeAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest repetitionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Repetitions == nil {
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
			got := mustJSON(t, normalizeGoRepetitions(result.Document))
			if want := mustJSON(t, fixture.Repetitions); got != want {
				t.Errorf("repetitions = %s, want %s", got, want)
			}
		})
	}
}

// normalizeGoRepetitions lists every repetition in source order, walking each
// body position depth-first so nested repetitions follow their parent.
func normalizeGoRepetitions(document *ast.Document) []normalizedRepetition {
	repetitions := []normalizedRepetition{}
	var walk func(nodes []ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch node := node.(type) {
			case *ast.ElementDecl:
				walk(node.Body)
			case *ast.RenderClause:
				walk(node.Body)
			case *ast.ElementNode:
				walk(node.Children)
			case *ast.RepetitionNode:
				repetitions = append(repetitions, normalizedRepetition{
					Item: node.Item, Start: node.P, End: node.E,
					KeywordStart: node.KeywordRange.Start, KeywordEnd: node.KeywordRange.End,
					ItemStart: node.ItemRange.Start, ItemEnd: node.ItemRange.End,
					InStart: node.InRange.Start, InEnd: node.InRange.End,
					Collection:      normalizeGoRepetitionPath(node.Collection),
					KeyKeywordStart: node.KeyKeywordRange.Start, KeyKeywordEnd: node.KeyKeywordRange.End,
					Key: normalizeGoRepetitionPath(node.Key),
				})
				walk(node.Body)
			}
		}
	}
	walk(document.Nodes)
	return repetitions
}

func normalizeGoRepetitionPath(path *ast.PathExpression) normalizedRepetitionPath {
	normalized := normalizedRepetitionPath{Name: path.String(), Start: path.P, End: path.E, Parts: []normalizedRepetitionPathPart{}}
	for _, part := range path.Parts {
		normalized.Parts = append(normalized.Parts, normalizedRepetitionPathPart{Name: part.Name, Start: part.P, End: part.E})
	}
	return normalized
}
