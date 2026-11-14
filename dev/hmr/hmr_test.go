package hmr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/dev/interp"
)

// Badge, Page, and Layout stand in for ktmldev generated components: their
// public Render offers the value to the delegation hook, and their compiled
// body ("generated") marks the instance from the context's Render Identity.
type Badge struct{ Label string }

func (c Badge) Render(ctx context.Context, w io.Writer) error {
	return dev.Render(ctx, w, c, func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<b data-ktml-i="`+dev.Scope(ctx)+`">`+c.Label+`</b>`)
		return err
	})
}

type Page struct{ Title string }

func (c Page) Render(ctx context.Context, w io.Writer) error {
	return dev.Render(ctx, w, c, func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<main data-ktml-i="`+dev.Scope(ctx)+`">compiled `+c.Title+`</main>`)
		return err
	})
}

type Layout struct{ Children ktml.Component }

func (c Layout) Render(ctx context.Context, w io.Writer) error {
	return dev.Render(ctx, w, c, func(ctx context.Context, w io.Writer) error {
		if _, err := io.WriteString(w, "<section>"); err != nil {
			return err
		}
		if err := c.Children.Render(dev.WithScope(ctx, dev.CallFrame(dev.Scope(ctx), "hmr_test.Layout", "o0")), w); err != nil {
			return err
		}
		_, err := io.WriteString(w, "</section>")
		return err
	})
}

const (
	badgeSource  = "Badge [label: string] {\n\tb \"{label}\"\n}\n"
	pageSource   = "import { Badge } from \"./badge.ktml\"\n\nPage [title: string] {\n\tmain {\n\t\th1 \"{title}\"\n\t\tBadge label=\"one\"\n\t\tBadge label=\"two\"\n\t}\n}\n"
	layoutSource = "Layout [children: content] {\n\tsection {\n\t\tchildren\n\t}\n}\n"
	token        = "control-token"
)

type fixture struct {
	root    string
	hmr     *hmr.HMR
	handler http.Handler
	pkg     string
}

func newFixture(t *testing.T, render func(http.ResponseWriter, *http.Request)) *fixture {
	t.Helper()
	root := t.TempDir()
	for name, source := range map[string]string{"badge.ktml": badgeSource, "page.ktml": pageSource, "layout.ktml": layoutSource} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pkg := reflect.TypeOf(Badge{}).PkgPath()
	h, err := hmr.New(hmr.Options{Root: root, Token: token, Interp: interp.Options{Packages: func(string) (string, error) { return pkg, nil }}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(dev.SetRenderer(h))
	return &fixture{root: root, hmr: h, handler: h.Handler(http.HandlerFunc(render)), pkg: pkg}
}

func (f *fixture) get(t *testing.T, session string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if session != "" {
		request.Header.Set(hmr.SessionHeader, session)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f *fixture) swap(t *testing.T, controlToken string, files map[string]string) (int, hmr.SwapResponse) {
	t.Helper()
	request := hmr.SwapRequest{Generation: 1}
	for name, source := range files {
		request.Files = append(request.Files, hmr.SwapFile{Path: filepath.Join(f.root, name), Source: source})
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, hmr.Prefix+"swap", bytes.NewReader(body))
	httpRequest.Header.Set(hmr.TokenHeader, controlToken)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, httpRequest)
	var decoded hmr.SwapResponse
	if response.Code == http.StatusOK || response.Code == http.StatusUnprocessableEntity {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode swap response %q: %v", response.Body.String(), err)
		}
	}
	return response.Code, decoded
}

func renderPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_ = Page{Title: "Board"}.Render(r.Context(), w)
}

func TestHandler_GIVENPageRequest_WHENRendered_THENTracksTheRootUnderARequestToken(t *testing.T) {
	f := newFixture(t, renderPage)
	response := f.get(t, "")
	session := response.Header().Get(hmr.RequestHeader)
	if session == "" {
		t.Fatal("no request token header")
	}
	if want := `<main data-ktml-i="@1">compiled Board</main>`; response.Body.String() != want {
		t.Errorf("body = %s, want %s", response.Body.String(), want)
	}
	instance, ok := f.hmr.Registry().Instance("1")
	if !ok || instance.SessionID != session || instance.ComponentID != f.pkg+".Page" || instance.Props != (Page{Title: "Board"}) {
		t.Fatalf("Instance(1) = %#v, %v", instance, ok)
	}
	if got := strings.Join(instance.Depends, ","); got != f.pkg+".Badge,"+f.pkg+".Page" {
		t.Errorf("Depends = %s", got)
	}
	if instance.Size <= 0 {
		t.Errorf("Size = %d", instance.Size)
	}

	fragment := f.get(t, "existing-session")
	if fragment.Header().Get(hmr.RequestHeader) != "" {
		t.Error("an HTMX request naming its session got a new request token")
	}
	if instance, ok := f.hmr.Registry().Instance("2"); !ok || instance.SessionID != "existing-session" {
		t.Errorf("fragment root = %#v, %v", instance, ok)
	}
}

func TestSwap_GIVENWrongOrMissingToken_WHENCalled_THENTheRouteDoesNotExist(t *testing.T) {
	f := newFixture(t, renderPage)
	for _, controlToken := range []string{"", "wrong"} {
		if code, _ := f.swap(t, controlToken, map[string]string{"badge.ktml": badgeSource}); code != http.StatusNotFound {
			t.Errorf("token %q: status = %d, want 404", controlToken, code)
		}
	}
}

func TestSwap_GIVENCompatibleChildChange_WHENSwapped_THENRerendersEveryLiveInstanceOfTheParentRoot(t *testing.T) {
	f := newFixture(t, renderPage)
	session := f.get(t, "").Header().Get(hmr.RequestHeader)
	code, response := f.swap(t, token, map[string]string{"badge.ktml": strings.Replace(badgeSource, `b "`, `b class="new" "`, 1)})
	if code != http.StatusOK {
		t.Fatalf("status = %d, diagnostic %s", code, response.Diagnostic)
	}
	if len(response.Patches) != 1 || len(response.Reload) != 0 {
		t.Fatalf("response = %#v", response)
	}
	patch := response.Patches[0]
	const want = `<main data-ktml-i="@1"><h1>Board</h1>` +
		`<b data-ktml-i="@1 hmr_test.Page#c0" class="new">one</b>` +
		`<b data-ktml-i="@1 hmr_test.Page#c1" class="new">two</b></main>`
	html := strings.ReplaceAll(patch.HTML, f.pkg, "hmr_test")
	if patch.Session != session || patch.Identity != "@1" || patch.Document || html != want {
		t.Errorf("patch = %#v\nwant html %s", patch, want)
	}
	// Requests after the swap render through the new program.
	if body := f.get(t, "").Body.String(); !strings.Contains(body, `class="new"`) {
		t.Errorf("render after swap = %s", body)
	}
}

func TestSwap_GIVENGoBuiltContent_WHENItsComponentChanges_THENPatchesTheRootThatRenderedIt(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_ = Layout{Children: Badge{Label: "child"}}.Render(r.Context(), w)
	})
	if body := f.get(t, "").Body.String(); body != `<section><b data-ktml-i="@1 hmr_test.Layout#o0">child</b></section>` {
		t.Fatalf("body = %s", body)
	}
	_, response := f.swap(t, token, map[string]string{"badge.ktml": strings.Replace(badgeSource, `b "`, `i "`, 1)})
	if len(response.Patches) != 1 {
		t.Fatalf("response = %#v", response)
	}
	html := strings.ReplaceAll(response.Patches[0].HTML, f.pkg, "hmr_test")
	if want := `<section data-ktml-i="@1"><i data-ktml-i="@1 hmr_test.Layout#o0">child</i></section>`; html != want {
		t.Errorf("patch html = %s, want %s", html, want)
	}
}

func TestSwap_GIVENDiagnostic_WHENSwapped_THENKeepsTheLastValidProgram(t *testing.T) {
	f := newFixture(t, renderPage)
	f.get(t, "")
	f.swap(t, token, map[string]string{"badge.ktml": strings.Replace(badgeSource, `b "`, `b class="valid" "`, 1)})
	code, response := f.swap(t, token, map[string]string{"badge.ktml": "Badge [label: string] {\n\tb \"{missing}\"\n}\n"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(response.Diagnostic, "missing") {
		t.Fatalf("status = %d, response = %#v", code, response)
	}
	if body := f.get(t, "").Body.String(); !strings.Contains(body, `class="valid"`) {
		t.Errorf("render after a diagnostic = %s", body)
	}
}

func TestSwap_GIVENRootThatFailsToRerender_WHENSwapped_THENDropsItAndReloadsItsSession(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_ = Badge{Label: "solo"}.Render(r.Context(), w)
	})
	session := f.get(t, "").Header().Get(hmr.RequestHeader)
	// The compiled Badge has no field for a newly required input.
	code, response := f.swap(t, token, map[string]string{
		"badge.ktml": "Badge [label: string, tone: string] {\n\tb \"{label} {tone}\"\n}\n",
		"page.ktml":  strings.ReplaceAll(strings.ReplaceAll(pageSource, `label="one"`, `label="one" tone="x"`), `label="two"`, `label="two" tone="y"`),
	})
	if code != http.StatusOK || len(response.Patches) != 0 || len(response.Reload) != 1 || response.Reload[0] != session {
		t.Fatalf("status = %d, response = %#v", code, response)
	}
	if _, ok := f.hmr.Registry().Instance("1"); ok {
		t.Error("the failed root is still tracked")
	}
}

func TestSession_GIVENSSEConnection_WHENReported_THENAdoptsTheRequestToken(t *testing.T) {
	f := newFixture(t, renderPage)
	session := f.get(t, "").Header().Get(hmr.RequestHeader)
	for _, connected := range []bool{true, false} {
		body, _ := json.Marshal(hmr.SessionRequest{Session: session, Connected: connected})
		request := httptest.NewRequest(http.MethodPost, hmr.Prefix+"session", bytes.NewReader(body))
		request.Header.Set(hmr.TokenHeader, token)
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("connected=%v: status = %d", connected, response.Code)
		}
	}
	if sessions, roots, _ := f.hmr.Registry().Stats(); sessions != 1 || roots != 1 {
		t.Errorf("Stats() = %d sessions, %d roots", sessions, roots)
	}
}

func TestInstall_GIVENNoControlToken_WHENInstalled_THENReturnsTheHandlerUnchanged(t *testing.T) {
	t.Setenv(hmr.TokenEnv, "")
	handler := http.NotFoundHandler()
	if got := hmr.Install(handler); reflect.ValueOf(got).Pointer() != reflect.ValueOf(handler).Pointer() {
		t.Error("Install wrapped the handler without a control token")
	}
}

func TestSwap_GIVENStringInURLAttribute_WHENSwapped_THENReportsTheURLDiagnosticAndKeepsTheLastValidProgram(t *testing.T) {
	f := newFixture(t, renderPage)
	f.get(t, "")
	code, response := f.swap(t, token, map[string]string{"badge.ktml": "Badge [label: string] {\n\ta href=\"{label}\" \"x\"\n}\n"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(response.Diagnostic, "href is a URL") {
		t.Fatalf("status = %d, response = %#v", code, response)
	}
	if body := f.get(t, "").Body.String(); strings.Contains(body, "href") {
		t.Errorf("render after a diagnostic = %s", body)
	}
}

func TestSwap_GIVENBooleanAsText_WHENSwapped_THENReportsTheBooleanDiagnosticAndKeepsTheLastValidProgram(t *testing.T) {
	f := newFixture(t, renderPage)
	f.get(t, "")
	code, response := f.swap(t, token, map[string]string{"badge.ktml": "Badge [label: string, on: boolean = false] {\n\tb \"{label} {on}\"\n}\n"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(response.Diagnostic, "boolean on cannot be written as text") {
		t.Fatalf("status = %d, response = %#v", code, response)
	}
	if body := f.get(t, "").Body.String(); strings.Contains(body, "false") {
		t.Errorf("render after a diagnostic = %s", body)
	}
}

func TestSwap_GIVENVoidElementWithContent_WHENSwapped_THENReportsTheVoidDiagnosticAndKeepsTheLastValidProgram(t *testing.T) {
	f := newFixture(t, renderPage)
	f.get(t, "")
	code, response := f.swap(t, token, map[string]string{"badge.ktml": "Badge [label: string] {\n\tbr { \"{label}\" }\n}\n"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(response.Diagnostic, "badge.ktml:25-27: [void-content]") {
		t.Fatalf("status = %d, response = %#v", code, response)
	}
	if body := f.get(t, "").Body.String(); strings.Contains(body, "<br>") {
		t.Errorf("render after a diagnostic = %s", body)
	}
}

func TestSwap_GIVENBooleanARIAAttribute_WHENSwapped_THENPatchesTrueOrFalseText(t *testing.T) {
	f := newFixture(t, renderPage)
	f.get(t, "")
	code, response := f.swap(t, token, map[string]string{
		"badge.ktml": "Badge [label: string, on: boolean = false] {\n\tb (aria-pressed=\"{on}\" hidden=\"{on}\") \"{label}\"\n}\n",
		"page.ktml":  strings.Replace(pageSource, `label="two"`, `label="two" on=true`, 1),
	})
	if code != http.StatusOK || len(response.Patches) != 1 {
		t.Fatalf("status = %d, response = %#v", code, response)
	}
	html := strings.ReplaceAll(response.Patches[0].HTML, f.pkg, "hmr_test")
	if !strings.Contains(html, `#c0" aria-pressed="false">one</b>`) || !strings.Contains(html, `#c1" aria-pressed="true" hidden>two</b>`) {
		t.Errorf("patch html = %s", html)
	}
}

// TestComponents_GIVENLiveRoots_WHENAsked_THENNamesTheirComponentsOnly
// answers the component picker: the component of each live root instance,
// and nothing about its props; an unknown instance is left out.
func TestComponents_GIVENLiveRoots_WHENAsked_THENNamesTheirComponentsOnly(t *testing.T) {
	f := newFixture(t, renderPage)
	f.get(t, "")
	ask := func(controlToken string, instances []string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(hmr.ComponentsRequest{Instances: instances})
		request := httptest.NewRequest(http.MethodPost, hmr.Prefix+"components", bytes.NewReader(body))
		request.Header.Set(hmr.TokenHeader, controlToken)
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		return response
	}
	response := ask(token, []string{"1", "99"})
	var decoded hmr.ComponentsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status %d, body %s, %v", response.Code, response.Body.String(), err)
	}
	if want := map[string]string{"1": f.pkg + ".Page"}; !reflect.DeepEqual(decoded.Components, want) {
		t.Errorf("Components = %v, want %v", decoded.Components, want)
	}
	if strings.Contains(response.Body.String(), "Board") {
		t.Errorf("the answer carries props: %s", response.Body.String())
	}
	if response := ask("wrong", []string{"1"}); response.Code != http.StatusNotFound {
		t.Errorf("wrong token: status %d, want 404", response.Code)
	}
}
