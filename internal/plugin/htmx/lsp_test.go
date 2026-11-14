package htmx

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleLSP_GIVENHTMXPost_WHENCompleting_THENReturnsMatchingChiRoutes(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/vendors", getVendors)
	r.Post("/vendors", createVendor)
	r.Post("/vendors/{id}/approve", approveVendor)
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///component.ktml", `Button hx-post="/vendors"`)

	responseBytes, err := HandleLSP("textDocument/completion", []byte(`{
		"id": 1,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 24}
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result struct {
			Items []struct {
				Label string `json:"label"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Result.Items) != 2 {
		t.Fatalf("completion count = %d, want 2: %s", len(response.Result.Items), responseBytes)
	}
	if response.Result.Items[0].Label != "/vendors" || response.Result.Items[1].Label != "/vendors/{id}/approve" {
		t.Errorf("completion labels = %#v", response.Result.Items)
	}
}

func TestHandleLSP_GIVENHTMXRoute_WHENFindingDefinition_THENReturnsChiRegistration(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/vendors/{id}/approve", approveVendor)
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///component.ktml", `Button hx-post="/vendors/{id}/approve"`)

	responseBytes, err := HandleLSP("textDocument/definition", []byte(`{
		"id": 2,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 25}
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result Location `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, want := response.Result.URI, "file://"+filepath.ToSlash(filepath.Join(dir, "router.go")); got != want {
		t.Errorf("definition URI = %q, want %q", got, want)
	}
}

func TestHandleLSP_GIVENCursorAcrossHTMXAttribute_WHENFindingDefinition_THENReturnsChiRegistration(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/get", handler)
	r.Post("/post", handler)
	r.Put("/put", handler)
	r.Patch("/patch", handler)
	r.Delete("/delete", handler)
	return r
}`)
	configureForTest(t, dir)

	tests := []struct {
		name      string
		attribute string
		positions []int
	}{
		{name: "get", attribute: `hx-get="/get"`, positions: []int{0, 3, 6, 8, 11}},
		{name: "post", attribute: `hx-post="/post"`, positions: []int{0, 4, 7, 9, 14}},
		{name: "put", attribute: `hx-put="/put"`, positions: []int{0, 3, 6, 8, 11}},
		{name: "patch", attribute: `hx-patch="/patch"`, positions: []int{0, 5, 8, 10, 16}},
		{name: "delete", attribute: `hx-delete="/delete"`, positions: []int{0, 6, 9, 11, 18}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			uri := "file:///" + test.name + ".ktml"
			text := "Button " + test.attribute
			openDocumentForTest(t, uri, text)
			for _, position := range test.positions {
				payload, err := json.Marshal(map[string]any{
					"id":           position + 1,
					"textDocument": map[string]any{"uri": uri},
					"position":     map[string]any{"line": 0, "character": len("Button ") + position},
				})
				if err != nil {
					t.Fatalf("marshal definition request: %v", err)
				}
				responseBytes, err := HandleLSP("textDocument/definition", payload)
				if err != nil {
					t.Fatalf("HandleLSP() at character %d: %v", position, err)
				}
				var response struct {
					Result *Location `json:"result"`
				}
				if err := json.Unmarshal(responseBytes, &response); err != nil {
					t.Fatalf("decode definition response: %v", err)
				}
				if response.Result == nil {
					t.Errorf("definition at character %d is nil: %s", position, responseBytes)
				}
			}
		})
	}
}

func TestHandleLSP_GIVENChiRegistration_WHENFindingReferences_THENReturnsHTMXConsumers(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/vendors/{id}/approve", approveVendor)
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///first.ktml", `Button hx-post="/vendors/{id}/approve"`)
	openDocumentForTest(t, "file:///second.ktml", "Form {\n\tButton hx-post=\"/vendors/{id}/approve\"\n}")
	openDocumentForTest(t, "file:///other.ktml", `Button hx-get="/vendors/{id}/approve"`)

	responseBytes, err := HandleLSP("textDocument/references", []byte(`{
		"id": 3,
		"textDocument": {"uri": "file://`+filepath.ToSlash(filepath.Join(dir, "router.go"))+`"},
		"position": {"line": 4, "character": 4},
		"context": {"includeDeclaration": false}
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result []Location `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Result) != 2 {
		t.Fatalf("reference count = %d, want 2: %s", len(response.Result), responseBytes)
	}
	if response.Result[0].URI != "file:///first.ktml" || response.Result[1].URI != "file:///second.ktml" {
		t.Errorf("reference URIs = %#v", response.Result)
	}
	if got, want := response.Result[1].Range.Start, (Position{Line: 1, Character: 17}); got != want {
		t.Errorf("second reference start = %#v, want %#v", got, want)
	}
}

func TestHandleLSP_GIVENHTMXAttribute_WHENFindingReferencesWithDeclaration_THENReturnsBothSides(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Delete("/vendors/{id}", deleteVendor)
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///component.ktml", `Button hx-delete="/vendors/{id}"`)

	responseBytes, err := HandleLSP("textDocument/references", []byte(`{
		"id": 4,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 10},
		"context": {"includeDeclaration": true}
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result []Location `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Result) != 2 {
		t.Fatalf("reference count = %d, want declaration and consumer: %s", len(response.Result), responseBytes)
	}
}

func TestHandleLSP_GIVENStaticHTMXRoute_WHENPreparingRename_THENReturnsRouteValueRange(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/tickets", listTickets)
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///component.ktml", `Button hx-get="/tickets"`)

	responseBytes, err := HandleLSP("textDocument/prepareRename", []byte(`{
		"id": 5,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 9}
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result *struct {
			Range       Range  `json:"range"`
			Placeholder string `json:"placeholder"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result == nil {
		t.Fatalf("prepareRename result is nil: %s", responseBytes)
	}
	if got, want := response.Result.Placeholder, "/tickets"; got != want {
		t.Errorf("placeholder = %q, want %q", got, want)
	}
	if got, want := response.Result.Range.Start, (Position{Line: 0, Character: 15}); got != want {
		t.Errorf("range start = %#v, want %#v", got, want)
	}
}

func TestHandleLSP_GIVENStaticChiRoute_WHENRenaming_THENEditsRegistrationAndMatchingConsumers(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/tickets", createTicket)
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///first.ktml", `Button hx-post="/tickets"`)
	openDocumentForTest(t, "file:///second.ktml", `Form hx-post="/tickets"`)
	openDocumentForTest(t, "file:///other.ktml", `Button hx-get="/tickets"`)
	routerURI := "file://" + filepath.ToSlash(filepath.Join(dir, "router.go"))

	responseBytes, err := HandleLSP("textDocument/rename", []byte(`{
		"id": 6,
		"textDocument": {"uri": "`+routerURI+`"},
		"position": {"line": 4, "character": 4},
		"newName": "/requests"
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result *struct {
			Changes map[string][]struct {
				NewText string `json:"newText"`
			} `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result == nil {
		t.Fatalf("rename result is nil: %s", responseBytes)
	}
	if got := response.Result.Changes[routerURI]; len(got) != 1 || got[0].NewText != `"/requests"` {
		t.Errorf("Chi edits = %#v", got)
	}
	for _, uri := range []string{"file:///first.ktml", "file:///second.ktml"} {
		if got := response.Result.Changes[uri]; len(got) != 1 || got[0].NewText != "/requests" {
			t.Errorf("%s edits = %#v", uri, got)
		}
	}
	if edits := response.Result.Changes["file:///other.ktml"]; len(edits) != 0 {
		t.Errorf("GET consumer edits = %#v, want none", edits)
	}
}

func TestHandleLSP_GIVENRenamedRoute_WHENFindingDefinitionBeforeFilesystemRefresh_THENUsesUpdatedIndex(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/tickets", listTickets)
	return r
}`)
	configureForTest(t, dir)
	uri := "file:///component.ktml"
	openDocumentForTest(t, uri, `Button hx-get="/tickets"`)

	if _, err := HandleLSP("textDocument/rename", []byte(`{
		"id": 8,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 9},
		"newName": "/requests"
	}`)); err != nil {
		t.Fatalf("rename error: %v", err)
	}
	openDocumentForTest(t, uri, `Button hx-get="/requests"`)

	responseBytes, err := HandleLSP("textDocument/definition", []byte(`{
		"id": 9,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 18}
	}`))
	if err != nil {
		t.Fatalf("definition error: %v", err)
	}
	var response struct {
		Result *Location `json:"result"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode definition response: %v", err)
	}
	if response.Result == nil {
		t.Fatalf("definition after rename is nil: %s", responseBytes)
	}
}

func TestHandleLSP_GIVENComposedChiRoute_WHENRenamingOutsideItsPrefix_THENFailsWithReason(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Route("/vendors", func(r chi.Router) {
		r.Post("/{id}/approve", approveVendor)
	})
	return r
}`)
	configureForTest(t, dir)
	openDocumentForTest(t, "file:///component.ktml", `Button hx-post="/vendors/{id}/approve"`)

	responseBytes, err := HandleLSP("textDocument/rename", []byte(`{
		"id": 7,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 10},
		"newName": "/people/{id}/approve"
	}`))
	if err != nil {
		t.Fatalf("HandleLSP() error = %v", err)
	}
	var response struct {
		Result any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result != nil || response.Error == nil || !strings.Contains(response.Error.Message, "keep that prefix") {
		t.Fatalf("rename response = %s, want an error explaining the prefix", responseBytes)
	}
}

func configureForTest(t *testing.T, dir string) {
	t.Helper()
	lspState.Lock()
	lspState.documents = make(map[string]string)
	lspState.Unlock()
	settings, err := json.Marshal(map[string]any{"chi": map[string]any{"dir": dir}})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := Configure(settings); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	runJobsForTest(t)
}

// runJobsForTest plays the host: it runs every pending background job on
// this instance, as worker and server at once, and returns the
// notifications the results produced.
func runJobsForTest(t *testing.T) [][]byte {
	t.Helper()
	var notifications [][]byte
	for range 10 {
		job, err := HandleLSP(pendingJobMethod, nil)
		if err != nil {
			t.Fatalf("pendingJob error = %v", err)
		}
		if job == nil {
			return notifications
		}
		result, err := HandleLSP(runJobMethod, job)
		if err != nil {
			t.Fatalf("runJob error = %v", err)
		}
		delivered, _ := json.Marshal(map[string]any{"job": json.RawMessage(job), "result": json.RawMessage(result)})
		published, err := HandleLSP(jobResultMethod, delivered)
		if err != nil {
			t.Fatalf("jobResult error = %v", err)
		}
		notifications = append(notifications, published)
	}
	t.Fatal("background jobs did not settle")
	return nil
}

func openDocumentForTest(t *testing.T, uri, text string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": text},
	})
	if err != nil {
		t.Fatalf("marshal didOpen: %v", err)
	}
	if _, err := HandleLSP("textDocument/didOpen", payload); err != nil {
		t.Fatalf("didOpen error = %v", err)
	}
}

func TestHandleLSP_GIVENGoFileChanges_WHENWatchedFilesChange_THENRepublishesDiagnosticsAndCompletesNewRoute(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes(r chi.Router) {
	r.Get("/old", h)
}`)
	configureForTest(t, dir)
	uri := "file:///component.ktml"
	openDocumentForTest(t, uri, `Button hx-get="/new"`)

	writeLater(t, filepath.Join(dir, "router.go"), `package web
import "github.com/go-chi/chi/v5"
func Routes(r chi.Router) {
	r.Get("/old", h)
	r.Get("/new", h)
	r.Get(dynamic(), h)
}`)
	changes, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": fileURI(filepath.Join(dir, "router.go")), "type": 2}}})
	if _, err := HandleLSP("workspace/didChangeWatchedFiles", changes); err != nil {
		t.Fatalf("didChangeWatchedFiles error = %v", err)
	}
	results := runJobsForTest(t)
	if len(results) != 1 {
		t.Fatalf("refresh jobs = %d, want 1", len(results))
	}
	notifications := results[0]
	var published []struct {
		Params struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Code string `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(notifications, &published); err != nil {
		t.Fatalf("decode notifications %s: %v", notifications, err)
	}
	if len(published) != 2 || published[0].Params.URI != uri || len(published[0].Params.Diagnostics) != 0 {
		t.Fatalf("notifications = %s, want cleared component diagnostics and the router's", notifications)
	}
	if got := published[1].Params; !strings.HasSuffix(got.URI, "/router.go") || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "dynamic-route" {
		t.Errorf("router diagnostics = %#v", got)
	}

	openDocumentForTest(t, "file:///typing.ktml", `Button hx-get="/n`)
	responseBytes, err := HandleLSP("textDocument/completion", []byte(`{"id":1,"textDocument":{"uri":"file:///typing.ktml"},"position":{"line":0,"character":17}}`))
	if err != nil {
		t.Fatalf("completion error = %v", err)
	}
	if !strings.Contains(string(responseBytes), `"label":"/new"`) {
		t.Errorf("completion = %s, want /new", responseBytes)
	}
}

func TestHandleLSP_GIVENIndexNotYetBuilt_WHENEditing_THENPublishesNoRouteDiagnosticsAndMarksCompletionIncomplete(t *testing.T) {
	dir := writeGoFixture(t, `package web
import "github.com/go-chi/chi/v5"
func Routes(r chi.Router) { r.Get("/known", h) }`)
	lspState.Lock()
	lspState.documents = make(map[string]string)
	lspState.Unlock()
	settings, _ := json.Marshal(map[string]any{"chi": map[string]any{"dir": dir}})
	if err := Configure(settings); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	opened, err := HandleLSP("textDocument/didOpen", []byte(`{"textDocument":{"uri":"file:///a.ktml","text":"a hx-get=\"/unknown\" hx-post=\"/k"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(opened), `"diagnostics":[]`) {
		t.Errorf("diagnostics before the index = %s, want none", opened)
	}
	completion, _ := HandleLSP("textDocument/completion", []byte(`{"id":1,"textDocument":{"uri":"file:///a.ktml"},"position":{"line":0,"character":32}}`))
	if !strings.Contains(string(completion), `"isIncomplete":true`) {
		t.Errorf("completion before the index = %s, want isIncomplete", completion)
	}

	results := runJobsForTest(t)
	if len(results) != 1 || !strings.Contains(string(results[0]), "htmx-missing-route") {
		t.Errorf("diagnostics once the index is ready = %q", results)
	}
	if again, _ := HandleLSP(pendingJobMethod, nil); again != nil {
		t.Errorf("pending job after the scan = %s, want none", again)
	}
}

func TestHandleLSP_GIVENWatchedFilesOutsideChiDir_WHENChanged_THENQueuesNoRefresh(t *testing.T) {
	dir := writeGoFixture(t, "package web\n")
	configureForTest(t, dir)
	changes := []byte(`{"changes":[{"uri":"file:///elsewhere/x.go","type":2},{"uri":"` + fileURI(filepath.Join(dir, "page.ktml")) + `","type":2}]}`)
	if _, err := HandleLSP("workspace/didChangeWatchedFiles", changes); err != nil {
		t.Fatal(err)
	}
	if job, _ := HandleLSP(pendingJobMethod, nil); job != nil {
		t.Errorf("pending job = %s, want none", job)
	}
}

func TestConfigure_GIVENInvalidPatterns_WHENConfigured_THENFailsWithThePatternPath(t *testing.T) {
	err := Configure([]byte(`{"chi":{"dir":".","patterns":{"routes":{"GET":["{router}.Get({path}, {handler})"]}}}}`))
	if err == nil || !strings.Contains(err.Error(), "chi.patterns.routes.GET[0]") {
		t.Fatalf("Configure() error = %v, want the pattern's path", err)
	}
}
