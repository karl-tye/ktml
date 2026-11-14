package formatter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/parser"
)

func TestFormat_GIVENRecordsAndAliasedImports_WHENFormatted_THENUsesCanonicalLayout(t *testing.T) {
	source := `import {Person as Owner,Item,} from "./people.ktmlschema"
record Empty [ ]
record User [id:string,nickname ?: string , tags:[ [Item] ],owner:Owner]
Card [user: User, items: [ Item ], count: integer = 3] { "{user.id}" }`
	want := `import { Person as Owner, Item } from "./people.ktmlschema"

record Empty []

record User [
	id: string,
	nickname?: string,
	tags: [[Item]],
	owner: Owner,
]

Card [
	user:  User,
	items: [Item],
	count: integer = 3,
] {
	"{user.id}"
}
`
	got := formatSource(t, source)
	if got != want {
		t.Fatalf("Format() =\n%s\nwant\n%s", got, want)
	}
}

func TestFormat_GIVENConformanceFixtures_WHENFormattedTwice_THENIsIdempotentAndPreservesShape(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "conformance", "valid")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			once := formatSource(t, string(source))
			twice := formatSource(t, once)
			if once != twice {
				t.Fatalf("Format() is not idempotent:\n%s\n---\n%s", once, twice)
			}
			if original, formatted := normalizedShape(t, string(source)), normalizedShape(t, once); original != formatted {
				t.Fatalf("parse/format/parse changed the shape:\n%s\n---\n%s", original, formatted)
			}
		})
	}
}

func formatSource(t *testing.T, source string) string {
	t.Helper()
	p := parser.New([]byte(source))
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse error = %v in\n%s", err, source)
	}
	return Format(document)
}

// normalizedShape is the syntax tree without source positions, element layout
// flags, or raw import spellings that formatting may legitimately change.
func normalizedShape(t *testing.T, source string) string {
	t.Helper()
	p := parser.New([]byte(source))
	document := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	stripPositions(value)
	normalized, _ := json.Marshal(value)
	return string(normalized)
}

func stripPositions(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if _, signature := typed["default_range"]; signature {
			typed["default"] = true
		}
		for key := range typed {
			if key == "P" || key == "E" || key == "start" || key == "end" || key == "multiline" || key == "attribute_group" || key == "optional_marker" || key == "default_operator" || strings.HasSuffix(key, "_range") {
				delete(typed, key)
				continue
			}
			stripPositions(typed[key])
		}
	case []any:
		for _, item := range typed {
			stripPositions(item)
		}
	}
}
