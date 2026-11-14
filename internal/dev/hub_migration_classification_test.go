package dev

import (
	"strings"
	"testing"
)

const rawTextPage = "Page [open: boolean] {\n\tscript ```go(1)```\n\tstyle ```a{}```\n\tp \"x\"\n}\n"

func TestGetChangedComponent_GIVENRawTextEdits_WHENClassified_THENPatchesOnlyRealChanges(t *testing.T) {
	tests := []struct {
		name, updated, want string
	}{
		{name: "unchanged", updated: rawTextPage, want: ""},
		{name: "script edited", updated: strings.Replace(rawTextPage, "go(1)", "go(2)", 1), want: "Page"},
		{name: "style edited", updated: strings.Replace(rawTextPage, "a{}", "a{b:c}", 1), want: "Page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, rawTextPage), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Fatalf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}

const conditionalPage = "Page [on: boolean, off: boolean] {\n\ta (href=\"/x\" if on { title=\"a\" } else { title=\"b\" }) \"x\"\n}\n"

func TestGetChangedComponent_GIVENConditionalAttributeEdits_WHENClassified_THENPatchesEveryChange(t *testing.T) {
	tests := []struct {
		name, updated, want string
	}{
		{name: "unchanged", updated: conditionalPage, want: ""},
		{name: "condition edited", updated: strings.Replace(conditionalPage, "if on", "if off", 1), want: "Page"},
		{name: "then branch edited", updated: strings.Replace(conditionalPage, `title="a"`, `title="c"`, 1), want: "Page"},
		{name: "else branch edited", updated: strings.Replace(conditionalPage, `title="b"`, `title="d"`, 1), want: "Page"},
		{name: "else removed", updated: strings.Replace(conditionalPage, ` else { title="b" }`, "", 1), want: "Page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, conditionalPage), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Fatalf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}

const forwardingPage = "Page [on: boolean, attrs: Attributes] {\n\tinput type=\"text\" ...attrs\n\tField attrs=(required if on { disabled })\n}\n"

func TestGetChangedComponent_GIVENSpreadAndBagEdits_WHENClassified_THENPatchesEveryChange(t *testing.T) {
	tests := []struct {
		name, updated, want string
	}{
		{name: "unchanged", updated: forwardingPage, want: ""},
		{name: "spread path edited", updated: strings.Replace(forwardingPage, "...attrs", "...other", 1), want: "Page"},
		{name: "bag entry edited", updated: strings.Replace(forwardingPage, "(required", "(hidden", 1), want: "Page"},
		{name: "bag condition edited", updated: strings.Replace(forwardingPage, "if on", "if !on", 1), want: "Page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getChangedComponent(compileHMRDocument(t, forwardingPage), compileHMRDocument(t, tt.updated))
			if got != tt.want {
				t.Fatalf("getChangedComponent() = %q, want %q", got, tt.want)
			}
		})
	}
}
