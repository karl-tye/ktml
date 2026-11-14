package formatter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

func TestFormatter(t *testing.T) {
	tests := []struct {
		name     string
		doc      *ast.Document
		expected string
	}{
		{
			name: "Top-level Spacing",
			doc: &ast.Document{
				Nodes: []ast.Node{
					&ast.ElementDecl{Name: "First"},
					&ast.ElementDecl{Name: "Second"},
				},
			},
			expected: "First {\n\n}\n\nSecond {\n\n}\n",
		},
		{
			name: "Indentation",
			doc: &ast.Document{
				Nodes: []ast.Node{
					&ast.ElementDecl{
						Name: "Container",
						Body: []ast.Node{
							&ast.ElementDecl{
								Name: "Child",
							},
						},
					},
				},
			},
			expected: "Container {\n\tChild {\n\n\t}\n}\n",
		},
		{
			name: "Single-line Attributes",
			doc: &ast.Document{
				Nodes: []ast.Node{
					&ast.ElementDecl{
						Name: "Button",
						Attributes: []*ast.AttributeDecl{
							{Name: "title", TypeDecl: "string"},
							{Name: "active", TypeDecl: "bool"},
						},
					},
				},
			},
			expected: "Button [title: string, active: bool] {\n\n}\n",
		},
		{
			name: "Multi-line Attribute Alignment - > 2 attrs",
			doc: &ast.Document{
				Nodes: []ast.Node{
					&ast.ElementDecl{
						Name: "Button",
						Attributes: []*ast.AttributeDecl{
							{Name: "a", TypeDecl: "int"},
							{Name: "longName", TypeDecl: "string"},
							{Name: "c", TypeDecl: "bool"},
						},
					},
				},
			},
			expected: "Button [\n\ta:        int,\n\tlongName: string,\n\tc:        bool,\n] {\n\n}\n",
		},
		{
			name: "Multi-line Attribute Alignment - > 60 chars",
			doc: &ast.Document{
				Nodes: []ast.Node{
					&ast.ElementDecl{
						Name: "Component",
						Attributes: []*ast.AttributeDecl{
							{Name: "thisIsAVeryLongAttributeNameThatExceedsSixtyCharactersInLength", TypeDecl: "string"},
						},
					},
				},
			},
			expected: "Component [\n\tthisIsAVeryLongAttributeNameThatExceedsSixtyCharactersInLength: string,\n] {\n\n}\n",
		},
		{
			name: "Shorthand Syntax",
			doc: &ast.Document{
				Nodes: []ast.Node{
					&ast.ElementNode{
						Name: "div",
						Attributes: []*ast.AttributeNode{
							{Name: "#my-id"},
							{Name: ".my-class"},
							{Name: "data-attr", Value: "\"1\""},
						},
					},
				},
			},
			expected: "div#my-id.my-class data-attr=\"1\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.doc)
			if got != tt.expected {
				t.Errorf("Format() = %q, want %q", got, tt.expected)
				t.Logf("Got:\n%s\nExpected:\n%s", got, tt.expected)
			}
		})
	}
}

func formatString(input string) string {
	doc := parser.New([]byte(input)).ParseDocument()
	return Format(doc)
}

func TestFormatter_BDD(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "GIVEN an empty input string WHEN the text formatting engine processes the input THEN an exact empty string is returned",
			input:    "",
			expected: "",
		},
		{
			name:     "GIVEN an input string with inconsistent, missing, or messy indentation WHEN the text formatting engine processes the input THEN the returned string has uniform and standardized indentation throughout",
			input:    "div {\n  span {\n\t\"hello\"\n}\n}",
			expected: "div {\n\tspan {\n\t\t\"hello\"\n\t}\n}\n",
		},
		{
			name:     "GIVEN an input string containing deeply nested hierarchical elements WHEN the text formatting engine processes the input THEN the returned string accurately reflects and maintains the proper indentation for every nesting level",
			input:    "a{b{c{d{\"deep\"}}}}",
			expected: "a {\n\tb {\n\t\tc {\n\t\t\td {\n\t\t\t\t\"deep\"\n\t\t\t}\n\t\t}\n\t}\n}\n",
		},
		{
			name:     "GIVEN an input string with missing spaces or tightly packed element boundaries WHEN the text formatting engine processes the input THEN the returned string contains the correct standardized spacing and structural line breaks between adjacent elements",
			input:    "div{}span{}",
			expected: "div {\n\n}\n\nspan {\n\n}\n",
		},
		{
			name:     "GIVEN an element with attributes on a single line WHEN the element is parsed and formatted THEN the attributes remain on a single line in their original format",
			input:    "Component {\n\tdiv class=\"hello\" id=\"world\" {}\n}",
			expected: "Component {\n\tdiv class=\"hello\" id=\"world\"\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatString(tt.input)
			if got != tt.expected {
				t.Errorf("formatString() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatFiles(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.ktml")
	f2 := filepath.Join(dir, "f2.ktml")

	os.WriteFile(f1, []byte("div{}"), 0644)
	os.WriteFile(f2, []byte("span{}"), 0644)

	err := FormatFiles(filepath.Join(dir, "*.ktml"))
	if err != nil {
		t.Fatalf("FormatFiles() error = %v", err)
	}

	b1, _ := os.ReadFile(f1)
	if string(b1) != "div {\n\n}\n" {
		t.Errorf("f1 not formatted: %q", string(b1))
	}

	b2, _ := os.ReadFile(f2)
	if string(b2) != "span {\n\n}\n" {
		t.Errorf("f2 not formatted: %q", string(b2))
	}
}
