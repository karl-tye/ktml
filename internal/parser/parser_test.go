package parser

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestParseImportStmt(t *testing.T) {
	input := `import { Button } from "@components"`
	p := New([]byte(input))
	doc := p.ParseDocument()

	if len(doc.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(doc.Nodes))
	}

	importStmt, ok := doc.Nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", doc.Nodes[0])
	}

	if len(importStmt.Items) != 1 || importStmt.Items[0] != "Button" {
		t.Errorf("expected Items to be ['Button'], got %v", importStmt.Items)
	}

	if importStmt.Path != "\"@components\"" {
		t.Errorf("expected Path to be '\"@components\"', got %s", importStmt.Path)
	}
}

func TestParseElementDecl(t *testing.T) {
	input := `main {
		div
	}`
	p := New([]byte(input))
	doc := p.ParseDocument()

	if len(doc.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(doc.Nodes))
	}

	elDecl, ok := doc.Nodes[0].(*ast.ElementDecl)
	if !ok {
		t.Fatalf("expected *ast.ElementDecl, got %T", doc.Nodes[0])
	}

	if elDecl.Name != "main" {
		t.Errorf("expected Name to be 'main', got %s", elDecl.Name)
	}

	if len(elDecl.Body) != 1 {
		t.Fatalf("expected 1 body node, got %d", len(elDecl.Body))
	}
}

func TestParseElementDeclWithAttributes(t *testing.T) {
	input := `Button[disabled: boolean] {
		"Click"
	}`
	p := New([]byte(input))
	doc := p.ParseDocument()

	if len(doc.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(doc.Nodes))
	}

	elDecl, ok := doc.Nodes[0].(*ast.ElementDecl)
	if !ok {
		t.Fatalf("expected *ast.ElementDecl, got %T", doc.Nodes[0])
	}

	if len(elDecl.Attributes) != 1 {
		t.Fatalf("expected 1 attribute, got %d", len(elDecl.Attributes))
	}

	attr := elDecl.Attributes[0]
	if attr.Name != "disabled" || attr.TypeDecl != "boolean" {
		t.Errorf("expected attribute 'disabled: boolean', got '%s: %s'", attr.Name, attr.TypeDecl)
	}
}

func TestParseNestedElements(t *testing.T) {
	input := `main {
		div#container {
			"Hello"
		}
	}`
	p := New([]byte(input))

	// This will currently panic, which is a failing test case for error handling and nested blocks
	doc := p.ParseDocument()

	if len(doc.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(doc.Nodes))
	}
}

func TestParseElementAttributes(t *testing.T) {
	input := `main {
		input id="name" disabled=true
	}`
	p := New([]byte(input))
	doc := p.ParseDocument()

	if len(doc.Nodes) != 1 {
		t.Fatalf("expected 1 node")
	}
}

func TestParseCommaImports(t *testing.T) {
	input := `import { Button, Icon } from "@components"`
	p := New([]byte(input))
	doc := p.ParseDocument()

	if len(doc.Nodes) != 1 {
		t.Fatalf("expected 1 node")
	}
}

func TestParseDocument_GIVENMissingClosingBrace_THENReportsError(t *testing.T) {
	p := New([]byte("div {"))
	p.ParseDocument()

	if err := p.Err(); err == nil {
		t.Fatal("Err() = nil, want missing closing brace error")
	}
}
