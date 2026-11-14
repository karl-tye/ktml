package wasm

import (
	"encoding/json"
	"testing"
)

func TestProxyErrorResponse_GIVENRequestIDs_THENCorrelatesOnlyUnambiguousValidIDs(t *testing.T) {
	for _, test := range []struct {
		payload string
		wantID  string
	}{
		{`{"jsonrpc":"2.0","id":"\u0073ame","method":"test"}`, `"\u0073ame"`},
		{`{"jsonrpc":"2.0","id":1e0,"method":"test"}`, `1e0`},
		{`{"jsonrpc":"2.0","id":1.5,"method":"test"}`, `null`},
		{`{"jsonrpc":"2.0","id":1,"id":2,"method":"test"}`, `null`},
		{`not-json`, `null`},
	} {
		var response struct {
			ID json.RawMessage `json:"id"`
		}
		payload := proxyErrorResponse([]byte(test.payload), -32001, "quoted \"failure\"\n")
		if err := json.Unmarshal(payload, &response); err != nil {
			t.Fatalf("proxy error response %s is invalid: %v", payload, err)
		}
		if string(response.ID) != test.wantID {
			t.Fatalf("proxyErrorResponse(%s) id = %s, want %s", test.payload, response.ID, test.wantID)
		}
	}
}
