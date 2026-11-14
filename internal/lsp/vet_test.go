package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

const vetCard = "Card [title: string, note?: string] {\n\th2 \"{title}\"\n}\n"

func TestVetAdapter_GIVENAPropsLiteralThatOmitsARequiredInput_WHENServed_THENItIsReportedOnTheGoFileUntilTheInputIsNoLongerRequired(t *testing.T) {
	root := writeProject(t, map[string]string{
		"go.mod":       "module example.com/app\n\ngo 1.26\n",
		"ui/card.ktml": vetCard,
		"ui/page.go":   "package ui\n\nvar card = Card{Note: \"n\"}\n",
	})
	adapter := NewVetAdapter(root)
	client := startTailwindHost(t, adapter)
	client.request("initialize", map[string]any{"capabilities": map[string]any{}})
	client.send(map[string]any{"method": "initialized", "params": map[string]any{}})

	goURI := fileURI(filepath.Join(root, "ui", "page.go"))
	reported := client.notification(publishDiagnosticsMethod, func(message map[string]any) bool {
		return message["params"].(map[string]any)["uri"] == goURI
	})
	diagnostics, _ := reported["params"].(map[string]any)["diagnostics"].([]any)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	diagnostic := diagnostics[0].(map[string]any)
	if !strings.Contains(diagnostic["message"].(string), "omits required input title") || diagnostic["source"] != vetSource {
		t.Errorf("diagnostic = %v", diagnostic)
	}
	if got := jsonText(diagnostic["range"]); got != `{"end":{"character":15,"line":2},"start":{"character":11,"line":2}}` {
		t.Errorf("range = %s, want the literal's type", got)
	}

	// An unsaved edit gives title a default: the finding goes.
	cardURI := fileURI(filepath.Join(root, "ui", "card.ktml"))
	client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": cardURI, "version": 1, "languageId": "ktml", "text": strings.Replace(vetCard, "title: string,", "title: string = \"Card\",", 1)}}})
	cleared := client.notification(publishDiagnosticsMethod, func(message map[string]any) bool {
		return message["params"].(map[string]any)["uri"] == goURI
	})
	if diagnostics, _ := cleared["params"].(map[string]any)["diagnostics"].([]any); len(diagnostics) != 0 {
		t.Errorf("diagnostics after the edit = %v, want none", diagnostics)
	}
}
