package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestServe_GIVENUnsupportedRequest_WHENHandled_THENReturnsMethodNotFoundWithOriginalID(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":"request-1","method":"ktml/unknown","params":{}}`)
	input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
	var output bytes.Buffer

	Serve(context.Background(), input, &output, nil)

	responsePayload := framedPayload(t, output.Bytes())
	var response struct {
		ID    string `json:"id"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "request-1" || response.Error == nil || response.Error.Code != -32601 {
		t.Errorf("response = %s, want method-not-found for request-1", responsePayload)
	}
}

func TestServe_GIVENUnsupportedNotification_WHENHandled_THENWritesNothing(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","method":"ktml/unknown","params":{}}`)
	input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
	var output bytes.Buffer

	Serve(context.Background(), input, &output, nil)

	if output.Len() != 0 {
		t.Errorf("Serve() output = %q, want none", output.String())
	}
}

func TestMergeResponsesFor_GIVENCompletions_WHENMerged_THENCombinesItemsAndIncompleteState(t *testing.T) {
	first := []byte(`{"jsonrpc":"2.0","id":1,"result":{"isIncomplete":true,"items":[{"label":"Card"}]}}`)
	second := []byte(`{"jsonrpc":"2.0","id":1,"result":{"isIncomplete":false,"items":[{"label":"hx-get"}]}}`)

	merged := mergeResponsesFor("textDocument/completion", [][]byte{first, second})
	var response struct {
		Result struct {
			IsIncomplete bool `json:"isIncomplete"`
			Items        []struct {
				Label string `json:"label"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(merged, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Result.IsIncomplete || len(response.Result.Items) != 2 {
		t.Errorf("merged response = %s", merged)
	}
}

func framedPayload(t *testing.T, framed []byte) []byte {
	t.Helper()
	separator := []byte("\r\n\r\n")
	parts := bytes.SplitN(framed, separator, 2)
	if len(parts) != 2 {
		t.Fatalf("invalid framed response %q", framed)
	}
	return parts[1]
}
