package gohtml

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerateChecked_GIVENUnsupportedMarkup_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{name: "doctype", source: "Page [] {\n\tdoctype html\n\thtml\n}\n", want: "doctype"},
		{name: "html comment", source: "Page [] {\n\t<!-- note -->\n\tdiv\n}\n", want: "HTML comment"},
		{name: "nested html comment", source: "Page [] {\n\tdiv {\n\t\t<!-- note -->\n\t}\n}\n", want: "HTML comment"},
		{name: "script body", source: "Page [] {\n\tscript \"if (a && b) { }\"\n}\n", want: "script content is raw text"},
		{name: "nested style body", source: "Page [] {\n\tdiv {\n\t\tstyle { \"a > b { }\" }\n\t}\n}\n", want: "style content is raw text"},
		{name: "script raw text", source: "Page [] {\n\tscript ```a && b```\n}\n", want: "script content is raw text"},
		{name: "conditional attributes", source: "Page [on: boolean] {\n\ta (if on { title=\"x\" })\n}\n", want: "conditional attributes"},
		{name: "attribute spread", source: "Page [] {\n\tdiv ...attrs\n}\n", want: "attribute spreads"},
		{name: "attribute bag", source: "Page [] {\n\tField attrs=(required)\n}\n", want: "attribute bags"},
		{name: "interpolated style attribute", source: "Page [w: string] {\n\tdiv style=\"width: {w}\"\n}\n", want: "style attribute style"},
		{name: "interpolated script attribute", source: "Page [name: string] {\n\tbutton @click=\"go('{name}')\"\n}\n", want: "script attribute @click"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := compiler.CompileSyntax("page.ktml", []byte(test.source))
			if result.HasErrors() {
				t.Fatalf("diagnostics = %#v", result.Diagnostics)
			}
			written := false
			output, err := GenerateChecked(result.Document, false, "page.ktml", func(string, []byte) { written = true })
			if err == nil || !strings.Contains(err.Error(), test.want) || output != "" || written {
				t.Fatalf("GenerateChecked() = %q, %v (wrote %v); want an error naming %s and no output", output, err, written, test.want)
			}
		})
	}
}

func TestGenerateChecked_GIVENLiteralFrameworkAttributes_WHENGenerated_THENPassesThemThrough(t *testing.T) {
	result := compiler.CompileSyntax("page.ktml", []byte("Page [label: string] {\n\tbutton @click.outside=\"open = false\" :class=\"{ on: open }\" hx-on::after-request=\"r()\" title=\"{label}\" \"Go\"\n}\n"))
	if result.HasErrors() {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	output, err := GenerateChecked(result.Document, false, "page.ktml", nil)
	if err != nil {
		t.Fatalf("GenerateChecked() error = %v", err)
	}
	for _, want := range []string{`@click.outside="open = false"`, `:class="{ on: open }"`, `hx-on::after-request="r()"`, `title="{{.Label}}"`} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %s:\n%s", want, output)
		}
	}
}

func TestLSP_MarkupDiagnostics_GIVENScriptAttributesDoctypesAndNames_THENPublishesCodesAtPreciseRanges(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	source := `Page [name: string, open: boolean, handler: TrustedScript] {
	div
	doctype html
	button @click="select('{name}')" :disabled="{open}" x-on:click="{handler}" title="{handler}"
	<!-- a --!> b -->
	a:href="/x"
}
`
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	want := "colon-chain::href"
	if joined := strings.Join(got, "\n"); !strings.HasPrefix(joined, want) {
		t.Fatalf("syntax diagnostics =\n%s\nwant prefix %s", joined, want)
	}

	source = strings.Replace(source, "\ta:href=\"/x\"\n", "", 1)
	response = openDocument(t, pagePath, source, 2)
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	got = nil
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	sort.Strings(got)
	wantSemantic := strings.Join([]string{
		"contextual-type-mismatch:{handler}",
		"invalid-html-comment:<!-- a --!> b -->",
		"misplaced-doctype:doctype html",
		"script-attribute-value:{name}",
	}, "\n")
	if joined := strings.Join(got, "\n"); joined != wantSemantic {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, wantSemantic)
	}
}

func TestLSP_MigrationLanguageDiagnostics_GIVENNewContexts_THENPublishesCodesAtPreciseRanges(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	source := "Page [name: string, on: boolean, data: JSONValue, attrs: Attributes] {\n" +
		"\tscript ```x = '</script>'```\n" +
		"\tdiv ```x```\n" +
		"\tscript \"go({name})\"\n" +
		"\tscript \"go('{data}')\"\n" +
		"\tdiv (title=\"a\" if on { title=\"b\" })\n" +
		"\tdiv (...attrs if on { ...attrs })\n" +
		"\tdiv style=\"width: {name}\"\n" +
		"}\n"
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	sort.Strings(got)
	want := strings.Join([]string{
		"duplicate-attribute:title=\"b\"",
		"multiple-spreads:...attrs",
		"raw-text-end-tag:```x = '</script>'```",
		"raw-text-placement:```x```",
		"script-body-value:{name}",
		"script-interpolation-position:{data}",
		"style-attribute-value:{name}",
	}, "\n")
	if joined := strings.Join(got, "\n"); joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}

func TestLSP_URLAttributeDiagnostics_GIVENStringsInURLAttributes_THENPublishesCodesAtPreciseRanges(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	source := "Page [name: string, count: integer] {\n" +
		"\ta href=\"{name}\" \"x\"\n" +
		"\tform action=\"/{name}\" {}\n" +
		"\timg (src=\"javascript:{count}\" alt=\"x\")\n" +
		"\ta href=\"/jobs/{name}\" \"ok\"\n" +
		"}\n"
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	sort.Strings(got)
	want := strings.Join([]string{
		"url-attribute-value:{count}",
		"url-attribute-value:{name}",
		"url-attribute-value:{name}",
	}, "\n")
	if joined := strings.Join(got, "\n"); joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}

func TestLSP_BooleanTextDiagnostics_GIVENBooleanPathsAsText_THENPublishesCodesAtPreciseRanges(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	source := "Page [on: boolean, name: string] {\n" +
		"\tp \"{on}\"\n" +
		"\tp title=\"open {on}\" \"x\"\n" +
		"\tbutton (hidden=\"{on}\" disabled=\"{on}\") \"{name}\"\n" +
		"}\n"
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	sort.Strings(got)
	if joined, want := strings.Join(got, "\n"), "boolean-text:{on}\nboolean-text:{on}"; joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}

func TestLSP_VoidContentDiagnostics_GIVENVoidElementWithContent_THENPublishesOneErrorAtTheName(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	source := "Page [] {\n\tbr { \"x\" }\n}\n"
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range    lspRange `json:"range"`
				Code     string   `json:"code"`
				Source   string   `json:"source"`
				Severity int      `json:"severity"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, fmt.Sprintf("%s:%s:%d:%s", diagnostic.Source, diagnostic.Code, diagnostic.Severity, diagnostic.Range.spell(t, source)))
	}
	// The compiler reports it; the HTML analyzer does not repeat it.
	if joined, want := strings.Join(got, "\n"), "ktml:void-content:1:br"; joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}
