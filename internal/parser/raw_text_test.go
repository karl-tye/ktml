package parser

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestParse_GIVENRawTextLiterals_WHENParsed_THENKeepsTheTextBetweenTheFencesByteForByte(t *testing.T) {
	source := "Page [] {\n\tscript ```\n\tif (a && b) { `${x}` }\n\t```\n\tstyle {\n\t\t```a{}``` ```b{}```\n\t}\n}\n"
	p := New([]byte(source))
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	body := document.Nodes[0].(*ast.ElementDecl).Body
	script := body[0].(*ast.ElementNode)
	raw := script.Children[0].(*ast.RawTextNode)
	if raw.Text != "\n\tif (a && b) { `${x}` }\n\t" || source[raw.P:raw.E] != "```"+raw.Text+"```" || script.E != raw.E {
		t.Fatalf("script raw text = %#v (element end %d)", raw, script.E)
	}
	style := body[1].(*ast.ElementNode)
	if len(style.Children) != 2 || style.Children[1].(*ast.RawTextNode).Text != "b{}" {
		t.Fatalf("style children = %#v", style.Children)
	}
}

func TestParse_GIVENUnterminatedRawText_WHENParsed_THENReportsTheOpeningFence(t *testing.T) {
	source := "Page [] {\n\tscript ```x\n}\n"
	p := New([]byte(source))
	p.ParseDocument()
	errors := p.Errors()
	if len(errors) == 0 || errors[0].Code != "unterminated-raw-text" || source[errors[0].Pos:errors[0].End] != "```" {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestParse_GIVENRawTextOnTheNextLine_WHENParsed_THENItIsASiblingNode(t *testing.T) {
	p := New([]byte("Page [] {\n\tscript\n\t```x```\n}\n"))
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	body := document.Nodes[0].(*ast.ElementDecl).Body
	if len(body) != 2 || len(body[0].(*ast.ElementNode).Children) != 0 {
		t.Fatalf("body = %#v", body)
	}
	if _, ok := body[1].(*ast.RawTextNode); !ok {
		t.Fatalf("second node = %T", body[1])
	}
}
