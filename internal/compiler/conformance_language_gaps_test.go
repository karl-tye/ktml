package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type normalizedElement struct {
	Name       string                `json:"name"`
	NameStart  int                   `json:"nameStart"`
	NameEnd    int                   `json:"nameEnd"`
	Depth      int                   `json:"depth"`
	Multiline  bool                  `json:"multiline"`
	Attributes []normalizedAttribute `json:"attributes"`
}

// normalizedAttribute names an attribute (#name is "id") and records its
// whole source range.
type normalizedAttribute struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type normalizedCondition struct {
	Start     int                     `json:"start"`
	End       int                     `json:"end"`
	IfStart   int                     `json:"ifStart"`
	IfEnd     int                     `json:"ifEnd"`
	ElseStart *int                    `json:"elseStart,omitempty"`
	ElseEnd   *int                    `json:"elseEnd,omitempty"`
	ElseIf    bool                    `json:"elseIf"`
	Condition normalizedConditionNode `json:"condition"`
}

type normalizedConditionNode struct {
	Kind     string                    `json:"kind"`
	Start    int                       `json:"start"`
	End      int                       `json:"end"`
	Path     *normalizedRepetitionPath `json:"path,omitempty"`
	Operands []normalizedConditionNode `json:"operands,omitempty"`
}

type normalizedSlot struct {
	Name         string `json:"name"`
	Start        int    `json:"start"`
	End          int    `json:"end"`
	KeywordStart int    `json:"keywordStart"`
	KeywordEnd   int    `json:"keywordEnd"`
	NameStart    int    `json:"nameStart"`
	NameEnd      int    `json:"nameEnd"`
}

type languageGapsManifest struct {
	Cases []struct {
		Name       string                `json:"name"`
		Source     string                `json:"source"`
		Elements   []normalizedElement   `json:"elements"`
		Conditions []normalizedCondition `json:"conditions"`
		Slots      []normalizedSlot      `json:"slots"`
	} `json:"cases"`
}

func TestConformanceCorpus_GIVENElementsConditionsAndSlots_WHENParsed_THENMatchesNormalizedShapeAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest languageGapsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Elements == nil && fixture.Conditions == nil && fixture.Slots == nil {
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
			elements, conditions, slots := normalizeGoBodies(result.Document)
			if fixture.Elements != nil {
				if got, want := mustJSON(t, elements), mustJSON(t, fixture.Elements); got != want {
					t.Errorf("elements = %s, want %s", got, want)
				}
			}
			if fixture.Conditions != nil {
				if got, want := mustJSON(t, conditions), mustJSON(t, fixture.Conditions); got != want {
					t.Errorf("conditions = %s, want %s", got, want)
				}
			}
			if fixture.Slots != nil {
				if got, want := mustJSON(t, slots), mustJSON(t, fixture.Slots); got != want {
					t.Errorf("slots = %s, want %s", got, want)
				}
			}
		})
	}
}

// normalizeGoBodies lists every body element, condition, and slot fill in
// source pre-order across all body positions. Element depth counts enclosing
// elements only.
func normalizeGoBodies(document *ast.Document) ([]normalizedElement, []normalizedCondition, []normalizedSlot) {
	elements := []normalizedElement{}
	conditions := []normalizedCondition{}
	slots := []normalizedSlot{}
	var walk func(nodes []ast.Node, depth int)
	walk = func(nodes []ast.Node, depth int) {
		for _, node := range nodes {
			switch node := node.(type) {
			case *ast.ElementDecl:
				walk(node.Body, depth)
			case *ast.RenderClause:
				walk(node.Body, depth)
			case *ast.RepetitionNode:
				walk(node.Body, depth)
			case *ast.SlotNode:
				slots = append(slots, normalizedSlot{
					Name: node.Name, Start: node.P, End: node.E,
					KeywordStart: node.KeywordRange.Start, KeywordEnd: node.KeywordRange.End,
					NameStart: node.NameRange.Start, NameEnd: node.NameRange.End,
				})
				walk(node.Body, depth)
			case *ast.ConditionNode:
				normalized := normalizedCondition{
					Start: node.P, End: node.E,
					IfStart: node.KeywordRange.Start, IfEnd: node.KeywordRange.End,
					ElseIf: node.ElseIf, Condition: normalizeGoCondition(node.Condition),
				}
				if node.ElseKeywordRange != nil {
					start, end := node.ElseKeywordRange.Start, node.ElseKeywordRange.End
					normalized.ElseStart, normalized.ElseEnd = &start, &end
				}
				conditions = append(conditions, normalized)
				walk(node.Then, depth)
				walk(node.Else, depth)
			case *ast.ElementNode:
				element := normalizedElement{
					Name: node.Name, NameStart: node.P, NameEnd: node.P + len(node.Name),
					Depth: depth, Multiline: node.Multiline, Attributes: []normalizedAttribute{},
				}
				for _, attribute := range node.Attributes {
					if !attribute.IsNamed() {
						continue
					}
					element.Attributes = append(element.Attributes, normalizedAttribute{Name: attribute.Name, Start: attribute.P, End: attribute.E})
				}
				elements = append(elements, element)
				walk(node.Children, depth+1)
			}
		}
	}
	walk(document.Nodes, 0)
	return elements, conditions, slots
}

func normalizeGoCondition(condition *ast.ConditionExpr) normalizedConditionNode {
	normalized := normalizedConditionNode{Kind: string(condition.Kind), Start: condition.P, End: condition.E}
	if condition.Path != nil {
		path := normalizeGoRepetitionPath(condition.Path)
		normalized.Path = &path
	}
	for _, operand := range condition.Operands {
		normalized.Operands = append(normalized.Operands, normalizeGoCondition(operand))
	}
	return normalized
}
