package parser

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func parseFirstElement(t *testing.T, body string) (*ast.ElementNode, string) {
	t.Helper()
	source := "Page [] {\n\t" + body + "\n}\n"
	p := New([]byte(source))
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	return document.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ElementNode), source
}

func TestParse_GIVENConditionalAttributeGroups_WHENParsed_THENKeepsBranchesInSourceOrder(t *testing.T) {
	element, source := parseFirstElement(t, `a (
		href="/x"
		if current { aria-current="page" #now } else if note? { title="{note}" } else { data-off }
		if:x="1"
		if="2"
	) "x"`)
	if len(element.Attributes) != 4 {
		t.Fatalf("attributes = %d, want 4", len(element.Attributes))
	}
	entry := element.Attributes[1]
	condition := entry.Condition
	if entry.Name != "" || condition == nil || condition.Condition.String() != "current" || !condition.ElseIf {
		t.Fatalf("conditional entry = %#v", entry)
	}
	if got := source[entry.P:entry.E]; !strings.HasPrefix(got, "if current {") || !strings.HasSuffix(got, "{ data-off }") {
		t.Fatalf("entry range = %q", got)
	}
	if len(condition.Then) != 2 || condition.Then[0].Name != "aria-current" || condition.Then[1].Name != "id" {
		t.Fatalf("then = %#v", condition.Then)
	}
	nested := condition.Else[0].Condition
	if nested == nil || nested.Condition.String() != "note?" || nested.Then[0].Name != "title" || nested.Else[0].Name != "data-off" {
		t.Fatalf("else if = %#v", condition.Else[0])
	}
	if source[condition.KeywordRange.Start:condition.KeywordRange.End] != "if" || source[condition.ElseKeywordRange.Start:condition.ElseKeywordRange.End] != "else" {
		t.Fatalf("keyword ranges = %#v %#v", condition.KeywordRange, condition.ElseKeywordRange)
	}
	if element.Attributes[2].Name != "if:x" || element.Attributes[3].Name != "if" || element.Attributes[3].Value != `"2"` {
		t.Fatalf("attributes named if = %#v %#v", element.Attributes[2], element.Attributes[3])
	}
	named := ast.NamedAttributes(element.Attributes)
	var names []string
	for _, attribute := range named {
		names = append(names, attribute.Name)
	}
	if got := strings.Join(names, ","); got != "href,aria-current,id,title,data-off,if:x,if" {
		t.Fatalf("NamedAttributes = %s", got)
	}
}

func TestParse_GIVENInlineConditionalGroup_WHENParsed_THENTheGroupMayStayOnOneLine(t *testing.T) {
	element, _ := parseFirstElement(t, `option (value="6" if six { selected }) "6 months"`)
	if len(element.Attributes) != 2 || element.Attributes[1].Condition == nil || len(element.Children) != 1 {
		t.Fatalf("element = %#v", element)
	}
}

func TestParse_GIVENInvalidConditionalAttributes_WHENParsed_THENReportsCodes(t *testing.T) {
	tests := []struct{ body, code string }{
		{body: `a (if { title="x" })`, code: "condition-expected"},
		{body: `a (if a == b { title="x" })`, code: "condition-comparison"},
		{body: `a (if a title="x")`, code: "condition-unexpected"},
		{body: `a (if a { title="x" } else title="y")`, code: "syntax"},
	}
	for _, test := range tests {
		t.Run(test.body, func(t *testing.T) {
			p := New([]byte("Page [] {\n\t" + test.body + "\n}\n"))
			p.ParseDocument()
			errors := p.Errors()
			if len(errors) == 0 || errors[0].Code != test.code {
				t.Fatalf("errors = %#v, want %s", errors, test.code)
			}
		})
	}
}
