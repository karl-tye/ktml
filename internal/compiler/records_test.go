package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func writeWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func loadEntry(t *testing.T, dir, entry string) *compiler.Snapshot {
	t.Helper()
	snapshot, err := compiler.NewWorkspace(dir).Load(filepath.Join(dir, entry))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return snapshot
}

const searchSchema = `record Address [
	city: string,
	zip?: string,
]

record User [
	id: string,
	name: string,
	age: integer,
	score: number,
	admin: boolean,
	nickname?: string,
	address: Address,
	previous?: Address,
	tags: [string],
	friends: [Address],
]
`

func TestWorkspace_GIVENSchemaRecords_WHENLoaded_THENResolvesNominalFieldTypes(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{"user.ktmlschema": searchSchema})
	snapshot := loadEntry(t, dir, "user.ktmlschema")
	if !snapshot.Valid() {
		t.Fatalf("diagnostics = %#v", snapshot.Diagnostics(filepath.Join(dir, "user.ktmlschema")))
	}
	records := snapshot.RecordsIn(filepath.Join(dir, "user.ktmlschema"))
	if len(records) != 2 || records[0].Name != "Address" || records[1].Name != "User" {
		t.Fatalf("records = %#v", records)
	}
	want := map[string]string{"id": "string", "age": "integer", "score": "number", "admin": "boolean", "address": "Address", "previous": "Address", "tags": "[string]", "friends": "[Address]"}
	for name, typeName := range want {
		field, ok := records[1].Field(name)
		if !ok || field.Type.String() != typeName {
			t.Errorf("field %s = %#v, want type %s", name, field, typeName)
		}
	}
	previous, _ := records[1].Field("previous")
	if !previous.Optional || previous.GoName != "Previous" || previous.Type.Record != records[0].ID {
		t.Errorf("previous = %#v", previous)
	}
	id, _ := records[1].Field("id")
	if id.GoName != "Id" {
		t.Errorf("id Go name = %s, want Id", id.GoName)
	}
}

func TestWorkspace_GIVENInvalidSchemaDeclarations_WHENLoaded_THENReportsExactDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string
		entry  string
		code   string
		marker string
	}{
		{name: "record in component document", files: map[string]string{"a.ktml": "record User []"}, entry: "a.ktml", code: "record-outside-schema", marker: "record User"},
		{name: "component in schema document", files: map[string]string{"a.ktmlschema": "Card { div }"}, entry: "a.ktmlschema", code: "component-in-schema", marker: "Card"},
		{name: "schema imports component", files: map[string]string{"a.ktmlschema": `import { Card } from "./card.ktml"`, "card.ktml": "Card { div }"}, entry: "a.ktmlschema", code: "schema-imports-component", marker: `"./card.ktml"`},
		{name: "lower type name", files: map[string]string{"a.ktmlschema": "record user []"}, entry: "a.ktmlschema", code: "invalid-type-name", marker: "user"},
		{name: "reserved type name", files: map[string]string{"a.ktmlschema": "record URL []"}, entry: "a.ktmlschema", code: "reserved-type-name", marker: "URL"},
		{name: "upper field name", files: map[string]string{"a.ktmlschema": "record A [Id: string]"}, entry: "a.ktmlschema", code: "invalid-field-name", marker: "Id"},
		{name: "hyphenated field name", files: map[string]string{"a.ktmlschema": "record A [display-name: string]"}, entry: "a.ktmlschema", code: "invalid-field-name", marker: "display-name"},
		{name: "duplicate field", files: map[string]string{"a.ktmlschema": "record A [id: string, id: integer]"}, entry: "a.ktmlschema", code: "duplicate-field", marker: "id: integer"},
		{name: "duplicate record", files: map[string]string{"a.ktmlschema": "record A []\nrecord A []"}, entry: "a.ktmlschema", code: "duplicate-record", marker: "A"},
		{name: "unknown type", files: map[string]string{"a.ktmlschema": "record A [b: Missing]"}, entry: "a.ktmlschema", code: "unknown-type", marker: "Missing"},
		{name: "contextual type", files: map[string]string{"a.ktmlschema": "record A [body: TrustedHTML]"}, entry: "a.ktmlschema", code: "unsupported-contextual-type", marker: "TrustedHTML"},
		{name: "direct recursion", files: map[string]string{"a.ktmlschema": "record A [self: [A]]"}, entry: "a.ktmlschema", code: "recursive-record", marker: "[A]"},
		{name: "indirect recursion", files: map[string]string{"a.ktmlschema": "record A [b: B]\nrecord B [a?: A]"}, entry: "a.ktmlschema", code: "recursive-record", marker: "A]"},
		{name: "unknown imported record", files: map[string]string{"a.ktmlschema": `import { Nope } from "./b.ktmlschema"`, "b.ktmlschema": "record B []"}, entry: "a.ktmlschema", code: "unknown-import", marker: "Nope"},
		{name: "import collides with local record", files: map[string]string{"a.ktmlschema": "import { B } from \"./b.ktmlschema\"\nrecord B []", "b.ktmlschema": "record B []"}, entry: "a.ktmlschema", code: "import-collision", marker: "B }"},
		{name: "component alias", files: map[string]string{"a.ktml": "import { Card as Tile } from \"./card.ktml\"\nPage { div }", "card.ktml": "Card { div }"}, entry: "a.ktml", code: "unsupported-component-alias", marker: "Tile"},
		{name: "record collides with component", files: map[string]string{"a.ktmlschema": "record Card []", "card.ktml": "import { Card } from \"./a.ktmlschema\"\nCard [c: Card] { div }"}, entry: "card.ktml", code: "generated-name-collision", marker: "Card []"},
		{name: "record collides with props", files: map[string]string{"a.ktmlschema": "record PageProps []", "page.ktml": "import { PageProps } from \"./a.ktmlschema\"\nPage [p: PageProps] { div }"}, entry: "page.ktml", code: "generated-name-collision", marker: "PageProps []"},
	}
	lengths := map[string]int{"duplicate field": 2, "duplicate record": 1, "indirect recursion": 1, "import collides with local record": 1, "record collides with component": 4, "record collides with props": 9}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "duplicate record" {
				tt.marker = "A"
			}
			dir := writeWorkspace(t, tt.files)
			snapshot := loadEntry(t, dir, tt.entry)
			var found *compiler.Diagnostic
			var all []compiler.Diagnostic
			for name := range tt.files {
				all = append(all, snapshot.Diagnostics(filepath.Join(dir, name))...)
			}
			for index := range all {
				if all[index].Code == tt.code && strings.Contains(tt.files[filepath.Base(all[index].File)], tt.marker) {
					found = &all[index]
				}
			}
			if found == nil {
				t.Fatalf("diagnostics = %#v, want %s", all, tt.code)
			}
			start := strings.LastIndex(tt.files[filepath.Base(found.File)], tt.marker)
			length, ok := lengths[tt.name]
			if !ok {
				length = len(tt.marker)
			}
			if found.Start != start || found.End != start+length || snapshot.Valid() {
				t.Errorf("diagnostic = %#v, want %s at %d-%d", *found, tt.code, start, start+length)
			}
		})
	}
}

const recordComponents = `import { User, Address as Place } from "./user.ktmlschema"

Badge [label: string, count: integer = 3] { span "{label}{count}" }

Where [place: Place] { span "{place.city}" }

Card [user: User, items: [Place], fallback?: User] {
	div id="{user.id}" title="{user.name} ({user.age})" {
		"{user.address.city}"
		Badge label="{user.name}" count="{user.age}" {}
		Where place="{user.address}"
	}
}
`

func TestWorkspace_GIVENRecordInputsAndFieldPaths_WHENLoaded_THENTypesEveryPath(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{"user.ktmlschema": searchSchema, "card.ktml": recordComponents})
	card := filepath.Join(dir, "card.ktml")
	snapshot := loadEntry(t, dir, "card.ktml")
	if !snapshot.Valid() {
		t.Fatalf("diagnostics = %#v", snapshot.Diagnostics(card))
	}
	component, ok := snapshot.Resolve(card, "Card")
	if !ok || component.Inputs[0].Type.String() != "User" || component.Inputs[1].Type.String() != "[Address]" || component.Inputs[2].Mode != compiler.OptionalInput {
		t.Fatalf("Card = %#v", component)
	}
	badge, _ := snapshot.Resolve(card, "Badge")
	if badge.Inputs[1].Default == nil || badge.Inputs[1].Default.Kind != compiler.IntegerLiteral || badge.Inputs[1].Default.Integer != 3 {
		t.Fatalf("Badge count default = %#v", badge.Inputs[1].Default)
	}
	if dependencies := snapshot.Dependencies(card); len(dependencies) != 1 || dependencies[0] != filepath.Join(dir, "user.ktmlschema") {
		t.Fatalf("dependencies = %v", dependencies)
	}
	paths := map[string]string{}
	for _, resolved := range snapshot.Paths(card) {
		parts := make([]string, len(resolved.Parts))
		for index, part := range resolved.Parts {
			parts[index] = part.Name
		}
		paths[strings.Join(parts, ".")] = resolved.Type.String()
	}
	for path, typeName := range map[string]string{"user.id": "string", "user.age": "integer", "user.address.city": "string", "user.address": "Address", "place.city": "string"} {
		if paths[path] != typeName {
			t.Errorf("path %s type = %q, want %s (all %v)", path, paths[path], typeName, paths)
		}
	}
	user, _ := snapshot.Records()[1], 0
	if user.Name != "User" {
		t.Fatalf("records = %#v", snapshot.Records())
	}
	field, _ := user.Field("name")
	references := snapshot.References(field.ID)
	if len(references) != 2 {
		t.Fatalf("name field references = %#v, want 2", references)
	}
	address := snapshot.Records()[0]
	var aliased, canonical int
	for _, reference := range snapshot.References(address.ID) {
		if reference.Alias {
			aliased++
		} else {
			canonical++
		}
	}
	if aliased != 2 || canonical != 4 {
		t.Errorf("Address references aliased=%d canonical=%d, want 2 and 4 (import + 3 schema fields)", aliased, canonical)
	}
}

func TestWorkspace_GIVENInvalidFieldPaths_WHENLoaded_THENReportsExactPartRanges(t *testing.T) {
	tests := []struct {
		name, body, code, marker string
	}{
		{name: "unknown field", body: `"{user.missing}"`, code: "unknown-field", marker: "missing"},
		{name: "field of scalar", body: `"{user.name.first}"`, code: "scalar-field-access", marker: "first"},
		{name: "field of collection", body: `"{user.friends.city}"`, code: "collection-field-access", marker: "city"},
		{name: "field through optional record", body: `"{user.previous.city}"`, code: "optional-value-access", marker: "previous"},
		{name: "field through optional input", body: `"{fallback.name}"`, code: "optional-value-access", marker: "fallback"},
		{name: "optional field rendered", body: `"{user.nickname}"`, code: "optional-value-access", marker: "nickname"},
		{name: "optional nested field rendered", body: `"{user.address.zip}"`, code: "optional-value-access", marker: "zip"},
		{name: "record rendered as text", body: `"{user.address}"`, code: "path-type-mismatch", marker: "{user.address}"},
		{name: "collection rendered in attribute", body: `div title="{user.tags}"`, code: "path-type-mismatch", marker: "{user.tags}"},
		{name: "record argument type mismatch", body: `Where place="{user}"`, code: "input-type-mismatch", marker: `place="{user}"`},
		{name: "scalar argument to record input", body: `Where place="{user.name}"`, code: "input-type-mismatch", marker: `place="{user.name}"`},
		{name: "literal argument to record input", body: `Where place="Paris"`, code: "input-type-mismatch", marker: `place="Paris"`},
		{name: "optional field argument", body: `Badge label="{user.nickname}"`, code: "optional-value-access", marker: "nickname"},
		{name: "fractional integer literal", body: `Badge label="x" count=1.5`, code: "input-type-mismatch", marker: "count=1.5"},
		{name: "quoted integer literal", body: `Badge label="x" count="1"`, code: "input-type-mismatch", marker: `count="1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := `import { User, Address } from "./user.ktmlschema"
Badge [label: string, count: integer = 3] { span "{label}" }
Where [place: Address] { span "{place.city}" }
Card [user: User, fallback?: User] { ` + tt.body + ` }
`
			dir := writeWorkspace(t, map[string]string{"user.ktmlschema": searchSchema, "card.ktml": source})
			snapshot := loadEntry(t, dir, "card.ktml")
			diagnostics := snapshot.Diagnostics(filepath.Join(dir, "card.ktml"))
			start := strings.LastIndex(source, tt.marker)
			if len(diagnostics) != 1 || diagnostics[0].Code != tt.code || diagnostics[0].Start != start || diagnostics[0].End != start+len(tt.marker) {
				t.Errorf("diagnostics = %#v, want one %s at %d-%d", diagnostics, tt.code, start, start+len(tt.marker))
			}
		})
	}
}

func TestWorkspace_GIVENInvalidTypedInputDeclarations_WHENLoaded_THENReportsTypeDiagnostics(t *testing.T) {
	tests := []struct {
		name, declaration, code, marker string
	}{
		{name: "unknown input type", declaration: "Card [user: Usr] { div }", code: "unknown-type", marker: "Usr"},
		{name: "record default", declaration: `Card [user: User = "x"] { div }`, code: "default-type-mismatch", marker: `"x"`},
		{name: "fractional integer default", declaration: "Card [count: integer = 1.5] { div }", code: "default-type-mismatch", marker: "1.5"},
		{name: "overflowing integer default", declaration: "Card [count: integer = 9223372036854775808] { div }", code: "default-type-mismatch", marker: "9223372036854775808"},
		{name: "contextual input type", declaration: "Card [body: TrustedHTML] { div }", code: "unsupported-contextual-type", marker: "TrustedHTML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "import { User } from \"./user.ktmlschema\"\n" + tt.declaration
			dir := writeWorkspace(t, map[string]string{"user.ktmlschema": searchSchema, "card.ktml": source})
			diagnostics := loadEntry(t, dir, "card.ktml").Diagnostics(filepath.Join(dir, "card.ktml"))
			start := strings.LastIndex(source, tt.marker)
			if len(diagnostics) != 1 || diagnostics[0].Code != tt.code || diagnostics[0].Start != start || diagnostics[0].End != start+len(tt.marker) {
				t.Errorf("diagnostics = %#v, want one %s at %d-%d", diagnostics, tt.code, start, start+len(tt.marker))
			}
		})
	}
}

func TestWorkspace_GIVENInvalidImportedSchema_WHENDependentLoaded_THENSnapshotBlocksGeneration(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"user.ktmlschema": "record User [id: Missing]",
		"card.ktml":       "import { User } from \"./user.ktmlschema\"\nCard [user: User] { div }",
	})
	snapshot := loadEntry(t, dir, "card.ktml")
	diagnostics := snapshot.Diagnostics(filepath.Join(dir, "user.ktmlschema"))
	if snapshot.Valid() || len(diagnostics) != 1 || diagnostics[0].Code != "unknown-type" || snapshot.Err() == nil {
		t.Fatalf("schema diagnostics = %#v, valid = %v; want blocking unknown-type", diagnostics, snapshot.Valid())
	}
}

func TestWorkspace_GIVENIndexedWorkspace_WHENSchemaOverlayChanges_THENDependentsAreRevalidated(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"user.ktmlschema": "record User [name: string]",
		"card.ktml":       "import { User } from \"./user.ktmlschema\"\nCard [user: User] { \"{user.name}\" }",
	})
	workspace := compiler.NewWorkspace(dir)
	snapshot, err := workspace.Index()
	if err != nil || !snapshot.Valid() {
		t.Fatalf("Index() = %v, valid=%v", err, snapshot.Valid())
	}
	schema := filepath.Join(dir, "user.ktmlschema")
	snapshot, err = workspace.Update(schema, 1, []byte("record User [title: string]"))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	diagnostics := snapshot.Diagnostics(filepath.Join(dir, "card.ktml"))
	if len(diagnostics) != 1 || diagnostics[0].Code != "unknown-field" {
		t.Fatalf("card diagnostics after schema change = %#v, want unknown-field", diagnostics)
	}
}

func TestSnapshot_GIVENSchemaSymbols_WHENQueriedByOffset_THENResolvesDeclarationsAndReferences(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{"user.ktmlschema": searchSchema, "card.ktml": recordComponents})
	card := filepath.Join(dir, "card.ktml")
	snapshot := loadEntry(t, dir, "card.ktml")
	tests := []struct {
		file, marker string
		offset       int
		kind         compiler.SymbolKind
		name         string
	}{
		{file: card, marker: "{user.address.city}", offset: 15, kind: compiler.FieldSymbol, name: "city"},
		{file: card, marker: "Place]", kind: compiler.RecordSymbol, name: "Address"},
		{file: card, marker: "User, Address", kind: compiler.RecordSymbol, name: "User"},
		{file: filepath.Join(dir, "user.ktmlschema"), marker: "nickname", kind: compiler.FieldSymbol, name: "nickname"},
	}
	for _, tt := range tests {
		source := string(snapshot.Source(tt.file))
		if tt.file != card {
			source = searchSchema
		}
		offset := strings.Index(source, tt.marker) + tt.offset
		symbol, _, ok := snapshot.SchemaSymbolAt(tt.file, offset)
		if !ok || symbol.Kind != tt.kind || symbol.Name != tt.name {
			t.Errorf("SchemaSymbolAt(%s) = %#v, %v; want %s %s", tt.marker, symbol, ok, tt.kind, tt.name)
		}
	}
	if _, _, ok := snapshot.SymbolAt(card, strings.Index(recordComponents, "Place]")); ok {
		t.Errorf("SymbolAt() resolved a record reference as a component")
	}
}
