package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const htmlLSPSource = "Page [open: boolean] {\n" +
	"\tdvi \"x\"\n" +
	"\timg src=\"/logo.png\"\n" +
	"\tbutton (type=\"button\" aria-expanded=\"{open}\" tabindex=\"2\") \"Menu\"\n" +
	"\tinput type=\"\"\n" +
	"}\n"

func htmlLSPDocument(t *testing.T) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.ktml")
	if err := os.WriteFile(path, []byte(htmlLSPSource), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, openDocument(t, path, htmlLSPSource, 1)
}

func TestLSP_HTMLDiagnostics_GIVENInvalidHTMLAndAccessibility_THENPublishesWarningsWithCompilerDiagnostics(t *testing.T) {
	_, response := htmlLSPDocument(t)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range    lspRange `json:"range"`
				Code     string   `json:"code"`
				Severity int      `json:"severity"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, htmlLSPSource))
		if diagnostic.Severity != 2 {
			t.Errorf("%s severity = %d, want warning", diagnostic.Code, diagnostic.Severity)
		}
	}
	sort.Strings(got)
	want := strings.Join([]string{
		"a11y-missing-alt:img",
		`a11y-positive-tabindex:tabindex="2"`,
		`html-invalid-attribute-value:type=""`,
		"html-unknown-element:dvi",
	}, "\n")
	if joined := strings.Join(got, "\n"); joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}

func TestLSP_HTMLCompletion_GIVENElementAttributeAndValuePositions_THENOffersHTML(t *testing.T) {
	path, _ := htmlLSPDocument(t)
	uri := "file://" + path
	tests := []struct {
		marker string
		shift  int
		want   string
	}{
		{"dvi", 1, "div"},
		{"src=", 0, "srcset"},
		{`input type="`, 12, "checkbox"},
	}
	for _, test := range tests {
		response := callLSP(t, "textDocument/completion", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     positionOf(t, htmlLSPSource, test.marker, test.shift),
		})
		var decoded struct {
			Result struct {
				Items []struct {
					Label string `json:"label"`
				} `json:"items"`
			} `json:"result"`
		}
		if err := json.Unmarshal(response, &decoded); err != nil {
			t.Fatalf("decode %s: %v", response, err)
		}
		found := false
		for _, item := range decoded.Result.Items {
			found = found || item.Label == test.want
		}
		if !found {
			t.Errorf("completion at %s+%d lacks %s: %s", test.marker, test.shift, test.want, response)
		}
	}
}

func TestLSP_HTMLHover_GIVENAnAttribute_THENDocumentsIt(t *testing.T) {
	path, _ := htmlLSPDocument(t)
	response := callLSP(t, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + path},
		"position":     positionOf(t, htmlLSPSource, "aria-expanded", 2),
	})
	if !strings.Contains(string(response), "Whether the controlled grouping is expanded.") {
		t.Errorf("hover = %s", response)
	}
}

func TestLSP_HTMLCodeActions_GIVENAFixableDiagnostic_THENReturnsItsQuickFix(t *testing.T) {
	path, _ := htmlLSPDocument(t)
	position := positionOf(t, htmlLSPSource, "tabindex", 1)
	response := callLSP(t, "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + path},
		"range":        map[string]any{"start": position, "end": position},
		"context":      map[string]any{"diagnostics": []any{}},
	})
	var decoded struct {
		Result []struct {
			Title string `json:"title"`
			Edit  struct {
				Changes map[string][]struct {
					Range   lspRange `json:"range"`
					NewText string   `json:"newText"`
				} `json:"changes"`
			} `json:"edit"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	for _, action := range decoded.Result {
		if action.Title != `Use tabindex="0"` {
			continue
		}
		edits := action.Edit.Changes["file://"+path]
		if len(edits) != 1 || edits[0].NewText != `"0"` || edits[0].Range.spell(t, htmlLSPSource) != `"2"` {
			t.Errorf("edits = %+v", edits)
		}
		return
	}
	t.Errorf("no tabindex fix in %s", response)
}

func TestLSP_Completion_GIVENACursorInAFreeTextAttributeValue_THENOffersNoComponentsOrImports(t *testing.T) {
	source := "Card [] {\n\tdiv class=\"flex bg-\" \"x\"\n}\n\nOther [] {\n\tp \"y\"\n}\n"
	path := filepath.Join(t.TempDir(), "card.ktml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	openDocument(t, path, source, 1)
	response := callLSP(t, "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + path},
		"position":     positionOf(t, source, "bg-", 3),
	})
	var decoded struct {
		Result struct {
			Items []struct {
				Label string `json:"label"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	if len(decoded.Result.Items) != 0 {
		t.Errorf("completion in a class value = %s, want no items: class names are Tailwind's", response)
	}
}

func TestLSP_Completion_GIVENACursorInAnAttributeBag_THENOffersGlobalAttributes(t *testing.T) {
	source := "Field [attrs: Attributes] {\n\tinput ...attrs\n}\n\nPage [] {\n\tField attrs=(hid)\n}\n"
	path := filepath.Join(t.TempDir(), "page.ktml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	openDocument(t, path, source, 1)
	response := callLSP(t, "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + path},
		"position":     positionOf(t, source, "(hid", 4),
	})
	var decoded struct {
		Result struct {
			Items []struct {
				Label string `json:"label"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	var labels []string
	for _, item := range decoded.Result.Items {
		labels = append(labels, item.Label)
	}
	if !slices.Contains(labels, "hidden") || slices.Contains(labels, "Field") {
		t.Errorf("completion in a bag = %v, want hidden and no components", labels)
	}
}
