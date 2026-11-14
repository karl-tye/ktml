package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// normalizedAttributeCondition is one conditional attribute group. Its
// branches list their named attributes; nested groups are separate entries
// that follow their parent in pre-order.
type normalizedAttributeCondition struct {
	Start     int                     `json:"start"`
	End       int                     `json:"end"`
	IfStart   int                     `json:"ifStart"`
	IfEnd     int                     `json:"ifEnd"`
	ElseStart *int                    `json:"elseStart,omitempty"`
	ElseEnd   *int                    `json:"elseEnd,omitempty"`
	ElseIf    bool                    `json:"elseIf"`
	Condition normalizedConditionNode `json:"condition"`
	Then      []normalizedAttribute   `json:"then"`
	Else      []normalizedAttribute   `json:"else"`
}

// normalizedForwarding is one spread (kind spread, with its path) or
// attribute bag literal (kind bag, with its name, parenthesized range, and
// named entries), in source pre-order.
type normalizedForwarding struct {
	Kind       string                    `json:"kind"`
	Start      int                       `json:"start"`
	End        int                       `json:"end"`
	Name       string                    `json:"name,omitempty"`
	Path       *normalizedRepetitionPath `json:"path,omitempty"`
	BagStart   int                       `json:"bagStart,omitempty"`
	BagEnd     int                       `json:"bagEnd,omitempty"`
	Attributes []normalizedAttribute     `json:"attributes,omitempty"`
}

func TestConformanceCorpus_GIVENSpreadsAndBags_WHENParsed_THENMatchesNormalizedForwarding(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		Cases []struct {
			Name       string                 `json:"name"`
			Source     string                 `json:"source"`
			Forwarding []normalizedForwarding `json:"forwarding"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Forwarding == nil {
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
			if got, want := mustJSON(t, normalizeGoForwarding(result.Document)), mustJSON(t, fixture.Forwarding); got != want {
				t.Errorf("forwarding = %s, want %s", got, want)
			}
		})
	}
}

// normalizeGoForwarding lists spreads and bag literals in source pre-order:
// elements in body pre-order, then their entries, conditional branches, and
// bag contents depth first.
func normalizeGoForwarding(document *ast.Document) []normalizedForwarding {
	result := []normalizedForwarding{}
	var entries func([]*ast.AttributeNode)
	entries = func(list []*ast.AttributeNode) {
		for _, entry := range list {
			switch {
			case entry.Condition != nil:
				entries(entry.Condition.Then)
				entries(entry.Condition.Else)
			case entry.Spread != nil:
				path := normalizeGoRepetitionPath(entry.Spread.Path)
				result = append(result, normalizedForwarding{Kind: "spread", Start: entry.P, End: entry.E, Path: &path})
			case entry.BagRange != nil:
				attributes := []normalizedAttribute{}
				for _, inner := range entry.Bag {
					if inner.IsNamed() {
						attributes = append(attributes, normalizedAttribute{Name: inner.Name, Start: inner.P, End: inner.E})
					}
				}
				result = append(result, normalizedForwarding{Kind: "bag", Start: entry.P, End: entry.E, Name: entry.Name, BagStart: entry.BagRange.Start, BagEnd: entry.BagRange.End, Attributes: attributes})
				entries(entry.Bag)
			}
		}
	}
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch node := node.(type) {
			case *ast.ElementDecl:
				walk(node.Body)
			case *ast.RenderClause:
				walk(node.Body)
			case *ast.RepetitionNode:
				walk(node.Body)
			case *ast.SlotNode:
				walk(node.Body)
			case *ast.ConditionNode:
				walk(node.Then)
				walk(node.Else)
			case *ast.ElementNode:
				entries(node.Attributes)
				walk(node.Children)
			}
		}
	}
	walk(document.Nodes)
	return result
}

func TestConformanceCorpus_GIVENConditionalAttributes_WHENParsed_THENMatchesNormalizedGroups(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		Cases []struct {
			Name                string                         `json:"name"`
			Source              string                         `json:"source"`
			AttributeConditions []normalizedAttributeCondition `json:"attributeConditions"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.AttributeConditions == nil {
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
			got := normalizeGoAttributeConditions(result.Document)
			if got, want := mustJSON(t, got), mustJSON(t, fixture.AttributeConditions); got != want {
				t.Errorf("attributeConditions = %s, want %s", got, want)
			}
		})
	}
}

// normalizeGoAttributeConditions lists every conditional attribute group in
// source pre-order: elements in body pre-order, then each element's entries.
func normalizeGoAttributeConditions(document *ast.Document) []normalizedAttributeCondition {
	groups := []normalizedAttributeCondition{}
	named := func(entries []*ast.AttributeNode) []normalizedAttribute {
		attributes := []normalizedAttribute{}
		for _, entry := range entries {
			if entry.IsNamed() {
				attributes = append(attributes, normalizedAttribute{Name: entry.Name, Start: entry.P, End: entry.E})
			}
		}
		return attributes
	}
	var entries func([]*ast.AttributeNode)
	entries = func(list []*ast.AttributeNode) {
		for _, entry := range list {
			group := entry.Condition
			if group == nil {
				continue
			}
			normalized := normalizedAttributeCondition{
				Start: entry.P, End: entry.E,
				IfStart: group.KeywordRange.Start, IfEnd: group.KeywordRange.End,
				ElseIf: group.ElseIf, Condition: normalizeGoCondition(group.Condition),
				Then: named(group.Then), Else: named(group.Else),
			}
			if group.ElseKeywordRange != nil {
				start, end := group.ElseKeywordRange.Start, group.ElseKeywordRange.End
				normalized.ElseStart, normalized.ElseEnd = &start, &end
			}
			groups = append(groups, normalized)
			entries(group.Then)
			entries(group.Else)
		}
	}
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch node := node.(type) {
			case *ast.ElementDecl:
				walk(node.Body)
			case *ast.RenderClause:
				walk(node.Body)
			case *ast.RepetitionNode:
				walk(node.Body)
			case *ast.SlotNode:
				walk(node.Body)
			case *ast.ConditionNode:
				walk(node.Then)
				walk(node.Else)
			case *ast.ElementNode:
				entries(node.Attributes)
				walk(node.Children)
			}
		}
	}
	walk(document.Nodes)
	return groups
}
