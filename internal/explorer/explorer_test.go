package explorer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/config"
)

var fixtureFiles = map[string]string{
	"ui/card.ktmlschema": `record Stage [
	key: string,
	label: string,
	note?: string,
]

state Progress {
	Idle,
	Running [callId: string, link: TrustedURL],
	Failed [message: string],
}
`,
	"ui/card.ktml": `import { Stage, Progress } from "./card.ktmlschema"

Card [
	title: string,
	count: integer = 3,
	open: boolean,
	stages: [Stage],
	progress: Progress,
	footer?: content,
] {
	section class="card" {
		h2 "{title} ({count})"
		if open {
			p class="open" "open"
		}
		ul {
			for stage in stages key stage.key {
				if stage.note? {
					li "{stage.label}: {stage.note}"
				} else {
					li "{stage.label}"
				}
			}
		}
		Status progress="{progress}"
		footer
	}
}

Status [progress: Progress]

Status [progress = Idle] {
	em "idle"
}

Status [progress = Running, callId, link] {
	a href="{link}" "running {callId}"
}

Status [progress = Failed, message] {
	strong class="failed" "{message}"
}
`,
	"ui/card.story.ktml": `import { Card } from "./card.ktml"

fixture report: TrustedURL = "/reports/42"
fixture other: TrustedURL = "/reports/43"

story Card.Running [
	title = "Board",
	open = true,
	stages = [[key = "a", label = "One", note = "first"]],
	progress = Running [callId = "c-1", link = report],
] {
	slot footer {
		small "Footer"
	}
}

story Card.Idle [
	title = "Idle",
	open = false,
	stages = [],
	progress = Idle,
]
`,
}

const runningKey = "ui/card.story.ktml#Card.Running"

func newTestServer(t *testing.T, files map[string]string, cfg *config.Explorer) (*Server, *httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Options{Root: root, Config: cfg, Logf: t.Logf})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	backend := httptest.NewServer(s.backendHandler())
	t.Cleanup(backend.Close)
	return s, backend, root
}

var markers = regexp.MustCompile(`<!--/?ktml:[^>]*-->| data-ktml-i="[^"]*"`)

// get fetches a page; previews are returned without their HMR instance
// markers, which the preview tests do not assert.
func get(t *testing.T, address string) (int, string) {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(address, Prefix+"preview") {
		return response.StatusCode, markers.ReplaceAllString(string(body), "")
	}
	return response.StatusCode, string(body)
}

func edit(t *testing.T, backend *httptest.Server, story string, values map[string]any, change Edit) editResponse {
	t.Helper()
	body, _ := json.Marshal(editRequest{Story: story, Values: values, Edit: change})
	response, err := http.Post(backend.URL+Prefix+"edit", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var decoded editResponse
	if err := decodeJSON(raw, &decoded); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return decoded
}

func TestIndex_GIVENStories_WHENOpened_THENListsThemAndDerivesTypedControls(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)
	code, body := get(t, backend.URL+"/?story="+url.QueryEscape(runningKey))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{
		`>Running</a>`, `>Idle</a>`, // the component's stories
		`<iframe class="preview" data-preview`,                                // the isolated preview
		`data-op="variant" data-path="[&#34;progress&#34;]"`,                  // a Variant select
		`<option value="Running" selected>`,                                   //
		`data-kind="fixture" data-path="[&#34;progress&#34;,&#34;link&#34;]"`, // a trusted value: fixtures only
		`<option value="report" selected>report</option><option value="other">other</option>`,
		`data-op="some" data-path="[&#34;count&#34;]" checked> use default`, // a defaulted input
		`data-kind="boolean"`,                                              // a boolean
		`data-op="add" data-path="[&#34;stages&#34;]"`, `data-op="remove"`, // a collection
		`data-op="none" data-path="[&#34;stages&#34;,0,&#34;note&#34;]"`, // an optional field that is Some
		`Given by the story body.`,                                       // content
		`data-diagnostic-count="0"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index lacks %s", want)
		}
	}
	if strings.Contains(body, `data-kind="string" data-path="[&#34;progress&#34;,&#34;link&#34;]"`) {
		t.Error("a trusted value has a free-text control")
	}
}

func TestPreview_GIVENAStory_WHENRendered_THENUsesTheInterpreterInAPreviewDocumentWithLocalAssets(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, &config.Explorer{Stylesheets: []string{"/static/app.css"}, BodyClass: "latte"})
	code, body := get(t, backend.URL+Prefix+"preview?story="+url.QueryEscape(runningKey))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	for _, want := range []string{
		`<link rel="stylesheet" href="/static/app.css">`,
		`<script defer src="/_explorer/assets/htmx.min.js"></script>`,
		`<script defer src="/_explorer/assets/alpine.min.js"></script>`,
		`<body class="latte">`,
		`<section class="card"><h2>Board (3)</h2><p class="open">open</p>`,
		`<a href="/reports/42">running c-1</a>`,
		`<small>Footer</small>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("preview loads a remote resource:\n%s", body)
	}
	for _, name := range []string{"assets/htmx.min.js", "assets/alpine.min.js", "assets/explorer.js", "assets/explorer.css"} {
		if code, _ := get(t, backend.URL+Prefix+name); code != http.StatusOK {
			t.Errorf("%s: status %d", name, code)
		}
	}
}

func TestEdit_GIVENControlChanges_WHENSent_THENTypesThemAndReturnsTheChangedInputsAndPreview(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)

	failed := edit(t, backend, runningKey, nil, Edit{Op: "variant", Path: []any{"progress"}, Value: "Failed"})
	if failed.Error != "" || failed.Controls == "" || len(failed.Values) != 1 {
		t.Fatalf("variant edit = %+v", failed)
	}
	message := edit(t, backend, runningKey, failed.Values, Edit{Op: "set", Path: []any{"progress", "message"}, Value: "<boom>"})
	if message.Controls != "" {
		t.Error("a scalar edit re-rendered the controls")
	}
	if _, body := get(t, backend.URL+message.Preview); !strings.Contains(body, `<strong class="failed">&lt;boom&gt;</strong>`) || !strings.Contains(body, "Board (3)") {
		t.Errorf("preview after edits:\n%s", body)
	}

	added := edit(t, backend, runningKey, nil, Edit{Op: "add", Path: []any{"stages"}})
	label := edit(t, backend, runningKey, added.Values, Edit{Op: "set", Path: []any{"stages", 1, "label"}, Value: "Two"})
	key := edit(t, backend, runningKey, label.Values, Edit{Op: "set", Path: []any{"stages", 1, "key"}, Value: "b"})
	none := edit(t, backend, runningKey, key.Values, Edit{Op: "none", Path: []any{"stages", 0, "note"}})
	defaulted := edit(t, backend, runningKey, none.Values, Edit{Op: "some", Path: []any{"count"}})
	count := edit(t, backend, runningKey, defaulted.Values, Edit{Op: "set", Path: []any{"count"}, Value: json.Number("7")})
	if _, body := get(t, backend.URL+count.Preview); !strings.Contains(body, "<li>One</li><li>Two</li>") || !strings.Contains(body, "Board (7)") {
		t.Errorf("preview after collection, optional, and default edits:\n%s", body)
	}
	removed := edit(t, backend, runningKey, count.Values, Edit{Op: "remove", Path: []any{"stages", 0}})
	if _, body := get(t, backend.URL+removed.Preview); !strings.Contains(body, "<ul><li>Two</li></ul>") {
		t.Errorf("preview after remove:\n%s", body)
	}

	fixture := edit(t, backend, runningKey, nil, Edit{Op: "set", Path: []any{"progress", "link"}, Value: map[string]any{"fixture": "other"}})
	if _, body := get(t, backend.URL+fixture.Preview); !strings.Contains(body, `href="/reports/43"`) {
		t.Errorf("preview after a fixture choice:\n%s", body)
	}
}

func TestPreview_GIVENAForgedTrustedValue_WHENRendered_THENTheStoryValueRendersInstead(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)
	forged := map[string]any{"progress": map[string]any{"variant": "Running", "fields": map[string]any{"callId": "x", "link": "javascript:alert(1)"}}}
	_, body := get(t, backend.URL+(&Server{}).previewURL(runningKey, forged))
	if strings.Contains(body, "javascript:") || !strings.Contains(body, `href="/reports/42"`) {
		t.Errorf("a forged trusted value rendered:\n%s", body)
	}
	_, index := get(t, backend.URL+"/?story="+url.QueryEscape(runningKey)+"&v="+encodeValues(forged))
	if !strings.Contains(index, "values come only from this story document&#39;s fixtures") {
		t.Errorf("the rejected value is not reported inline")
	}
}

func TestPreview_GIVENARenderError_WHENRendered_THENShowsItInTheFrameAndAsARangedDiagnostic(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)
	duplicate := map[string]any{"stages": []any{
		map[string]any{"key": "k", "label": "a", "note": nil},
		map[string]any{"key": "k", "label": "b", "note": nil},
	}}
	_, body := get(t, backend.URL+(&Server{}).previewURL(runningKey, duplicate))
	if !strings.Contains(body, "data-explorer-render-error") || !strings.Contains(body, "duplicate key") {
		t.Fatalf("preview of a render error:\n%s", body)
	}
	_, index := get(t, backend.URL+"/?story="+url.QueryEscape(runningKey))
	if !strings.Contains(index, `data-code="render"`) || !strings.Contains(index, "ui/card.ktml:") || !strings.Contains(index, "<mark>stages: [Stage]</mark>") {
		t.Errorf("render diagnostic:\n%s", index)
	}
}

func TestApply_GIVENAnInvalidStory_WHENChanged_THENShowsTheRangedDiagnosticAndKeepsTheLastProgram(t *testing.T) {
	s, backend, root := newTestServer(t, fixtureFiles, nil)
	path := filepath.Join(root, "ui", "card.story.ktml")
	broken := strings.Replace(fixtureFiles["ui/card.story.ktml"], "progress = Idle", "progress = Paused", 1)
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Apply(context.Background(), []string{path})
	_, index := get(t, backend.URL+"/?story="+url.QueryEscape(runningKey))
	if !strings.Contains(index, `data-code="unknown-variant"`) || !strings.Contains(index, "ui/card.story.ktml:") || !strings.Contains(index, "<mark>Paused</mark>") {
		t.Errorf("diagnostics after a broken story:\n%s", index)
	}
	if _, body := get(t, backend.URL+Prefix+"preview?story="+url.QueryEscape(runningKey)); !strings.Contains(body, "running c-1") {
		t.Errorf("the last valid program stopped rendering:\n%s", body)
	}

	fixed := strings.Replace(fixtureFiles["ui/card.story.ktml"], `callId = "c-1"`, `callId = "c-2"`, 1)
	if err := os.WriteFile(path, []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Apply(context.Background(), []string{path})
	if _, body := get(t, backend.URL+Prefix+"preview?story="+url.QueryEscape(runningKey)); !strings.Contains(body, "running c-2") {
		t.Errorf("the fixed story did not swap in:\n%s", body)
	}
	if _, index := get(t, backend.URL+"/"); !strings.Contains(index, `data-diagnostic-count="0"`) {
		t.Errorf("diagnostics were not cleared")
	}
}

func TestListen_GIVENANonLoopbackAddress_WHENListening_THENRefuses(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", ":0", "192.0.2.1:0", "example.com:0"} {
		listener, err := Listen(address)
		if !errors.Is(err, ErrNotLoopback) {
			if listener != nil {
				listener.Close()
			}
			t.Errorf("Listen(%s) error = %v, want ErrNotLoopback", address, err)
		}
	}
	listener, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(loopback) error = %v", err)
	}
	listener.Close()
}

func TestLoopbackOnly_GIVENAForeignHost_WHENRequested_THENForbidden(t *testing.T) {
	handler := loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for host, want := range map[string]int{"127.0.0.1:7332": 200, "localhost:7332": 200, "[::1]:7332": 200, "evil.example": 403, "evil.example:7332": 403} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Host = host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Errorf("Host %s: status %d, want %d", host, response.Code, want)
		}
	}
}

func TestBackend_GIVENAForeignHost_WHENRequested_THENForbiddenLikeTheFrontServer(t *testing.T) {
	s, _, _ := newTestServer(t, fixtureFiles, nil)
	handler := s.backendHandler()
	for host, want := range map[string]int{"127.0.0.1:41234": 200, "localhost:41234": 200, "[::1]:41234": 200, "evil.example": 403, "evil.example:41234": 403, "127.0.0.1.evil.example:41234": 403} {
		for _, path := range []string{"/", Prefix + "preview?story=" + url.QueryEscape(runningKey), "/__ktml/dev/swap"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Host = host
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if got := response.Code; (want == 403) != (got == 403) {
				t.Errorf("Host %s %s: status %d, want %d", host, path, got, want)
			}
		}
	}
}

// TestEdit_GIVENEmptyAttributeBags_WHENValuesAroundThemChange_THENBagsStayEmpty
// edits a story whose values hold attribute bags: a bag has no control and
// stays the empty bag, the only one a story gives.
func TestEdit_GIVENEmptyAttributeBags_WHENValuesAroundThemChange_THENBagsStayEmpty(t *testing.T) {
	files := map[string]string{
		"ui/field.ktmlschema": "record Row [\n\tkey: string,\n\tlabel: string,\n\tattrs: Attributes,\n]\n",
		"ui/field.ktml":       "import { Row } from \"./field.ktmlschema\"\n\nField [attrs: Attributes, rows: [Row]] {\n\tul ...attrs {\n\t\tfor row in rows key row.key {\n\t\t\tli ...row.attrs \"{row.label}\"\n\t\t}\n\t}\n}\n",
		"ui/field.story.ktml": "import { Field } from \"./field.ktml\"\n\nstory Field.Default [\n\tattrs = [],\n\trows = [[key = \"a\", label = \"One\", attrs = []]],\n]\n",
	}
	_, backend, _ := newTestServer(t, files, nil)
	const key = "ui/field.story.ktml#Field.Default"
	if status, body := get(t, backend.URL+"/?story="+url.QueryEscape(key)); status != http.StatusOK || !strings.Contains(body, "Empty attribute bag") {
		t.Errorf("index status %d lacks the bag note:\n%s", status, body)
	}
	added := edit(t, backend, key, nil, Edit{Op: "add", Path: []any{"rows"}})
	if added.Error != "" {
		t.Fatalf("add = %+v", added)
	}
	label := edit(t, backend, key, added.Values, Edit{Op: "set", Path: []any{"rows", 1, "label"}, Value: "Two"})
	rowKey := edit(t, backend, key, label.Values, Edit{Op: "set", Path: []any{"rows", 1, "key"}, Value: "b"})
	if rowKey.Error != "" {
		t.Fatalf("set = %+v", rowKey)
	}
	if _, body := get(t, backend.URL+rowKey.Preview); !strings.Contains(body, "<ul><li>One</li><li>Two</li></ul>") {
		t.Errorf("preview after edits:\n%s", body)
	}
	if forged := edit(t, backend, key, map[string]any{"rows": []any{map[string]any{"key": "a", "label": "x", "attrs": []any{"onclick"}}}}, Edit{Op: "set", Path: []any{"rows", 0, "label"}, Value: "y"}); !strings.Contains(forged.Controls, "a story gives only the empty attribute bag") {
		t.Errorf("a non-empty bag was accepted: %+v", forged)
	}
}

// TestState_GIVENAStory_WHENRequested_THENItNamesTheStoryForClientNavigation
// checks the state a click in the story tree swaps in without a reload: the
// story's key, title, and source location beside its panels and preview,
// and the default story's key when none is named.
func TestState_GIVENAStory_WHENRequested_THENItNamesTheStoryForClientNavigation(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)
	_, body := get(t, backend.URL+Prefix+"state?story="+url.QueryEscape(runningKey))
	var state stateResponse
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("state: %v\n%s", err, body)
	}
	if state.Story != runningKey || state.Title != "Card.Running" || !strings.HasPrefix(state.Source, "ui/card.story.ktml:") {
		t.Errorf("story %q title %q source %q", state.Story, state.Title, state.Source)
	}
	if !strings.Contains(state.Preview, "story="+url.QueryEscape(runningKey)) || state.Controls == "" || !strings.Contains(state.Navigation, `aria-current="page"`) {
		t.Errorf("state lacks the story's preview, controls, or selection: %+v", state)
	}
}
