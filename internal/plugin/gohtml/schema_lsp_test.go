package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/textposition"
)

const lspUserSchema = `record Address [
	city: string,
	zip?: string,
]

record User [
	id: string,
	address: Address,
]
`

const lspCard = `import { User, Address as Place } from "./user.ktmlschema"

Card [user: User, place: Place] {
	div title="{user.id}" { "{user.address.city}" }
}
`

func schemaLSPWorkspace(t *testing.T) (dir, schemaPath, cardPath string) {
	t.Helper()
	dir = t.TempDir()
	schemaPath = filepath.Join(dir, "user.ktmlschema")
	cardPath = filepath.Join(dir, "card.ktml")
	for path, source := range map[string]string{schemaPath: lspUserSchema, cardPath: lspCard} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dir, schemaPath, cardPath
}

func callLSP(t *testing.T, method string, params map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("encode params: %v", err)
	}
	response, err := HandleLSP(method, payload)
	if err != nil {
		t.Fatalf("%s error = %v", method, err)
	}
	return response
}

func openDocument(t *testing.T, path, text string, version int) []byte {
	t.Helper()
	uri := "file://" + path
	response := callLSP(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": version, "text": text},
	})
	t.Cleanup(func() {
		payload, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}})
		_, _ = HandleLSP("textDocument/didClose", payload)
	})
	return response
}

// positionOf returns the LSP position of the first marker in source, plus
// shift bytes.
func positionOf(t *testing.T, source, marker string, shift int) map[string]any {
	t.Helper()
	offset := strings.Index(source, marker)
	if offset < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	line, character := textposition.Position([]byte(source), offset+shift)
	return map[string]any{"line": line, "character": character}
}

type publishedDiagnostics struct {
	Method string `json:"method"`
	Params struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	} `json:"params"`
}

func decodeNotifications(t *testing.T, response []byte) []publishedDiagnostics {
	t.Helper()
	var notifications []publishedDiagnostics
	if strings.HasPrefix(strings.TrimSpace(string(response)), "[") {
		if err := json.Unmarshal(response, &notifications); err != nil {
			t.Fatalf("decode notifications %s: %v", response, err)
		}
		return notifications
	}
	var single publishedDiagnostics
	if err := json.Unmarshal(response, &single); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	return []publishedDiagnostics{single}
}

func diagnosticCodes(notification publishedDiagnostics) string {
	var codes []string
	for _, diagnostic := range notification.Params.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return strings.Join(codes, ",")
}

func TestLSP_SchemaDiagnostics_GIVENSchemaEdits_THENPublishesSchemaAndOpenDependentDiagnostics(t *testing.T) {
	_, schemaPath, cardPath := schemaLSPWorkspace(t)
	schemaURI, cardURI := "file://"+schemaPath, "file://"+cardPath

	opened := decodeNotifications(t, openDocument(t, schemaPath, "record User [\n\tid: Missing,\n]\n", 1))
	if len(opened) != 1 || opened[0].Params.URI != schemaURI || diagnosticCodes(opened[0]) != "unknown-type" {
		t.Fatalf("didOpen schema notifications = %#v, want unknown-type for the schema", opened)
	}

	callLSP(t, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": schemaURI, "version": 2},
		"contentChanges": []map[string]any{{"text": lspUserSchema}},
	})
	openDocument(t, cardPath, lspCard, 1)

	withoutCity := strings.Replace(lspUserSchema, "\tcity: string,\n", "", 1)
	changed := decodeNotifications(t, callLSP(t, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": schemaURI, "version": 3},
		"contentChanges": []map[string]any{{"text": withoutCity}},
	}))
	got := make(map[string]string)
	for _, notification := range changed {
		if notification.Method != "textDocument/publishDiagnostics" {
			t.Errorf("notification method = %q", notification.Method)
		}
		got[notification.Params.URI] = diagnosticCodes(notification)
	}
	if code, ok := got[schemaURI]; !ok || code != "" {
		t.Errorf("schema diagnostics = %q (published %v), want none", code, ok)
	}
	if got[cardURI] != "unknown-field" {
		t.Errorf("dependent diagnostics = %q, want unknown-field from the unsaved schema", got[cardURI])
	}

	closed := decodeNotifications(t, callLSP(t, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": schemaURI},
	}))
	got = make(map[string]string)
	for _, notification := range closed {
		got[notification.Params.URI] = diagnosticCodes(notification)
	}
	if code, ok := got[cardURI]; !ok || code != "" {
		t.Errorf("dependent diagnostics after close = %q (published %v), want none from the saved schema", code, ok)
	}
}

func TestLSP_SchemaCompletion_GIVENUnsavedSchemaAndInvalidDocument_THENCompletesFromOverlay(t *testing.T) {
	_, schemaPath, cardPath := schemaLSPWorkspace(t)
	openDocument(t, schemaPath, strings.Replace(lspUserSchema, "\tcity: string,", "\tcity: string,\n\tstreet: string,", 1), 1)
	tests := []struct {
		name   string
		source string
		marker string
		want   string
		kind   int
	}{
		{name: "fields after dot", source: "import { User } from \"./user.ktmlschema\"\nCard [user: User] { \"{user.address.", marker: "address.", want: "city,street,zip", kind: 5},
		{name: "inputs after brace", source: "import { User } from \"./user.ktmlschema\"\nCard [user: User, n: integer] { p { \"{", marker: "\"{", want: "user,n", kind: 6},
		{name: "type position", source: "import { User as Person } from \"./user.ktmlschema\"\nCard [user: ", marker: "user: ", want: "string,boolean,number,integer,Person,content"},
		{name: "import braces", source: "import {  } from \"./user.ktmlschema\"\n", marker: "{ ", want: "Address,User", kind: 22},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openDocument(t, cardPath, tt.source, 1)
			response := callLSP(t, "textDocument/completion", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + cardPath},
				"position": positionOf(t, tt.source, tt.marker, len(tt.marker)),
			})
			var decoded struct {
				Result struct {
					Items []struct {
						Label    string `json:"label"`
						Kind     int    `json:"kind"`
						TextEdit struct {
							NewText string `json:"newText"`
						} `json:"textEdit"`
					} `json:"items"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode completion %s: %v", response, err)
			}
			var labels []string
			for _, item := range decoded.Result.Items {
				labels = append(labels, item.Label)
				if tt.kind != 0 && item.Kind != tt.kind {
					t.Errorf("item %s kind = %d, want %d", item.Label, item.Kind, tt.kind)
				}
				if item.TextEdit.NewText != item.Label {
					t.Errorf("item %s textEdit = %q", item.Label, item.TextEdit.NewText)
				}
			}
			if got := strings.Join(labels, ","); got != tt.want {
				t.Errorf("completion labels = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLSP_SchemaHoverAndDefinition_GIVENSchemaOccurrences_THENShowsSignaturesAndDeclarations(t *testing.T) {
	_, schemaPath, cardPath := schemaLSPWorkspace(t)
	sources := map[string]string{schemaPath: lspUserSchema, cardPath: lspCard}
	tests := []struct {
		name       string
		file       string
		marker     string
		hover      string
		definition string
	}{
		{name: "record declaration", file: schemaPath, marker: "User [", hover: "record User [id: string, address: Address]", definition: "user.ktmlschema:User ["},
		{name: "field type reference", file: schemaPath, marker: "Address,\n]", hover: "record Address [city: string, zip?: string]", definition: "user.ktmlschema:Address ["},
		{name: "field declaration", file: schemaPath, marker: "zip?", hover: "Address.zip?: string", definition: "user.ktmlschema:zip?"},
		{name: "import specifier", file: cardPath, marker: "User, Address", hover: "record User [id: string, address: Address]", definition: "user.ktmlschema:User ["},
		{name: "input type reference", file: cardPath, marker: "Place]", hover: "record Address [city: string, zip?: string]", definition: "user.ktmlschema:Address ["},
		{name: "path field part", file: cardPath, marker: "city}", hover: "Address.city: string", definition: "user.ktmlschema:city:"},
		{name: "path root", file: cardPath, marker: "user.address", hover: "input user: User"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := sources[tt.file]
			params := map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + tt.file},
				"position": positionOf(t, source, tt.marker, 1),
			}
			var hover struct {
				Result struct {
					Contents struct {
						Value string `json:"value"`
					} `json:"contents"`
				} `json:"result"`
			}
			response := callLSP(t, "textDocument/hover", params)
			if err := json.Unmarshal(response, &hover); err != nil {
				t.Fatalf("decode hover: %v", err)
			}
			if want := "```ktml\n" + tt.hover + "\n```"; hover.Result.Contents.Value != want {
				t.Errorf("hover = %q, want %q", hover.Result.Contents.Value, want)
			}
			if tt.definition == "" {
				return
			}
			var definition struct {
				Result *lspLocation `json:"result"`
			}
			response = callLSP(t, "textDocument/definition", params)
			if err := json.Unmarshal(response, &definition); err != nil || definition.Result == nil {
				t.Fatalf("definition = %s (%v)", response, err)
			}
			wantFile, wantMarker, _ := strings.Cut(tt.definition, ":")
			if got := definition.Result.spell(t, sources); got != wantFile+":"+strings.TrimRight(strings.Split(wantMarker, " ")[0], "?:") {
				t.Errorf("definition = %s, want %s", got, tt.definition)
			}
		})
	}
}

type lspLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct{ Line, Character int } `json:"start"`
		End   struct{ Line, Character int } `json:"end"`
	} `json:"range"`
}

func (l lspLocation) spell(t *testing.T, sources map[string]string) string {
	t.Helper()
	path := strings.TrimPrefix(l.URI, "file://")
	source := []byte(sources[path])
	start := textposition.Offset(source, l.Range.Start.Line, l.Range.Start.Character)
	end := textposition.Offset(source, l.Range.End.Line, l.Range.End.Character)
	return filepath.Base(path) + ":" + string(source[start:end])
}

func TestLSP_SchemaReferences_GIVENFieldDeclarationInSchema_THENFindsUnopenedImporters(t *testing.T) {
	_, schemaPath, cardPath := schemaLSPWorkspace(t)
	sources := map[string]string{schemaPath: lspUserSchema, cardPath: lspCard}
	tests := []struct {
		name        string
		marker      string
		declaration bool
		want        string
	}{
		{name: "field with declaration", marker: "city", declaration: true, want: "card.ktml:city,user.ktmlschema:city"},
		{name: "record without declaration", marker: "Address [", want: "card.ktml:Address,card.ktml:Place,user.ktmlschema:Address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := callLSP(t, "textDocument/references", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + schemaPath},
				"position": positionOf(t, lspUserSchema, tt.marker, 1),
				"context":  map[string]any{"includeDeclaration": tt.declaration},
			})
			var decoded struct {
				Result []lspLocation `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode references %s: %v", response, err)
			}
			var spelled []string
			for _, location := range decoded.Result {
				spelled = append(spelled, location.spell(t, sources))
			}
			sort.Strings(spelled)
			if got := strings.Join(spelled, ","); got != tt.want {
				t.Errorf("references = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLSP_SchemaRename_GIVENRecordAndFieldSymbols_THENEditsNamedOccurrencesAcrossDocuments(t *testing.T) {
	_, schemaPath, cardPath := schemaLSPWorkspace(t)
	sources := map[string]string{schemaPath: lspUserSchema, cardPath: lspCard}
	tests := []struct {
		name    string
		file    string
		marker  string
		newName string
		want    string
	}{
		{name: "record keeps aliases", file: schemaPath, marker: "Address [", newName: "Location", want: "card.ktml:Address,user.ktmlschema:Address,user.ktmlschema:Address"},
		{name: "field from path part", file: cardPath, marker: "address.city", newName: "home", want: "card.ktml:address,user.ktmlschema:address"},
		{name: "invalid record name", file: schemaPath, marker: "User [", newName: "user"},
		{name: "invalid field name", file: schemaPath, marker: "city", newName: "City"},
		{name: "colliding field name", file: schemaPath, marker: "city", newName: "zip"},
		{name: "aliased occurrence", file: cardPath, marker: "Place]", newName: "Location"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := callLSP(t, "textDocument/rename", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + tt.file},
				"position": positionOf(t, sources[tt.file], tt.marker, 1), "newName": tt.newName,
			})
			var decoded struct {
				Result *struct {
					Changes map[string][]struct {
						Range   json.RawMessage `json:"range"`
						NewText string          `json:"newText"`
					} `json:"changes"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode rename %s: %v", response, err)
			}
			if tt.want == "" {
				if decoded.Result != nil {
					t.Errorf("rename = %s, want rejection", response)
				}
				return
			}
			if decoded.Result == nil {
				t.Fatalf("rename = %s, want edits", response)
			}
			var spelled []string
			for uri, edits := range decoded.Result.Changes {
				for _, edit := range edits {
					var location lspLocation
					location.URI = uri
					if err := json.Unmarshal(edit.Range, &location.Range); err != nil {
						t.Fatalf("decode range: %v", err)
					}
					if edit.NewText != tt.newName {
						t.Errorf("edit text = %q, want %q", edit.NewText, tt.newName)
					}
					spelled = append(spelled, location.spell(t, sources))
				}
			}
			sort.Strings(spelled)
			if got := strings.Join(spelled, ","); got != tt.want {
				t.Errorf("rename edits = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLSP_SchemaPrepareRename_GIVENOccurrences_THENAllowsOnlyDeclaredNameSpellings(t *testing.T) {
	_, _, cardPath := schemaLSPWorkspace(t)
	tests := []struct {
		marker      string
		placeholder string
	}{
		{marker: "User, Address", placeholder: "User"},
		{marker: "city}", placeholder: "city"},
		{marker: "Place]", placeholder: ""},
	}
	for _, tt := range tests {
		t.Run(tt.marker, func(t *testing.T) {
			response := callLSP(t, "textDocument/prepareRename", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + cardPath},
				"position": positionOf(t, lspCard, tt.marker, 1),
			})
			var decoded struct {
				Result *struct {
					Placeholder string `json:"placeholder"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode prepareRename %s: %v", response, err)
			}
			got := ""
			if decoded.Result != nil {
				got = decoded.Result.Placeholder
			}
			if got != tt.placeholder {
				t.Errorf("placeholder = %q, want %q", got, tt.placeholder)
			}
		})
	}
}

func TestLSP_SemanticTokens_GIVENSchemaDocument_THENReturnsRelativeEncodedLegendTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ktmlschema")
	source := "record A [\n\tid: string,\n\tb?: [A],\n]\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	response := callLSP(t, "textDocument/semanticTokens/full", map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": "file://" + path},
	})
	var decoded struct {
		Result struct {
			Data []int `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode semantic tokens %s: %v", response, err)
	}
	// keyword record, type A (declaration), property id (declaration), type
	// string, property b (declaration), type A.
	want := []int{
		0, 0, 6, 0, 0,
		0, 7, 1, 1, 1,
		1, 1, 2, 2, 1,
		0, 4, 6, 1, 0,
		1, 1, 1, 2, 1,
		0, 5, 1, 1, 0,
	}
	if len(decoded.Result.Data) != len(want) {
		t.Fatalf("data = %v, want %v", decoded.Result.Data, want)
	}
	for index := range want {
		if decoded.Result.Data[index] != want[index] {
			t.Fatalf("data = %v, want %v", decoded.Result.Data, want)
		}
	}
}
