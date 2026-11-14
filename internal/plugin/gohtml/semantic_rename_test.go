package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLSP_Rename_GIVENComponentNameInTextAndAttributes_THENEditsOnlySemanticComponentSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	uri := "file://" + path
	source := `Button { button title="Button" { "Button" } }
Page { Button }`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"id":           1,
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": 1},
		"newName":      "ActionButton",
	})

	responseBytes, err := HandleLSP("textDocument/rename", payload)
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result struct {
			Changes map[string][]struct {
				NewText string `json:"newText"`
			} `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	edits := response.Result.Changes[uri]
	if len(edits) != 2 {
		t.Fatalf("rename edits = %#v, want declaration and component use only", edits)
	}
}

func TestLSP_PrepareRename_GIVENComponentAndTextPositions_THENAllowsOnlyComponentSymbol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	uri := "file://" + path
	if err := os.WriteFile(path, []byte(`Button { button { "Button" } }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	request := func(character int) any {
		payload, _ := json.Marshal(map[string]any{
			"id":           1,
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": 0, "character": character},
		})
		responseBytes, err := HandleLSP("textDocument/prepareRename", payload)
		if err != nil {
			t.Fatalf("HandleLSP() error = %v", err)
		}
		var response struct {
			Result any `json:"result"`
		}
		if err := json.Unmarshal(responseBytes, &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return response.Result
	}

	if result := request(1); result == nil {
		t.Error("prepareRename rejected a component declaration")
	}
	if result := request(19); result != nil {
		t.Errorf("prepareRename accepted text content: %#v", result)
	}
}

func TestLSP_Rename_GIVENInvalidIdentifier_THENRejectsEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	uri := "file://" + path
	if err := os.WriteFile(path, []byte(`Button { button }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": uri},
		"position": map[string]any{"line": 0, "character": 1}, "newName": "bad name",
	})
	responseBytes, err := HandleLSP("textDocument/rename", payload)
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result != nil {
		t.Errorf("rename result = %#v, want nil", response.Result)
	}
}

func TestLSP_Rename_GIVENSameNamedUnrelatedComponent_THENEditsOnlyResolvedImportedSymbol(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	unrelatedDir := filepath.Join(dir, "unrelated")
	if err := os.Mkdir(unrelatedDir, 0o700); err != nil {
		t.Fatalf("create unrelated directory: %v", err)
	}
	unrelatedPath := filepath.Join(unrelatedDir, "components.ktml")
	files := map[string]string{
		componentsPath: `Button { button }`,
		pagePath:       "import { Button } from \"components\"\nPage { Button }",
		unrelatedPath:  `Button { article }`,
	}
	for path, source := range files {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": "file://" + pagePath},
		"position": map[string]any{"line": 1, "character": 8}, "newName": "ActionButton",
	})
	responseBytes, err := HandleLSP("textDocument/rename", payload)
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result struct {
			Changes map[string][]json.RawMessage `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, edited := response.Result.Changes["file://"+unrelatedPath]; edited {
		t.Errorf("rename edited unrelated symbol: %s", responseBytes)
	}
	if len(response.Result.Changes["file://"+componentsPath]) != 1 || len(response.Result.Changes["file://"+pagePath]) != 2 {
		t.Errorf("rename changes = %s, want declaration, import, and call", responseBytes)
	}
}
