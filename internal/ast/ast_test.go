package ast

import (
	"encoding/json"
	"testing"
)

func TestTextNode(t *testing.T) {
	node := &TextNode{P: 1, E: 5, Value: "test"}
	if node.Pos() != 1 {
		t.Errorf("Pos() = %v, want 1", node.Pos())
	}
	if node.End() != 5 {
		t.Errorf("End() = %v, want 5", node.End())
	}
}

func TestElementNode(t *testing.T) {
	node := &ElementNode{P: 10, E: 20, Name: "div"}
	if node.Pos() != 10 {
		t.Errorf("Pos() = %v, want 10", node.Pos())
	}
	if node.End() != 20 {
		t.Errorf("End() = %v, want 20", node.End())
	}
}

func TestAttributeNode(t *testing.T) {
	node := &AttributeNode{P: 2, E: 8, Name: "class", Value: "btn"}
	if node.Pos() != 2 {
		t.Errorf("Pos() = %v, want 2", node.Pos())
	}
	if node.End() != 8 {
		t.Errorf("End() = %v, want 8", node.End())
	}
}

func TestElementDecl(t *testing.T) {
	node := &ElementDecl{P: 5, E: 15, Name: "MyElement"}
	if node.Pos() != 5 {
		t.Errorf("Pos() = %v, want 5", node.Pos())
	}
	if node.End() != 15 {
		t.Errorf("End() = %v, want 15", node.End())
	}
}

func TestAttributeDecl(t *testing.T) {
	node := &AttributeDecl{P: 3, E: 9, Name: "disabled", TypeDecl: "boolean"}
	if node.Pos() != 3 {
		t.Errorf("Pos() = %v, want 3", node.Pos())
	}
	if node.End() != 9 {
		t.Errorf("End() = %v, want 9", node.End())
	}
}

func TestImportStmt(t *testing.T) {
	node := &ImportStmt{P: 0, E: 10, Items: []string{"A", "B"}, Path: "pkg"}
	if node.Pos() != 0 {
		t.Errorf("Pos() = %v, want 0", node.Pos())
	}
	if node.End() != 10 {
		t.Errorf("End() = %v, want 10", node.End())
	}
}

func TestDocument(t *testing.T) {
	node := &Document{P: 0, E: 100}
	if node.Pos() != 0 {
		t.Errorf("Pos() = %v, want 0", node.Pos())
	}
	if node.End() != 100 {
		t.Errorf("End() = %v, want 100", node.End())
	}
}

func TestDocumentJSON_GIVENStatesAndClauses_WHENRoundTripped_THENKeepsNodes(t *testing.T) {
	doc := &Document{Nodes: []Node{
		&StateDecl{Name: "S", Variants: []*VariantDecl{{Name: "A"}, {Name: "B", Payload: true, Fields: []*FieldDecl{{Name: "x", TypeDecl: "string"}}}}},
		&ElementDecl{Name: "C", Bodyless: true},
		&RenderClause{Name: "C", Selector: "s", Pattern: Pattern{Qualifier: "S", Name: "B"}, Bindings: []PayloadBinding{{Field: "x", Alias: "y"}}, Body: []Node{&TextNode{Value: `"hi"`}}},
	}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Document
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v\n%s", err, data)
	}
	state, ok := decoded.Nodes[0].(*StateDecl)
	if !ok || len(state.Variants) != 2 || !state.Variants[1].Payload || state.Variants[1].Fields[0].Name != "x" {
		t.Errorf("state = %#v", decoded.Nodes[0])
	}
	if signature, ok := decoded.Nodes[1].(*ElementDecl); !ok || !signature.IsSignature() {
		t.Errorf("signature = %#v", decoded.Nodes[1])
	}
	clause, ok := decoded.Nodes[2].(*RenderClause)
	if !ok || clause.Pattern.String() != "S.B" || clause.Bindings[0].LocalName() != "y" || len(clause.Body) != 1 {
		t.Errorf("clause = %#v", decoded.Nodes[2])
	}
}
