package htmx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnostics_GIVENMethodMismatchAndMissingRoute_WHENDocumentOpens_THENPublishesBothProblems(t *testing.T) {
	dir := t.TempDir()
	goSource := `package web
import "github.com/go-chi/chi/v5"
func Routes() { r := chi.NewRouter(); r.Get("/known", handler) }
func handler() {}`
	if err := os.WriteFile(filepath.Join(dir, "routes.go"), []byte(goSource), 0o600); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	configureForTest(t, dir)
	payload := []byte(`{"textDocument":{"uri":"file:///page.ktml","text":"Page { button hx-post=\"/known\" button hx-get=\"/missing\" }"}}`)

	response, err := HandleLSP("textDocument/didOpen", payload)
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var notification struct {
		Method string `json:"method"`
		Params struct {
			Diagnostics []struct {
				Code string `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if notification.Method != "textDocument/publishDiagnostics" || len(notification.Params.Diagnostics) != 2 {
		t.Fatalf("diagnostics response = %s", response)
	}
	if notification.Params.Diagnostics[0].Code != "htmx-method-mismatch" || notification.Params.Diagnostics[1].Code != "htmx-missing-route" {
		t.Errorf("diagnostics response = %s", response)
	}
}
