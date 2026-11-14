package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestServe_GIVENMalformedRequestOrNotificationEnvelope_WHENReceived_THENReturnsInvalidRequest(t *testing.T) {
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"method":null}`,
		`{"jsonrpc":"2.0","method":42}`,
		`{"jsonrpc":"2.0","id":null,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","result":null}`,
		`{"jsonrpc":"2.0","id":true,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1.5,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","params":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","params":"bad"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","error":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","method":"other"}`,
		`{"jsonrpc":"2.0","id":1,"id":2,"method":"test"}`,
		`{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"method":"test"}`,
		`{"jsonrpc":null,"id":1,"method":"test"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
			var output bytes.Buffer

			Serve(context.Background(), input, &output, nil)

			responsePayload := framedPayload(t, output.Bytes())
			var response struct {
				Error *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(responsePayload, &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error == nil || response.Error.Code != -32600 {
				t.Fatalf("Serve() response = %s, want invalid request", responsePayload)
			}
		})
	}
}

func TestServe_GIVENValidResponseEnvelope_WHENReceived_THENWritesNothing(t *testing.T) {
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`,
	} {
		input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
		var output bytes.Buffer
		Serve(context.Background(), input, &output, nil)
		if output.Len() != 0 {
			t.Fatalf("Serve(%s) output = %q, want no response to a response", payload, output.String())
		}
	}
}

func TestServe_GIVENBuiltInNotifications_THENNeverWritesResponses(t *testing.T) {
	for _, payload := range []string{
		`{"jsonrpc":"2.0","method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"shutdown"}`,
	} {
		input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
		var output bytes.Buffer
		Serve(context.Background(), input, &output, nil)
		if output.Len() != 0 {
			t.Fatalf("Serve(%s) output = %q", payload, output.String())
		}
	}
}

func TestServe_GIVENMalformedJSONVersusInvalidStructure_THENUsesDistinctErrors(t *testing.T) {
	for _, test := range []struct {
		payload string
		code    int
	}{
		{`{"jsonrpc":"2.0","id":1,"method":`, -32700},
		{`[]`, -32600},
		{`[{"jsonrpc":"2.0","id":1,"method":"test"}]`, -32600},
	} {
		input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(test.payload), test.payload))
		var output bytes.Buffer
		Serve(context.Background(), input, &output, nil)
		var response struct {
			ID    json.RawMessage `json:"id"`
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(framedPayload(t, output.Bytes()), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error.Code != test.code || string(response.ID) != "null" {
			t.Fatalf("Serve(%s) response = %s, want code %d and id null", test.payload, framedPayload(t, output.Bytes()), test.code)
		}
	}
}

func TestServe_GIVENAmbiguousDuplicateID_THENInvalidRequestUsesNullID(t *testing.T) {
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"id":2,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","result":null}`,
		`{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":-32000,"message":"both"}}`,
	} {
		input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
		var output bytes.Buffer
		Serve(context.Background(), input, &output, nil)
		var response struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(framedPayload(t, output.Bytes()), &response); err != nil {
			t.Fatal(err)
		}
		if string(response.ID) != "null" {
			t.Fatalf("response = %s, want id null", framedPayload(t, output.Bytes()))
		}
	}
}

func TestServe_GIVENValidNotificationEdgeCases_WHENReceived_THENWritesNothing(t *testing.T) {
	for _, payload := range []string{
		`{"jsonrpc":"2.0","method":"ktml/unknown","params":[]}`,
		`{"jsonrpc":"2.0","method":"","params":{}}`,
	} {
		input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
		var output bytes.Buffer
		Serve(context.Background(), input, &output, nil)
		if output.Len() != 0 {
			t.Fatalf("Serve(%s) output = %q, want none", payload, output.String())
		}
	}
}

func TestServe_GIVENOutOfRangeContentLength_WHENReceived_THENStopsWithoutPanicOrOutput(t *testing.T) {
	tests := []struct {
		name   string
		length string
	}{
		{name: "negative", length: "-1"},
		{name: "beyond maximum message size", length: "4611686018427387904"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := `{"jsonrpc":"2.0","id":1,"method":"test"}`
			input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %s\r\n\r\n%s", test.length, payload))
			var output bytes.Buffer
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("Serve() panicked on Content-Length %s: %v", test.length, recovered)
					}
				}()
				Serve(context.Background(), input, &output, nil)
			}()
			if output.Len() != 0 {
				t.Errorf("Serve() output = %q, want no response for unframeable input", output.String())
			}
		})
	}
}
