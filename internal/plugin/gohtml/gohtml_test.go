package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestGenerate_DevMode_WithID(t *testing.T) {
	doc := &ast.Document{
		Nodes: []ast.Node{
			&ast.ElementDecl{
				Name: "TestComponent",
				Body: []ast.Node{
					&ast.ElementNode{
						Name: "div",
						Attributes: []*ast.AttributeNode{
							{Name: "id", Value: "existing-id"},
						},
					},
				},
			},
		},
	}

	result := Generate(doc, true, "test.ktml", nil)

	if !strings.Contains(result, `data-ktml-file="test.ktml"`) {
		t.Errorf("expected HTML to include data-ktml-file attribute, got:\n%s", result)
	}
	if !strings.Contains(result, `id="existing-id"`) {
		t.Errorf("expected HTML to preserve existing id, got:\n%s", result)
	}
}

func TestGenerate_DevMode_WithoutID(t *testing.T) {
	doc := &ast.Document{
		Nodes: []ast.Node{
			&ast.ElementDecl{
				Name: "TestComponent",
				Body: []ast.Node{
					&ast.ElementNode{
						Name: "div",
					},
				},
			},
		},
	}

	result := Generate(doc, true, "test.ktml", nil)

	if !strings.Contains(result, `data-ktml-file="test.ktml"`) {
		t.Errorf("expected HTML to include data-ktml-file attribute, got:\n%s", result)
	}
	if !strings.Contains(result, `id="ktml-`) {
		t.Errorf("expected HTML to include an auto-generated id starting with ktml-, got:\n%s", result)
	}
}

func TestGenerate_ProductionMode(t *testing.T) {
	doc := &ast.Document{
		Nodes: []ast.Node{
			&ast.ElementDecl{
				Name: "TestComponent",
				Body: []ast.Node{
					&ast.ElementNode{
						Name: "div",
					},
				},
			},
		},
	}

	result := Generate(doc, false, "test.ktml", nil)

	if strings.Contains(result, `data-ktml-file`) {
		t.Errorf("expected HTML to omit data-ktml-file attribute, got:\n%s", result)
	}
	if strings.Contains(result, `id="ktml-`) {
		t.Errorf("expected HTML to omit auto-generated id, got:\n%s", result)
	}
}

func TestGenerate_GIVENDevelopmentMode_WHENAComponentIsCalled_THENSerializesNoPropCache(t *testing.T) {
	doc := &ast.Document{
		Nodes: []ast.Node{
			&ast.ElementDecl{
				Name: "App",
				Attributes: []*ast.AttributeDecl{
					{Name: "userName", TypeDecl: "string"},
				},
				Body: []ast.Node{
					&ast.ElementNode{Name: "div"},
				},
			},
			&ast.ElementDecl{
				Name: "Index",
				Body: []ast.Node{
					&ast.ElementNode{
						Name: "App",
						Attributes: []*ast.AttributeNode{
							{Name: "userName", Value: "{userName}"},
						},
					},
				},
			},
		},
	}

	result := Generate(doc, true, "test.ktml", nil)

	// Props never reach the browser (ADR-0004): no prop cache is rendered.
	if strings.Contains(result, `__ktml_cache`) || strings.Contains(result, `"UserName":`) {
		t.Errorf("development output serializes props into the page:\n%s", result)
	}
	if !strings.Contains(result, `data-ktml-component="App"`) {
		t.Errorf("expected component name tracking, got:\n%s", result)
	}
}

func TestLSP_Diagnostics(t *testing.T) {
	payload := []byte(`{
		"textDocument": {
			"uri": "file:///test.ktml",
			"text": "Component {\n\tinvalid[\n}"
		}
	}`)
	resp, err := HandleLSP("textDocument/didOpen", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var notification struct {
		Method string `json:"method"`
		Params struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Range struct {
					Start struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"start"`
				} `json:"range"`
				Message string `json:"message"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(resp, &notification); err != nil {
		t.Fatalf("failed to unmarshal response: %v, resp: %s", err, string(resp))
	}
	if notification.Method != "textDocument/publishDiagnostics" {
		t.Errorf("expected publishDiagnostics, got %q", notification.Method)
	}
	if len(notification.Params.Diagnostics) == 0 {
		t.Fatal("expected at least 1 diagnostic error")
	}
}

func TestLSP_Definition(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Button {\n\tbutton\n}\n\nMain {\n\tButton\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	defPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		},
		"position": {
			"line": 5,
			"character": 1
		}
	}`)
	resp, err := HandleLSP("textDocument/definition", defPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result *struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal definition response: %v, resp: %s", err, string(resp))
	}

	if response.Result == nil {
		t.Fatal("expected definition result, got nil")
	}

	if response.Result.URI != uri {
		t.Errorf("expected URI %q, got %q", uri, response.Result.URI)
	}
	if response.Result.Range.Start.Line != 0 || response.Result.Range.Start.Character != 0 {
		t.Errorf("expected declaration at line 0, character 0, got line %d, character %d",
			response.Result.Range.Start.Line, response.Result.Range.Start.Character)
	}
}

func TestLSP_Definition_GIVENImportedComponent_WHENRequested_THENReturnsImportedDeclaration(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.ktml")
	componentsPath := filepath.Join(dir, "components.ktml")
	index := "import { EnquiryCount } from \"components\"\n\nPage {\n\tEnquiryCount count=\"3\"\n}"
	components := "EnquiryCount [count: string] {\n\tspan { \"{count} enquiries\" }\n}"
	if err := os.WriteFile(indexPath, []byte(index), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(componentsPath, []byte(components), 0o600); err != nil {
		t.Fatalf("write components: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"id":           1,
		"textDocument": map[string]any{"uri": "file://" + indexPath},
		"position":     map[string]any{"line": 3, "character": 2},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	responseBytes, err := HandleLSP("textDocument/definition", payload)
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result *struct {
			URI string `json:"uri"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result == nil || response.Result.URI != "file://"+componentsPath {
		t.Fatalf("definition response = %s", responseBytes)
	}
}

func TestLSP_References(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Button {\n\tbutton\n}\n\nMain {\n\tButton {}\n\tButton {}\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	refPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		},
		"position": {
			"line": 0,
			"character": 0
		},
		"context": {
			"includeDeclaration": true
		}
	}`)
	resp, err := HandleLSP("textDocument/references", refPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result []struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal references response: %v, resp: %s", err, string(resp))
	}

	if len(response.Result) != 3 {
		t.Fatalf("expected 3 references, got %d: %v", len(response.Result), response.Result)
	}
}

func TestLSP_DocumentSymbol(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "import { Button } from \"./ui\"\n\nMain [title: string] {\n\tdiv\n\t\ttitle\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	symPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		}
	}`)
	resp, err := HandleLSP("textDocument/documentSymbol", symPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result []struct {
			Name   string `json:"name"`
			Detail string `json:"detail"`
			Kind   int    `json:"kind"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v, resp: %s", err, string(resp))
	}

	if len(response.Result) != 2 {
		t.Fatalf("expected 2 symbols (1 import + 1 component), got %d", len(response.Result))
	}

	if response.Result[0].Name == "Main" && response.Result[0].Kind != 25 {
		t.Errorf("expected component symbol kind 25, got %d", response.Result[0].Kind)
	}
}

func TestLSP_Hover(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Button [label: string, variant: string] {\n\tbutton\n\t\tlabel\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hoverPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		},
		"position": {
			"line": 0,
			"character": 0
		}
	}`)
	resp, err := HandleLSP("textDocument/hover", hoverPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result struct {
			Contents struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v, resp: %s", err, string(resp))
	}

	if response.Result.Contents.Value == "" {
		t.Fatal("expected hover contents, got empty")
	}
	if !strings.Contains(response.Result.Contents.Value, "label") {
		t.Errorf("expected hover to mention 'label' attribute, got: %s", response.Result.Contents.Value)
	}
}

func TestLSP_DidClose(t *testing.T) {
	didOpenPayload := []byte(`{
		"textDocument": {
			"uri": "file:///test.ktml",
			"text": "Component {\n\tdiv\n}"
		}
	}`)
	HandleLSP("textDocument/didOpen", didOpenPayload)

	docs := getOpenDocuments()
	if _, ok := docs["file:///test.ktml"]; !ok {
		t.Fatal("expected document to be tracked after didOpen")
	}

	didClosePayload := []byte(`{
		"textDocument": {
			"uri": "file:///test.ktml"
		}
	}`)
	resp, err := HandleLSP("textDocument/didClose", didClosePayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	docs = getOpenDocuments()
	if _, ok := docs["file:///test.ktml"]; ok {
		t.Fatal("expected document to be removed after didClose")
	}

	var notification struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(resp, &notification); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if notification.Method != "textDocument/publishDiagnostics" {
		t.Errorf("expected publishDiagnostics on close, got %q", notification.Method)
	}
}

func TestLSP_Formatting(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Button {\nbutton\n}\n"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	fmtPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		}
	}`)
	resp, err := HandleLSP("textDocument/formatting", fmtPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result []struct {
			NewText string `json:"newText"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal: %v, resp: %s", err, string(resp))
	}

	if len(response.Result) == 0 {
		t.Fatal("expected at least 1 text edit")
	}
	if !strings.Contains(response.Result[0].NewText, "\tbutton") {
		t.Errorf("expected formatted output with indented button, got: %s", response.Result[0].NewText)
	}
}

func TestLSP_Completion(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Button [label: string] {\n\tbutton\n\t\tlabel\n}\n\nMain {\n\tB\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	compPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		},
		"position": {
			"line": 5,
			"character": 1
		}
	}`)
	resp, err := HandleLSP("textDocument/completion", compPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result struct {
			IsIncomplete bool `json:"isIncomplete"`
			Items        []struct {
				Label  string `json:"label"`
				Kind   int    `json:"kind"`
				Detail string `json:"detail"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal: %v, resp: %s", err, string(resp))
	}

	if !response.Result.IsIncomplete {
		t.Error("expected isIncomplete to be true")
	}

	hasButton := false
	hasImport := false
	for _, item := range response.Result.Items {
		if item.Label == "Button" {
			hasButton = true
		}
		if item.Label == "import" {
			hasImport = true
		}
	}
	if !hasButton {
		t.Error("expected 'Button' in completion items")
	}
	if !hasImport {
		t.Error("expected 'import' keyword in completion items")
	}
}

func TestLSP_WorkspaceSymbol(t *testing.T) {
	dir := t.TempDir()

	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)

	filePath := filepath.Join(dir, "test.ktml")
	text := "MyComponent [title: string] {\n\tdiv\n\t\ttitle\n}"
	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	symPayload := []byte(`{
		"id": 123,
		"query": "MyComponent"
	}`)
	resp, err := HandleLSP("workspace/symbol", symPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result []struct {
			Name     string `json:"name"`
			Kind     int    `json:"kind"`
			Location struct {
				URI string `json:"uri"`
			} `json:"location"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal: %v, resp: %s", err, string(resp))
	}

	if len(response.Result) == 0 {
		t.Fatal("expected at least 1 symbol")
	}
	if response.Result[0].Name != "MyComponent" {
		t.Errorf("expected MyComponent, got %s", response.Result[0].Name)
	}
}

func TestLSP_CodeAction(t *testing.T) {
	dir := t.TempDir()

	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)

	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Main {\n\tMissingComponent\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	otherPath := filepath.Join(dir, "other.ktml")
	otherText := "MissingComponent {\n\tdiv\n}"
	if err := os.WriteFile(otherPath, []byte(otherText), 0644); err != nil {
		t.Fatalf("failed to write other file: %v", err)
	}

	actionPayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		},
		"range": {
			"start": {"line": 1, "character": 1},
			"end": {"line": 1, "character": 17}
		},
		"context": {
			"diagnostics": [
				{
					"range": {
						"start": {"line": 1, "character": 1},
						"end": {"line": 1, "character": 17}
					},
					"message": "could not find MissingComponent"
				}
			]
		}
	}`)
	resp, err := HandleLSP("textDocument/codeAction", actionPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result []struct {
			Title string `json:"title"`
			Kind  string `json:"kind"`
			Edit  struct {
				Changes map[string][]map[string]any `json:"changes"`
			} `json:"edit"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal: %v, resp: %s", err, string(resp))
	}

	hasImport := false
	for _, action := range response.Result {
		if strings.Contains(action.Title, "Import") {
			hasImport = true
			break
		}
	}
	if !hasImport {
		t.Error("expected import quick-fix action")
	}
}

func TestLSP_Rename(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := "Button {\n\tbutton\n}\n\nMain {\n\tButton\n\tButton\n}"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	renamePayload := []byte(`{
		"id": 123,
		"textDocument": {
			"uri": "` + uri + `"
		},
		"position": {
			"line": 0,
			"character": 0
		},
		"newName": "MyButton"
	}`)
	resp, err := HandleLSP("textDocument/rename", renamePayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result *struct {
			Changes map[string][]struct {
				NewText string `json:"newText"`
				Range   struct {
					Start struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"start"`
				} `json:"range"`
			} `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		t.Fatalf("failed to unmarshal: %v, resp: %s", err, string(resp))
	}

	if response.Result == nil {
		t.Fatal("expected rename result")
	}
	if len(response.Result.Changes) == 0 {
		t.Fatal("expected changes")
	}
	for _, edits := range response.Result.Changes {
		for _, edit := range edits {
			if edit.NewText != "MyButton" {
				t.Errorf("expected newText 'MyButton', got %q", edit.NewText)
			}
		}
	}
}

func TestLSP_Rename_GIVENCursorInsideHTMXRouteAttribute_THENDefersToHTMXProvider(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.ktml")
	uri := "file://" + filePath
	text := `Button hx-get="/enquiries"`
	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	for _, character := range []int{8, 18} {
		payload, err := json.Marshal(map[string]any{
			"id":           character,
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": 0, "character": character},
			"newName":      "/requests",
		})
		if err != nil {
			t.Fatalf("marshal rename request: %v", err)
		}
		responseBytes, err := HandleLSP("textDocument/rename", payload)
		if err != nil {
			t.Fatalf("HandleLSP() at character %d: %v", character, err)
		}
		var response struct {
			Result any `json:"result"`
		}
		if err := json.Unmarshal(responseBytes, &response); err != nil {
			t.Fatalf("decode rename response: %v", err)
		}
		if response.Result != nil {
			t.Errorf("rename result at character %d = %#v, want nil", character, response.Result)
		}
	}
}
