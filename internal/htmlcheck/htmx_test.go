package htmlcheck_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/htmlcheck"
)

// analyzeWith analyzes one Page body with options.
func analyzeWith(t *testing.T, body string, options htmlcheck.Options) (string, []htmlcheck.Diagnostic) {
	t.Helper()
	source := "Page [] {\n\t" + strings.ReplaceAll(body, "\n", "\n\t") + "\n}\n"
	fixture := loadSnapshot(t, source)
	for _, diagnostic := range fixture.snapshot.Diagnostics(fixture.path) {
		if diagnostic.Severity == compiler.SeverityError {
			t.Fatalf("compiler diagnostic in fixture: %v", diagnostic)
		}
	}
	return source, htmlcheck.AnalyzeWith(fixture.snapshot, fixture.path, options)
}

func TestAnalyze_GIVENUnknownHTMXAttribute_WHENAnalyzed_THENReportsItWithASuggestion(t *testing.T) {
	tests := []struct{ name, body, want, fixed string }{
		{"misspelt trigger", `div hx-tigger="click"`, "html-unknown-attribute:hx-tigger", `div hx-trigger="click"`},
		{"data- prefixed", `div data-hx-tigger="click"`, "html-unknown-attribute:data-hx-tigger", `div data-hx-trigger="click"`},
		{"htmx 4 modifier on a typo", `div hx-indicatr:inherited="#spinner"`, "html-unknown-attribute:hx-indicatr:inherited", `div hx-indicator:inherited="#spinner"`},
		{"unknown name", `div hx-require="#form"`, "html-unknown-attribute:hx-require", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, diagnostics := analyzeWith(t, test.body, htmlcheck.Options{})
			if got := strings.Join(spelled(source, diagnostics), ","); got != test.want {
				t.Fatalf("diagnostics = %s, want %s", got, test.want)
			}
			if diagnostics[0].Severity != htmlcheck.Warning {
				t.Errorf("severity = %d, want a warning", diagnostics[0].Severity)
			}
			if test.fixed == "" {
				if len(diagnostics[0].Fixes) != 0 {
					t.Errorf("fixes = %v, want none", diagnostics[0].Fixes)
				}
				return
			}
			if len(diagnostics[0].Fixes) != 1 || !strings.Contains(diagnostics[0].Message, "did you mean") {
				t.Fatalf("diagnostic = %+v, want one did-you-mean fix", diagnostics[0])
			}
			edit := diagnostics[0].Fixes[0].Edits[0]
			edited := source[:edit.Start] + edit.Text + source[edit.End:]
			if want := "Page [] {\n\t" + test.fixed + "\n}\n"; edited != want {
				t.Errorf("fixed = %q, want %q", edited, want)
			}
		})
	}
}

func TestAnalyze_GIVENHTMX2And4Attributes_WHENAnalyzed_THENStaysSilent(t *testing.T) {
	names := []string{
		"hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete",
		"hx-trigger", "hx-target", "hx-swap", "hx-swap-oob", "hx-select", "hx-select-oob",
		"hx-include", "hx-indicator", "hx-params", "hx-encoding", "hx-sync", "hx-boost",
		"hx-push-url", "hx-replace-url", "hx-confirm", "hx-prompt", "hx-history", "hx-history-elt",
		"hx-request", "hx-validate", "hx-headers", "hx-vals", "hx-vars", "hx-ext", "hx-inherit",
		"hx-disinherit", "hx-disable", "hx-disabled-elt", "hx-preserve", "hx-action", "hx-method",
		"hx-config", "hx-optimistic", "hx-partial", "hx-ignore", "hx-preload", "hx-alpine-compat",
		"hx-targets", "hx-sse", "hx-ws",
		// htmx 4 modifiers.
		"hx-indicator:append", "hx-indicator:inherited", "hx-ext:inherited", "hx-target:inherited",
		"hx-sse:connect", "hx-sse:close", "hx-ws:connect", "hx-status:404", "hx-status:5xx",
		// hx-on forms.
		"hx-on", "hx-on:click", "hx-on::after-request", "hx-on:htmx:before:swap", "hx-on-click", "hx-on--after-request",
		// response-targets.
		"hx-target-404", "hx-target-4*", "hx-target-5xx", "hx-target-error",
		// data- prefix and case.
		"data-hx-get", "data-hx-on:click", "data-hx-indicator:inherited", "HX-Get",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			source, diagnostics := analyzeWith(t, `div `+name+`="x"`, htmlcheck.Options{})
			if got := spelled(source, diagnostics); len(got) != 0 {
				t.Errorf("diagnostics = %v, want none", got)
			}
		})
	}
}

func TestAnalyze_GIVENDeclaredExtensionAttribute_WHENAnalyzed_THENStaysSilent(t *testing.T) {
	options := htmlcheck.Options{ExtensionAttributes: []string{"hx-require"}}
	for _, name := range []string{"hx-require", "data-hx-require", "hx-require:inherited", "hx-require:append"} {
		t.Run(name, func(t *testing.T) {
			source, diagnostics := analyzeWith(t, `div `+name+`="#form"`, options)
			if got := spelled(source, diagnostics); len(got) != 0 {
				t.Errorf("diagnostics = %v, want none", got)
			}
		})
	}
	source, diagnostics := analyzeWith(t, `div hx-requir="#form"`, options)
	if got := strings.Join(spelled(source, diagnostics), ","); got != "html-unknown-attribute:hx-requir" || !strings.Contains(diagnostics[0].Message, "did you mean hx-require?") {
		t.Errorf("a typo of a declared attribute: %s %v", got, diagnostics)
	}
}

// appHTMXNames is every distinct hx-* name a large production application
// uses, from
//
//	grep -rhoE '\b(data-)?hx-[a-z0-9:*-]+' --include=*.ktml --include=*.templ internal | sort -u
//
// run in the application. hx-require is its own extension attribute.
var appHTMXNames = []string{
	"hx-alpine-compat", "hx-boost", "hx-confirm", "hx-delete", "hx-disable", "hx-disabled-elt",
	"hx-encoding", "hx-ext:inherited", "hx-get", "hx-include", "hx-indicator", "hx-indicator:append",
	"hx-indicator:inherited", "hx-on", "hx-on::after-request", "hx-on::before:request",
	"hx-on::before-swap", "hx-on:htmx:before:swap", "hx-params", "hx-post", "hx-preload",
	"hx-preserve", "hx-push-url", "hx-put", "hx-require", "hx-select", "hx-sse", "hx-sse:connect",
	"hx-swap", "hx-swap-oob", "hx-sync", "hx-target", "hx-target-4*", "hx-target-error",
	"hx-targets", "hx-trigger", "hx-vals",
}

func TestAnalyze_GIVENTheHTMXNamesAnApplicationUses_WHENAnalyzed_THENOnlyItsUndeclaredExtensionIsReported(t *testing.T) {
	var body strings.Builder
	for _, name := range appHTMXNames {
		body.WriteString("div " + name + "=\"x\"\n")
	}
	source, diagnostics := analyzeWith(t, body.String(), htmlcheck.Options{})
	if got := strings.Join(spelled(source, diagnostics), ","); got != "html-unknown-attribute:hx-require" {
		t.Errorf("without configuration: %s, want only hx-require", got)
	}
	source, diagnostics = analyzeWith(t, body.String(), htmlcheck.Options{ExtensionAttributes: []string{"hx-require"}})
	if got := spelled(source, diagnostics); len(got) != 0 {
		t.Errorf("with hx-require declared: %v, want none", got)
	}
}
