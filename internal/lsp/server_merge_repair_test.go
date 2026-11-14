package lsp

import (
	"encoding/json"
	"testing"
)

func TestMergeResponses_GIVENResultAndErrorInEitherOrder_THENReturnsOnlyResult(t *testing.T) {
	result := []byte(`{"jsonrpc":"2.0","id":"request","result":{"value":1}}`)
	rpcErr := []byte(`{"jsonrpc":"2.0","id":"request","error":{"code":-32000,"message":"plugin failed"}}`)

	for _, method := range []string{"textDocument/definition", "textDocument/completion"} {
		for _, responses := range [][][]byte{{result, rpcErr}, {rpcErr, result}} {
			merged := mergeResponsesFor(method, responses)
			message := classifyRPCMessage(merged)
			if message.err != nil || message.kind != rpcResponse {
				t.Fatalf("mergeResponsesFor(%q) = %s, classification error = %v", method, merged, message.err)
			}
			if _, hasResult := message.object.members["result"]; !hasResult {
				t.Fatalf("mergeResponsesFor(%q) = %s, want result response", method, merged)
			}
			if _, hasError := message.object.members["error"]; hasError {
				t.Fatalf("mergeResponsesFor(%q) = %s, contains result and error", method, merged)
			}
		}
	}
}

func TestMergeResponsesFor_GIVENOnlyInvalidCompletionResults_THENStillAnswersRequest(t *testing.T) {
	invalid := []byte(`{"jsonrpc":"2.0","id":7,"result":"not completions"}`)
	malformedItems := []byte(`{"jsonrpc":"2.0","id":7,"result":{"items":"bad"}}`)

	merged := mergeResponsesFor("textDocument/completion", [][]byte{invalid, malformedItems})
	message := classifyRPCMessage(merged)
	if message.err != nil || message.kind != rpcResponse {
		t.Fatalf("mergeResponsesFor() = %q, classification error = %v; a request must receive a response", merged, message.err)
	}
	if string(message.id) != "7" {
		t.Errorf("merged response ID = %s, want 7", message.id)
	}
	if _, hasError := message.object.members["error"]; !hasError {
		t.Errorf("merged response = %s, want error for invalid completion results", merged)
	}
}

func TestMergeResponses_GIVENInvalidAndErrorResponses_THENReturnsDeterministicValidError(t *testing.T) {
	first := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"zeta"}}`)
	second := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"alpha"}}`)
	invalid := []byte(`{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":-32000,"message":"both"}}`)

	a := mergeResponsesFor("textDocument/definition", [][]byte{first, invalid, second})
	b := mergeResponsesFor("textDocument/definition", [][]byte{second, invalid, first})
	if string(a) != string(b) {
		t.Fatalf("error merge depends on plugin order: %s != %s", a, b)
	}
	message := classifyRPCMessage(a)
	if message.err != nil || message.kind != rpcResponse {
		t.Fatalf("merged error = %s, classification error = %v", a, message.err)
	}
	if _, hasError := message.object.members["error"]; !hasError {
		t.Fatalf("merged response = %s, want error", a)
	}
	if _, hasResult := message.object.members["result"]; hasResult {
		t.Fatalf("merged response = %s, contains error and result", a)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(a, &envelope); err != nil {
		t.Fatalf("decode merged error: %v", err)
	}
}

func TestMergeResponsesFor_GIVENOneLocationAndALocationList_WHENDefinitionMerges_THENKeepsEveryLocation(t *testing.T) {
	single := []byte(`{"jsonrpc":"2.0","id":3,"result":{"uri":"file:///a.ktmlschema","range":{"start":{"line":1,"character":1},"end":{"line":1,"character":6}}}}`)
	list := []byte(`{"jsonrpc":"2.0","id":3,"result":[{"uri":"file:///present.go","range":{"start":{"line":5,"character":3},"end":{"line":5,"character":8}}}]}`)
	empty := []byte(`{"jsonrpc":"2.0","id":3,"result":null}`)
	merged := mergeResponsesFor("textDocument/definition", [][]byte{single, empty, list})
	var response struct {
		Result []struct {
			URI string `json:"uri"`
		} `json:"result"`
	}
	if err := json.Unmarshal(merged, &response); err != nil {
		t.Fatalf("merged = %s: %v", merged, err)
	}
	if len(response.Result) != 2 || response.Result[0].URI != "file:///a.ktmlschema" || response.Result[1].URI != "file:///present.go" {
		t.Errorf("merged = %s; want the schema location then the Go location", merged)
	}
}

func TestMergeResponsesFor_GIVENOneDefinitionAnswer_WHENMerged_THENKeepsItsShape(t *testing.T) {
	single := []byte(`{"jsonrpc":"2.0","id":3,"result":{"uri":"file:///a.ktml","range":{"start":{"line":1,"character":1},"end":{"line":1,"character":6}}}}`)
	empty := []byte(`{"jsonrpc":"2.0","id":3,"result":null}`)
	merged := mergeResponsesFor("textDocument/definition", [][]byte{empty, single, empty})
	var response struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(merged, &response); err != nil || response.Result["uri"] != "file:///a.ktml" {
		t.Errorf("merged = %s (%v); want the single Location", merged, err)
	}
}
