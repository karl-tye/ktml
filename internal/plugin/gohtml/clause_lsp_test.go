package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/textposition"
)

const lspPaymentSchema = `record Money [
	display: string,
]

state Payment {
	Unpaid,
	Pending [reference: string],
	Paid [amount: Money, receipt: URL],
}
`

const lspPaymentCard = `import { Payment, Money } from "./payment.ktmlschema"

Card [payment: Payment, title: string]

Card [payment = Unpaid] { "{title}" }

Card [payment = Payment.Pending, reference] { "{reference}" }

Card [payment = Paid, amount as total, receipt] { a href="{receipt}" { "{total.display}" } }
`

func clauseLSPWorkspace(t *testing.T) (schemaPath, cardPath string) {
	t.Helper()
	dir := t.TempDir()
	schemaPath = filepath.Join(dir, "payment.ktmlschema")
	cardPath = filepath.Join(dir, "card.ktml")
	for path, source := range map[string]string{schemaPath: lspPaymentSchema, cardPath: lspPaymentCard} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return schemaPath, cardPath
}

func TestLSP_ClauseDiagnostics_GIVENWarningsAndErrors_THENPublishesCompilerSeverities(t *testing.T) {
	_, cardPath := clauseLSPWorkspace(t)
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "default fallthrough is a warning",
			source: "import { Payment } from \"./payment.ktmlschema\"\n\nCard [payment: Payment] default { \"x\" }\n\nCard [payment = Unpaid] { \"u\" }\n",
			want:   "2:default-fallthrough:default:default body of Card handles Pending, Paid",
		},
		{
			name:   "unreachable default is a warning",
			source: "Card [on: boolean] default { \"x\" }\n\nCard [on = true] { \"t\" }\n\nCard [on = false] { \"f\" }\n",
			want:   "2:unreachable-default:default:default body of Card is unreachable; every case has a render clause",
		},
		{
			name:   "unknown Variant is an error",
			source: "import { Payment } from \"./payment.ktmlschema\"\n\nCard [payment: Payment]\n\nCard [payment = Lost] { \"u\" }\n",
			want:   "1:unknown-variant:Lost:state Payment has no Variant Lost",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := openDocument(t, cardPath, tt.source, 1)
			var notification struct {
				Params struct {
					Diagnostics []struct {
						Range    lspRange `json:"range"`
						Severity int      `json:"severity"`
						Code     string   `json:"code"`
						Message  string   `json:"message"`
					} `json:"diagnostics"`
				} `json:"params"`
			}
			if err := json.Unmarshal(response, &notification); err != nil {
				t.Fatalf("decode notification %s: %v", response, err)
			}
			var got []string
			for _, diagnostic := range notification.Params.Diagnostics {
				spelled := diagnostic.Range.spell(t, tt.source)
				got = append(got, strings.Join([]string{string(rune('0' + diagnostic.Severity)), diagnostic.Code, spelled, diagnostic.Message}, ":"))
			}
			if joined := strings.Join(got, "\n"); joined != tt.want {
				t.Errorf("diagnostics = %s, want %s", joined, tt.want)
			}
		})
	}
}

func TestLSP_ClauseCompletion_GIVENClauseHeadersAndBodies_THENOffersPatternsBindingsAndStates(t *testing.T) {
	_, cardPath := clauseLSPWorkspace(t)
	const header = "import { Payment, Money } from \"./payment.ktmlschema\"\n\nCard [payment: Payment, on: boolean]\n\n"
	tests := []struct {
		name   string
		source string
		marker string
		want   string
	}{
		{name: "Variants after selector", source: header + "Card [payment = ", marker: "payment = ", want: "Unpaid:20,Pending:20,Paid:20"},
		{name: "boolean cases", source: header + "Card [on = ", marker: "on = ", want: "true:20,false:20"},
		{name: "payload fields in binding position", source: header + "Card [payment = Paid, ", marker: "Paid, ", want: "amount:5,receipt:5"},
		{name: "bindings as path roots", source: header + "Card [payment = Paid, amount as total] { \"{", marker: "\"{", want: "payment:6,on:6,total:6"},
		{name: "states in type positions", source: header + "Other [state: ", marker: "state: ", want: "string:25,boolean:25,number:25,integer:25,Money:22,Payment:13,content:25"},
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
						Label string `json:"label"`
						Kind  int    `json:"kind"`
					} `json:"items"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode completion %s: %v", response, err)
			}
			var got []string
			for _, item := range decoded.Result.Items {
				got = append(got, item.Label+":"+strconv.Itoa(item.Kind))
			}
			if joined := strings.Join(got, ","); joined != tt.want {
				t.Errorf("completion = %s, want %s", joined, tt.want)
			}
		})
	}
}

func TestLSP_ClauseHoverAndDefinition_GIVENStatesPatternsAndBindings_THENDescribesAndLocatesSymbols(t *testing.T) {
	schemaPath, cardPath := clauseLSPWorkspace(t)
	sources := map[string]string{schemaPath: lspPaymentSchema, cardPath: lspPaymentCard}
	const state = "state Payment { Unpaid, Pending [reference: string], Paid [amount: Money, receipt: URL] }"
	tests := []struct {
		name       string
		file       string
		marker     string
		hover      string
		definition string
	}{
		{name: "state declaration", file: schemaPath, marker: "Payment {", hover: state, definition: "payment.ktmlschema:Payment {"},
		{name: "Variant declaration", file: schemaPath, marker: "Pending [", hover: "Payment.Pending [reference: string]", definition: "payment.ktmlschema:Pending ["},
		{name: "payload field declaration", file: schemaPath, marker: "reference:", hover: "Payment.Pending.reference: string", definition: "payment.ktmlschema:reference:"},
		{name: "state input type", file: cardPath, marker: "Payment, title", hover: state, definition: "payment.ktmlschema:Payment {"},
		{name: "Pattern", file: cardPath, marker: "Unpaid]", hover: "Payment.Unpaid", definition: "payment.ktmlschema:Unpaid,"},
		{name: "Pattern qualifier", file: cardPath, marker: "Payment.Pending", hover: state, definition: "payment.ktmlschema:Payment {"},
		{name: "qualified Pattern name", file: cardPath, marker: "Pending, reference", hover: "Payment.Pending [reference: string]", definition: "payment.ktmlschema:Pending ["},
		{name: "unaliased binding", file: cardPath, marker: "reference] {", hover: "binding reference: string\nPayment.Pending.reference: string", definition: "payment.ktmlschema:reference:"},
		{name: "aliased binding field", file: cardPath, marker: "amount as", hover: "Payment.Paid.amount: Money", definition: "payment.ktmlschema:amount:"},
		{name: "binding alias", file: cardPath, marker: "total, receipt]", hover: "binding total: Money\nPayment.Paid.amount: Money", definition: "card.ktml:total, receipt]"},
		{name: "aliased binding root", file: cardPath, marker: "total.display", hover: "binding total: Money\nPayment.Paid.amount: Money", definition: "card.ktml:total, receipt]"},
		{name: "unaliased binding root", file: cardPath, marker: "reference}", hover: "binding reference: string\nPayment.Pending.reference: string", definition: "card.ktml:reference] {"},
		{name: "field of binding root", file: cardPath, marker: "display}", hover: "Money.display: string", definition: "payment.ktmlschema:display:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + tt.file},
				"position": positionOf(t, sources[tt.file], tt.marker, 1),
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
				t.Fatalf("decode hover %s: %v", response, err)
			}
			if want := "```ktml\n" + tt.hover + "\n```"; hover.Result.Contents.Value != want {
				t.Errorf("hover = %q, want %q", hover.Result.Contents.Value, want)
			}
			var definition struct {
				Result *lspLocation `json:"result"`
			}
			response = callLSP(t, "textDocument/definition", params)
			if err := json.Unmarshal(response, &definition); err != nil || definition.Result == nil {
				t.Fatalf("definition = %s (%v)", response, err)
			}
			wantFile, wantMarker, _ := strings.Cut(tt.definition, ":")
			if got := definition.Result.at(t, sources); got != wantFile+":"+strconv.Itoa(strings.Index(sources[filepath.Join(filepath.Dir(tt.file), wantFile)], wantMarker)) {
				t.Errorf("definition = %s, want %s", definition.Result.spell(t, sources), tt.definition)
			}
		})
	}
}

func TestLSP_ClauseReferencesAndRename_GIVENStateSymbolsAndBindings_THENFindsAndEditsOccurrences(t *testing.T) {
	schemaPath, cardPath := clauseLSPWorkspace(t)
	sources := map[string]string{schemaPath: lspPaymentSchema, cardPath: lspPaymentCard}
	tests := []struct {
		name       string
		file       string
		marker     string
		references string
		newName    string
		rename     string
		prepare    string
	}{
		{name: "Variant from Pattern", file: cardPath, marker: "Paid, amount", references: "card.ktml:Paid,payment.ktmlschema:Paid", newName: "Settled", rename: "card.ktml:Paid,payment.ktmlschema:Paid", prepare: "Paid"},
		{name: "state from qualifier", file: cardPath, marker: "Payment.Pending", references: "card.ktml:Payment,card.ktml:Payment,card.ktml:Payment,payment.ktmlschema:Payment", newName: "Charge", rename: "card.ktml:Payment,card.ktml:Payment,card.ktml:Payment,payment.ktmlschema:Payment", prepare: "Payment"},
		{name: "payload field from unaliased root", file: cardPath, marker: "reference}", references: "card.ktml:reference,card.ktml:reference,payment.ktmlschema:reference", newName: "ref", rename: "card.ktml:reference,card.ktml:reference,payment.ktmlschema:reference", prepare: "reference"},
		{name: "payload field keeps binding alias", file: schemaPath, marker: "amount:", references: "card.ktml:amount,payment.ktmlschema:amount", newName: "sum", rename: "card.ktml:amount,payment.ktmlschema:amount", prepare: "amount"},
		{name: "binding alias is not renamed", file: cardPath, marker: "total, receipt]", references: "card.ktml:total,card.ktml:total", newName: "sum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := positionOf(t, sources[tt.file], tt.marker, 1)
			uri := "file://" + tt.file
			var references struct {
				Result []lspLocation `json:"result"`
			}
			response := callLSP(t, "textDocument/references", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position,
				"context": map[string]any{"includeDeclaration": true},
			})
			if err := json.Unmarshal(response, &references); err != nil {
				t.Fatalf("decode references %s: %v", response, err)
			}
			var spelled []string
			for _, location := range references.Result {
				spelled = append(spelled, location.spell(t, sources))
			}
			sort.Strings(spelled)
			if got := strings.Join(spelled, ","); got != tt.references {
				t.Errorf("references = %s, want %s", got, tt.references)
			}

			var prepare struct {
				Result *struct {
					Placeholder string `json:"placeholder"`
				} `json:"result"`
			}
			response = callLSP(t, "textDocument/prepareRename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position})
			if err := json.Unmarshal(response, &prepare); err != nil {
				t.Fatalf("decode prepareRename %s: %v", response, err)
			}
			placeholder := ""
			if prepare.Result != nil {
				placeholder = prepare.Result.Placeholder
			}
			if placeholder != tt.prepare {
				t.Errorf("prepareRename placeholder = %q, want %q", placeholder, tt.prepare)
			}

			var rename struct {
				Result *struct {
					Changes map[string][]struct {
						Range   json.RawMessage `json:"range"`
						NewText string          `json:"newText"`
					} `json:"changes"`
				} `json:"result"`
			}
			response = callLSP(t, "textDocument/rename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position, "newName": tt.newName})
			if err := json.Unmarshal(response, &rename); err != nil {
				t.Fatalf("decode rename %s: %v", response, err)
			}
			if tt.rename == "" {
				if rename.Result != nil {
					t.Errorf("rename = %s, want rejection", response)
				}
				return
			}
			if rename.Result == nil {
				t.Fatalf("rename = %s, want edits", response)
			}
			spelled = nil
			for editURI, edits := range rename.Result.Changes {
				for _, edit := range edits {
					location := lspLocation{URI: editURI}
					if err := json.Unmarshal(edit.Range, &location.Range); err != nil {
						t.Fatalf("decode range: %v", err)
					}
					spelled = append(spelled, location.spell(t, sources))
				}
			}
			sort.Strings(spelled)
			if got := strings.Join(spelled, ","); got != tt.rename {
				t.Errorf("rename edits = %s, want %s", got, tt.rename)
			}
		})
	}
}

func TestLSP_SemanticTokens_GIVENStateDocument_THENEncodesVariantsAsEnumMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.ktmlschema")
	source := "state Flow {\n\tOpen,\n\tDone [at: string],\n}\n"
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
	// keyword state, type Flow (declaration), enumMember Open and Done
	// (declarations), property at (declaration), type string.
	want := []int{
		0, 0, 5, 0, 0,
		0, 6, 4, 1, 1,
		1, 1, 4, 4, 1,
		1, 1, 4, 4, 1,
		0, 6, 2, 2, 1,
		0, 4, 6, 1, 0,
	}
	got := fmtInts(decoded.Result.Data)
	if wanted := fmtInts(want); got != wanted {
		t.Errorf("data = %s, want %s", got, wanted)
	}
}

func fmtInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, " ")
}

// at renders a location as its base file name and start byte offset.
func (l lspLocation) at(t *testing.T, sources map[string]string) string {
	t.Helper()
	path := strings.TrimPrefix(l.URI, "file://")
	return filepath.Base(path) + ":" + strconv.Itoa(textposition.Offset([]byte(sources[path]), l.Range.Start.Line, l.Range.Start.Character))
}

type lspRange struct {
	Start struct{ Line, Character int } `json:"start"`
	End   struct{ Line, Character int } `json:"end"`
}

func (r lspRange) spell(t *testing.T, source string) string {
	t.Helper()
	start := textposition.Offset([]byte(source), r.Start.Line, r.Start.Character)
	end := textposition.Offset([]byte(source), r.End.Line, r.End.Character)
	return source[start:end]
}
