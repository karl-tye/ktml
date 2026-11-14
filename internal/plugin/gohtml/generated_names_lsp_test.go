package gohtml

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestLSP_GeneratedNameDiagnostics_GIVENCaseOnlyComponentNames_THENPublishesBothDeclarations(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	source := "Row {\n\tdiv\n}\n\nrow {\n\tspan\n}\n"
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	sort.Strings(got)
	want := "generated-name-collision:Row\ngenerated-name-collision:row"
	if joined := strings.Join(got, "\n"); joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}
