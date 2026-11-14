package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const extensionSource = "Page [] {\n\tform hx-require=\"#terms\" hx-post=\"/save\" {}\n}\n"

// extensionDiagnostics publishes the diagnostics of a document that uses
// the hx-require extension attribute.
func extensionDiagnostics(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.ktml")
	if err := os.WriteFile(path, []byte(extensionSource), 0o600); err != nil {
		t.Fatal(err)
	}
	response := openDocument(t, path, extensionSource, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, extensionSource))
	}
	return got
}

func configure(t *testing.T, settings string) {
	t.Helper()
	if err := Configure([]byte(settings)); err != nil {
		t.Fatalf("Configure(%s) = %v", settings, err)
	}
	t.Cleanup(func() { _ = Configure([]byte(`{}`)) })
}

func TestLSP_HTMLDiagnostics_GIVENNoExtensionAttributes_THENReportsHXRequire(t *testing.T) {
	configure(t, `{"_ktml": {"path_map": {}}}`)
	if got := strings.Join(extensionDiagnostics(t), ","); got != "html-unknown-attribute:hx-require" {
		t.Errorf("diagnostics = %s, want hx-require reported", got)
	}
}

func TestLSP_HTMLDiagnostics_GIVENHostDeclaredExtensionAttribute_THENAcceptsIt(t *testing.T) {
	configure(t, `{"_ktml": {"path_map": {}, "html": {"extension_attributes": ["hx-require"]}}}`)
	if got := extensionDiagnostics(t); len(got) != 0 {
		t.Errorf("diagnostics = %v, want none", got)
	}
}
