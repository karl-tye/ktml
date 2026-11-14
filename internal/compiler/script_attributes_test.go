package compiler_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestScriptAttribute_GIVENAttributeNames_WHENClassified_THENMatchesTheCoreList(t *testing.T) {
	script := []string{
		"onclick", "onChange", "ONLOAD", "@click", "@click.outside", "@htmx:after:request.window", ":class", ":disabled",
		"x-data", "x-init", "x-show", "x-show.important", "x-text", "x-html", "x-model", "x-model.number", "x-modelable",
		"x-for", "x-effect", "x-if", "x-id", "x-on:click", "x-on:keydown.enter", "x-bind:value", "x-bind:hx-target",
		"x-trap", "x-trap.noscroll", "x-intersect", "x-intersect.once", "x-anchor", "x-sort", "x-sort:item", "x-mask:dynamic",
		"hx-on", "hx-on:click", "hx-on::after-request", "hx-on:htmx:before:swap", "hx-vals", "hx-vars", "hx-headers",
		"hx-trigger", "data-hx-on:click", "data-hx-vals", "X-DATA",
		"hx-on-click", "hx-on--after-request", "data-hx-on--before-request", "HX-ON-CLICK", "on*", "x-on*:click",
		"hx-trigger*", "hx-vals*",
	}
	plain := []string{
		"class", "id", "href", "value", "open", "x-cloak", "x-ref", "x-transition", "x-transition:enter-start",
		"x-transition.opacity.duration.100ms", "x-teleport", "x-ignore", "x-collapse", "hx-get", "hx-post", "hx-target",
		"hx-swap", "hx-ext:inherited", "hx-indicator:append", "hx-sse:connect", "hx-push-url", "data-x-data", "aria-label",
		"x-datum", "hx-onion", "xmlns:swipe", "data-on", "hx-target-4*", "hx-target-*", "hx-target-error",
	}
	for _, name := range script {
		if !compiler.ScriptAttribute(name) {
			t.Errorf("ScriptAttribute(%q) = false, want true", name)
		}
	}
	for _, name := range plain {
		if compiler.ScriptAttribute(name) {
			t.Errorf("ScriptAttribute(%q) = true, want false", name)
		}
	}
}

const scriptInputs = `Panel [name: string, count: integer, ratio: number, link: URL, open: boolean, handler: TrustedScript, state: JSONValue] {
`

func TestWorkspace_GIVENScriptAttributeValues_WHENTyped_THENAcceptsOnlyTrustedScriptJSONAndBooleans(t *testing.T) {
	valid := []string{
		`button @click="{handler}"`,
		`div x-data="{state}"`,
		`div x-data="{ open: false, label: 'a<b' }"`,
		`button :disabled="{open}"`,
		`div x-show="!{open} && ready"`,
		`div x-init="boot({state}); {handler}"`,
		`a onclick="go()"`,
		`div data-config="{state}"`,
		`div title="{name}" data-count="{count}" href="{link}"`,
		`div hx-vals="{state}" hx-trigger="every 2s"`,
	}
	for _, line := range valid {
		t.Run(line, func(t *testing.T) {
			snapshot, path := gapWorkspace(t, scriptInputs+"\t"+line+"\n}\n")
			if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
				t.Fatalf("diagnostics = %v", diagnostics)
			}
		})
	}
}

func TestWorkspace_GIVENUnsafeScriptAttributeValues_WHENTyped_THENReportsCodesAtThePath(t *testing.T) {
	tests := []struct {
		line, code, span string
	}{
		{line: `button @click="{name}"`, code: "script-attribute-value", span: "{name}"},
		{line: `button @click.prevent="select('{name}')"`, code: "script-attribute-value", span: "{name}"},
		{line: `input :value="{count}"`, code: "script-attribute-value", span: "{count}"},
		{line: `input :value="{ratio}"`, code: "script-attribute-value", span: "{ratio}"},
		{line: `a onclick="go('{name}')"`, code: "script-attribute-value", span: "{name}"},
		{line: `div x-data="{link}"`, code: "script-attribute-value", span: "{link}"},
		{line: `div x-bind:class="{name}"`, code: "script-attribute-value", span: "{name}"},
		{line: `div hx-on::after-request="{name}"`, code: "script-attribute-value", span: "{name}"},
		{line: `div hx-trigger="{name}"`, code: "script-attribute-value", span: "{name}"},
		{line: `div data-hx-vals="{name}"`, code: "script-attribute-value", span: "{name}"},
		{line: `div title="{handler}"`, code: "contextual-type-mismatch", span: "{handler}"},
		{line: `div data-x="a{state}"`, code: "contextual-type-mismatch", span: "{state}"},
		{line: `p "{state}"`, code: "contextual-type-mismatch", span: "{state}"},
	}
	for _, test := range tests {
		t.Run(test.line, func(t *testing.T) {
			component := scriptInputs + "\t" + test.line + "\n}\n"
			snapshot, path := gapWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics = %v, want one %s", diagnostics, test.code)
			}
			source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Fatalf("range = %q, want %q", got, test.span)
			}
		})
	}
}

func TestWorkspace_GIVENAStringInAScriptAttributeOrBody_WHENTyped_THENTheHintNamesKTMLsJSONConstructor(t *testing.T) {
	for _, line := range []string{`button @click="{name}"`, `script "go({name})"`} {
		t.Run(line, func(t *testing.T) {
			snapshot, path := gapWorkspace(t, scriptInputs+"\t"+line+"\n}\n")
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "ktml.JSON") || strings.Contains(diagnostics[0].Message, "TrustJSON") {
				t.Fatalf("diagnostics = %v, want one naming ktml.JSON", diagnostics)
			}
		})
	}
}
