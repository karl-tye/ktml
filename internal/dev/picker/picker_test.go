package picker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/explorer"
)

// fakeExplorer is a program where Dashboard (a document) calls Layout at c0
// and BoardView at c1, which calls JobCard at c0, which calls Badge at c0.
type fakeExplorer struct {
	ensured []string
	stories map[string]bool
}

const pkg = "example.com/app/web."

func (f *fakeExplorer) Describe(id string) (explorer.ComponentInfo, bool) {
	name, ok := strings.CutPrefix(id, pkg)
	if !ok {
		return explorer.ComponentInfo{}, false
	}
	return explorer.ComponentInfo{Name: name, Document: name == "Dashboard" || name == "Layout", Story: f.stories[id]}, true
}

func (f *fakeExplorer) CallTarget(caller, site string) (string, bool) {
	target, ok := map[string]string{
		"Dashboard#c0": "Layout", "Dashboard#c1": "BoardView", "BoardView#c0": "JobCard", "JobCard#c0": "Badge", "Layout#c0": "Badge",
	}[strings.TrimPrefix(caller, pkg)+"#"+site]
	if !ok {
		return "", false
	}
	return pkg + target, true
}

func (f *fakeExplorer) EnsureStory(_ context.Context, id string) (string, bool, error) {
	if _, ok := f.Describe(id); !ok {
		return "", false, errors.New("no component")
	}
	f.ensured = append(f.ensured, id)
	created := !f.stories[id]
	f.stories[id] = true
	return "web/x.story.ktml#X.Default", created, nil
}

func newHandler(t *testing.T) (*Handler, *fakeExplorer, *[]string) {
	t.Helper()
	fake := &fakeExplorer{stories: map[string]bool{pkg + "BoardView": true}}
	var asked []string
	roots := func(_ context.Context, instances []string) (map[string]string, error) {
		asked = append(asked, instances...)
		return map[string]string{"7": pkg + "Badge"}, nil
	}
	return New(fake, roots, "http://127.0.0.1:7349"), fake, &asked
}

func post(handler http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7340"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:7340")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for name, value := range headers {
		if value == "" {
			request.Header.Del(name)
		} else {
			request.Header.Set(name, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestResolve_GIVENPageIdentities_WHENResolved_THENEveryInstanceHasItsComponentChain(t *testing.T) {
	handler, _, asked := newHandler(t)
	badge := `@1 ` + pkg + `Dashboard#c1 ` + pkg + `BoardView#r0="job 1" ` + pkg + `BoardView#c0 ` + pkg + `JobCard#c0`
	content := `@1 ` + pkg + `Dashboard#c0 ` + pkg + `Layout#o0 ` + pkg + `Dashboard#c1`
	request, _ := json.Marshal(resolveRequest{Identities: []string{badge, content, "@7", "@8", "nonsense", "@1 broken#"}})
	response := post(handler, Prefix+"resolve", string(request), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var got resolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	chain := func(identity string) []string {
		var names []string
		for _, entry := range got.Chains[identity] {
			names = append(names, got.Components[entry.Component].Name+"="+entry.Identity)
		}
		return names
	}
	want := []string{
		"Dashboard=@1",
		"BoardView=@1 " + pkg + "Dashboard#c1",
		"JobCard=@1 " + pkg + `Dashboard#c1 ` + pkg + `BoardView#r0="job 1" ` + pkg + "BoardView#c0",
		"Badge=" + badge,
	}
	if got := chain(badge); !reflect.DeepEqual(got, want) {
		t.Errorf("badge chain = %q\nwant %q", got, want)
	}
	// Content rendered at Layout's outlet is called by Dashboard: Layout is
	// on the chain, the outlet is not.
	if got := chain(content); !reflect.DeepEqual(got, []string{"Dashboard=@1", "Layout=@1 " + pkg + "Dashboard#c0", "BoardView=" + content}) {
		t.Errorf("content chain = %q", got)
	}
	// A root without frames is named by the application; an unknown root
	// and a malformed identity are left out.
	if got := chain("@7"); !reflect.DeepEqual(got, []string{"Badge=@7"}) {
		t.Errorf("@7 chain = %q", got)
	}
	if !reflect.DeepEqual(*asked, []string{"7", "8"}) {
		t.Errorf("asked the application for %v, want only the frameless roots", *asked)
	}
	for _, missing := range []string{"@8", "nonsense", "@1 broken#"} {
		if _, ok := got.Chains[missing]; ok {
			t.Errorf("%q resolved", missing)
		}
	}
	dashboard := got.Components[pkg+"Dashboard"]
	if !dashboard.Document || got.Components[pkg+"BoardView"].Story != true || got.Components[pkg+"Badge"].Story {
		t.Errorf("components = %+v", got.Components)
	}
}

func TestOpen_GIVENAComponent_WHENOpened_THENEnsuresItsStoryAndReturnsTheExplorerURL(t *testing.T) {
	handler, fake, _ := newHandler(t)
	response := post(handler, Prefix+"open", `{"component":"`+pkg+`Badge"}`, nil)
	var got openResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status %d: %s (%v)", response.Code, response.Body.String(), err)
	}
	if got.URL != "http://127.0.0.1:7349/_explorer/open?component=example.com%2Fapp%2Fweb.Badge" || !got.Created {
		t.Errorf("open = %+v", got)
	}
	if !reflect.DeepEqual(fake.ensured, []string{pkg + "Badge"}) {
		t.Errorf("ensured %v", fake.ensured)
	}
	if response := post(handler, Prefix+"open", `{"component":"elsewhere.Missing"}`, nil); response.Code != http.StatusUnprocessableEntity {
		t.Errorf("open a missing component: %d", response.Code)
	}
}

// TestHandler_GIVENCrossSiteOrMalformedRequests_WHENSent_THENRefusesThem
// accepts only same-origin JSON POSTs from a loopback Host: a page on another
// site, a simple form post, a DNS rebinding Host, or a GET never reaches the
// Explorer, so no other site can make ktml dev write a story.
func TestHandler_GIVENCrossSiteOrMalformedRequests_WHENSent_THENRefusesThem(t *testing.T) {
	handler, fake, _ := newHandler(t)
	body := `{"component":"` + pkg + `Badge"}`
	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"another origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"another port", map[string]string{"Origin": "http://127.0.0.1:9999"}, http.StatusForbidden},
		{"no origin", map[string]string{"Origin": ""}, http.StatusForbidden},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site fetch", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"form post", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"text post", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		if response := post(handler, Prefix+"open", body, test.headers); response.Code != test.want {
			t.Errorf("%s: status %d, want %d", test.name, response.Code, test.want)
		}
	}
	rebinding := httptest.NewRequest(http.MethodPost, "http://evil.example:7340"+Prefix+"open", strings.NewReader(body))
	rebinding.Header.Set("Content-Type", "application/json")
	rebinding.Header.Set("Origin", "http://evil.example:7340")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rebinding)
	if response.Code != http.StatusForbidden {
		t.Errorf("DNS rebinding Host: status %d", response.Code)
	}
	get := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7340"+Prefix+"open?component="+pkg+"Badge", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", response.Code)
	}
	if response := post(handler, Prefix+"elsewhere", body, nil); response.Code != http.StatusNotFound {
		t.Errorf("unknown route: status %d", response.Code)
	}
	if len(fake.ensured) != 0 {
		t.Errorf("a refused request ensured %v", fake.ensured)
	}
	// Responses carry no CORS grant.
	response = post(handler, Prefix+"resolve", `{"identities":[]}`, nil)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("resolve: %d, CORS %q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
}
