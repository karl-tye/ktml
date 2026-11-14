package dev

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGetChangedComponent_GIVENInputSchemaChanges_WHENClassified_THENOnlyCompatibleChangesPatch(t *testing.T) {
	tests := []struct {
		name        string
		oldSource   string
		newSource   string
		wantPatched string
	}{
		{
			name:      "required addition reloads",
			oldSource: `Card [label: string] { div { "old" } }`,
			newSource: `Card [label: string, count: number] { div { "new" } }`,
		},
		{
			name:      "removal reloads",
			oldSource: `Card [label: string, count: number] { div { "old" } }`,
			newSource: `Card [label: string] { div { "new" } }`,
		},
		{
			name:      "rename reloads",
			oldSource: `Card [label: string] { div { "old" } }`,
			newSource: `Card [title: string] { div { "new" } }`,
		},
		{
			name:      "type change reloads",
			oldSource: `Card [label: string] { div { "old" } }`,
			newSource: `Card [label: number] { div { "new" } }`,
		},
		{
			name:        "optional addition patches",
			oldSource:   `Card [label: string] { div { "old" } }`,
			newSource:   `Card [label: string, badge?: string] { div { "new" } }`,
			wantPatched: "Card",
		},
		{
			name:        "defaulted addition patches",
			oldSource:   `Card [label: string] { div { "old" } }`,
			newSource:   `Card [label: string, badge: string = "new"] { div { "new" } }`,
			wantPatched: "Card",
		},
		{
			name:        "default change patches",
			oldSource:   `Card [badge: string = "old"] { div { "old" } }`,
			newSource:   `Card [badge: string = "new"] { div { "new" } }`,
			wantPatched: "Card",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldDoc := compileHMRDocument(t, test.oldSource)
			newDoc := compileHMRDocument(t, test.newSource)
			if got := getChangedComponent(oldDoc, newDoc); got != test.wantPatched {
				t.Errorf("getChangedComponent() = %q, want %q", got, test.wantPatched)
			}
		})
	}
}

func compileHMRDocument(t *testing.T, source string) *ast.Document {
	t.Helper()
	result := compiler.CompileSyntax("components.ktml", []byte(source))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	return result.Document
}
