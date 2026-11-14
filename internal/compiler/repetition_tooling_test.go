package compiler_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const repetitionToolingBoard = `import { Card, Column, Board } from "./board.ktmlschema"

Columns [columns: [Column], title: string] {
	for column in columns key column.id {
		h2 "{column.title}"
		for card in column.cards key card.id {
			p title="{card.title}" { "{card.title} {column.title} {title}" }
		}
	}
	for card in columns key card.id { em "{card.title}" }
}
`

func repetitionToolingWorkspace(t *testing.T) (string, *compiler.Snapshot) {
	t.Helper()
	dir := writeWorkspace(t, map[string]string{"board.ktmlschema": boardSchema, "board.ktml": repetitionToolingBoard})
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("workspace is invalid: %v", snapshot.Err())
	}
	return dir, snapshot
}

func TestSchemaCompletions_GIVENKeyedRepetitions_WHENCompleting_THENOffersCollectionKeyAndItemPaths(t *testing.T) {
	dir, snapshot := repetitionToolingWorkspace(t)
	const header = "import { Card, Column, Board } from \"./board.ktmlschema\"\n\nColumns [columns: [Column], title: string] {\n"
	const nested = header + "\tfor column in columns key column.id {\n\t\t"
	const columnFields = "id,title,cards,extra"
	const cardFields = "id,title,rank,score,done,note,tags"
	tests := []struct {
		name    string
		source  string
		context bool
		want    string
		detail  string
		kinds   string
	}{
		{name: "collection roots after in", source: header + "\tul { for column in |", context: true, want: "columns,title", detail: "[Column],string", kinds: "input"},
		{name: "collection root prefix", source: header + "\tfor column in co|", context: true, want: "columns,title"},
		{name: "collection roots include enclosing items", source: nested + "for card in |", context: true, want: "columns,title,column", detail: "[Column],string,Column", kinds: "input,input,item"},
		{name: "collection fields after a period", source: nested + "for card in column.|", context: true, want: columnFields, kinds: "field"},
		{name: "collection field prefix", source: nested + "for card in column.ca| key card.id {}", context: true, want: columnFields},
		{name: "collection of scalars has no fields", source: header + "\tfor column in title.|", context: true, want: ""},
		{name: "item is the only key root", source: header + "\tfor column in columns key |", context: true, want: "column", detail: "Column", kinds: "item"},
		{name: "key root prefix", source: header + "\tfor column in columns key co|", context: true, want: "column"},
		{name: "item fields after key item period", source: header + "\tfor column in columns key column.|", context: true, want: columnFields, kinds: "field"},
		{name: "nested item fields in key", source: nested + "for card in column.cards key card.|", context: true, want: cardFields},
		{name: "item names are body roots", source: nested + "for card in column.cards key card.id {\n\t\t\tp \"{|", context: true, want: "columns,title,column,card", detail: "[Column],string,Column,Card", kinds: "input,input,item,item"},
		{name: "item fields in body", source: nested + "for card in column.cards key card.id {\n\t\t\tp \"{card.|\" }", context: true, want: cardFields},
		{name: "sibling repetition item is out of scope", source: header + "\tfor column in columns key column.id {}\n\tp \"{|", context: true, want: "columns,title"},
		{name: "clause bindings are collection roots", source: "import { Board } from \"./board.ktmlschema\"\n\nView [board: Board]\n\nView [board = Loaded, columns as list] {\n\tfor column in |", context: true, want: "board,list", kinds: "input,binding"},
		{name: "optional collection item has no fields", source: header + "\tfor column in columns key column.id { for card in column.extra key card.| }", context: true, want: ""},
		{name: "item name position", source: header + "\tfor |", context: false},
		{name: "in keyword position", source: header + "\tfor column |", context: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := strings.Index(tt.source, "|")
			source := []byte(strings.Replace(tt.source, "|", "", 1))
			list, ok := compiler.SchemaCompletions(filepath.Join(dir, "board.ktml"), source, offset, snapshot)
			if ok != tt.context {
				t.Fatalf("context = %v, want %v (items %#v)", ok, tt.context, list.Items)
			}
			if !ok {
				return
			}
			var labels, details, kinds []string
			for _, item := range list.Items {
				labels = append(labels, item.Label)
				details = append(details, item.Detail)
				kinds = append(kinds, string(item.Kind))
			}
			if got := strings.Join(labels, ","); got != tt.want {
				t.Errorf("labels = %q, want %q", got, tt.want)
			}
			if tt.detail != "" {
				if got := strings.Join(details, ","); got != tt.detail {
					t.Errorf("details = %q, want %q", got, tt.detail)
				}
			}
			if tt.kinds != "" {
				want := tt.kinds
				if !strings.Contains(want, ",") {
					want = strings.TrimSuffix(strings.Repeat(want+",", len(kinds)), ",")
				}
				if got := strings.Join(kinds, ","); got != want {
					t.Errorf("kinds = %q, want %q", got, want)
				}
			}
			prefixStart := offset
			for prefixStart > 0 && strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", rune(source[prefixStart-1])) {
				prefixStart--
			}
			if list.Start != prefixStart {
				t.Errorf("replace start = %d, want %d", list.Start, prefixStart)
			}
		})
	}
}

func TestSemanticTokens_GIVENKeyedRepetition_THENClassifiesKeywordsItemsAndPaths(t *testing.T) {
	source := "Columns [columns: [Column]] {\n\tul { for column in columns key column.id { li \"{column.title}\" } }\n\tlabel for=\"x\"\n}\n"
	want := "parameter*:columns,type:Column,keyword:for,parameter*:column,keyword:in,parameter:columns,keyword:key,parameter:column,property:id,parameter:column,property:title"
	var got []string
	for _, current := range compiler.SemanticTokens([]byte(source)) {
		kind := string(current.Type)
		if current.Declaration {
			kind += "*"
		}
		got = append(got, kind+":"+source[current.Start:current.End])
	}
	if joined := strings.Join(got, ","); joined != want {
		t.Errorf("tokens = %s\nwant     %s", joined, want)
	}
}

func spellRanges(source string, ranges []compiler.SourceRange) string {
	spelled := make([]string, 0, len(ranges))
	for _, current := range ranges {
		spelled = append(spelled, source[current.Start:current.End]+"@"+itoa(current.Start))
	}
	sort.Strings(spelled)
	return strings.Join(spelled, ",")
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// at spells the start offsets of each marker in source.
func at(source string, markers ...string) string {
	spelled := make([]string, 0, len(markers))
	for _, marker := range markers {
		name, context, _ := strings.Cut(marker, "|")
		start := strings.Index(source, context)
		if start < 0 {
			return "missing " + context
		}
		spelled = append(spelled, name+"@"+itoa(start+strings.Index(context, name)))
	}
	sort.Strings(spelled)
	return strings.Join(spelled, ",")
}

func TestBindingOccurrenceAt_GIVENRepetitionItems_THENResolvesDeclarationsUsesAndSignatures(t *testing.T) {
	dir, snapshot := repetitionToolingWorkspace(t)
	file := filepath.Join(dir, "board.ktml")
	source := repetitionToolingBoard
	columnUses := at(source, "column|column.id {", "column|column.title}\"\n", "column|column.cards", "column|column.title} {title}")
	cardUses := at(source, "card|card.id {\n\t\t\tp", "card|card.title}\" {", "card|card.title} {column")
	tests := []struct {
		name       string
		marker     string
		root       bool
		signature  string
		references string
	}{
		{name: "item declaration", marker: "column in", signature: "item column: Column\nfor column in columns key column.id", references: columnUses},
		{name: "key root", marker: "column.id {", root: true, signature: "item column: Column\nfor column in columns key column.id", references: columnUses},
		{name: "nested collection root", marker: "column.cards", root: true, signature: "item column: Column\nfor column in columns key column.id", references: columnUses},
		{name: "attribute body root", marker: "card.title}\" {", root: true, signature: "item card: Card\nfor card in column.cards key card.id", references: cardUses},
		{name: "sibling item with the same name", marker: "card.title}\" }", root: true, signature: "item card: Column\nfor card in columns key card.id", references: at(source, "card|card.id { em", "card|card.title}\" }")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			occurrence, ok := snapshot.BindingOccurrenceAt(file, strings.Index(source, tt.marker)+1)
			if !ok {
				t.Fatalf("no binding at %q", tt.marker)
			}
			if occurrence.Root != tt.root {
				t.Errorf("root = %v, want %v", occurrence.Root, tt.root)
			}
			if got := snapshot.BindingSignature(occurrence.Binding); got != tt.signature {
				t.Errorf("signature = %q, want %q", got, tt.signature)
			}
			if got := spellRanges(source, snapshot.BindingReferences(occurrence.Binding)); got != tt.references {
				t.Errorf("references = %s, want %s", got, tt.references)
			}
		})
	}
	if occurrence, ok := snapshot.BindingOccurrenceAt(file, strings.Index(source, "{title}")+1); ok {
		t.Errorf("input root resolved as binding %+v", occurrence)
	}
}

func TestBindingOccurrenceAt_GIVENItemUseInUntypedPath_THENStillResolvesTheItem(t *testing.T) {
	snapshot, file, source := boardWorkspace(t, "Cards [cards: [Card]] {\n\tfor card in cards key card.id { p \"{card.nope}\" }\n}\n")
	if snapshot.Valid() {
		t.Fatal("workspace is valid, want unknown-field")
	}
	occurrence, ok := snapshot.BindingOccurrenceAt(file, strings.Index(source, "card.nope")+1)
	if !ok || !occurrence.Root || occurrence.Binding.Name != "card" {
		t.Fatalf("occurrence = %+v, %v", occurrence, ok)
	}
	want := at(source, "card|card.id {", "card|card.nope")
	if got := spellRanges(source, snapshot.BindingReferences(occurrence.Binding)); got != want {
		t.Errorf("references = %s, want %s", got, want)
	}
}

func TestItemRename_GIVENRepetitionItems_WHENRenamed_THENEditsOnlyThatItemAndRejectsCollisions(t *testing.T) {
	dir, snapshot := repetitionToolingWorkspace(t)
	file := filepath.Join(dir, "board.ktml")
	source := repetitionToolingBoard
	item := func(marker string) compiler.Binding {
		t.Helper()
		occurrence, ok := snapshot.BindingOccurrenceAt(file, strings.Index(source, marker)+1)
		if !ok {
			t.Fatalf("no binding at %q", marker)
		}
		return occurrence.Binding
	}
	tests := []struct {
		name    string
		marker  string
		newName string
		want    string
		err     string
	}{
		{name: "outer item", marker: "column in", newName: "lane", want: at(source, "column|column in", "column|column.id {", "column|column.title}\"\n", "column|column.cards", "column|column.title} {title}")},
		{name: "nested item keeps sibling", marker: "card in column", newName: "ticket", want: at(source, "card|card in column", "card|card.id {\n\t\t\tp", "card|card.title}\" {", "card|card.title} {column")},
		{name: "sibling item may reuse a name out of scope", marker: "card in columns", newName: "column", want: at(source, "card|card in columns", "card|card.id { em", "card|card.title}\" }")},
		{name: "invalid identifier", marker: "column in", newName: "9lanes", err: "item name 9lanes must be an identifier"},
		{name: "keyword", marker: "column in", newName: "import", err: "item name import must be an identifier"},
		{name: "input collision", marker: "column in", newName: "title", err: "item name title is an input of Columns"},
		{name: "nested item collision", marker: "column in", newName: "card", err: "item name card is an item of a nested or enclosing repetition"},
		{name: "enclosing item collision", marker: "card in column", newName: "column", err: "item name column is an item of a nested or enclosing repetition"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges, err := snapshot.ItemRename(item(tt.marker), tt.newName)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("error = %v, want %s", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ItemRename() error = %v", err)
			}
			if got := spellRanges(source, ranges); got != tt.want {
				t.Errorf("edits = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestItemRename_GIVENClauseBindingInScope_THENRejectsCollision(t *testing.T) {
	snapshot, file, source := boardWorkspace(t, "View [board: Board]\n\nView [board = Loaded, columns as list] {\n\tfor column in list key column.id {}\n}\n\nView [board = Empty] {}\n")
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	occurrence, ok := snapshot.BindingOccurrenceAt(file, strings.Index(source, "column in")+1)
	if !ok {
		t.Fatal("no item binding")
	}
	if _, err := snapshot.ItemRename(occurrence.Binding, "list"); err == nil || err.Error() != "item name list is a binding of the enclosing clause" {
		t.Errorf("error = %v", err)
	}
	if _, err := snapshot.ItemRename(occurrence.Binding, "board"); err == nil {
		t.Error("renaming to an input succeeded")
	}
}
