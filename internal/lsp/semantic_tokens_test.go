package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func numbers(values ...int) []json.Number {
	result := make([]json.Number, len(values))
	for i, value := range values {
		result[i] = json.Number(strings.TrimSpace(string(rune('0' + value))))
	}
	return result
}

func applyTokenEdits(before []json.Number, edits []map[string]any) []json.Number {
	after := append([]json.Number(nil), before...)
	for _, edit := range edits {
		start, deleteCount := edit["start"].(int), edit["deleteCount"].(int)
		data := edit["data"].([]json.Number)
		after = append(append(append([]json.Number(nil), after[:start]...), data...), after[start+deleteCount:]...)
	}
	return after
}

func TestTokenEdits_GIVENTwoResults_WHENDiffed_THENOneEditTurnsTheFirstIntoTheSecond(t *testing.T) {
	tests := []struct{ before, after []json.Number }{
		{numbers(0, 1, 2, 3, 4), numbers(0, 1, 2, 3, 4)},
		{numbers(0, 1, 2, 3, 4), numbers(0, 1, 9, 3, 4)},
		{numbers(0, 1, 2, 3, 4), numbers(0, 1, 2, 3, 4, 5, 6, 7, 8, 9)},
		{numbers(0, 1, 2, 3, 4, 5, 6, 7, 8, 9), numbers(5, 6, 7, 8, 9)},
		{numbers(), numbers(1, 2)},
	}
	for _, test := range tests {
		edits := tokenEdits(test.before, test.after)
		if got := applyTokenEdits(test.before, edits); strings.Join(toStrings(got), ",") != strings.Join(toStrings(test.after), ",") {
			t.Errorf("edits %v turn %v into %v, want %v", edits, test.before, got, test.after)
		}
	}
	if edits := tokenEdits(numbers(1, 2), numbers(1, 2)); len(edits) != 0 {
		t.Errorf("equal data has edits %v", edits)
	}
}

func toStrings(values []json.Number) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.String()
	}
	return result
}

func TestServe_GIVENSemanticTokens_WHENADeltaOrARangeIsAsked_THENAnswersFromTheFullResult(t *testing.T) {
	schema := "record Row [\n\tid: string,\n\tlabel: string,\n]\n"
	dir := writeProject(t, map[string]string{"ktml.config.json": "{}", "row.ktmlschema": schema})
	uri := "file://" + filepath.Join(dir, "row.ktmlschema")
	client := startProjectHost(t, dir, "gohtml")
	client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "languageId": "ktmlschema", "text": schema}}})
	document := map[string]any{"uri": uri}

	full := client.request(semanticTokensFull, map[string]any{"textDocument": document})["result"].(map[string]any)
	resultID, _ := full["resultId"].(string)
	data, _ := full["data"].([]any)
	if resultID == "" || len(data) == 0 {
		t.Fatalf("full = %v", full)
	}

	edited := strings.Replace(schema, "\tlabel: string,\n", "\tlabel: string,\n\tcount: number,\n", 1)
	client.send(map[string]any{"method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []any{map[string]any{"text": edited}}}})
	delta := client.request(semanticTokensDelta, map[string]any{"textDocument": document, "previousResultId": resultID})["result"].(map[string]any)
	edits, ok := delta["edits"].([]any)
	if !ok || len(edits) != 1 || delta["resultId"] == resultID {
		t.Fatalf("delta = %v", delta)
	}
	edit := edits[0].(map[string]any)
	start, deleteCount := int(edit["start"].(float64)), int(edit["deleteCount"].(float64))
	patched := append(append(append([]any(nil), data[:start]...), edit["data"].([]any)...), data[start+deleteCount:]...)
	again := client.request(semanticTokensFull, map[string]any{"textDocument": document})["result"].(map[string]any)
	if got, want := jsonText(patched), jsonText(again["data"]); got != want {
		t.Errorf("patched data = %s, want the full result %s", got, want)
	}

	stale := client.request(semanticTokensDelta, map[string]any{"textDocument": document, "previousResultId": "no-such-result"})["result"].(map[string]any)
	if _, isFull := stale["data"]; !isFull {
		t.Errorf("a delta from an unknown result = %v, want a full result", stale)
	}

	ranged := client.request("textDocument/semanticTokens/range", map[string]any{"textDocument": document, "range": map[string]any{"start": map[string]any{"line": 2, "character": 0}, "end": map[string]any{"line": 3, "character": 0}}})["result"].(map[string]any)
	tokens, _ := ranged["data"].([]any)
	if len(tokens) == 0 || len(tokens) >= len(again["data"].([]any)) || tokens[0].(float64) != 2 {
		t.Errorf("range tokens = %v, want only line 2's, relative to the document start", tokens)
	}
}

func jsonText(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
