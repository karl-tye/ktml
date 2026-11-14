package htmx

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/symbolindex"
)

func indexForTest(t *testing.T, request map[string]any) symbolindex.Result {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := HandleLSP(symbolindex.Method, payload)
	if err != nil {
		t.Fatalf("index error = %v", err)
	}
	var response struct {
		Result symbolindex.Result `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return response.Result
}

func TestHandleLSP_GIVENChiRoutesAndHTMXAttributes_WHENIndexed_THENRoutesAreDeclaredInGoAndReferencedFromKTML(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/vendors", getVendors)
	r.Post("/vendors/{id}/approve", approveVendor)
	return r
}`)
	configureForTest(t, dir)
	uri := "file:///component.ktml"
	openDocumentForTest(t, uri, "Button hx-post=\"/vendors/{id}/approve\"\nLink hx-get=\"/nowhere\"")

	result := indexForTest(t, map[string]any{"id": 1, "uris": []string{uri}})
	goURI := "file://" + filepath.ToSlash(filepath.Join(dir, "router.go"))
	var routes, references []string
	for _, document := range result.Documents {
		for _, symbol := range document.Symbols {
			if document.URI != goURI || symbol.Kind != symbolindex.Route {
				t.Errorf("symbol %+v in %s", symbol, document.URI)
			}
			routes = append(routes, symbol.ID)
		}
		for _, reference := range document.References {
			if document.URI != uri || reference.Range.Start.Character != 16 {
				t.Errorf("reference %+v in %s", reference, document.URI)
			}
			references = append(references, reference.Symbol)
		}
	}
	if len(routes) != 2 || routes[0] != "route:GET /vendors" || routes[1] != "route:POST /vendors/{id}/approve" {
		t.Errorf("routes = %v", routes)
	}
	if len(references) != 1 || references[0] != "route:POST /vendors/{id}/approve" {
		t.Errorf("references = %v, want only the attribute that reaches a route", references)
	}
}

func TestHandleLSP_GIVENNoScanYet_WHENIndexed_THENNothingIsIndexed(t *testing.T) {
	settings, _ := json.Marshal(map[string]any{"chi": map[string]any{"dir": t.TempDir()}})
	if err := Configure(settings); err != nil {
		t.Fatal(err)
	}
	if result := indexForTest(t, map[string]any{"id": 1, "workspace": true}); len(result.Documents) != 0 || len(result.Removed) != 0 {
		t.Errorf("result = %+v, want nothing before the first scan", result)
	}
}
