package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const paymentSchema = `record Money [
	display: string,
]

state Payment {
	Unpaid,
	Pending [reference: string, since?: string],
	Paid [amount: Money, receipt: URL],
	Failed [reason: string],
}
`

func clauseWorkspace(t *testing.T, component string) (*compiler.Snapshot, string) {
	t.Helper()
	source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
	dir := writeWorkspace(t, map[string]string{"payment.ktmlschema": paymentSchema, "card.ktml": source})
	return loadEntry(t, dir, "card.ktml"), source
}

func codes(diagnostics []compiler.Diagnostic) []string {
	var result []string
	for _, diagnostic := range diagnostics {
		result = append(result, string(diagnostic.Severity)+":"+diagnostic.Code)
	}
	return result
}

func TestWorkspace_GIVENExhaustiveStateClauses_WHENLoaded_THENGroupsWithTypedBindings(t *testing.T) {
	snapshot, _ := clauseWorkspace(t, `Card [payment: Payment, title: string]

Card [payment = Unpaid] { "{title}" }

Card [payment = Payment.Pending, reference] { "{reference}" }

Card [payment = Paid, amount as total, receipt] { a href="{receipt}" { "{total.display}" } }

Card [payment = Failed, reason] { "{title}: {reason}" }
`)
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	card, ok := snapshot.Resolve(filepath.Join(filepath.Dir(snapshotFile(t, snapshot)), "card.ktml"), "Card")
	if !ok {
		t.Fatal("Card not resolved")
	}
	group, ok := snapshot.ClauseGroup(card.ID)
	if !ok || group.Kind != compiler.StateSelector || group.Selector.Name != "payment" || group.HasDefault || len(group.Clauses) != 4 {
		t.Fatalf("group = %+v", group)
	}
	if strings.Join(group.Cases, ",") != "Unpaid,Pending,Paid,Failed" {
		t.Errorf("cases = %v", group.Cases)
	}
	paid := group.Clauses[2]
	if paid.Case != "Paid" || len(paid.Bindings) != 2 {
		t.Fatalf("paid = %+v", paid)
	}
	if total := paid.Bindings[0]; total.Name != "total" || total.Field != "amount" || total.Type.Kind != compiler.RecordTypeKind || total.Type.Name != "Money" {
		t.Errorf("total binding = %+v", total)
	}
	if receipt := paid.Bindings[1]; receipt.Type.Kind != compiler.ContextualTypeKind || receipt.Type.Name != compiler.URLType {
		t.Errorf("receipt binding = %+v", receipt)
	}
	for _, diagnostic := range snapshot.Diagnostics(card.File) {
		t.Errorf("unexpected diagnostic %v", diagnostic)
	}
}

// snapshotFile returns one loaded component document for directory lookups.
func snapshotFile(t *testing.T, snapshot *compiler.Snapshot) string {
	t.Helper()
	for _, component := range snapshot.Components() {
		return component.File
	}
	t.Fatal("no components")
	return ""
}

func TestWorkspace_GIVENDefaultBodies_WHENLoaded_THENWarnsAboutFallthroughAndUnreachableDefaults(t *testing.T) {
	tests := []struct {
		name, component, code, message string
	}{
		{
			name:      "fallthrough lists residual cases in declaration order",
			component: "Card [payment: Payment] default { \"other\" }\nCard [payment = Pending] { \"p\" }",
			code:      "default-fallthrough", message: "Unpaid, Paid, Failed",
		},
		{
			name:      "complete coverage makes default unreachable",
			component: "Card [on: boolean = false] default { \"x\" }\nCard [on = true] { \"t\" }\nCard [on = false] { \"f\" }",
			code:      "unreachable-default", message: "unreachable",
		},
		{
			name:      "optional fallthrough",
			component: "Card [note?: string] default { \"none\" }\nCard [note = Some, value as text] { \"{text}\" }",
			code:      "default-fallthrough", message: "None",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot, source := clauseWorkspace(t, tt.component)
			if !snapshot.Valid() {
				t.Fatalf("snapshot invalid: %v", snapshot.Err())
			}
			diagnostics := snapshot.Diagnostics(snapshotFile(t, snapshot))
			start := strings.Index(source, "default")
			if len(diagnostics) != 1 || diagnostics[0].Code != tt.code || diagnostics[0].Severity != compiler.SeverityWarning || diagnostics[0].Start != start || diagnostics[0].End != start+len("default") || !strings.Contains(diagnostics[0].Message, tt.message) {
				t.Fatalf("diagnostics = %#v, want one %s warning containing %q", diagnostics, tt.code, tt.message)
			}
		})
	}
}

func TestWorkspace_GIVENInvalidClauseGroups_WHENLoaded_THENReportsExactDiagnostics(t *testing.T) {
	exhaustive := "Card [payment = Unpaid] {}\nCard [payment = Pending] {}\nCard [payment = Paid] {}\nCard [payment = Failed] {}\n"
	tests := []struct {
		name, component, code, marker string
		occurrence                    int
	}{
		{name: "gap in bodyless signature", component: "Card [payment: Payment]\nCard [payment = Unpaid] {}", code: "non-exhaustive-clauses", marker: "Card"},
		{name: "overlap", component: "Card [payment: Payment] default {}\nCard [payment = Paid] {}\nCard [payment = Payment.Paid] {}", code: "overlapping-clause", marker: "Payment.Paid"},
		{name: "unknown variant", component: "Card [payment: Payment] default {}\nCard [payment = Refunded] {}", code: "unknown-variant", marker: "Refunded"},
		{name: "wrong qualifier", component: "Card [payment: Payment] default {}\nCard [payment = Money.Paid] {}", code: "pattern-type-mismatch", marker: "Money.Paid"},
		{name: "boolean pattern on state", component: "Card [payment: Payment] default {}\nCard [payment = true] {}", code: "pattern-type-mismatch", marker: "true", occurrence: 0},
		{name: "variant pattern on boolean", component: "Card [on: boolean] default {}\nCard [on = Paid] {}", code: "pattern-type-mismatch", marker: "Paid"},
		{name: "invalid selector type", component: "Card [title: string] default {}\nCard [title = Some] {}", code: "invalid-selector-type", marker: "title", occurrence: 1},
		{name: "unknown selector", component: "Card [payment: Payment] default {}\nCard [state = Paid] {}", code: "unknown-selector", marker: "state"},
		{name: "mixed selector", component: "Card [payment: Payment, on: boolean] default {}\nCard [payment = Paid] {}\nCard [on = true] {}", code: "mixed-selector", marker: "on", occurrence: 2},
		{name: "missing clauses", component: "Card [payment: Payment]\nOther [] {}", code: "missing-clauses", marker: "Card"},
		{name: "missing clauses after default", component: "Card [payment: Payment] default {}", code: "missing-clauses", marker: "Card"},
		{name: "clause without signature", component: "Card [payment = Paid] {}", code: "clause-without-signature", marker: "Card"},
		{name: "clause after ordinary component", component: "Card [payment: Payment] { div }\nCard [payment = Paid] {}", code: "clause-after-component", marker: "Card", occurrence: 1},
		{name: "noncontiguous clause", component: "Card [payment: Payment] default {}\nCard [payment = Paid] {}\nOther [] {}\nCard [payment = Failed] {}", code: "noncontiguous-clause", marker: "Card", occurrence: 2},
		{name: "unknown binding", component: "Card [payment: Payment] default {}\nCard [payment = Failed, message] {}", code: "unknown-binding", marker: "message"},
		{name: "binding on nullary", component: "Card [payment: Payment] default {}\nCard [payment = Unpaid, reason] {}", code: "unexpected-binding", marker: "reason"},
		{name: "binding on None", component: "Card [note?: string] default {}\nCard [note = None, value] {}", code: "unexpected-binding", marker: "value"},
		{name: "duplicate binding", component: "Card [payment: Payment] default {}\nCard [payment = Failed, reason, reason as why] {}", code: "duplicate-binding", marker: "reason as why"},
		{name: "shadowing binding", component: "Card [payment: Payment, reason: string] default {}\nCard [payment = Failed, reason] {}", code: "shadowing-binding", marker: "reason", occurrence: 1},
		{name: "children outlet in clause", component: "Card [on: boolean]\nCard [on = true] { children }\nCard [on = false] {}", code: "undeclared-content", marker: "children", occurrence: 0},
		{name: "unbound payload field is not in scope", component: "Card [payment: Payment] default {}\nCard [payment = Failed] { \"{reason}\" }", code: "unknown-path-root", marker: "reason"},
		{name: "optional payload binding cannot be read", component: "Card [payment: Payment] default {}\nCard [payment = Pending, since] { \"{since}\" }", code: "optional-value-access", marker: "since", occurrence: 1},
		{name: "state field access", component: "Card [payment: Payment] { \"{payment.reason}\" }", code: "state-field-access", marker: "reason"},
		{name: "state rendered as text", component: "Card [payment: Payment] { \"{payment}\" }", code: "path-type-mismatch", marker: "{payment}"},
		{name: "URL rendered as text", component: "Card [payment: Payment] default {}\nCard [payment = Paid, receipt] { \"{receipt}\" }", code: "contextual-type-mismatch", marker: "{receipt}"},
		{name: "URL in mixed attribute", component: "Card [payment: Payment] default {}\nCard [payment = Paid, receipt] { a href=\"/x{receipt}\" }", code: "contextual-type-mismatch", marker: "{receipt}"},
		{name: "duplicate component group", component: "Card [payment: Payment]\n" + exhaustive + "Card [payment: Payment] { div }", code: "duplicate-component", marker: "Card"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot, source := clauseWorkspace(t, tt.component)
			diagnostics := snapshot.Diagnostics(cardFile(t, snapshot))
			start := nthIndex(source, tt.marker, tt.occurrence)
			var errors []string
			found := false
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == compiler.SeverityError {
					errors = append(errors, diagnostic.Code)
				}
				if diagnostic.Code == tt.code && diagnostic.Severity == compiler.SeverityError && diagnostic.Start == start && diagnostic.End == start+len(tt.marker) {
					found = true
				}
			}
			if !found || len(errors) != 1 {
				t.Fatalf("diagnostics = %v %#v, want exactly one error %s at %d-%d", codes(diagnostics), diagnostics, tt.code, start, start+len(tt.marker))
			}
		})
	}
}

// cardFile returns the loaded card.ktml path, which may declare no component.
func cardFile(t *testing.T, snapshot *compiler.Snapshot) string {
	t.Helper()
	for _, component := range snapshot.Components() {
		path := filepath.Join(filepath.Dir(component.File), "card.ktml")
		if snapshot.HasFile(path) {
			return path
		}
	}
	for _, record := range snapshot.Records() {
		return filepath.Join(filepath.Dir(record.File), "card.ktml")
	}
	t.Fatal("card.ktml not loaded")
	return ""
}

func nthIndex(source, marker string, occurrence int) int {
	offset := 0
	for i := 0; ; i++ {
		index := strings.Index(source[offset:], marker)
		if index < 0 {
			return -1
		}
		if i == occurrence {
			return offset + index
		}
		offset += index + len(marker)
	}
}

func TestWorkspace_GIVENInvalidStates_WHENLoaded_THENReportsExactDiagnostics(t *testing.T) {
	tests := []struct {
		name, schema, code, marker string
		occurrence                 int
	}{
		{name: "state outside schema", schema: "", code: "state-outside-schema", marker: "state S"},
		{name: "empty state", schema: "state S {}", code: "empty-state", marker: "S"},
		{name: "empty payload", schema: "state S { A [] }", code: "empty-variant-payload", marker: "[]"},
		{name: "invalid variant name", schema: "state S { loading }", code: "invalid-variant-name", marker: "loading"},
		{name: "duplicate variant", schema: "state S { A, A }", code: "duplicate-variant", marker: "A", occurrence: 1},
		{name: "duplicate type", schema: "record S []\nstate S { A }", code: "duplicate-record", marker: "S", occurrence: 1},
		{name: "reserved name", schema: "state URL { A }", code: "reserved-type-name", marker: "URL"},
		{name: "recursive state", schema: "state S { A [next: S] }", code: "recursive-state", marker: "S", occurrence: 1},
		{name: "recursive through record", schema: "record R [s: S]\nstate S { A [items: [R]] }", code: "recursive-record", marker: "[R]"},
		{name: "duplicate payload field", schema: "state S { A [x: string, x: string] }", code: "duplicate-field", marker: "x", occurrence: 1},
		{name: "unknown payload type", schema: "state S { A [x: Nope] }", code: "unknown-type", marker: "Nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"a.ktmlschema": tt.schema}
			entry := "a.ktmlschema"
			source := tt.schema
			if tt.schema == "" {
				source = "state S { A }\nCard [] { div }"
				files = map[string]string{"a.ktml": source}
				entry = "a.ktml"
			}
			dir := writeWorkspace(t, files)
			diagnostics := loadEntry(t, dir, entry).Diagnostics(filepath.Join(dir, entry))
			start := nthIndex(source, tt.marker, tt.occurrence)
			if len(diagnostics) != 1 || diagnostics[0].Code != tt.code || diagnostics[0].Start != start || diagnostics[0].End != start+len(tt.marker) {
				t.Fatalf("diagnostics = %#v, want one %s at %d-%d", diagnostics, tt.code, start, start+len(tt.marker))
			}
		})
	}
}

func TestWorkspace_GIVENStateInputs_WHENPassedAndAliased_THENTypesNominallyAndResolvesAliasedQualifiers(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"payment.ktmlschema": paymentSchema,
		"card.ktml": `import { Payment as P } from "./payment.ktmlschema"
import { Badge } from "./badge.ktml"

Card [payment: P, on: boolean]

Card [on = true] { Badge payment="{payment}" }

Card [on = false] { Badge payment="{payment}" }
`,
		"badge.ktml": `import { Payment } from "./payment.ktmlschema"

Badge [payment: Payment] default { "?" }

Badge [payment = Payment.Paid] { "paid" }
`,
	})
	snapshot := loadEntry(t, dir, "card.ktml")
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	aliased := writeWorkspace(t, map[string]string{
		"payment.ktmlschema": paymentSchema,
		"card.ktml": `import { Payment as P } from "./payment.ktmlschema"
Card [payment: P] default {}
Card [payment = P.Paid] {}
Card [payment = Payment.Failed] {}
`,
	})
	diagnostics := loadEntry(t, aliased, "card.ktml").Diagnostics(filepath.Join(aliased, "card.ktml"))
	var errors []string
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == compiler.SeverityError {
			errors = append(errors, diagnostic.Code)
		}
	}
	if strings.Join(errors, ",") != "pattern-type-mismatch" {
		t.Fatalf("diagnostics = %#v, want only the unaliased qualifier rejected", diagnostics)
	}
}

func TestWorkspace_GIVENURLInputs_WHENCalledWithLiteralOrString_THENRejectsWithoutCasts(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"payment.ktmlschema": paymentSchema,
		"card.ktml": `import { Payment } from "./payment.ktmlschema"

Link [href: URL, label: string] { a href="{href}" { "{label}" } }

Card [payment: Payment] default {}

Card [payment = Paid, receipt] {
	Link href="{receipt}" label="Receipt" {}
	Link href="https://example.com" label="x" {}
	Link href="{receipt}" label="{receipt}" {}
}
`,
	})
	diagnostics := loadEntry(t, dir, "card.ktml").Diagnostics(filepath.Join(dir, "card.ktml"))
	got := strings.Join(codes(diagnostics), ",")
	if got != "warning:default-fallthrough,error:input-type-mismatch,error:contextual-type-mismatch" {
		t.Fatalf("diagnostics = %s\n%#v", got, diagnostics)
	}
}

func TestSnapshot_GIVENPatternsAndBindings_WHENQueried_THENReferencesBindToSymbols(t *testing.T) {
	snapshot, source := clauseWorkspace(t, `Card [payment: Payment]

Card [payment = Unpaid] {}

Card [payment = Payment.Pending, reference] { "{reference}" }

Card [payment = Paid, receipt as link] { a href="{link}" }

Card [payment = Failed, reason] {}
`)
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	file := snapshotFile(t, snapshot)
	symbol, _, ok := snapshot.SchemaSymbolAt(file, strings.Index(source, "Pending"))
	if !ok || symbol.Kind != compiler.VariantSymbol || symbol.Name != "Pending" {
		t.Fatalf("symbol at pattern = %+v %v", symbol, ok)
	}
	references := snapshot.References(symbol.ID)
	if len(references) != 1 || references[0].Kind != compiler.PatternReference || references[0].Range.File != file {
		t.Errorf("variant references = %+v", references)
	}
	field, _, ok := snapshot.SchemaSymbolAt(file, strings.Index(source, "reference]"))
	if !ok || field.Kind != compiler.FieldSymbol || field.Name != "reference" {
		t.Fatalf("symbol at binding = %+v %v", field, ok)
	}
	if references := snapshot.References(field.ID); len(references) != 2 {
		t.Errorf("payload field references = %+v, want header binding and body root", references)
	}
	receipt, _, ok := snapshot.SchemaSymbolAt(file, strings.Index(source, "receipt as"))
	if !ok || receipt.Name != "receipt" {
		t.Fatalf("symbol at aliased binding = %+v %v", receipt, ok)
	}
	if references := snapshot.References(receipt.ID); len(references) != 1 {
		t.Errorf("aliased payload field references = %+v, want header binding only", references)
	}
	binding, ok := snapshot.BindingAt(file, strings.Index(source, "link]"))
	if !ok || binding.Name != "link" || binding.Field != "receipt" {
		t.Errorf("binding = %+v %v", binding, ok)
	}
	ranges, err := snapshot.SchemaRename(field.ID, "ref")
	if err != nil || len(ranges) != 3 {
		t.Errorf("SchemaRename() = %+v, %v; want declaration, binding, and body root", ranges, err)
	}
}

func TestSnapshot_GIVENStateSymbols_WHENRenamed_THENValidatesAndCollectsEveryOccurrence(t *testing.T) {
	snapshot, source := clauseWorkspace(t, `Card [payment: Payment] default {}

Card [payment = Payment.Paid] {}
`)
	if err := snapshot.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	file := cardFile(t, snapshot)
	variant, _, ok := snapshot.SchemaSymbolAt(file, strings.Index(source, "Paid]"))
	if !ok || variant.Kind != compiler.VariantSymbol {
		t.Fatalf("variant = %+v", variant)
	}
	if ranges, err := snapshot.SchemaRename(variant.ID, "Settled"); err != nil || len(ranges) != 2 {
		t.Errorf("rename Variant = %+v, %v; want declaration and Pattern", ranges, err)
	}
	if _, err := snapshot.SchemaRename(variant.ID, "Failed"); err == nil {
		t.Error("rename Variant onto an existing Variant succeeded")
	}
	state, _, ok := snapshot.SchemaSymbolAt(file, strings.Index(source, "Payment.Paid"))
	if !ok || state.Kind != compiler.StateSymbol {
		t.Fatalf("state = %+v", state)
	}
	// Declaration, import specifier, input type, and Pattern qualifier.
	if ranges, err := snapshot.SchemaRename(state.ID, "Charge"); err != nil || len(ranges) != 4 {
		t.Errorf("rename state = %+v, %v", ranges, err)
	}
	if _, err := snapshot.SchemaRename(state.ID, "Money"); err == nil {
		t.Error("rename state onto a record name succeeded")
	}
	field, _, ok := snapshot.SchemaSymbolAt(filepath.Join(filepath.Dir(file), "payment.ktmlschema"), strings.Index(paymentSchema, "reason"))
	if !ok || field.Kind != compiler.FieldSymbol {
		t.Fatalf("payload field = %+v", field)
	}
	if _, err := snapshot.SchemaRename(field.ID, "reason"); err != nil {
		t.Errorf("rename payload field to itself = %v", err)
	}
}
