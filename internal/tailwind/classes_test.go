package tailwind

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/textposition"
)

// at returns the byte range of the occurrence-th (from 0) match of text in
// source.
func at(t *testing.T, source, text string, occurrence int) (int, int) {
	t.Helper()
	offset := 0
	for i := 0; ; i++ {
		index := strings.Index(source[offset:], text)
		if index < 0 {
			t.Fatalf("%q occurrence %d not found", text, occurrence)
		}
		if i == occurrence {
			return offset + index, offset + index + len(text)
		}
		offset += index + len(text)
	}
}

func TestExtract_GIVENClassAttributes_THENReturnsEveryStaticAndSupportedConditionalToken(t *testing.T) {
	source := "import { Field } from \"./ui\"\n\n" +
		"Card [on: boolean, open: boolean, items: [Item]] {\n" +
		"\tdiv class=\"flex  p-4\" {\n" +
		"\t\tdiv (class=\"a\" if on { class=\"b\" } else { class=\"c\" }) \"x\"\n" +
		"\t\tField attrs=(class=\"q\" if on { class=\"r\" }) \"y\"\n" +
		"\t\tbutton :class=\"open && 'rotate-180 x'\" x-bind:class=\"open ? `p` : 'q2'\" \"z\"\n" +
		"\t\tif on {\n\t\t\tspan class=\"in-if\"\n\t\t}\n" +
		"\t\tfor item in items key item.id {\n\t\t\tli class=\"in-for\"\n\t\t}\n" +
		"\t\tField {\n\t\t\tslot label {\n\t\t\t\tem class=\"in-slot\"\n\t\t\t}\n\t\t}\n" +
		"\t}\n}\n"
	tests := []struct {
		class string
		// needle locates the token: its first occurrence in the source,
		// with the token at skip bytes into it.
		needle      string
		skip        int
		conditional bool
		bag         bool
		script      bool
	}{
		{class: "flex", needle: "flex"},
		{class: "p-4", needle: "p-4"},
		{class: "a", needle: `"a"`, skip: 1},
		{class: "b", needle: `"b"`, skip: 1, conditional: true},
		{class: "c", needle: `"c"`, skip: 1, conditional: true},
		{class: "q", needle: `"q"`, skip: 1, bag: true},
		{class: "r", needle: `"r"`, skip: 1, bag: true, conditional: true},
		{class: "rotate-180", needle: "rotate-180", script: true},
		{class: "x", needle: " x'", skip: 1, script: true},
		{class: "p", needle: "`p`", skip: 1, script: true},
		{class: "q2", needle: "q2", script: true},
		{class: "in-if", needle: "in-if"},
		{class: "in-for", needle: "in-for"},
		{class: "in-slot", needle: "in-slot"},
	}
	extraction := Extract([]byte(source))
	if len(extraction.Tokens) != len(tests) {
		t.Fatalf("tokens = %+v, want %d", extraction.Tokens, len(tests))
	}
	for index, test := range tests {
		got := extraction.Tokens[index]
		start, _ := at(t, source, test.needle, 0)
		start += test.skip
		end := start + len(test.class)
		if got.Class != test.class || got.Start != start || got.End != end {
			t.Errorf("token %d = %q [%d,%d), want %q [%d,%d)", index, got.Class, got.Start, got.End, test.class, start, end)
		}
		if got.Conditional != test.conditional || got.Bag != test.bag || got.Script != test.script {
			t.Errorf("token %q flags = conditional %v bag %v script %v, want %v %v %v", got.Class, got.Conditional, got.Bag, got.Script, test.conditional, test.bag, test.script)
		}
	}
	if len(extraction.Dynamic) != 0 {
		t.Errorf("dynamic = %+v, want none", extraction.Dynamic)
	}
}

func TestExtract_GIVENInterpolatedClasses_THENReportsWhatCannotBeDiscovered(t *testing.T) {
	source := "Card [class: string, color: string, view: View] {\n" +
		"\tp class=\"mt-2 {class}\" \"a\"\n" +
		"\tp class=\"text-{color}-500 font-bold\" \"b\"\n" +
		"\tp :class=\"{view.classKey}\" \"c\"\n" +
		"\tp :class=\"on && 'bg-{color}'\" \"d\"\n" +
		"}\n"
	extraction := Extract([]byte(source))
	var classes []string
	for _, token := range extraction.Tokens {
		classes = append(classes, token.Class)
	}
	if got, want := strings.Join(classes, " "), "mt-2 font-bold"; got != want {
		t.Errorf("tokens = %q, want %q", got, want)
	}
	tests := []struct {
		text    string
		path    string
		partial bool
	}{
		{text: "{class}", path: "class"},
		{text: "text-{color}-500", path: "color", partial: true},
		{text: "{view.classKey}", path: "view.classKey"},
		{text: "bg-{color}", path: "color", partial: true},
	}
	if len(extraction.Dynamic) != len(tests) {
		t.Fatalf("dynamic = %+v, want %d", extraction.Dynamic, len(tests))
	}
	for index, test := range tests {
		got := extraction.Dynamic[index]
		start, end := at(t, source, test.text, 0)
		if got.Start != start || got.End != end || got.Path != test.path || got.Partial != test.partial {
			t.Errorf("dynamic %d = %+v, want [%d,%d) path %q partial %v", index, got, start, end, test.path, test.partial)
		}
	}
}

func TestExtract_GIVENADocumentThatDoesNotParse_THENKeepsTheClassesTheParserRecovers(t *testing.T) {
	source := "Card [] {\n\tdiv class=\"bad\n\tp class=\"ok\" \"x\"\n}\n"
	extraction := Extract([]byte(source))
	if len(extraction.Tokens) != 1 || extraction.Tokens[0].Class != "ok" {
		t.Fatalf("tokens = %+v, want ok", extraction.Tokens)
	}
}

func TestClasses_GIVENRepeatedTokens_THENReturnsThemSortedAndUnique(t *testing.T) {
	source := "Card [on: boolean] {\n\tp (class=\"b a\" if on { class=\"a c\" }) \"x\"\n}\n"
	if got, want := strings.Join(Extract([]byte(source)).Classes(), " "), "a b c"; got != want {
		t.Errorf("classes = %q, want %q", got, want)
	}
}

func TestVirtualDocument_GIVENKTML_THENKeepsOnlyClassListsAtTheirLSPPositions(t *testing.T) {
	source := "// é ünïcode\nCard [c: string, on: boolean] {\n\th1 \"ç {c}\"\n\tdiv (id=\"x\" class=\"flex {c} p-4\" if on { class=\"b\" } :class=\"on && 'q'\") \"é\"\n}\n"
	virtual := VirtualDocument([]byte(source), Extract([]byte(source)))
	sourceLines, virtualLines := strings.Split(source, "\n"), strings.Split(virtual, "\n")
	if len(sourceLines) != len(virtualLines) {
		t.Fatalf("virtual lines = %d, want %d:\n%s", len(virtualLines), len(sourceLines), virtual)
	}
	tests := []struct{ virtual, source string }{
		{`class="flex     p-4"`, `class="flex {c} p-4"`},
		{`class="b"`, `class="b"`},
		{`:class="on && 'q'"`, `:class="on && 'q'"`},
	}
	for _, test := range tests {
		index := strings.Index(virtual, test.virtual)
		if index < 0 {
			t.Fatalf("virtual document lacks %q:\n%s", test.virtual, virtual)
		}
		line, character := textposition.Position([]byte(virtual), index)
		wantLine, wantCharacter := textposition.Position([]byte(source), strings.Index(source, test.source))
		if line != wantLine || character != wantCharacter {
			t.Errorf("%q at %d:%d, want %d:%d", test.virtual, line, character, wantLine, wantCharacter)
		}
	}
	for _, gone := range []string{"Card", "h1", "id=", "if on", "{c}", "é", "ç"} {
		if strings.Contains(virtual, gone) {
			t.Errorf("virtual document keeps %q:\n%s", gone, virtual)
		}
	}
	// Every line keeps its UTF-16 length, so positions after the kept text
	// map one to one as well.
	for index := range sourceLines {
		if got, want := utf16Length(virtualLines[index]), utf16Length(sourceLines[index]); got != want {
			t.Errorf("line %d has %d UTF-16 units, want %d", index, got, want)
		}
	}
}

func utf16Length(text string) int {
	length := 0
	for _, r := range text {
		if r >= 0x10000 {
			length += 2
		} else {
			length++
		}
	}
	return length
}

func TestExtract_GIVENABacktickQuotedClassBinding_THENTakesItsDoubleQuotedStrings(t *testing.T) {
	source := "Card [] {\n\tp :class=`{ \"font-bold\": on, 'italic': off }` \"x\"\n}\n"
	if got, want := strings.Join(Extract([]byte(source)).Classes(), " "), "font-bold italic"; got != want {
		t.Errorf("classes = %q, want %q", got, want)
	}
}

func TestExtract_GIVENAStoryBody_THENReturnsItsClasses(t *testing.T) {
	source := "import { Card } from \"./card.ktml\"\n\nstory Card.A [title = \"x\"] {\n\tspan class=\"story-body\"\n\tslot footer {\n\t\tem class=\"story-slot\"\n\t}\n}\n"
	classes := strings.Join(Extract([]byte(source)).Classes(), " ")
	if classes != "story-body story-slot" {
		t.Errorf("Classes() = %q", classes)
	}
}
