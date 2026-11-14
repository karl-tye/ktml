package parser

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func parseValid(t *testing.T, source string) *ast.Document {
	t.Helper()
	p := New([]byte(source))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	return doc
}

// span returns the range of the nth (0-based) occurrence of needle.
func span(t *testing.T, source, needle string, occurrence int) ast.SourceRange {
	t.Helper()
	offset := 0
	for i := 0; ; i++ {
		index := strings.Index(source[offset:], needle)
		if index < 0 {
			t.Fatalf("%q occurrence %d not found", needle, occurrence)
		}
		if i == occurrence {
			return ast.SourceRange{Start: offset + index, End: offset + index + len(needle)}
		}
		offset += index + len(needle)
	}
}

func TestParseState_GIVENNullaryAndPayloadVariants_WHENParsed_THENRecordsCompleteRanges(t *testing.T) {
	source := "state Search {\n  Loading,\n  Loaded [items: [Item], total?: integer],\n  Failed [state: string]\n}\nrecord After [state: string]"
	doc := parseValid(t, source)
	if len(doc.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(doc.Nodes))
	}
	state, ok := doc.Nodes[0].(*ast.StateDecl)
	if !ok {
		t.Fatalf("node = %T, want *ast.StateDecl", doc.Nodes[0])
	}
	if state.Name != "Search" || state.P != 0 || state.E != strings.Index(source, "}")+1 || state.KeywordRange != (ast.SourceRange{Start: 0, End: 5}) || state.NameRange != span(t, source, "Search", 0) {
		t.Fatalf("state = %+v", state)
	}
	if len(state.Variants) != 3 {
		t.Fatalf("variants = %d, want 3", len(state.Variants))
	}
	loading, loaded, failed := state.Variants[0], state.Variants[1], state.Variants[2]
	if loading.Name != "Loading" || loading.Payload || loading.NameRange != span(t, source, "Loading", 0) || loading.E != loading.NameRange.End {
		t.Errorf("loading = %+v", loading)
	}
	payload := span(t, source, "[items: [Item], total?: integer]", 0)
	if loaded.Name != "Loaded" || !loaded.Payload || loaded.PayloadRange == nil || *loaded.PayloadRange != payload || loaded.E != payload.End || len(loaded.Fields) != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if field := loaded.Fields[1]; field.Name != "total" || !field.Optional || field.TypeDecl != "integer" {
		t.Errorf("total = %+v", field)
	}
	if len(failed.Fields) != 1 || failed.Fields[0].Name != "state" {
		t.Errorf("failed = %+v", failed)
	}
	if record, ok := doc.Nodes[1].(*ast.RecordDecl); !ok || len(record.Fields) != 1 || record.Fields[0].Name != "state" {
		t.Errorf("record after state = %#v", doc.Nodes[1])
	}
}

func TestParseState_GIVENEmptyStateAndEmptyPayload_WHENParsed_THENKeepsPayloadMarker(t *testing.T) {
	doc := parseValid(t, "state Empty {}\nstate S { A [], B, }")
	empty := doc.Nodes[0].(*ast.StateDecl)
	if len(empty.Variants) != 0 {
		t.Errorf("empty variants = %+v", empty.Variants)
	}
	s := doc.Nodes[1].(*ast.StateDecl)
	if len(s.Variants) != 2 || !s.Variants[0].Payload || len(s.Variants[0].Fields) != 0 || s.Variants[1].Payload {
		t.Errorf("variants = %+v %+v", s.Variants[0], s.Variants[1])
	}
}

func TestParseClauses_GIVENSignatureAndClauses_WHENParsed_THENRecordsPatternsAndBindings(t *testing.T) {
	source := `Results [state: Search, query: string]

Results [state = Loading] { "wait" }

Results [state = Search.Loaded, items, total as count] { div }

Results [flag = true] {}`
	doc := parseValid(t, source)
	if len(doc.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(doc.Nodes))
	}
	signature, ok := doc.Nodes[0].(*ast.ElementDecl)
	if !ok || !signature.Bodyless || signature.DefaultRange != nil || !signature.IsSignature() || len(signature.Attributes) != 2 || signature.Body != nil {
		t.Fatalf("signature = %#v", doc.Nodes[0])
	}
	if signature.E != strings.Index(source, "]")+1 {
		t.Errorf("signature end = %d", signature.E)
	}
	loading := doc.Nodes[1].(*ast.RenderClause)
	if loading.Name != "Results" || loading.NameRange != span(t, source, "Results", 1) || loading.Selector != "state" || loading.SelectorRange != span(t, source, "state", 1) {
		t.Errorf("loading = %+v", loading)
	}
	if loading.Pattern.Name != "Loading" || loading.Pattern.Qualifier != "" || loading.Pattern.NameRange != span(t, source, "Loading", 0) || len(loading.Bindings) != 0 || len(loading.Body) != 1 {
		t.Errorf("loading pattern = %+v body = %d", loading.Pattern, len(loading.Body))
	}
	if loading.P != span(t, source, "Results", 1).Start || loading.E != span(t, source, `"wait" }`, 0).End {
		t.Errorf("loading range = %d-%d", loading.P, loading.E)
	}
	loaded := doc.Nodes[2].(*ast.RenderClause)
	pattern := loaded.Pattern
	if pattern.Qualifier != "Search" || *pattern.QualifierRange != span(t, source, "Search", 1) || pattern.Name != "Loaded" || pattern.NameRange != span(t, source, "Loaded", 0) || pattern.String() != "Search.Loaded" {
		t.Errorf("qualified pattern = %+v", pattern)
	}
	if pattern.P != span(t, source, "Search.Loaded", 0).Start || pattern.E != span(t, source, "Search.Loaded", 0).End {
		t.Errorf("pattern range = %d-%d", pattern.P, pattern.E)
	}
	if len(loaded.Bindings) != 2 {
		t.Fatalf("bindings = %+v", loaded.Bindings)
	}
	items, count := loaded.Bindings[0], loaded.Bindings[1]
	if items.Field != "items" || items.Alias != "" || items.FieldRange != span(t, source, "items", 0) || items.LocalName() != "items" {
		t.Errorf("items = %+v", items)
	}
	if count.Field != "total" || count.Alias != "count" || *count.AliasRange != span(t, source, "count", 0) || count.LocalName() != "count" || count.P != span(t, source, "total as count", 0).Start || count.E != span(t, source, "total as count", 0).End {
		t.Errorf("count = %+v", count)
	}
	flag := doc.Nodes[3].(*ast.RenderClause)
	if flag.Selector != "flag" || flag.Pattern.Name != "true" || len(flag.Body) != 0 {
		t.Errorf("boolean clause = %+v", flag)
	}
}

func TestParseClauses_GIVENDefaultSignature_WHENParsed_THENRecordsDefaultBody(t *testing.T) {
	source := "Subtitle [subtitle?: string] default { span }\nSubtitle [subtitle = Some, value as text] { \"{text}\" }"
	doc := parseValid(t, source)
	signature := doc.Nodes[0].(*ast.ElementDecl)
	if signature.Bodyless || signature.DefaultRange == nil || *signature.DefaultRange != span(t, source, "default", 0) || len(signature.Body) != 1 {
		t.Fatalf("signature = %+v", signature)
	}
	clause := doc.Nodes[1].(*ast.RenderClause)
	if clause.Pattern.Name != "Some" || len(clause.Bindings) != 1 || clause.Bindings[0].Field != "value" || clause.Bindings[0].Alias != "text" {
		t.Errorf("clause = %+v", clause)
	}
}

func TestParseClauses_GIVENLegacyOverride_WHENValueIsLiteral_THENKeepsOverrideForm(t *testing.T) {
	doc := parseValid(t, `Button [disabled="true"] { button }`+"\n"+`Button [size = 2] { button }`)
	for index, node := range doc.Nodes {
		declaration, ok := node.(*ast.ElementDecl)
		if !ok || len(declaration.Attributes) != 1 || declaration.Attributes[0].Kind != ast.OverrideDeclaration {
			t.Errorf("node %d = %#v, want legacy override", index, node)
		}
	}
}

func TestParseClauses_GIVENMalformedHeaders_WHENParsed_THENReportsSyntaxErrors(t *testing.T) {
	tests := []struct {
		name, source string
	}{
		{"trailing comma", "A [s = X, f,] {}"},
		{"missing body", "A [s = X]\nB [] {}"},
		{"default without body", "A [s: S] default\nA [s = X] {}"},
		{"binding without name", "A [s = X, as y] {}"},
		{"unterminated qualifier", "A [s = S.] {}"},
		{"variant missing separator", "state S { A B }"},
		{"state missing close", "state S { A, B\nrecord R []"},
		{"component without inputs or body", "A\nB [] {}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New([]byte(tt.source))
			p.ParseDocument()
			if p.Err() == nil {
				t.Fatalf("ParseDocument(%q) succeeded, want syntax error", tt.source)
			}
		})
	}
}

func TestParseRecord_GIVENFieldNamedStateOrRecord_WHENParsed_THENItIsAField(t *testing.T) {
	doc := parseValid(t, "record R [state: S, record: string]")
	record := doc.Nodes[0].(*ast.RecordDecl)
	if len(record.Fields) != 2 || record.Fields[0].Name != "state" || record.Fields[1].Name != "record" {
		t.Fatalf("fields = %+v", record.Fields)
	}
}
