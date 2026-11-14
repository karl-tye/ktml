package lsp

import (
	"encoding/json"
	"testing"
)

func publish(uri string, messages ...string) []byte {
	diagnostics := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		diagnostics = append(diagnostics, map[string]any{
			"range":   map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 1}},
			"message": message,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params":  map[string]any{"uri": uri, "diagnostics": diagnostics},
	})
	return payload
}

func publishedMessages(t *testing.T, notification []byte) (string, []string) {
	t.Helper()
	var decoded struct {
		Method string `json:"method"`
		Params struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Message string `json:"message"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(notification, &decoded); err != nil {
		t.Fatalf("decode notification %s: %v", notification, err)
	}
	if decoded.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("method = %q, want publishDiagnostics", decoded.Method)
	}
	messages := make([]string, 0, len(decoded.Params.Diagnostics))
	for _, diagnostic := range decoded.Params.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	return decoded.Params.URI, messages
}

func TestDiagnosticsMerger_GIVENTwoPluginsPublishSameURI_WHENMerged_THENSendsOneUnion(t *testing.T) {
	merger := newDiagnosticsMerger()
	out := merger.notifications([][]byte{
		publish("file:///a.ktml", "syntax"),
		publish("file:///a.ktml"),
	})
	if len(out) != 1 {
		t.Fatalf("notifications = %d, want 1: %q", len(out), out)
	}
	uri, messages := publishedMessages(t, out[0])
	if uri != "file:///a.ktml" || len(messages) != 1 || messages[0] != "syntax" {
		t.Fatalf("merged = %s %q, want file:///a.ktml [syntax]", uri, messages)
	}
}

func TestDiagnosticsMerger_GIVENLaterPublishFromOnePlugin_WHENMerged_THENKeepsOtherPluginsDiagnostics(t *testing.T) {
	merger := newDiagnosticsMerger()
	merger.notifications([][]byte{publish("file:///a.ktml", "syntax"), publish("file:///a.ktml", "route")})
	out := merger.notifications([][]byte{nil, publish("file:///a.ktml")})
	if len(out) != 1 {
		t.Fatalf("notifications = %d, want 1", len(out))
	}
	_, messages := publishedMessages(t, out[0])
	if len(messages) != 1 || messages[0] != "syntax" {
		t.Fatalf("messages = %q, want [syntax]", messages)
	}
}

func TestDiagnosticsMerger_GIVENBatchAndOtherNotifications_WHENMerged_THENForwardsOthersAndOneDiagnosticsPerURI(t *testing.T) {
	merger := newDiagnosticsMerger()
	batch, _ := json.Marshal([]json.RawMessage{publish("file:///a.ktml", "a"), publish("file:///b.ktml", "b")})
	logMessage := []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{"type":3,"message":"hi"}}`)
	out := merger.notifications([][]byte{batch, logMessage, publish("file:///b.ktml", "b2")})
	if len(out) != 3 {
		t.Fatalf("notifications = %d, want 3: %q", len(out), out)
	}
	if string(out[0]) != string(logMessage) {
		t.Errorf("first = %s, want the log message forwarded unchanged", out[0])
	}
	uri, messages := publishedMessages(t, out[1])
	if uri != "file:///a.ktml" || len(messages) != 1 {
		t.Errorf("second = %s %q", uri, messages)
	}
	uri, messages = publishedMessages(t, out[2])
	if uri != "file:///b.ktml" || len(messages) != 2 || messages[0] != "b" || messages[1] != "b2" {
		t.Errorf("third = %s %q, want b.ktml [b b2]", uri, messages)
	}
}

func TestDiagnosticsMerger_GIVENBothPluginsRepublish_WHENMerged_THENEachReplacesItsOwnEarlierSet(t *testing.T) {
	merger := newDiagnosticsMerger()
	merger.notifications([][]byte{publish("file:///a.ktml", "old0"), publish("file:///a.ktml", "old1")})
	out := merger.notifications([][]byte{publish("file:///a.ktml", "new0"), publish("file:///a.ktml", "new1")})
	_, messages := publishedMessages(t, out[0])
	if len(messages) != 2 || messages[0] != "new0" || messages[1] != "new1" {
		t.Fatalf("messages = %q, want [new0 new1]", messages)
	}
}
