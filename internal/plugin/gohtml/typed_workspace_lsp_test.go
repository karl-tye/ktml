package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLSP_Definition_GIVENUnsavedImportedComponentCall_THENUsesTypedWorkspaceOverlay(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	if err := os.WriteFile(componentsPath, []byte(`Button { button }`), 0o600); err != nil {
		t.Fatalf("write components: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write page: %v", err)
	}
	uri := "file://" + pagePath
	unsaved := "import { Button } from \"components\"\nPage { Button }"
	openPayload, _ := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 1, "text": unsaved},
	})
	if _, err := HandleLSP("textDocument/didOpen", openPayload); err != nil {
		t.Fatalf("didOpen error = %v", err)
	}
	t.Cleanup(func() {
		closePayload, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}})
		_, _ = HandleLSP("textDocument/didClose", closePayload)
	})
	definitionPayload, _ := json.Marshal(map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": uri},
		"position": map[string]any{"line": 1, "character": 8},
	})

	responseBytes, err := HandleLSP("textDocument/definition", definitionPayload)
	if err != nil {
		t.Fatalf("definition error = %v", err)
	}
	var response struct {
		Result *struct {
			URI string `json:"uri"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode definition: %v", err)
	}
	if response.Result == nil || response.Result.URI != "file://"+componentsPath {
		t.Errorf("definition response = %s", responseBytes)
	}
}

func TestLSP_References_GIVENSameNamedUnrelatedComponent_THENReturnsOnlyResolvedSymbolReferences(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	unrelatedPath := filepath.Join(dir, "unrelated.ktml")
	files := map[string]string{
		componentsPath: `Button { button }`,
		pagePath:       "import { Button } from \"components\"\nPage { Button }",
		unrelatedPath:  `Button { article } Other { Button }`,
	}
	for path, source := range files {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": "file://" + pagePath},
		"position": map[string]any{"line": 1, "character": 8},
		"context":  map[string]any{"includeDeclaration": false},
	})

	responseBytes, err := HandleLSP("textDocument/references", payload)
	if err != nil {
		t.Fatalf("references error = %v", err)
	}
	var response struct {
		Result []struct {
			URI string `json:"uri"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode references: %v", err)
	}
	if len(response.Result) != 2 {
		t.Fatalf("references response = %s, want import and call", responseBytes)
	}
	for _, location := range response.Result {
		if location.URI != "file://"+pagePath {
			t.Errorf("references included unrelated symbol: %s", responseBytes)
		}
	}
}

func TestLSP_Hover_GIVENImportedComponentCall_THENShowsTypedWorkspaceSchema(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	if err := os.WriteFile(componentsPath, []byte(`Button [label: string, disabled: boolean] { button }`), 0o600); err != nil {
		t.Fatalf("write components: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("import { Button } from \"components\"\nPage { Button label=\"Save\" disabled=true }"), 0o600); err != nil {
		t.Fatalf("write page: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": "file://" + pagePath},
		"position": map[string]any{"line": 1, "character": 8},
	})

	responseBytes, err := HandleLSP("textDocument/hover", payload)
	if err != nil {
		t.Fatalf("hover error = %v", err)
	}
	var response struct {
		Result struct {
			Contents struct {
				Value string `json:"value"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode hover: %v", err)
	}
	if value := response.Result.Contents.Value; value != "component Button(label: string, disabled: boolean)" {
		t.Errorf("hover value = %q", value)
	}
}
