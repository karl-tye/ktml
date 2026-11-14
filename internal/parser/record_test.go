package parser

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestParseRecord_GIVENFieldsOfEveryTypeShape_WHENParsed_THENRecordsCompleteRanges(t *testing.T) {
	source := "record Item [id: string, note?: integer, tags: [[Tag]],]"
	p := New([]byte(source))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(doc.Nodes))
	}
	record, ok := doc.Nodes[0].(*ast.RecordDecl)
	if !ok {
		t.Fatalf("node = %T, want *ast.RecordDecl", doc.Nodes[0])
	}
	if record.Name != "Item" || record.P != 0 || record.E != len(source) || record.KeywordRange != (ast.SourceRange{Start: 0, End: 6}) || record.NameRange != (ast.SourceRange{Start: 7, End: 11}) {
		t.Fatalf("record = %+v", record)
	}
	tests := []struct {
		name, typeDecl       string
		start, end           int
		typeStart, typeEnd   int
		optional             bool
		optionalStart, depth int
	}{
		{name: "id", typeDecl: "string", start: 13, end: 23, typeStart: 17, typeEnd: 23},
		{name: "note", typeDecl: "integer", start: 25, end: 39, typeStart: 32, typeEnd: 39, optional: true, optionalStart: 29},
		{name: "tags", typeDecl: "[[Tag]]", start: 41, end: 54, typeStart: 47, typeEnd: 54, depth: 2},
	}
	if len(record.Fields) != len(tests) {
		t.Fatalf("fields = %d, want %d", len(record.Fields), len(tests))
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := record.Fields[index]
			if field.Name != tt.name || field.TypeDecl != tt.typeDecl || field.P != tt.start || field.E != tt.end || field.NameRange.Start != tt.start || field.NameRange.End != tt.start+len(tt.name) {
				t.Errorf("field = %+v", field)
			}
			if field.TypeRange != (ast.SourceRange{Start: tt.typeStart, End: tt.typeEnd}) || field.TypeExpr.P != tt.typeStart || field.TypeExpr.E != tt.typeEnd {
				t.Errorf("type range = %+v / %d-%d", field.TypeRange, field.TypeExpr.P, field.TypeExpr.E)
			}
			if field.Optional != tt.optional || (tt.optional && *field.OptionalMarker != (ast.SourceRange{Start: tt.optionalStart, End: tt.optionalStart + 1})) {
				t.Errorf("optional = %v %+v", field.Optional, field.OptionalMarker)
			}
			depth := 0
			expr := field.TypeExpr
			for expr.Kind == ast.CollectionTypeExpr {
				depth++
				expr = expr.Element
			}
			if depth != tt.depth || expr.NameRange == nil || expr.NameRange.End-expr.NameRange.Start != len(expr.Name) {
				t.Errorf("type expression depth = %d, element = %+v", depth, expr)
			}
		})
	}
}

func TestParseRecord_GIVENEmptyRecord_WHENParsed_THENHasNoFields(t *testing.T) {
	p := New([]byte("record Empty []"))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	record := doc.Nodes[0].(*ast.RecordDecl)
	if len(record.Fields) != 0 || record.E != 15 {
		t.Fatalf("record = %+v", record)
	}
}

func TestParseImport_GIVENAliases_WHENParsed_THENRecordsSpecifierRanges(t *testing.T) {
	source := `import { Item, State as Search } from "./search.ktmlschema"`
	p := New([]byte(source))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	imp := doc.Nodes[0].(*ast.ImportStmt)
	if len(imp.Items) != 2 || imp.Items[0] != "Item" || imp.Items[1] != "State" {
		t.Fatalf("items = %v", imp.Items)
	}
	if imp.PathRange != (ast.SourceRange{Start: 38, End: len(source)}) {
		t.Fatalf("path range = %+v", imp.PathRange)
	}
	first, second := imp.Specifiers[0], imp.Specifiers[1]
	if first.Name != "Item" || first.NameRange != (ast.SourceRange{Start: 9, End: 13}) || first.Alias != "" || first.LocalName() != "Item" {
		t.Errorf("first specifier = %+v", first)
	}
	if second.Name != "State" || second.NameRange != (ast.SourceRange{Start: 15, End: 20}) || second.Alias != "Search" || *second.AliasRange != (ast.SourceRange{Start: 24, End: 30}) || second.LocalName() != "Search" {
		t.Errorf("second specifier = %+v", second)
	}
}

func TestParseInput_GIVENCollectionAndRecordTypes_WHENParsed_THENTypeIsStructured(t *testing.T) {
	source := "Card [items: [ Item ], owner?: User] { div }"
	p := New([]byte(source))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	inputs := doc.Nodes[0].(*ast.ElementDecl).Attributes
	if inputs[0].TypeDecl != "[Item]" || inputs[0].TypeRange != (ast.SourceRange{Start: 13, End: 21}) || inputs[0].TypeExpr.Kind != ast.CollectionTypeExpr || inputs[0].E != 21 {
		t.Errorf("collection input = %+v", inputs[0])
	}
	if inputs[1].TypeDecl != "User" || inputs[1].Mode != ast.OptionalInput || inputs[1].TypeExpr.Kind != ast.NamedTypeExpr {
		t.Errorf("record input = %+v", inputs[1])
	}
}

func TestParseRecord_GIVENInvalidSyntax_WHENParsed_THENReportsRangeAndRecoversNextDeclaration(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		start, end int
		nextKind   string
	}{
		{name: "missing colon", source: "record A [id string]\nrecord B []", start: 13, end: 19, nextKind: "RecordDecl"},
		{name: "schema default", source: "record A [id: string = \"x\"]\nrecord B []", start: 21, end: 22, nextKind: "RecordDecl"},
		{name: "missing bracket", source: "record A [id: string\nrecord B []", start: 21, end: 27, nextKind: "RecordDecl"},
		{name: "missing comma", source: "record A [id: string title: string]\nCard { div }", start: 21, end: 26, nextKind: "ElementDecl"},
		{name: "import without comma", source: "import { A B } from \"./a.ktmlschema\"\nrecord B []", start: 11, end: 12, nextKind: "RecordDecl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New([]byte(tt.source))
			doc := p.ParseDocument()
			errors := p.Errors()
			if len(errors) == 0 || errors[0].Pos != tt.start || errors[0].End != tt.end {
				t.Fatalf("errors = %+v, want first at %d-%d", errors, tt.start, tt.end)
			}
			if len(doc.Nodes) == 0 || doc.Nodes[len(doc.Nodes)-1].Type() != tt.nextKind {
				t.Fatalf("nodes = %#v, want recovered %s", doc.Nodes, tt.nextKind)
			}
		})
	}
}

func TestParseElement_GIVENRecordAsElementName_WHENNotTopLevel_THENRemainsElement(t *testing.T) {
	p := New([]byte(`Page { record "x" }`))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	if element := doc.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ElementNode); element.Name != "record" {
		t.Fatalf("element = %+v", element)
	}
}
