package htmx

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleLSP_GIVENFrameworkNamesContainingHXRoutes_WHENFindingDefinition_THENOnlyRealRouteAttributesMatch(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/vendors", createVendor)
	return r
}`)
	configureForTest(t, dir)
	tests := []struct {
		name, line string
		route      bool
	}{
		{name: "plain", line: `Button hx-post="/vendors"`, route: true},
		{name: "data prefix", line: `Button data-hx-post="/vendors"`, route: true},
		{name: "continuation group", line: `Button (hx-post="/vendors")`, route: true},
		{name: "conditional group", line: `Button (if on {hx-post="/vendors"})`, route: true},
		{name: "alpine bind long form", line: `Button x-bind:hx-post="/vendors"`},
		{name: "alpine bind shorthand", line: `Button :hx-post="/vendors"`},
		{name: "namespaced suffix", line: `Button my-hx-post="/vendors"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			openDocumentForTest(t, "file:///component.ktml", test.line)
			character := strings.Index(test.line, "/vendors") + 2
			responseBytes, err := HandleLSP("textDocument/definition", []byte(`{
				"id": 2,
				"textDocument": {"uri": "file:///component.ktml"},
				"position": {"line": 0, "character": `+itoa(character)+`}
			}`))
			if err != nil {
				t.Fatalf("HandleLSP() error = %v", err)
			}
			var response struct {
				Result *Location `json:"result"`
			}
			if err := json.Unmarshal(responseBytes, &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got := response.Result != nil && response.Result.URI != ""; got != test.route {
				t.Fatalf("definition found = %v, want %v: %s", got, test.route, responseBytes)
			}
		})
	}
}

func itoa(value int) string {
	digits := []byte{}
	for {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
		if value == 0 {
			return string(digits)
		}
	}
}
