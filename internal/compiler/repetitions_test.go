package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const boardSchema = `record Card [
	id: string,
	title: string,
	rank: integer,
	score: number,
	done: boolean,
	note?: string,
	tags: [string],
]

record Column [
	id: integer,
	title: string,
	cards: [Card],
	extra?: [Card],
]

state Board {
	Empty,
	Loaded [columns: [Column], archived?: [Card]],
}
`

func boardWorkspace(t *testing.T, component string) (*compiler.Snapshot, string, string) {
	t.Helper()
	source := "import { Card, Column, Board } from \"./board.ktmlschema\"\n" + component
	dir := writeWorkspace(t, map[string]string{"board.ktmlschema": boardSchema, "board.ktml": source})
	return loadEntry(t, dir, "board.ktml"), filepath.Join(dir, "board.ktml"), source
}

func TestWorkspace_GIVENNestedKeyedRepetitions_WHENLoaded_THENTypesItemsKeysAndSourceIdentity(t *testing.T) {
	snapshot, file, source := boardWorkspace(t, `Columns [columns: [Column], labels: [string]] {
	for column in columns key column.id {
		h2 "{column.title}"
		for card in column.cards key card.id {
			p "{card.title} {column.title}"
			for tag in card.tags key tag { span "{tag}" }
		}
	}
	for label in labels key label { em "{label}" }
}
`)
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	repetitions := snapshot.Repetitions(file)
	if len(repetitions) != 4 {
		t.Fatalf("repetitions = %d, want 4", len(repetitions))
	}
	columns, cards, tags, labels := repetitions[0], repetitions[1], repetitions[2], repetitions[3]
	for index, want := range []string{"r0", "r1", "r2", "r3"} {
		if repetitions[index].Source != want {
			t.Errorf("repetition %d source = %s, want %s", index, repetitions[index].Source, want)
		}
	}
	if columns.Item.Name != "column" || columns.Item.Type.Kind != compiler.RecordTypeKind || columns.Item.Type.Name != "Column" || columns.Item.Repetition != columns.ID {
		t.Errorf("column item = %+v", columns.Item)
	}
	if columns.KeyType != compiler.IntegerType || columns.Key.Parts[1].Name != "id" {
		t.Errorf("column key = %s %+v", columns.KeyType, columns.Key)
	}
	if cards.Parent != columns.ID || tags.Parent != cards.ID || labels.Parent != "" {
		t.Errorf("parents = %q %q %q", cards.Parent, tags.Parent, labels.Parent)
	}
	if cards.KeyType != compiler.StringType || cards.Collection.Parts[0].Binding != "column" {
		t.Errorf("cards = key %s collection %+v", cards.KeyType, cards.Collection)
	}
	if tags.Item.Type.Kind != compiler.ScalarTypeKind || tags.KeyType != compiler.StringType || len(tags.Key.Parts) != 1 {
		t.Errorf("tags = item %+v key %+v", tags.Item, tags.Key)
	}
	if labels.Collection.Parts[0].Input != "labels" || labels.Component != columns.Component {
		t.Errorf("labels = %+v", labels)
	}
	if got := source[columns.Item.Range.Start:columns.Item.Range.End]; got != "column" {
		t.Errorf("item range = %q", got)
	}
	use := strings.Index(source, "{card.title} {column.title}") + len("{card.title} ")
	if binding, ok := snapshot.BindingAt(file, columns.Item.Range.Start); !ok || binding.Repetition != columns.ID {
		t.Errorf("BindingAt(declaration) = %+v, %v", binding, ok)
	}
	found := false
	for _, resolved := range snapshot.Paths(file) {
		if resolved.Range.Start == use && resolved.Parts[0].Binding == "column" && resolved.Parts[0].BindingRange == columns.Item.Range {
			found = true
		}
	}
	if !found {
		t.Errorf("no path use of column at %d bound to its declaration", use)
	}
}

func TestWorkspace_GIVENRepetitionsInClauseBodies_WHENLoaded_THENBindingsAndSomeValuesAreCollections(t *testing.T) {
	snapshot, file, _ := boardWorkspace(t, `View [board: Board, more?: [Card]]

View [board = Empty] {
	for card in more key card.rank { "{card.title}" }
}

View [board = Loaded, columns] {
	for column in columns key column.id { "{column.title}" }
}

Extra [more?: [Card]]

Extra [more = Some, value as cards] {
	for card in cards key card.rank { "{card.title}" }
}

Extra [more = None] { "none" }
`)
	diagnostics := snapshot.Diagnostics(file)
	if got := strings.Join(codes(diagnostics), ","); got != "error:optional-collection" {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	repetitions := snapshot.Repetitions(file)
	if len(repetitions) != 3 {
		t.Fatalf("repetitions = %d", len(repetitions))
	}
	if repetitions[0].Source != "r0" || repetitions[1].Source != "r1" || repetitions[2].Source != "r0" {
		t.Errorf("sources = %s %s %s", repetitions[0].Source, repetitions[1].Source, repetitions[2].Source)
	}
	if repetitions[2].KeyType != compiler.IntegerType || repetitions[2].Collection.Parts[0].Binding != "cards" {
		t.Errorf("Some repetition = %+v", repetitions[2])
	}
}

func TestWorkspace_GIVENInvalidRepetitions_WHENLoaded_THENReportsPreciseDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
		at   string
	}{
		{name: "scalar collection", body: `for c in title key c {}`, code: "repetition-not-collection", at: "title"},
		{name: "record collection", body: `for c in column key c.id {}`, code: "repetition-not-collection", at: "column"},
		{name: "optional collection", body: `for c in column.extra key c.id {}`, code: "optional-collection", at: "column.extra"},
		{name: "unknown collection root", body: `for c in nothing key c.id {}`, code: "unknown-path-root", at: "nothing"},
		{name: "collection through collection", body: `for c in columns.cards key c.id {}`, code: "collection-field-access", at: "cards"},
		{name: "key not rooted", body: `for c in column.cards key column.id {}`, code: "key-not-rooted", at: "column.id"},
		{name: "number key", body: `for c in column.cards key c.score {}`, code: "invalid-key-type", at: "c.score"},
		{name: "boolean key", body: `for c in column.cards key c.done {}`, code: "invalid-key-type", at: "c.done"},
		{name: "record key", body: `for c in columns key c {}`, code: "invalid-key-type", at: "c {"},
		{name: "collection key", body: `for c in column.cards key c.tags {}`, code: "invalid-key-type", at: "c.tags"},
		{name: "optional key", body: `for c in column.cards key c.note {}`, code: "optional-key", at: "c.note"},
		{name: "unknown key field", body: `for c in column.cards key c.missing {}`, code: "unknown-field", at: "missing"},
		{name: "item shadows input", body: `for title in column.cards key title.id {}`, code: "shadowing-binding", at: "title in"},
		{name: "item shadows enclosing item", body: `for c in columns key c.id { for c in c.cards key c.id {} }`, code: "shadowing-binding", at: "c in c.cards"},
		{name: "item out of scope after body", body: `for c in column.cards key c.id {} "{c.title}"`, code: "unknown-path-root", at: "c.title"},
		{name: "item out of scope in collection", body: `for c in c.cards key c.id {}`, code: "unknown-path-root", at: "c.cards"},
		{name: "rendering a record item", body: `for c in column.cards key c.id { "{c}" }`, code: "path-type-mismatch", at: "{c}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Panel [title: string, column: Column, columns: [Column]] {\n\t" + test.body + "\n}\n"
			snapshot, file, source := boardWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(file)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code || diagnostics[0].Severity != compiler.SeverityError {
				t.Fatalf("diagnostics = %v, want one %s", diagnostics, test.code)
			}
			bodyStart := strings.Index(source, test.body)
			want := bodyStart + strings.Index(test.body, test.at)
			if diagnostics[0].Start != want {
				t.Errorf("diagnostic at %d (%q), want %d (%q)", diagnostics[0].Start, source[diagnostics[0].Start:diagnostics[0].End], want, test.at)
			}
			if snapshot.Valid() {
				t.Error("Valid() = true for an invalid repetition")
			}
		})
	}
}

func TestWorkspace_GIVENRepetitionBodyWithChildren_WHENLoaded_THENComponentAcceptsChildren(t *testing.T) {
	snapshot, file, _ := boardWorkspace(t, `List [labels: [string]] {
	for label in labels key label { children }
}
`)
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	list, ok := snapshot.Resolve(file, "List")
	if !ok || !list.AcceptsChildren {
		t.Errorf("List = %+v, want AcceptsChildren", list)
	}
}
