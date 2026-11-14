package dev

import (
	"strings"
	"testing"
)

const markupPage = `Page [open: boolean] {
	<!-- shell -->
	doctype html
	html {
		body @click="toggle()" :class="{ on: true }" {
			<!-- menu -->
			p "x"
		}
	}
}
`

func TestGetChangedComponent_GIVENDoctypeCommentAndFrameworkNameEdits_WHENClassified_THENPatchesBodyChanges(t *testing.T) {
	tests := []struct {
		name, updated, want string
	}{
		{name: "unchanged", updated: markupPage, want: ""},
		{name: "comment text edit", updated: strings.Replace(markupPage, "<!-- menu -->", "<!-- main menu -->", 1), want: "Page"},
		{name: "comment removed", updated: strings.Replace(markupPage, "\t<!-- shell -->\n", "", 1), want: "Page"},
		{name: "framework name renamed", updated: strings.Replace(markupPage, "@click=", "@click.prevent=", 1), want: "Page"},
		{name: "framework value edited", updated: strings.Replace(markupPage, "toggle()", "open = !open", 1), want: "Page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, markupPage), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Fatalf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}
