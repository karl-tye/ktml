package htmx

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the HTMX LSP golden files")

// TestLSPGolden drives the plugin over testdata/lsp/app and compares every
// response with testdata/lsp/golden. Run with -update to rewrite them.
func TestLSPGolden(t *testing.T) {
	app, err := filepath.Abs(filepath.Join("testdata", "lsp", "app"))
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]any{
		"chi":   map[string]any{"dir": "./web"},
		"_ktml": map[string]any{"path_map": map[string]string{".": app}},
	})
	lspState.Lock()
	lspState.documents = make(map[string]string)
	lspState.Unlock()
	if err := Configure(settings); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	page := filepath.Join(app, "views", "vendors.ktml")
	router := filepath.Join(app, "web", "router.go")
	pageSource := readFile(t, page)
	routerSource := readFile(t, router)
	pageURI, routerURI := fileURI(page), fileURI(router)

	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": pageURI, "text": pageSource}})
	if _, err := HandleLSP("textDocument/didOpen", open); err != nil {
		t.Fatalf("didOpen error = %v", err)
	}
	// The scan finishes after the document opened, as with a slow scan.
	notifications := runJobsForTest(t)[0]

	at := func(source, marker string, delta int) Position {
		t.Helper()
		offset := strings.Index(source, marker)
		if offset < 0 {
			t.Fatalf("marker %q not found", marker)
		}
		return offsetPosition(source, offset+delta)
	}
	request := func(method, uri string, position Position, extra map[string]any) []byte {
		t.Helper()
		params := map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position}
		for key, value := range extra {
			params[key] = value
		}
		payload, _ := json.Marshal(params)
		response, err := HandleLSP(method, payload)
		if err != nil {
			t.Fatalf("%s error = %v", method, err)
		}
		return response
	}

	typing := "VendorRow() {\n\tbutton hx-post=\"/ven"
	typingURI := "file:///typing.ktml"
	openTyping, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": typingURI, "text": typing}})
	if _, err := HandleLSP("textDocument/didOpen", openTyping); err != nil {
		t.Fatalf("didOpen error = %v", err)
	}

	cases := []struct {
		name     string
		response []byte
	}{
		{"diagnostics", notifications},
		{"completion", request("textDocument/completion", typingURI, offsetPosition(typing, len(typing)), nil)},
		{"definition", request("textDocument/definition", pageURI, at(pageSource, `hx-post="/vendors/{vendor.id}/approve"`, 12), nil)},
		{"hover", request("textDocument/hover", pageURI, at(pageSource, `hx-post="/vendors/{vendor.id}/approve"`, 12), nil)},
		{"references-from-chi", request("textDocument/references", routerURI, at(routerSource, `"/{id}/approve"`, 3), map[string]any{"context": map[string]any{"includeDeclaration": true}})},
		{"prepare-rename", request("textDocument/prepareRename", pageURI, at(pageSource, `hx-post="/vendors/{vendor.id}/approve"`, 12), nil)},
		{"rename-changing-params", request("textDocument/rename", routerURI, at(routerSource, `"/{id}/approve"`, 3), map[string]any{"newName": "/vendors/{vendorID}/approve"})},
		{"prepare-rename-slash-route", request("textDocument/prepareRename", routerURI, at(routerSource, `r.Get("/", vendors.List)`, 7), nil)},
		// Last: a rename updates the index for the edits it returns.
		{"rename", request("textDocument/rename", routerURI, at(routerSource, `"/{id}/approve"`, 3), map[string]any{"newName": "/vendors/{id}/approval"})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var decoded any
			if err := json.Unmarshal(test.response, &decoded); err != nil {
				t.Fatalf("decode %s: %v", test.response, err)
			}
			got, _ := json.MarshalIndent(decoded, "", "  ")
			got = append(bytes.ReplaceAll(got, []byte(app), []byte("$APP")), '\n')
			golden := filepath.Join("testdata", "lsp", "golden", test.name+".json")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s mismatch\ngot:\n%s\nwant:\n%s", test.name, got, want)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
