package gohtml

import "testing"

func TestLSP_GIVENEmptyOpenDocument_WHENTracked_THENKeepsUnsavedOverlay(t *testing.T) {
	uri := "file:///empty.ktml"
	if _, err := HandleLSP("textDocument/didOpen", []byte(`{"textDocument":{"uri":"file:///empty.ktml","text":""}}`)); err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = HandleLSP("textDocument/didClose", []byte(`{"textDocument":{"uri":"file:///empty.ktml"}}`))
	})
	text, ok := getOpenDocuments()[uri]
	if !ok || text != "" {
		t.Errorf("open document = (%q, %v), want empty tracked overlay", text, ok)
	}
}
