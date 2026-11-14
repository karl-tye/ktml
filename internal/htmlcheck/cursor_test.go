package htmlcheck_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/htmlcheck"
)

func TestCursorAt_GIVENMarkupPositions_WHENClassified_THENReturnsKindElementAndPrefix(t *testing.T) {
	tests := []struct {
		name, source string
		want         htmlcheck.Cursor
	}{
		{"element at a line start", "Page [] {\n\tdi|\n}", htmlcheck.Cursor{Kind: htmlcheck.ElementName, Prefix: "di"}},
		{"element after a brace", "Page [] { div { sp| } }", htmlcheck.Cursor{Kind: htmlcheck.ElementName, Prefix: "sp"}},
		{"attribute after the name", "Page [] {\n\tdiv cl|\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "div", Prefix: "cl"}},
		{"attribute after a value", "Page [] {\n\tinput type=\"text\" |\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "input"}},
		{"attribute in a group", "Page [] {\n\tbutton (\n\t\ttype=\"button\"\n\t\taria-|\n\t)\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "button", Prefix: "aria-"}},
		{"attribute in a conditional group", "Page [on: boolean] {\n\tdiv (if on { hid| })\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "div", Prefix: "hid"}},
		{"attribute value", "Page [] {\n\tinput type=\"ch|\"\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeValue, Element: "input", Attribute: "type", Prefix: "ch"}},
		{"alpine value", "Page [] {\n\tdiv x-data=`{ open: false }` @click=\"op|\"\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeValue, Element: "div", Attribute: "@click", Prefix: "op"}},
		{"multi-line backtick value", "Page [] {\n\tdiv x-data=`{\n\t\topen: fa|\n\t}`\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeValue, Element: "div", Attribute: "x-data", Prefix: "{\n\t\topen: fa"}},
		{"text child", "Page [] {\n\tp \"hel|\"\n}", htmlcheck.Cursor{}},
		{"declaration", "Page [na|] {\n}", htmlcheck.Cursor{}},
		{"top level", "Pa|", htmlcheck.Cursor{}},
		{"comment", "Page [] {\n\t// div cl|\n}", htmlcheck.Cursor{}},
		{"after a closed body", "Page [] {\n\tdiv { p \"x\" }\n\tse|\n}", htmlcheck.Cursor{Kind: htmlcheck.ElementName, Prefix: "se"}},
		{"after a closed group", "Page [] {\n\tdiv (\n\t\tid=\"x\"\n\t) cl|\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "div", Prefix: "cl"}},
		{"after an import", "import { Card } from \"./card.ktml\"\n\nPage [] {\n\tdiv cl|\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "div", Prefix: "cl"}},
		{"in an import", "import { Ca| } from \"./card.ktml\"", htmlcheck.Cursor{}},
		{"inside an if body", "Page [on: boolean] {\n\tif on {\n\t\tbu|\n\t}\n}", htmlcheck.Cursor{Kind: htmlcheck.ElementName, Prefix: "bu"}},
		{"first attribute in a bag", "Page [] {\n\tField attrs=(req|)\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Prefix: "req", Bag: true}},
		{"attribute after a value in a bag", "Page [] {\n\tField attrs=(title=\"x\" aria-|)\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Prefix: "aria-", Bag: true}},
		{"value in a bag", "Page [] {\n\tField attrs=(dir=\"r|\")\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeValue, Attribute: "dir", Prefix: "r", Bag: true}},
		{"conditional group in a bag", "Page [on: boolean] {\n\tField attrs=(if on { dis| })\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Prefix: "dis", Bag: true}},
		{"after a bag", "Page [] {\n\tField attrs=(id=\"a\") lab|\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Element: "Field", Prefix: "lab"}},
		{"bag in a group", "Page [] {\n\tdiv (\n\t\tid=\"x\"\n\t)\n\tField (attrs=(hid|) label=\"y\")\n}", htmlcheck.Cursor{Kind: htmlcheck.AttributeName, Prefix: "hid", Bag: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "|")
			source := []byte(strings.Replace(test.source, "|", "", 1))
			got := htmlcheck.CursorAt(source, offset)
			if got.Kind != test.want.Kind || got.Element != test.want.Element || got.Attribute != test.want.Attribute || got.Prefix != test.want.Prefix || got.Bag != test.want.Bag {
				t.Errorf("CursorAt = %+v, want %+v", got, test.want)
			}
			if got.Kind != htmlcheck.NoMarkup && got.PrefixStart != offset-len(got.Prefix) {
				t.Errorf("PrefixStart = %d, want %d", got.PrefixStart, offset-len(got.Prefix))
			}
		})
	}
}

func TestCursorAt_GIVENCallPositions_WHENClassified_THENNamesTheCallItsBagAndItsSlot(t *testing.T) {
	tests := []struct {
		name, source                             string
		body, bagElement, bagAttribute, supplied string
		slot                                     bool
	}{
		{"argument after another", "Page [] {\n\tCard title=\"a\" no|\n}", "", "", "", "title", false},
		{"bag value", "Page [] {\n\tField label=\"x\" attrs=(dir=\"r|\")\n}", "", "Field", "attrs", "dir", false},
		{"after a bag", "Page [] {\n\tField attrs=(id=\"a\") |\n}", "", "", "", "attrs", false},
		{"slot name", "Page [] {\n\tModal title=\"x\" {\n\t\tslot |\n\t}\n}", "Modal", "", "", "", true},
		{"call body", "Page [] {\n\tModal {\n\t\tp \"x\"\n\t\tbu|\n\t}\n}", "Modal", "", "", "", false},
		{"slot body", "Page [] {\n\tModal {\n\t\tslot footer {\n\t\t\tbu|\n\t\t}\n\t}\n}", "", "", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "|")
			got := htmlcheck.CursorAt([]byte(strings.Replace(test.source, "|", "", 1)), offset)
			if got.Body != test.body || got.BagElement != test.bagElement || got.BagAttribute != test.bagAttribute || strings.Join(got.Supplied, ",") != test.supplied || got.Slot != test.slot {
				t.Errorf("CursorAt = %+v", got)
			}
		})
	}
}

func TestComplete_GIVENMarkupPositions_WHENCompleted_THENOffersHTMLNamesAndValues(t *testing.T) {
	component := func(name string) bool { return name == "Card" }
	tests := []struct {
		name, source string
		want, absent []string
	}{
		{"elements", "Page [] {\n\tbu|\n}", []string{"button"}, []string{"div"}},
		{"no elements for a component prefix", "Page [] {\n\tCa|\n}", nil, []string{"canvas"}},
		{"attributes of input", "Page [] {\n\tinput pla|\n}", []string{"placeholder"}, []string{"class"}},
		{"aria attributes", "Page [] {\n\tdiv aria-ex|\n}", []string{"aria-expanded"}, nil},
		{"event handlers", "Page [] {\n\tdiv oncl|\n}", []string{"onclick"}, nil},
		{"element-specific attributes only", "Page [] {\n\tdiv hre|\n}", nil, []string{"href"}},
		{"input types", "Page [] {\n\tinput type=\"ch|\"\n}", []string{"checkbox"}, []string{"text"}},
		{"aria values", "Page [] {\n\tdiv aria-live=\"|\"\n}", []string{"polite", "assertive", "off"}, nil},
		{"roles", "Page [] {\n\tdiv role=\"dia|\"\n}", []string{"dialog"}, nil},
		{"no attributes of components", "Page [] {\n\tCard ti|\n}", nil, []string{"title"}},
		{"no framework attributes", "Page [] {\n\tdiv x-|\n}", nil, []string{"x-data"}},
		{"global attributes in a bag", "Page [] {\n\tCard attrs=(hid|)\n}", []string{"hidden"}, nil},
		{"aria attributes in a bag", "Page [] {\n\tCard attrs=(aria-la|)\n}", []string{"aria-label"}, nil},
		{"global values in a bag", "Page [] {\n\tCard attrs=(dir=\"|\")\n}", []string{"rtl", "ltr"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "|")
			source := []byte(strings.Replace(test.source, "|", "", 1))
			labels := make(map[string]bool)
			for _, item := range htmlcheck.Complete(source, offset, component) {
				labels[item.Label] = true
				if item.End != offset || item.Start > offset {
					t.Errorf("item %s range", item.Label)
				}
			}
			for _, label := range test.want {
				if !labels[label] {
					t.Errorf("missing %s in %v", label, labels)
				}
			}
			for _, label := range test.absent {
				if labels[label] {
					t.Errorf("unexpected %s", label)
				}
			}
		})
	}
}

func TestHover_GIVENHTMLNames_WHENHovered_THENDocumentsThem(t *testing.T) {
	source := "Card [] { p }\nPage [] {\n\tbutton (type=\"button\" aria-expanded=\"false\") \"x\"\n\tCard\n}\n"
	tests := []struct{ marker, want string }{
		{"button (", "A button."},
		{"type=", "The type of the control"},
		{"aria-expanded", "Whether the controlled grouping is expanded."},
	}
	snapshot := loadSnapshot(t, source)
	for _, test := range tests {
		offset := strings.Index(source, test.marker) + 1
		value, start, end, ok := htmlcheck.Hover(snapshot.snapshot, snapshot.path, offset)
		if !ok || !strings.Contains(value, test.want) || start > offset || end < offset {
			t.Errorf("Hover(%s) = %q %d-%d %v", test.marker, value, start, end, ok)
		}
	}
	if _, _, _, ok := htmlcheck.Hover(snapshot.snapshot, snapshot.path, strings.Index(source, "\tCard")+2); ok {
		t.Error("hover on a component call")
	}
}
