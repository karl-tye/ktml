package wasm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestConfiguredSettings_GIVENHostSettings_WHENConfigured_THENTheyJoinThePathMapUnderKTML(t *testing.T) {
	host := map[string]any{"html": map[string]any{"extension_attributes": []string{"hx-require"}}}
	data, err := configuredSettings(map[string]any{"chi": "x"}, map[string]string{".": "/project"}, host)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Chi  string `json:"chi"`
		KTML struct {
			PathMap map[string]string `json:"path_map"`
			HTML    struct {
				ExtensionAttributes []string `json:"extension_attributes"`
			} `json:"html"`
		} `json:"_ktml"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Chi != "x" || decoded.KTML.PathMap["."] != "/project" || len(decoded.KTML.HTML.ExtensionAttributes) != 1 || decoded.KTML.HTML.ExtensionAttributes[0] != "hx-require" {
		t.Errorf("settings = %s", data)
	}
}

func TestGohtmlPlugin_GIVENHostExtensionAttributes_WHENADocumentOpens_THENTheHTMLAnalyzerAcceptsThem(t *testing.T) {
	wasmBytes := buildGohtmlWasm(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	source := "Page [] {\n\tform (hx-require=\"#terms\" hx-tigger=\"submit\") {}\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": "file://" + path, "version": 1, "text": source}})
	ctx := context.Background()
	for _, test := range []struct {
		name string
		host map[string]any
		want string
	}{
		{"undeclared", nil, "html-unknown-attribute:hx-require,html-unknown-attribute:hx-tigger"},
		{"declared", map[string]any{"html": map[string]any{"extension_attributes": []string{"hx-require"}}}, "html-unknown-attribute:hx-tigger"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{AllowRead: []string{dir}, Host: test.host})
			if err != nil {
				t.Fatalf("load gohtml: %v", err)
			}
			defer plugin.Close(ctx)
			response, err := plugin.ExecuteLSP(ctx, "textDocument/didOpen", open)
			if err != nil {
				t.Fatalf("didOpen: %v", err)
			}
			var notification struct {
				Params struct {
					Diagnostics []struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"diagnostics"`
				} `json:"params"`
			}
			if err := json.Unmarshal(response, &notification); err != nil {
				t.Fatalf("decode %s: %v", response, err)
			}
			var got []string
			for _, diagnostic := range notification.Params.Diagnostics {
				name := strings.Fields(diagnostic.Message)[0]
				got = append(got, diagnostic.Code+":"+name)
			}
			sort.Strings(got)
			if joined := strings.Join(got, ","); joined != test.want {
				t.Errorf("diagnostics = %s, want %s", joined, test.want)
			}
		})
	}
}

func TestConfiguredSettings_GIVENHostSettingsNamingThePathMap_WHENConfigured_THENTheHostPathMapWins(t *testing.T) {
	data, err := configuredSettings(nil, map[string]string{}, map[string]any{"path_map": "spoofed"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"_ktml":{"path_map":{}}}` {
		t.Errorf("settings = %s", data)
	}
}
