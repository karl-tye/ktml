package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func clauseToolingWorkspace(t *testing.T) (string, *compiler.Snapshot) {
	t.Helper()
	dir := writeWorkspace(t, map[string]string{
		"payment.ktmlschema": paymentSchema,
		"card.ktml":          "import { Payment } from \"./payment.ktmlschema\"\n\nCard [payment: Payment]\n\nCard [payment = Unpaid] {}\n\nCard [payment = Pending] {}\n\nCard [payment = Paid] {}\n\nCard [payment = Failed] {}\n",
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

func TestSchemaCompletions_GIVENRenderClauses_WHENCompleting_THENOffersPatternsBindingsAndStates(t *testing.T) {
	dir, snapshot := clauseToolingWorkspace(t)
	const header = "import { Payment, Money } from \"./payment.ktmlschema\"\n\nCard [payment: Payment, on: boolean, note?: string]\n\n"
	tests := []struct {
		name    string
		file    string
		source  string
		context bool
		want    string
		detail  string
		kinds   string
	}{
		{name: "Variants after selector", file: "card.ktml", source: header + "Card [payment = |", context: true, want: "Unpaid,Pending,Paid,Failed", detail: "Payment.Unpaid,Payment.Pending [reference: string, since?: string],Payment.Paid [amount: Money, receipt: URL],Payment.Failed [reason: string]", kinds: "variant"},
		{name: "Variant prefix", file: "card.ktml", source: header + "Card [payment = Pa|] {}", context: true, want: "Unpaid,Pending,Paid,Failed"},
		{name: "boolean selector", file: "card.ktml", source: header + "Card [on = |", context: true, want: "true,false", kinds: "variant"},
		{name: "optional selector", file: "card.ktml", source: header + "Card [note = |", context: true, want: "Some,None", kinds: "variant"},
		{name: "qualified Variants", file: "card.ktml", source: header + "Card [payment = Payment.|", context: true, want: "Unpaid,Pending,Paid,Failed"},
		{name: "foreign qualifier", file: "card.ktml", source: header + "Card [payment = Money.|", context: true, want: ""},
		{name: "unknown selector", file: "card.ktml", source: header + "Card [missing = |", context: true, want: ""},
		{name: "payload fields in binding position", file: "card.ktml", source: header + "Card [payment = Pending, |", context: true, want: "reference,since", detail: "string,string", kinds: "field"},
		{name: "bound payload fields are skipped", file: "card.ktml", source: header + "Card [payment = Payment.Paid, receipt, a|] {}", context: true, want: "amount"},
		{name: "Some binds value", file: "card.ktml", source: header + "Card [note = Some, |", context: true, want: "value", detail: "string"},
		{name: "nullary Variant binds nothing", file: "card.ktml", source: header + "Card [payment = Unpaid, |", context: true, want: ""},
		{name: "alias position", file: "card.ktml", source: header + "Card [payment = Paid, amount as |", context: false},
		{name: "clause body roots include bindings", file: "card.ktml", source: header + "Card [payment = Paid, amount as total, receipt] { \"{|", context: true, want: "payment,on,note,total,receipt", detail: "Payment,boolean,string,Money,URL", kinds: "input,input,input,binding,binding"},
		{name: "record binding fields", file: "card.ktml", source: header + "Card [payment = Paid, amount as total] { p { \"{total.|\" } }", context: true, want: "display", detail: "string"},
		{name: "Some binding root", file: "card.ktml", source: header + "Card [note = Some, value as text] { \"{t|", context: true, want: "payment,on,note,text"},
		{name: "default body roots", file: "card.ktml", source: "import { Payment } from \"./payment.ktmlschema\"\n\nCard [payment: Payment] default { \"{|", context: true, want: "payment"},
		{name: "state type position", file: "card.ktml", source: header + "Other [state: |", context: true, want: "string,boolean,number,integer,Money,Payment,content", kinds: "scalar,scalar,scalar,scalar,record,state,content"},
		{name: "payload field type position", file: "payment.ktmlschema", source: "record Money []\nstate Flow {\n\tOpen [at: |", context: true, want: "string,boolean,number,integer,Flow,Money"},
		{name: "import braces offer states", file: "card.ktml", source: "import { | } from \"./payment.ktmlschema\"\n", context: true, want: "Money,Payment", kinds: "record,state"},
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

func TestSemanticTokens_GIVENStatesAndRenderClauses_THENClassifiesClauseSyntax(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "state declaration",
			source: "state Payment {\n\tUnpaid,\n\tPaid [amount?: Money, items: [Money]],\n}\n",
			want:   "keyword:state,type*:Payment,enumMember*:Unpaid,enumMember*:Paid,property*:amount,type:Money,property*:items,type:Money",
		},
		{
			name:   "signature with default and clauses",
			source: "Card [payment: Payment] default { \"{payment}\" }\n\nCard [payment = Payment.Paid, amount as total, receipt] { a href=\"{receipt}\" { \"{total.display}\" } }\n\nCard [payment = Unpaid] {}\n",
			want:   "parameter*:payment,type:Payment,keyword:default,parameter:payment,parameter:payment,type:Payment,enumMember:Paid,property:amount,keyword:as,parameter*:total,property*:receipt,parameter:receipt,parameter:total,property:display,parameter:payment,enumMember:Unpaid",
		},
		{
			name:   "bodyless signature and boolean Pattern",
			source: "Toggle [on: boolean]\n\nToggle [on = true] {}\n",
			want:   "parameter*:on,type:boolean,parameter:on,enumMember:true",
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

func TestBindingOccurrenceAt_GIVENClauseBindings_THENResolvesDeclarationsRootsAndSignatures(t *testing.T) {
	snapshot, source := clauseWorkspace(t, `Card [payment: Payment, note?: string]

Card [payment = Unpaid] {}

Card [payment = Pending, reference, since] { "{reference}" }

Card [payment = Paid, amount as total] { p title="{total.display}" { "{total.display}" } }

Card [payment = Failed] {}
`)
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	file := cardFile(t, snapshot)
	tests := []struct {
		name       string
		marker     string
		root       bool
		signature  string
		references string
	}{
		{name: "unaliased declaration", marker: "reference,", signature: "binding reference: string\nPayment.Pending.reference: string", references: "reference"},
		{name: "optional binding", marker: "since]", signature: "binding since?: string\nPayment.Pending.since?: string", references: ""},
		{name: "alias declaration", marker: "total]", signature: "binding total: Money\nPayment.Paid.amount: Money", references: "total,total"},
		{name: "aliased root", marker: "total.display}\" {", root: true, signature: "binding total: Money\nPayment.Paid.amount: Money", references: "total,total"},
		{name: "unaliased root", marker: "reference}", root: true, signature: "binding reference: string\nPayment.Pending.reference: string", references: "reference"},
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
			var spelled []string
			for _, reference := range snapshot.BindingReferences(occurrence.Binding) {
				spelled = append(spelled, source[reference.Start:reference.End])
			}
			if got := strings.Join(spelled, ","); got != tt.references {
				t.Errorf("references = %q, want %q", got, tt.references)
			}
		})
	}
	if _, ok := snapshot.BindingOccurrenceAt(file, strings.Index(source, "payment: Payment")); ok {
		t.Error("input declaration resolved as a binding")
	}
}
