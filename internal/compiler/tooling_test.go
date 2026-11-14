package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const toolingUserSchema = `record Address [
	city: string,
	zip?: string,
]

record User [
	id: string,
	address: Address,
	nickname?: string,
	previous?: Address,
]
`

const toolingCard = `import { User, Address as Place } from "./user.ktmlschema"

Card [user: User, place: Place, fallback?: User] {
	div title="{user.id}" { "{user.address.city} {place.city}" }
}
`

func toolingWorkspace(t *testing.T) (string, *compiler.Snapshot) {
	t.Helper()
	dir := writeWorkspace(t, map[string]string{
		"user.ktmlschema":  toolingUserSchema,
		"order.ktmlschema": "import { Address as Place } from \"./user.ktmlschema\"\n\nrecord Order [\n\tto: Place,\n]\n",
		"card.ktml":        toolingCard,
		"button.ktml":      "Button [label: string] { button }\nIcon { span }\n",
	})
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("workspace is invalid: %v", snapshot.Err())
	}
	return dir, snapshot
}

func TestSchemaCompletions_GIVENEditedSource_WHENCompleting_THENOffersContextualSchemaItems(t *testing.T) {
	dir, snapshot := toolingWorkspace(t)
	const header = "import { User, Address as Place } from \"./user.ktmlschema\"\n"
	tests := []struct {
		name    string
		file    string
		source  string
		context bool
		want    string
		detail  string
	}{
		{name: "field after dot in unterminated path", file: "card.ktml", source: header + "Card [user: User] { \"{user.|", context: true, want: "id,address,nickname,previous", detail: "string,Address,string,Address"},
		{name: "field prefix in closed path", file: "card.ktml", source: header + "Card [user: User] { div title=\"{user.addr|}\" }", context: true, want: "id,address,nickname,previous"},
		{name: "empty field part", file: "card.ktml", source: header + "Card [user: User] { \"{user.|}\" }", context: true, want: "id,address,nickname,previous"},
		{name: "nested record field", file: "card.ktml", source: header + "Card [user: User] { p { \"x {user.address.|\" } }", context: true, want: "city,zip", detail: "string,string"},
		{name: "aliased record input", file: "card.ktml", source: header + "Card [place: Place] { \"{place.|\" }", context: true, want: "city,zip"},
		{name: "inputs after brace", file: "card.ktml", source: header + "Card [user: User, items: [Place], count: integer = 1] { \"{|\" }", context: true, want: "user,items,count", detail: "User,[Address],integer"},
		{name: "optional value has no fields", file: "card.ktml", source: header + "Card [fallback?: User] { \"{fallback.|\" }", context: true, want: ""},
		{name: "collection has no fields", file: "card.ktml", source: header + "Card [items: [Place]] { \"{items.|\" }", context: true, want: ""},
		{name: "unknown root", file: "card.ktml", source: header + "Card [user: User] { \"{missing.|\" }", context: true, want: ""},
		{name: "literal brace is not a path", file: "card.ktml", source: header + "Card [user: User] { \"{ user.|\" }", context: false},
		{name: "plain text", file: "card.ktml", source: header + "Card [user: User] { \"hello |\" }", context: false},
		{name: "component input type", file: "card.ktml", source: header + "Card [user: |", context: true, want: "string,boolean,number,integer,Place,User,content"},
		{name: "collection element type", file: "card.ktml", source: header + "Card [items?: [Pl|] { div }", context: true, want: "string,boolean,number,integer,Place,User"},
		{name: "schema field type sees local and imported records", file: "order.ktmlschema", source: "import { Address as Place } from \"./user.ktmlschema\"\nrecord Draft []\nrecord Order [\n\tto: |\n", context: true, want: "string,boolean,number,integer,Draft,Order,Place"},
		{name: "default value is not a type position", file: "card.ktml", source: header + "Card [count: integer = |", context: false},
		{name: "schema import braces", file: "order.ktmlschema", source: "import { | } from \"./user.ktmlschema\"\n", context: true, want: "Address,User"},
		{name: "schema import braces skip listed names", file: "card.ktml", source: "import { User, A| } from \"./user.ktmlschema\"\n", context: true, want: "Address"},
		{name: "component import braces", file: "card.ktml", source: "import { | } from \"./button.ktml\"\n", context: true, want: "Button,Icon"},
		{name: "schema cannot import components", file: "order.ktmlschema", source: "import { | } from \"./button.ktml\"\n", context: true, want: ""},
		{name: "alias position", file: "card.ktml", source: "import { User as | } from \"./user.ktmlschema\"\n", context: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := strings.Index(tt.source, "|")
			source := []byte(strings.Replace(tt.source, "|", "", 1))
			list, ok := compiler.SchemaCompletions(filepath.Join(dir, tt.file), source, offset, snapshot)
			if ok != tt.context {
				t.Fatalf("context = %v, want %v (items %#v)", ok, tt.context, list.Items)
			}
			if !ok {
				return
			}
			var labels, details []string
			for _, item := range list.Items {
				labels = append(labels, item.Label)
				details = append(details, item.Detail)
			}
			if got := strings.Join(labels, ","); got != tt.want {
				t.Errorf("labels = %q, want %q", got, tt.want)
			}
			if tt.detail != "" {
				if got := strings.Join(details, ","); got != tt.detail {
					t.Errorf("details = %q, want %q", got, tt.detail)
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

func TestSchemaRename_GIVENRecordAndField_WHENRenamed_THENEditsDeclarationAndNamedReferencesOnly(t *testing.T) {
	dir, snapshot := toolingWorkspace(t)
	userPath := filepath.Join(dir, "user.ktmlschema")
	cardPath := filepath.Join(dir, "card.ktml")
	sources := map[string]string{userPath: toolingUserSchema, cardPath: toolingCard, filepath.Join(dir, "order.ktmlschema"): string(snapshot.Source(filepath.Join(dir, "order.ktmlschema")))}
	spell := func(ranges []compiler.SourceRange) string {
		var spelled []string
		for _, current := range ranges {
			spelled = append(spelled, filepath.Base(current.File)+":"+sources[current.File][current.Start:current.End])
		}
		return strings.Join(spelled, ",")
	}
	tests := []struct {
		name   string
		file   string
		marker string
		want   string
	}{
		{name: "record from declaration", file: userPath, marker: "Address [", want: "card.ktml:Address,order.ktmlschema:Address,user.ktmlschema:Address,user.ktmlschema:Address,user.ktmlschema:Address"},
		{name: "field from path part", file: cardPath, marker: "city} {place", want: "card.ktml:city,card.ktml:city,user.ktmlschema:city"},
		{name: "field from declaration", file: userPath, marker: "address: Address", want: "card.ktml:address,user.ktmlschema:address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			occurrence, ok := snapshot.SchemaOccurrenceAt(tt.file, strings.Index(sources[tt.file], tt.marker)+1)
			if !ok {
				t.Fatalf("no schema symbol at %q", tt.marker)
			}
			newName := "renamed"
			if occurrence.Symbol.Kind == compiler.RecordSymbol {
				newName = "Renamed"
			}
			ranges, err := snapshot.SchemaRename(occurrence.Symbol.ID, newName)
			if err != nil {
				t.Fatalf("SchemaRename() error = %v", err)
			}
			if got := spell(ranges); got != tt.want {
				t.Errorf("rename ranges = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSchemaRename_GIVENInvalidOrCollidingName_THENRejectsRename(t *testing.T) {
	dir, snapshot := toolingWorkspace(t)
	userPath := filepath.Join(dir, "user.ktmlschema")
	record, _, _ := snapshot.SchemaSymbolAt(userPath, strings.Index(toolingUserSchema, "User ["))
	field, _, _ := snapshot.SchemaSymbolAt(userPath, strings.Index(toolingUserSchema, "nickname"))
	tests := []struct {
		name    string
		id      compiler.SymbolID
		newName string
	}{
		{name: "lower record name", id: record.ID, newName: "user"},
		{name: "reserved record name", id: record.ID, newName: "URL"},
		{name: "non-ASCII record name", id: record.ID, newName: "Usér"},
		{name: "record collides with local record", id: record.ID, newName: "Address"},
		{name: "upper field name", id: field.ID, newName: "Nick"},
		{name: "hyphenated field name", id: field.ID, newName: "nick-name"},
		{name: "field collides with field", id: field.ID, newName: "id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := snapshot.SchemaRename(tt.id, tt.newName); err == nil {
				t.Errorf("SchemaRename(%s) succeeded, want rejection", tt.newName)
			}
		})
	}
}

func TestSchemaOccurrenceAt_GIVENAliasedTypeReference_THENMarksAlias(t *testing.T) {
	dir, snapshot := toolingWorkspace(t)
	cardPath := filepath.Join(dir, "card.ktml")
	tests := []struct {
		name   string
		marker string
		alias  bool
	}{
		{name: "aliased type reference", marker: "Place, fallback", alias: true},
		{name: "import source name", marker: "Address as", alias: false},
		{name: "named type reference", marker: "User, place", alias: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			occurrence, ok := snapshot.SchemaOccurrenceAt(cardPath, strings.Index(toolingCard, tt.marker)+1)
			if !ok {
				t.Fatalf("no occurrence at %q", tt.marker)
			}
			if occurrence.Alias != tt.alias {
				t.Errorf("alias = %v, want %v", occurrence.Alias, tt.alias)
			}
		})
	}
}

func TestPathPartAt_GIVENPathParts_THENRendersInputAndFieldSignatures(t *testing.T) {
	dir, snapshot := toolingWorkspace(t)
	cardPath := filepath.Join(dir, "card.ktml")
	tests := []struct {
		marker string
		want   string
	}{
		{marker: "user.address.city}", want: "input user: User"},
		{marker: "address.city}", want: "User.address: Address"},
		{marker: "city} {place", want: "Address.city: string"},
	}
	for _, tt := range tests {
		t.Run(tt.marker, func(t *testing.T) {
			part, ok := snapshot.PathPartAt(cardPath, strings.Index(toolingCard, tt.marker)+1)
			if !ok {
				t.Fatalf("no path part at %q", tt.marker)
			}
			if got := snapshot.PartSignature(part); got != tt.want {
				t.Errorf("signature = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSemanticTokens_GIVENSchemaAndComponentSources_THENClassifiesSchemaSyntax(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "schema document",
			source: "import { Address as Place } from \"./user.ktmlschema\"\nrecord Order [\n\tto?: [Place],\n\tcount: integer,\n]\n",
			want:   "keyword:import,type:Address,keyword:as,type:Place,keyword:from,keyword:record,type*:Order,property*:to,type:Place,property*:count,type:integer",
		},
		{
			name:   "component document",
			source: "import { User } from \"./user.ktmlschema\"\nimport { Button } from \"./button.ktml\"\nCard [user: User] { div title=\"{user.name}\" { \"{user.address.city}\" } }\n",
			want:   "keyword:import,type:User,keyword:from,keyword:import,keyword:from,parameter*:user,type:User,parameter:user,property:name,parameter:user,property:address,property:city",
		},
		{
			name:   "recovered invalid document keeps valid declarations",
			source: "record A [\n\tid: string,\n]\nrecord B [ x: ]\n",
			want:   "keyword:record,type*:A,property*:id,type:string,keyword:record,type*:B",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			last := -1
			for _, current := range compiler.SemanticTokens([]byte(tt.source)) {
				if current.Start < last {
					t.Fatalf("tokens are not sorted at %d", current.Start)
				}
				last = current.End
				kind := string(current.Type)
				if current.Declaration {
					kind += "*"
				}
				got = append(got, kind+":"+tt.source[current.Start:current.End])
			}
			if joined := strings.Join(got, ","); joined != tt.want {
				t.Errorf("tokens = %s\nwant     %s", joined, tt.want)
			}
		})
	}
}
