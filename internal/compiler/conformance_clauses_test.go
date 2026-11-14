package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type normalizedState struct {
	Name         string              `json:"name"`
	Start        int                 `json:"start"`
	End          int                 `json:"end"`
	KeywordStart int                 `json:"keywordStart"`
	KeywordEnd   int                 `json:"keywordEnd"`
	NameStart    int                 `json:"nameStart"`
	NameEnd      int                 `json:"nameEnd"`
	Variants     []normalizedVariant `json:"variants"`
}

type normalizedVariant struct {
	Name         string            `json:"name"`
	Start        int               `json:"start"`
	End          int               `json:"end"`
	NameStart    int               `json:"nameStart"`
	NameEnd      int               `json:"nameEnd"`
	Payload      bool              `json:"payload"`
	PayloadStart int               `json:"payloadStart,omitempty"`
	PayloadEnd   int               `json:"payloadEnd,omitempty"`
	Fields       []normalizedField `json:"fields"`
}

type normalizedClause struct {
	Name          string              `json:"name"`
	Start         int                 `json:"start"`
	End           int                 `json:"end"`
	NameStart     int                 `json:"nameStart"`
	NameEnd       int                 `json:"nameEnd"`
	Selector      string              `json:"selector"`
	SelectorStart int                 `json:"selectorStart"`
	SelectorEnd   int                 `json:"selectorEnd"`
	Pattern       normalizedPattern   `json:"pattern"`
	Bindings      []normalizedBinding `json:"bindings"`
}

type normalizedPattern struct {
	Name           string `json:"name"`
	Start          int    `json:"start"`
	End            int    `json:"end"`
	NameStart      int    `json:"nameStart"`
	NameEnd        int    `json:"nameEnd"`
	Qualifier      string `json:"qualifier,omitempty"`
	QualifierStart int    `json:"qualifierStart,omitempty"`
	QualifierEnd   int    `json:"qualifierEnd,omitempty"`
}

type normalizedBinding struct {
	Field      string `json:"field"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	FieldStart int    `json:"fieldStart"`
	FieldEnd   int    `json:"fieldEnd"`
	Alias      string `json:"alias,omitempty"`
	AliasStart int    `json:"aliasStart,omitempty"`
	AliasEnd   int    `json:"aliasEnd,omitempty"`
}

type clauseManifest struct {
	Cases []struct {
		Name    string             `json:"name"`
		Source  string             `json:"source"`
		States  []normalizedState  `json:"states"`
		Clauses []normalizedClause `json:"clauses"`
	} `json:"cases"`
}

func TestConformanceCorpus_GIVENStatesAndClauses_WHENParsed_THENMatchesNormalizedShapeAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest clauseManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.States == nil && fixture.Clauses == nil {
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
			if fixture.States != nil {
				got := mustJSON(t, normalizeGoStates(result.Document))
				if want := mustJSON(t, fixture.States); got != want {
					t.Errorf("states = %s, want %s", got, want)
				}
			}
			if fixture.Clauses != nil {
				got := mustJSON(t, normalizeGoClauses(result.Document))
				if want := mustJSON(t, fixture.Clauses); got != want {
					t.Errorf("clauses = %s, want %s", got, want)
				}
			}
		})
	}
}

func normalizeGoStates(document *ast.Document) []normalizedState {
	states := []normalizedState{}
	for _, node := range document.Nodes {
		state, ok := node.(*ast.StateDecl)
		if !ok {
			continue
		}
		normalized := normalizedState{
			Name: state.Name, Start: state.P, End: state.E,
			KeywordStart: state.KeywordRange.Start, KeywordEnd: state.KeywordRange.End,
			NameStart: state.NameRange.Start, NameEnd: state.NameRange.End,
			Variants: []normalizedVariant{},
		}
		for _, variant := range state.Variants {
			item := normalizedVariant{
				Name: variant.Name, Start: variant.P, End: variant.E,
				NameStart: variant.NameRange.Start, NameEnd: variant.NameRange.End,
				Payload: variant.Payload, Fields: normalizeGoFields(variant.Fields),
			}
			if variant.PayloadRange != nil {
				item.PayloadStart, item.PayloadEnd = variant.PayloadRange.Start, variant.PayloadRange.End
			}
			normalized.Variants = append(normalized.Variants, item)
		}
		states = append(states, normalized)
	}
	return states
}

func normalizeGoClauses(document *ast.Document) []normalizedClause {
	clauses := []normalizedClause{}
	for _, node := range document.Nodes {
		clause, ok := node.(*ast.RenderClause)
		if !ok {
			continue
		}
		pattern := clause.Pattern
		normalized := normalizedClause{
			Name: clause.Name, Start: clause.P, End: clause.E,
			NameStart: clause.NameRange.Start, NameEnd: clause.NameRange.End,
			Selector: clause.Selector, SelectorStart: clause.SelectorRange.Start, SelectorEnd: clause.SelectorRange.End,
			Pattern: normalizedPattern{
				Name: pattern.Name, Start: pattern.P, End: pattern.E,
				NameStart: pattern.NameRange.Start, NameEnd: pattern.NameRange.End,
			},
			Bindings: []normalizedBinding{},
		}
		if pattern.QualifierRange != nil {
			normalized.Pattern.Qualifier = pattern.Qualifier
			normalized.Pattern.QualifierStart, normalized.Pattern.QualifierEnd = pattern.QualifierRange.Start, pattern.QualifierRange.End
		}
		for _, binding := range clause.Bindings {
			item := normalizedBinding{
				Field: binding.Field, Start: binding.P, End: binding.E,
				FieldStart: binding.FieldRange.Start, FieldEnd: binding.FieldRange.End,
			}
			if binding.AliasRange != nil {
				item.Alias, item.AliasStart, item.AliasEnd = binding.Alias, binding.AliasRange.Start, binding.AliasRange.End
			}
			normalized.Bindings = append(normalized.Bindings, item)
		}
		clauses = append(clauses, normalized)
	}
	return clauses
}
