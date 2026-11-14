package parser_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

func TestParseDocument_GIVENQuotedInterpolation_WHENParsed_THENBuildsCanonicalSegments(t *testing.T) {
	source := []byte(`Card [name: string] { "Hello, {name}!" }`)
	p := parser.New(source)
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}

	declaration := document.Nodes[0].(*ast.ElementDecl)
	text := declaration.Body[0].(*ast.TextNode)
	if text.Quoted == nil {
		t.Fatal("quoted text structure = nil")
	}
	if got, want := len(text.Quoted.Segments), 3; got != want {
		t.Fatalf("segment count = %d, want %d", got, want)
	}
	if got := text.Quoted.Segments[0]; got.Kind != ast.LiteralSegment || got.Literal != "Hello, " || got.P != 23 || got.E != 30 {
		t.Errorf("literal segment = %#v", got)
	}
	path := text.Quoted.Segments[1]
	if path.Kind != ast.PathSegment || path.Path == nil || path.P != 30 || path.E != 36 {
		t.Fatalf("path segment = %#v", path)
	}
	if got, want := path.Path.String(), "name"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got := path.Path.Parts[0]; got.Name != "name" || got.P != 31 || got.E != 35 {
		t.Errorf("path part = %#v", got)
	}
	if got := text.Quoted.Segments[2]; got.Kind != ast.LiteralSegment || got.Literal != "!" || got.P != 36 || got.E != 37 {
		t.Errorf("trailing literal segment = %#v", got)
	}
}

func TestParseDocument_GIVENQuotedAttributePath_WHENParsed_THENBuildsDottedPathStructure(t *testing.T) {
	source := []byte(`Card [user: User] { a title="Profile: {user.Name}" }`)
	p := parser.New(source)
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}

	declaration := document.Nodes[0].(*ast.ElementDecl)
	attribute := declaration.Body[0].(*ast.ElementNode).Attributes[0]
	if attribute.Quoted == nil || len(attribute.Quoted.Segments) != 2 {
		t.Fatalf("quoted attribute structure = %#v", attribute.Quoted)
	}
	path := attribute.Quoted.Segments[1].Path
	if got, want := path.String(), "user.Name"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	start := strings.Index(string(source), "{user.Name}")
	if path.P != start || path.E != start+len("{user.Name}") {
		t.Errorf("path range = %d-%d, want %d-%d", path.P, path.E, start, start+len("{user.Name}"))
	}
	if got := path.Parts[1]; got.Name != "Name" || got.P != start+6 || got.E != start+10 {
		t.Errorf("field path part = %#v", got)
	}
}

func TestParseDocument_GIVENSpacedLiteralBraces_WHENParsed_THENDoesNotTreatEmbeddedObjectAsInterpolation(t *testing.T) {
	source := []byte(`Page { body x-data="{ filtersOpen: false }" }`)
	p := parser.New(source)
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	attribute := document.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ElementNode).Attributes[0]
	if attribute.Quoted == nil || len(attribute.Quoted.Segments) != 1 || attribute.Quoted.Segments[0].Kind != ast.LiteralSegment || attribute.Quoted.Segments[0].Literal != "{ filtersOpen: false }" {
		t.Errorf("quoted literal = %#v", attribute.Quoted)
	}
}

func TestParseDocument_GIVENBacktickText_WHENParsed_THENTreatsBracesAsLiteral(t *testing.T) {
	source := []byte("Card [name: string] { `literal {name}` }")
	p := parser.New(source)
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	text := document.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.TextNode)
	if text.Quoted == nil || len(text.Quoted.Segments) != 1 || text.Quoted.Segments[0].Kind != ast.LiteralSegment || text.Quoted.Segments[0].Literal != "literal {name}" {
		t.Errorf("backtick quoted structure = %#v", text.Quoted)
	}
}
