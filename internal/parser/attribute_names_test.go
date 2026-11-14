package parser

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestParseAttribute_GIVENFrameworkAttributeNames_WHENParsed_THENEachIsOneName(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "alpine event shorthand", body: "\tbutton @click=\"open = true\" @click.outside=\"open = false\"", want: "button(@click @click.outside)"},
		{name: "alpine bind shorthand", body: "\tinput :disabled=\"busy\" :class=\"c\"", want: "input(:disabled :class)"},
		{name: "long forms with modifiers", body: "\tinput x-bind:value=\"v\" x-on:keydown.enter=\"go()\" x-model.number=\"n\"", want: "input(x-bind:value x-on:keydown.enter x-model.number)"},
		{name: "htmx names", body: "\tbody hx-on::after-request=\"x\" hx-on:htmx:before:swap=\"y\" hx-ext:inherited=\"sse\"", want: "body(hx-on::after-request hx-on:htmx:before:swap hx-ext:inherited)"},
		{name: "digits after a dot", body: "\tdiv x-transition.opacity.duration.100ms", want: "div(x-transition.opacity.duration.100ms)"},
		{name: "valueless with modifiers", body: "\tdiv @dragover.prevent @keydown.slash.window=\"f()\"", want: "div(@dragover.prevent @keydown.slash.window)"},
		{name: "shorthand value", body: "\tdiv @click \"go()\"", want: "div(@click)"},
		{name: "in a continuation group", body: "\tdiv (:class=\"c\"\n\t\t@click=\"x\"\n\t\tx-on:click.stop=\"y\"\n\t)", want: "div(:class @click x-on:click.stop)"},
		{name: "group allows adjacency", body: "\tdiv (a=\"x\":b=\"y\")", want: "div(a :b)"},
		{name: "keywords continued by a name are attributes", body: "\tlabel if:x=\"1\" for.y=\"2\"", want: "label(if:x for.y)"},
		{name: "slot attribute with a colon", body: "\tslot x:y=\"z\"", want: "slot(x:y)"},
		{name: "a newline still ends the list", body: "\tdiv @click=\"a\"\n\tspan :class=\"b\"", want: "div(@click), span(:class)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, body := parseBody(t, test.body)
			if got := elementShape(body); got != test.want {
				t.Fatalf("shape = %s, want %s", got, test.want)
			}
		})
	}
}

func TestParseAttribute_GIVENFrameworkAttributeName_WHENParsed_THENRangeAndValueAreExact(t *testing.T) {
	source, body := parseBody(t, "\tbutton x-on:click.outside=\"open = false\"")
	attribute := body[0].(*ast.ElementNode).Attributes[0]
	if got := source[attribute.P:attribute.E]; got != "x-on:click.outside=\"open = false\"" {
		t.Fatalf("attribute range = %q", got)
	}
	if attribute.Value != "\"open = false\"" {
		t.Fatalf("value = %q", attribute.Value)
	}
}

func TestParseAttribute_GIVENMalformedFrameworkNames_WHENParsed_THENReportsCodes(t *testing.T) {
	tests := []struct {
		name, body, code string
	}{
		{name: "colon chain", body: "\ta:href=\"/x\"", code: "colon-chain"},
		{name: "component colon chain", body: "\tProfile:user=\"u\"", code: "colon-chain"},
		{name: "event directly after element", body: "\tbutton@click=\"x\"", code: "colon-chain"},
		{name: "dotted shorthand", body: "\tdiv.dialog", code: "colon-chain"},
		{name: "adjacent after a value", body: "\tdiv a=\"x\":b=\"y\"", code: "colon-chain"},
		{name: "lone at", body: "\tdiv @ x", code: "invalid-attribute-name"},
		{name: "double colon start", body: "\tdiv ::x=\"y\"", code: "invalid-attribute-name"},
		{name: "digit after at", body: "\tdiv @1=\"y\"", code: "invalid-attribute-name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := New([]byte("Panel [] {\n" + test.body + "\n}\n"))
			parser.ParseDocument()
			errors := parser.Errors()
			if len(errors) == 0 || errors[0].Code != test.code {
				t.Fatalf("errors = %#v, want first code %s", errors, test.code)
			}
		})
	}
}

func TestParseMarkup_GIVENDoctypeAndHTMLComments_WHENParsed_THENBuildsNodes(t *testing.T) {
	source, body := parseBody(t, "\t<!-- top -->\n\tdoctype html\n\thtml lang=\"en\" <!-- same line -->\n\tdiv {\n\t\t<!--\n\t\t  multi {not} -- line\n\t\t-->\n\t}")
	if got := elementShape(body); got != "HTMLCommentNode, DoctypeNode, html(lang), HTMLCommentNode, div(){HTMLCommentNode}" {
		t.Fatalf("shape = %s", got)
	}
	comment := body[0].(*ast.HTMLCommentNode)
	if comment.Text != " top " || source[comment.P:comment.E] != "<!-- top -->" {
		t.Fatalf("comment = %#v", comment)
	}
	doctype := body[1].(*ast.DoctypeNode)
	if doctype.Name != "html" || source[doctype.KeywordRange.Start:doctype.KeywordRange.End] != "doctype" || source[doctype.NameRange.Start:doctype.NameRange.End] != "html" || source[doctype.P:doctype.E] != "doctype html" {
		t.Fatalf("doctype = %#v", doctype)
	}
	nested := body[4].(*ast.ElementNode).Children[0].(*ast.HTMLCommentNode)
	if nested.Text != "\n\t\t  multi {not} -- line\n\t\t" {
		t.Fatalf("multi-line comment text = %q", nested.Text)
	}
}

func TestParseMarkup_GIVENInvalidDoctypeOrComment_WHENParsed_THENReportsErrors(t *testing.T) {
	tests := []struct {
		name, source, code string
	}{
		{name: "doctype without a name", source: "Panel [] {\n\tdoctype\n\thtml\n}\n", code: "syntax"},
		{name: "top-level doctype", source: "doctype html\nPanel [] {}\n", code: "syntax"},
		{name: "top-level comment", source: "<!-- x -->\nPanel [] {}\n", code: "syntax"},
		{name: "unterminated comment", source: "Panel [] {\n\t<!-- open\n}\n", code: "unterminated-html-comment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := New([]byte(test.source))
			parser.ParseDocument()
			errors := parser.Errors()
			if len(errors) == 0 || errors[0].Code != test.code {
				t.Fatalf("errors = %#v, want first code %s", errors, test.code)
			}
		})
	}
}

func TestParse_GIVENStarInAttributeNames_WHENParsed_THENItIsPartOfTheName(t *testing.T) {
	p := New([]byte("Page [] {\n\tform hx-target-4*=\"#e\" hx-target-*=\"#a\" (\n\t)\n\tdiv (hx-target-5*=\"#b\" a*b)\n}\n"))
	p.ParseDocument()
	errors := p.Errors()
	if len(errors) != 1 || errors[0].Code != "mixed-attribute-list" {
		t.Fatalf("errors = %#v, want one mixed-attribute-list", errors)
	}
	p = New([]byte("Page [] {\n\tform hx-target-4*=\"#e\" hx-target-*=\"#a\"\n\tdiv (hx-target-5*=\"#b\" a*b)\n}\n"))
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	body := document.Nodes[0].(*ast.ElementDecl).Body
	var names []string
	for _, node := range body {
		for _, attribute := range node.(*ast.ElementNode).Attributes {
			names = append(names, attribute.Name)
		}
	}
	if got := strings.Join(names, ","); got != "hx-target-4*,hx-target-*,hx-target-5*,a*b" {
		t.Fatalf("names = %s", got)
	}
}
