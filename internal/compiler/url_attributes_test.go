package compiler_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestURLAttribute_GIVENNames_WHENClassified_THENMatchesURLAttributesOnly(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"href", true}, {"HREF", true}, {"src", true}, {"action", true}, {"formaction", true},
		{"srcset", true}, {"poster", true}, {"data", true}, {"xlink:href", true},
		{"hx-get", true}, {"data-hx-post", true}, {"HX-DELETE", true},
		{"title", false}, {"data-href", false}, {"hx-push-url", false}, {"hx-target", false},
		{"class", false}, {"onclick", false},
	}
	for _, test := range tests {
		if got := compiler.URLAttribute(test.name); got != test.want {
			t.Errorf("URLAttribute(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestWorkspace_GIVENURLValuesInURLAttributes_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Link [url: URL, trusted: TrustedURL, on: boolean, label: string, count: integer, ratio: number] {
	a href="{url}" label
	a (HREF="{trusted}" if on { title="x" })
	form (action="{url}" if on { hx-post="{url}" })
	img src="/static/logo.png"
	a href="javascript:void(0)" "static"
	button hx-get="{trusted}" "go"
	a data-href="{label}" title="{label}" "plain"
	a href="/jobs/{label}/edit?n={count}#{ratio}" "relative path"
	a href="?q={label}" "query"
	a href="#{label}" "fragment"
	a href="HTTPS://example.com/{label}" "absolute"
	a href="https://example.com?q={label}" "absolute query"
	a href="mailto:{label}" "mail"
	a href="tel:{count}" "phone"
	button hx-put="/enquiries/{count}/status" "put"
	img src="/static/app.js?v={label}"
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENInvalidURLValues_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "string in href", body: `a href="{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "string in form action", body: `form action="{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "string in img src", body: `img src="{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "string in hx-get", body: `button hx-get="{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "upper-case name", body: `a HREF="{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "integer in href", body: `a href="{count}"`, code: "url-attribute-value", span: "{count}"},
		{name: "boolean in href", body: `a href="{on}"`, code: "url-attribute-value", span: "{on}"},
		{name: "json in href", body: `a href="{data}"`, code: "url-attribute-value", span: "{data}"},
		{name: "string before text", body: `a href="{label}/x"`, code: "url-attribute-value", span: "{label}"},
		{name: "scheme from a path", body: `a href="{label}:x"`, code: "url-attribute-value", span: "{label}"},
		{name: "host from a path", body: `a href="/{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "protocol-relative host", body: `a href="//{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "backslash host", body: `a href="/\\{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "tab before host", body: `a href="/\t/{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "host not reached", body: `a href="https://{label}/x"`, code: "url-attribute-value", span: "{label}"},
		{name: "partial host", body: `a href="https://example.com{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "unsafe scheme prefix", body: `a href="javascript:{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "leading space", body: `a href=" /x/{label}"`, code: "url-attribute-value", span: "{label}"},
		{name: "boolean after a safe prefix", body: `a href="/x/{on}"`, code: "url-attribute-value", span: "{on}"},
		{name: "json after a safe prefix", body: `a href="/x/{data}"`, code: "url-attribute-value", span: "{data}"},
		{name: "second path after a safe prefix", body: `a href="/x/{label}/{on}"`, code: "url-attribute-value", span: "{on}"},
		{name: "URL among text", body: `a href="/x/{url}"`, code: "contextual-type-mismatch", span: "{url}"},
		{name: "string in conditional href", body: `a (if on { href="{label}" })`, code: "url-attribute-value", span: "{label}"},
		{name: "string in a bag href", body: `Field label="x" extra=(href="{label}")`, code: "url-attribute-value", span: "{label}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := forwardingComponents + "Page [on: boolean, label: string, count: integer, url: URL, data: JSONValue] {\n\t" + test.body + "\n}\n"
			snapshot, path := gapWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics = %v, want one %s", codes(diagnostics), test.code)
			}
			source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Fatalf("range = %q, want %q", got, test.span)
			}
		})
	}
}

func TestWorkspace_GIVENAPathInSrcdoc_WHENLoaded_THENReportsAnHTMLAttributeValue(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{name: "string", body: `iframe srcdoc="{label}"`},
		{name: "text around a string", body: `iframe srcdoc="<p>{label}</p>"`},
		{name: "upper-case name", body: `iframe SRCDOC="{label}"`},
		{name: "integer", body: `iframe srcdoc="{count}"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, path := gapWorkspace(t, "Frame [label: string, count: integer] {\n\t"+test.body+"\n}\n")
			if got := codes(snapshot.Diagnostics(path)); len(got) != 1 || got[0] != "error:html-attribute-value" {
				t.Fatalf("diagnostics = %v, want [error:html-attribute-value]", got)
			}
		})
	}
}

func TestWorkspace_GIVENLiteralSrcdoc_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, "Frame {\n\tiframe srcdoc=\"<p>static</p>\"\n}\n")
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}
