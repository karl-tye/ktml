package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInitializeResponse_GIVENSemanticTokens_THENAdvertisesFullDeltaRangeAndLegend(t *testing.T) {
	var response struct {
		Result struct {
			Capabilities struct {
				SemanticTokensProvider *struct {
					Legend struct {
						TokenTypes     []string `json:"tokenTypes"`
						TokenModifiers []string `json:"tokenModifiers"`
					} `json:"legend"`
					Full struct {
						Delta bool `json:"delta"`
					} `json:"full"`
					Range bool `json:"range"`
				} `json:"semanticTokensProvider"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(initializeResponse(json.RawMessage("1")), &response); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	provider := response.Result.Capabilities.SemanticTokensProvider
	if provider == nil || !provider.Full.Delta || !provider.Range {
		t.Fatalf("semanticTokensProvider = %#v, want full results with deltas, and ranges", provider)
	}
	if got := strings.Join(provider.Legend.TokenTypes, ","); got != "keyword,type,property,parameter,enumMember,operator" {
		t.Errorf("token types = %s", got)
	}
	if got := strings.Join(provider.Legend.TokenModifiers, ","); got != "declaration" {
		t.Errorf("token modifiers = %s", got)
	}
}

func TestMergeResponsesFor_GIVENSemanticTokensFromSeveralPlugins_THENKeepsOneEncodedResult(t *testing.T) {
	tokens := []byte(`{"jsonrpc":"2.0","id":4,"result":{"data":[0,0,6,0,0,0,7,1,1,1]}}`)
	other := []byte(`{"jsonrpc":"2.0","id":4,"result":{"data":[2,0,3,0,0]}}`)
	null := []byte(`{"jsonrpc":"2.0","id":4,"result":null}`)
	tests := []struct {
		name      string
		responses [][]byte
		want      string
	}{
		{name: "null first", responses: [][]byte{null, tokens}, want: "0,0,6,0,0,0,7,1,1,1"},
		{name: "null last", responses: [][]byte{tokens, null}, want: "0,0,6,0,0,0,7,1,1,1"},
		{name: "two results keep the first", responses: [][]byte{tokens, other}, want: "0,0,6,0,0,0,7,1,1,1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := mergeResponsesFor("textDocument/semanticTokens/full", tt.responses)
			var response struct {
				ID     int `json:"id"`
				Result struct {
					Data []json.Number `json:"data"`
				} `json:"result"`
			}
			if err := json.Unmarshal(merged, &response); err != nil {
				t.Fatalf("decode merged %s: %v", merged, err)
			}
			var data []string
			for _, value := range response.Result.Data {
				data = append(data, value.String())
			}
			if got := strings.Join(data, ","); got != tt.want || response.ID != 4 {
				t.Errorf("merged = %s, want data %s", merged, tt.want)
			}
		})
	}
}

func TestPluginNotifications_GIVENSingleOrBatchedPluginNotifications_THENForwardsEachValidNotification(t *testing.T) {
	first := `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///a.ktmlschema","diagnostics":[]}}`
	second := `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///b.ktml","diagnostics":[]}}`
	response := `{"jsonrpc":"2.0","id":1,"result":null}`
	tests := []struct {
		name    string
		payload string
		want    []string
	}{
		{name: "single notification", payload: first, want: []string{first}},
		{name: "batch", payload: "[" + first + "," + second + "]", want: []string{first, second}},
		{name: "batch drops non-notifications", payload: "[" + first + "," + response + "]", want: []string{first}},
		{name: "response is not forwarded", payload: response},
		{name: "malformed batch", payload: "[" + first},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pluginNotifications([]byte(tt.payload))
			if len(got) != len(tt.want) {
				t.Fatalf("notifications = %q, want %q", got, tt.want)
			}
			for index := range got {
				if string(got[index]) != tt.want[index] {
					t.Errorf("notification %d = %s, want %s", index, got[index], tt.want[index])
				}
			}
		})
	}
}
