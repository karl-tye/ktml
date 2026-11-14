package parser

import (
	"encoding/json"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func mustRepetition(t *testing.T, node ast.Node) *ast.RepetitionNode {
	t.Helper()
	repetition, ok := node.(*ast.RepetitionNode)
	if !ok {
		t.Fatalf("node = %T, want *ast.RepetitionNode", node)
	}
	return repetition
}

func pathSpelling(path *ast.PathExpression) string {
	if path == nil {
		return "<nil>"
	}
	return path.String()
}

func TestParseRepetition_GIVENNestedKeyedLoops_WHENParsed_THENRecordsHeaderRangesAndBodies(t *testing.T) {
	source := "Board [groups: [Group]] {\n\tfor group in groups key group.id {\n\t\th2 \"{group.title}\"\n\t\tfor card in group.cards key card.id {\n\t\t\tp \"{card.title}\"\n\t\t}\n\t}\n}\n"
	parser := New([]byte(source))
	document := parser.ParseDocument()
	if err := parser.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	declaration := document.Nodes[0].(*ast.ElementDecl)
	outer := mustRepetition(t, declaration.Body[0])
	slice := func(r ast.SourceRange) string { return source[r.Start:r.End] }
	if got := slice(outer.KeywordRange); got != "for" {
		t.Errorf("keyword = %q", got)
	}
	if outer.Item != "group" || slice(outer.ItemRange) != "group" {
		t.Errorf("item = %q range %q", outer.Item, slice(outer.ItemRange))
	}
	if got := slice(outer.InRange); got != "in" {
		t.Errorf("in = %q", got)
	}
	if got := pathSpelling(outer.Collection); got != "groups" || source[outer.Collection.P:outer.Collection.E] != "groups" {
		t.Errorf("collection = %q", got)
	}
	if got := slice(outer.KeyKeywordRange); got != "key" {
		t.Errorf("key keyword = %q", got)
	}
	if got := pathSpelling(outer.Key); got != "group.id" || source[outer.Key.P:outer.Key.E] != "group.id" {
		t.Errorf("key = %q", got)
	}
	if outer.Key.Parts[1].Name != "id" || source[outer.Key.Parts[1].P:outer.Key.Parts[1].E] != "id" {
		t.Errorf("key parts = %#v", outer.Key.Parts)
	}
	if source[outer.P:outer.E] != "for group in groups key group.id {\n\t\th2 \"{group.title}\"\n\t\tfor card in group.cards key card.id {\n\t\t\tp \"{card.title}\"\n\t\t}\n\t}" {
		t.Errorf("whole range = %q", source[outer.P:outer.E])
	}
	if len(outer.Body) != 2 {
		t.Fatalf("outer body = %d nodes", len(outer.Body))
	}
	inner := mustRepetition(t, outer.Body[1])
	if inner.Item != "card" || pathSpelling(inner.Collection) != "group.cards" || pathSpelling(inner.Key) != "card.id" {
		t.Errorf("inner header = %q in %q key %q", inner.Item, pathSpelling(inner.Collection), pathSpelling(inner.Key))
	}
}

func TestParseRepetition_GIVENScalarItemKeyAndEmptyBody_WHENParsed_THENKeyIsTheItem(t *testing.T) {
	source := `Tags [tags: [string]] { ul { for tag in tags key tag {} } }`
	parser := New([]byte(source))
	document := parser.ParseDocument()
	if err := parser.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	list := document.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ElementNode)
	repetition := mustRepetition(t, list.Children[0])
	if pathSpelling(repetition.Key) != "tag" || len(repetition.Body) != 0 {
		t.Errorf("repetition = key %q body %d", pathSpelling(repetition.Key), len(repetition.Body))
	}
}

func TestParseRepetition_GIVENChildlessElementBeforeLoop_WHENParsed_THENLoopIsASibling(t *testing.T) {
	source := "Items [items: [string]] {\n\tli class=\"head\"\n\tfor item in items key item {\n\t\tli \"{item}\"\n\t}\n\tul for other in items key other {}\n}\n"
	parser := New([]byte(source))
	document := parser.ParseDocument()
	if err := parser.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	body := document.Nodes[0].(*ast.ElementDecl).Body
	if len(body) != 4 {
		t.Fatalf("body = %d nodes, want li, for, ul, for", len(body))
	}
	head := body[0].(*ast.ElementNode)
	if len(head.Attributes) != 1 || len(head.Children) != 0 {
		t.Errorf("li swallowed the loop: attributes %d children %d", len(head.Attributes), len(head.Children))
	}
	mustRepetition(t, body[1])
	if list := body[2].(*ast.ElementNode); len(list.Children) != 0 {
		t.Errorf("ul took the loop as an unbraced child")
	}
	mustRepetition(t, body[3])
}

func TestParseRepetition_GIVENForAsAttributeOrElement_WHENNotALoopHeader_THENStaysAnIdentifier(t *testing.T) {
	source := `Field [] { label for="email" "Email" for {} }`
	parser := New([]byte(source))
	document := parser.ParseDocument()
	if err := parser.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	body := document.Nodes[0].(*ast.ElementDecl).Body
	label := body[0].(*ast.ElementNode)
	if label.Attributes[0].Name != "for" || label.Attributes[0].Value != `"email"` {
		t.Errorf("label attribute = %#v", label.Attributes[0])
	}
	if element, ok := body[1].(*ast.ElementNode); !ok || element.Name != "for" {
		t.Errorf("for {} = %#v, want an element", body[1])
	}
}

func TestParseRepetition_GIVENMalformedHeaders_WHENParsed_THENReportsSyntaxErrors(t *testing.T) {
	tests := []struct {
		name   string
		source string
		start  int
	}{
		{name: "missing key", source: `C [] { for x in xs { } }`, start: 19},
		{name: "missing key path", source: `C [] { for x in xs key { } }`, start: 23},
		{name: "spaced path separator", source: `C [] { for x in a . b key x { } }`, start: 18},
		{name: "trailing separator", source: `C [] { for x in a. key x { } }`, start: 19},
		{name: "missing body", source: `C [] { for x in xs key x }`, start: 25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := New([]byte(test.source))
			parser.ParseDocument()
			errors := parser.Errors()
			if len(errors) == 0 {
				t.Fatalf("ParseDocument() accepted %q", test.source)
			}
			if errors[0].Pos != test.start {
				t.Errorf("first error at %d (%s), want %d", errors[0].Pos, errors[0].Msg, test.start)
			}
		})
	}
}

func TestRepetitionNode_GIVENNestedBodies_WHENJSONRoundTripped_THENKeepsRepetitions(t *testing.T) {
	source := "C [xs: [string]] {\n\tdiv {\n\t\tfor x in xs key x {\n\t\t\tfor y in xs key y { \"{y}\" }\n\t\t}\n\t}\n}\n"
	parser := New([]byte(source))
	document := parser.ParseDocument()
	if err := parser.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded ast.Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	div := decoded.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ElementNode)
	outer := mustRepetition(t, div.Children[0])
	inner := mustRepetition(t, outer.Body[0])
	if inner.Item != "y" || pathSpelling(inner.Key) != "y" || inner.KeyKeywordRange != outer.Body[0].(*ast.RepetitionNode).KeyKeywordRange {
		t.Errorf("inner = %#v", inner)
	}
	if _, ok := inner.Body[0].(*ast.TextNode); !ok {
		t.Errorf("inner body = %#v", inner.Body)
	}
}
