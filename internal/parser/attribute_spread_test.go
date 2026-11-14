package parser

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestParse_GIVENSpreadsAndBags_WHENParsed_THENKeepsPathsEntriesAndRanges(t *testing.T) {
	element, source := parseFirstElement(t, `input type="text" ...attrs class="x"`)
	if len(element.Attributes) != 3 || element.Attributes[1].Spread == nil {
		t.Fatalf("attributes = %#v", element.Attributes)
	}
	spread := element.Attributes[1]
	if spread.Spread.Path.String() != "attrs" || source[spread.P:spread.E] != "...attrs" || source[spread.Spread.OperatorRange.Start:spread.Spread.OperatorRange.End] != "..." {
		t.Fatalf("spread = %#v", spread)
	}
	element, _ = parseFirstElement(t, "input (\n\t\t...view.attrs\n\t\tif on { ...more }\n\t)")
	if element.Attributes[0].Spread.Path.String() != "view.attrs" || element.Attributes[1].Condition.Then[0].Spread == nil {
		t.Fatalf("grouped spreads = %#v", element.Attributes)
	}
	element, source = parseFirstElement(t, "Field label=\"x\" attrs=(required hx-post=\"/x\"\n\t\tif on { disabled } ...more) \"y\"")
	bag := element.Attributes[1]
	if bag.Name != "attrs" || bag.BagRange == nil || len(bag.Bag) != 4 || bag.Bag[2].Condition == nil || bag.Bag[3].Spread == nil {
		t.Fatalf("bag = %#v", bag)
	}
	if got := source[bag.BagRange.Start:bag.BagRange.End]; got[0] != '(' || got[len(got)-1] != ')' || source[bag.P:bag.E] != "attrs="+got {
		t.Fatalf("bag ranges = %q / %q", got, source[bag.P:bag.E])
	}
	if len(element.Children) != 1 {
		t.Fatalf("children = %#v", element.Children)
	}
	element, _ = parseFirstElement(t, `Field attrs=()`)
	if element.Attributes[0].BagRange == nil || len(element.Attributes[0].Bag) != 0 {
		t.Fatalf("empty bag = %#v", element.Attributes[0])
	}
	if named := ast.NamedAttributes(element.Attributes); len(named) != 1 {
		t.Fatalf("NamedAttributes = %d", len(named))
	}
}

func TestParse_GIVENInvalidSpreads_WHENParsed_THENReportsCodes(t *testing.T) {
	tests := []struct{ body, code string }{
		{body: `input ... attrs`, code: "invalid-spread"},
		{body: `input (...)`, code: "invalid-spread"},
		{body: `input type="x"...attrs`, code: "colon-chain"},
		{body: `Field attrs=(required`, code: "syntax"},
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
