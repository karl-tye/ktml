package lsp

import (
	"encoding/json"
	"testing"
)

func TestMergeResponses_GIVENDefinitionAndNull_WHENMerged_THENPreservesDefinition(t *testing.T) {
	definition := []byte(`{
		"jsonrpc":"2.0",
		"id":1,
		"result":{
			"uri":"file:///components.ktml",
			"range":{
				"start":{"line":1,"character":0},
				"end":{"line":1,"character":12}
			}
		}
	}`)
	nullResult := []byte(`{"jsonrpc":"2.0","id":1,"result":null}`)

	for _, responses := range [][][]byte{
		{definition, nullResult},
		{nullResult, definition},
	} {
		merged := mergeResponses(responses)
		var response struct {
			Result *struct {
				URI string `json:"uri"`
			} `json:"result"`
		}
		if err := json.Unmarshal(merged, &response); err != nil {
			t.Fatalf("decode merged response: %v", err)
		}
		if response.Result == nil || response.Result.URI != "file:///components.ktml" {
			t.Errorf("merged response = %s", merged)
		}
	}
}

func TestMergeResponses_GIVENTheIndexAndAnAnalyzerFindTheSameLocation_WHENMerged_THENItAppearsOnce(t *testing.T) {
	location := `{"uri":"file:///app.go","range":{"start":{"line":3,"character":1},"end":{"line":3,"character":9}}}`
	other := `{"uri":"file:///b.ktml","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`
	tests := []struct {
		name, method string
		responses    []string
		want         string
	}{
		{"definition kept alone", "textDocument/definition", []string{`{"jsonrpc":"2.0","id":1,"result":` + location + `}`, `{"jsonrpc":"2.0","id":1,"result":[` + location + `]}`}, location},
		{"definition joined", "textDocument/definition", []string{`{"jsonrpc":"2.0","id":1,"result":` + location + `}`, `{"jsonrpc":"2.0","id":1,"result":[` + location + `,` + other + `]}`}, `[` + location + `,` + other + `]`},
		{"references", "textDocument/references", []string{`{"jsonrpc":"2.0","id":1,"result":[` + location + `]}`, `{"jsonrpc":"2.0","id":1,"result":[` + location + `,` + other + `]}`}, `[` + location + `,` + other + `]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var responses [][]byte
			for _, response := range test.responses {
				responses = append(responses, []byte(response))
			}
			var merged, want struct {
				Result any `json:"result"`
			}
			if err := json.Unmarshal(mergeResponsesFor(test.method, responses), &merged); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(`{"result":`+test.want+`}`), &want)
			got, _ := json.Marshal(merged.Result)
			wanted, _ := json.Marshal(want.Result)
			if string(got) != string(wanted) {
				t.Errorf("merged = %s, want %s", got, wanted)
			}
		})
	}
}

func TestInitialize_GIVENTheDeeperLanguageFeatures_WHENAdvertised_THENTheClientSeesThem(t *testing.T) {
	var response struct {
		Result struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(initializeResponse(json.RawMessage(`1`)), &response); err != nil {
		t.Fatal(err)
	}
	capabilities := response.Result.Capabilities
	for _, name := range []string{"callHierarchyProvider", "documentHighlightProvider", "documentRangeFormattingProvider"} {
		if capabilities[name] != true {
			t.Errorf("%s = %v", name, capabilities[name])
		}
	}
	onType, _ := capabilities["documentOnTypeFormattingProvider"].(map[string]any)
	if onType["firstTriggerCharacter"] != "}" {
		t.Errorf("documentOnTypeFormattingProvider = %v", onType)
	}
	signature, _ := capabilities["signatureHelpProvider"].(map[string]any)
	if triggers, _ := json.Marshal(signature["triggerCharacters"]); string(triggers) != `[" ","=","("]` {
		t.Errorf("signature help triggers = %s", triggers)
	}
}

func TestMergeResponses_GIVENAKTMLAndAGoHover_WHENMerged_THENBothShowKTMLFirst(t *testing.T) {
	ktmlHover := []byte(`{"jsonrpc":"2.0","id":1,"result":{"contents":{"kind":"markdown","value":"input view: CardView"},"range":{"start":{"line":4,"character":7},"end":{"line":4,"character":11}}}}`)
	goHover := []byte(`{"jsonrpc":"2.0","id":1,"result":{"contents":{"kind":"markdown","value":"field View CardView"},"range":{"start":{"line":4,"character":7},"end":{"line":4,"character":11}}}}`)
	null := []byte(`{"jsonrpc":"2.0","id":1,"result":null}`)
	var merged struct {
		Result struct {
			Contents struct {
				Value string `json:"value"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(mergeResponsesFor("textDocument/hover", [][]byte{ktmlHover, null, goHover}), &merged); err != nil {
		t.Fatal(err)
	}
	if want := "input view: CardView\n\n---\n\nfield View CardView"; merged.Result.Contents.Value != want {
		t.Errorf("merged hover = %q, want %q", merged.Result.Contents.Value, want)
	}
}
