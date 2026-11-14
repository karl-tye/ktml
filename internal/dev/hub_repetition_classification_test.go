package dev

import "testing"

const repetitionList = `List [items: [string], other: [string], title: string] {
	ul {
		for item in items key item {
			li "{item}"
		}
	}
}
`

func TestGetChangedComponent_GIVENKeyedRepetitionEdits_WHENClassified_THENPatchesBodyChangesOnly(t *testing.T) {
	tests := []struct {
		name, updated, want string
	}{
		{name: "unchanged", updated: repetitionList, want: ""},
		{name: "repetition body edit", updated: `List [items: [string], other: [string], title: string] {
	ul {
		for item in items key item {
			li "{item} {title}"
		}
	}
}
`, want: "List"},
		{name: "collection edit", updated: `List [items: [string], other: [string], title: string] {
	ul {
		for item in other key item {
			li "{item}"
		}
	}
}
`, want: "List"},
		{name: "item rename", updated: `List [items: [string], other: [string], title: string] {
	ul {
		for entry in items key entry {
			li "{entry}"
		}
	}
}
`, want: "List"},
		{name: "repetition added", updated: `List [items: [string], other: [string], title: string] {
	ul {
		for item in items key item {
			li "{item}"
		}
		for item in other key item {}
	}
}
`, want: "List"},
		{name: "required input added", updated: `List [items: [string], other: [string], title: string, count: integer] {
	ul {
		for item in items key item {
			li "{item}"
		}
	}
}
`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, repetitionList), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Errorf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}
