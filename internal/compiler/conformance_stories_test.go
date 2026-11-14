package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// normalizedStoryDecl is a top-level story or fixture declaration. Kind is
// "story" or "fixture"; the story-only and fixture-only fields are omitted for
// the other kind.
type normalizedStoryDecl struct {
	Kind           string                `json:"kind"`
	Name           string                `json:"name"`
	Start          int                   `json:"start"`
	End            int                   `json:"end"`
	KeywordStart   int                   `json:"keywordStart"`
	KeywordEnd     int                   `json:"keywordEnd"`
	NameStart      int                   `json:"nameStart"`
	NameEnd        int                   `json:"nameEnd"`
	Component      string                `json:"component,omitempty"`
	ComponentStart int                   `json:"componentStart,omitempty"`
	ComponentEnd   int                   `json:"componentEnd,omitempty"`
	ArgumentsStart int                   `json:"argumentsStart,omitempty"`
	ArgumentsEnd   int                   `json:"argumentsEnd,omitempty"`
	Arguments      []normalizedValueArg  `json:"arguments,omitempty"`
	BodyStart      int                   `json:"bodyStart,omitempty"`
	BodyEnd        int                   `json:"bodyEnd,omitempty"`
	Type           string                `json:"type,omitempty"`
	TypeStart      int                   `json:"typeStart,omitempty"`
	TypeEnd        int                   `json:"typeEnd,omitempty"`
	Value          *normalizedStoryValue `json:"value,omitempty"`
}

type normalizedValueArg struct {
	Name      string               `json:"name"`
	Start     int                  `json:"start"`
	End       int                  `json:"end"`
	NameStart int                  `json:"nameStart"`
	NameEnd   int                  `json:"nameEnd"`
	Value     normalizedStoryValue `json:"value"`
}

// normalizedStoryValue is a story value: kind scalar (with its literal kind
// and raw spelling), name (with an optional payload), fields, list, or empty.
type normalizedStoryValue struct {
	Kind         string                 `json:"kind"`
	Start        int                    `json:"start"`
	End          int                    `json:"end"`
	Literal      string                 `json:"literal,omitempty"`
	Raw          string                 `json:"raw,omitempty"`
	Name         string                 `json:"name,omitempty"`
	PayloadStart int                    `json:"payloadStart,omitempty"`
	PayloadEnd   int                    `json:"payloadEnd,omitempty"`
	Fields       []normalizedValueArg   `json:"fields,omitempty"`
	Items        []normalizedStoryValue `json:"items,omitempty"`
}

type storyManifest struct {
	Cases []struct {
		Name    string                `json:"name"`
		Source  string                `json:"source"`
		Stories []normalizedStoryDecl `json:"stories"`
	} `json:"cases"`
}

func TestConformanceCorpus_GIVENStoriesAndFixtures_WHENParsed_THENMatchesNormalizedShapeAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest storyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Stories == nil {
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
			got := mustJSON(t, normalizeGoStories(result.Document))
			if want := mustJSON(t, fixture.Stories); got != want {
				t.Errorf("stories = %s, want %s", got, want)
			}
		})
	}
}

func normalizeGoStories(document *ast.Document) []normalizedStoryDecl {
	declarations := []normalizedStoryDecl{}
	for _, node := range document.Nodes {
		switch declaration := node.(type) {
		case *ast.FixtureDecl:
			value := normalizeGoScalarValue(declaration.Value)
			declarations = append(declarations, normalizedStoryDecl{
				Kind: "fixture", Name: declaration.Name, Start: declaration.P, End: declaration.E,
				KeywordStart: declaration.KeywordRange.Start, KeywordEnd: declaration.KeywordRange.End,
				NameStart: declaration.NameRange.Start, NameEnd: declaration.NameRange.End,
				Type: declaration.TypeDecl, TypeStart: declaration.TypeRange.Start, TypeEnd: declaration.TypeRange.End,
				Value: &value,
			})
		case *ast.StoryDecl:
			item := normalizedStoryDecl{
				Kind: "story", Name: declaration.Name, Start: declaration.P, End: declaration.E,
				KeywordStart: declaration.KeywordRange.Start, KeywordEnd: declaration.KeywordRange.End,
				NameStart: declaration.NameRange.Start, NameEnd: declaration.NameRange.End,
				Component:      declaration.Component,
				ComponentStart: declaration.ComponentRange.Start, ComponentEnd: declaration.ComponentRange.End,
				Arguments: normalizeGoValueArgs(declaration.Arguments),
			}
			if declaration.ArgumentsRange != nil {
				item.ArgumentsStart, item.ArgumentsEnd = declaration.ArgumentsRange.Start, declaration.ArgumentsRange.End
			}
			if declaration.BodyRange != nil {
				item.BodyStart, item.BodyEnd = declaration.BodyRange.Start, declaration.BodyRange.End
			}
			declarations = append(declarations, item)
		}
	}
	return declarations
}

func normalizeGoValueArgs(arguments []*ast.ValueArgument) []normalizedValueArg {
	var normalized []normalizedValueArg
	for _, argument := range arguments {
		normalized = append(normalized, normalizedValueArg{
			Name: argument.Name, Start: argument.P, End: argument.E,
			NameStart: argument.NameRange.Start, NameEnd: argument.NameRange.End,
			Value: normalizeGoStoryValue(argument.Value),
		})
	}
	return normalized
}

func normalizeGoScalarValue(literal *ast.ScalarLiteral) normalizedStoryValue {
	return normalizedStoryValue{Kind: string(ast.ScalarValue), Start: literal.P, End: literal.E, Literal: string(literal.Kind), Raw: literal.Raw}
}

func normalizeGoStoryValue(value *ast.ValueExpr) normalizedStoryValue {
	if value.Kind == ast.ScalarValue {
		return normalizeGoScalarValue(value.Scalar)
	}
	normalized := normalizedStoryValue{
		Kind: string(value.Kind), Start: value.P, End: value.E, Name: value.Name,
		Fields: normalizeGoValueArgs(value.Fields),
	}
	if value.PayloadRange != nil {
		normalized.PayloadStart, normalized.PayloadEnd = value.PayloadRange.Start, value.PayloadRange.End
	}
	for _, item := range value.Items {
		normalized.Items = append(normalized.Items, normalizeGoStoryValue(item))
	}
	return normalized
}
