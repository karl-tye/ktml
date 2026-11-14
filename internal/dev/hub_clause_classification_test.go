package dev

import "testing"

const clauseBase = `Card [on: boolean, title: string]
Card [on = true] { div { "{title}" } }
Card [on = false] { span }
`

func TestGetChangedComponent_GIVENClauseGroupEdits_WHENClassified_THENPatchesOnlyCompatibleChanges(t *testing.T) {
	tests := []struct {
		name, updated, want string
	}{
		{name: "clause body edit", updated: "Card [on: boolean, title: string]\nCard [on = true] { div { \"new {title}\" } }\nCard [on = false] { span }\n", want: "Card"},
		{name: "pattern edit", updated: "Card [on: boolean, title: string]\nCard [on = false] { div { \"{title}\" } }\nCard [on = true] { span }\n", want: "Card"},
		{name: "clause replaced by default", updated: "Card [on: boolean, title: string] default { span }\nCard [on = true] { div { \"{title}\" } }\n", want: "Card"},
		{name: "optional input added", updated: "Card [on: boolean, title: string, note?: string]\nCard [on = true] { div { \"{title}\" } }\nCard [on = false] { span }\n", want: "Card"},
		{name: "required input added", updated: "Card [on: boolean, title: string, count: integer]\nCard [on = true] { div { \"{title}\" } }\nCard [on = false] { span }\n", want: ""},
		{name: "selector type changed", updated: "Card [on?: boolean, title: string]\nCard [on = Some] { div { \"{title}\" } }\nCard [on = None] { span }\n", want: ""},
		{name: "unchanged", updated: clauseBase, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, clauseBase), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Errorf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequiresRebuild_GIVENStateSchemaChange_WHENClassified_THENRebuilds(t *testing.T) {
	if !requiresRebuild("billing/payment.ktmlschema") || requiresRebuild("web/payment.ktml") {
		t.Fatal("state schemas must rebuild and component documents must not")
	}
}
