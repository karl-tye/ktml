package picker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/explorer"
)

// fakeExplorer is a program where Dashboard (a document) calls Layout at c0
// and BoardView at c1, which calls JobCard at c0, which calls Badge at c0.
type fakeExplorer struct {
	ensured []string
	stories map[string]bool
	located []string
}

// Locate answers web/<component>.ktml line n+1 for step n, and nothing for
// a step tagged "x"; it records the component, links, and tags.
func (f *fakeExplorer) Locate(id string, links []explorer.Link, path []compiler.ElementStep) []explorer.Location {
	name := strings.TrimPrefix(id, pkg)
	record := name
	for _, link := range links {
		record += " " + strings.TrimPrefix(link.Caller, pkg) + "#" + link.Site
	}
	record += " |"
	locations := make([]explorer.Location, len(path))
	for index, step := range path {
		record += " " + step.Tag
		if step.Tag == "x" {
			continue
		}
		locations[index] = explorer.Location{File: "web/" + name + ".ktml", Path: "/project/web/" + name + ".ktml", Line: index + 1, Column: 2, Component: name}
	}
	f.located = append(f.located, record)
	return locations
}

func (f *fakeExplorer) Declaration(id string) (explorer.Location, bool) {
	name, ok := strings.CutPrefix(id, pkg)
	if !ok {
		return explorer.Location{}, false
	}
	return explorer.Location{File: "web/" + name + ".ktml", Path: "/project/web/" + name + ".ktml", Line: 9, Column: 1, Component: name}, true
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
		"Shell#c0": "Frame", "Planner#c0": "Row", "Planner#c1": "Badge", "Row#c0": "Badge",
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
	roots := func(_ context.Context, instances, outlets []string) (map[string]string, map[string]string, error) {
		asked = append(asked, instances...)
		named := make(map[string]string)
		for _, outlet := range outlets {
			// The application recorded a Go-built Planner in Shell's slot.
			if outlet == "@5 "+pkg+"Shell#c0 "+pkg+"Frame#o0 "+pkg+"Shell#o0" {
				named[outlet] = pkg + "Planner"
			}
		}
		return map[string]string{"7": pkg + "Badge", "5": pkg + "Shell", "6": pkg + "Shell"}, named, nil
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

// TestLocate_GIVENAnInstanceAndAnElementPath_WHENLocated_THENFollowsTheCallsThatSuppliedItsContent
// locates every element of a path inside the innermost instance, passing the
// calls that rendered the instance and, outward, each caller's instance, so
// that content at an outlet is found where its caller declared it.
func TestLocate_GIVENAnInstanceAndAnElementPath_WHENLocated_THENFollowsTheCallsThatSuppliedItsContent(t *testing.T) {
	handler, fake, _ := newHandler(t)
	badge := `@1 ` + pkg + `Dashboard#c1 ` + pkg + `BoardView#r0="job 1" ` + pkg + `BoardView#c0 ` + pkg + `JobCard#c0`
	content := `@1 ` + pkg + `Dashboard#c0 ` + pkg + `Layout#o0 ` + pkg + `Dashboard#c1`
	locate := func(identity string, tags ...string) locateResponse {
		t.Helper()
		var path []compiler.ElementStep
		for _, tag := range tags {
			path = append(path, compiler.ElementStep{Tag: tag})
		}
		request, _ := json.Marshal(locateRequest{Identity: identity, Path: path})
		response := post(handler, Prefix+"locate", string(request), nil)
		if response.Code != http.StatusOK {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
		var got locateResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := locate(badge, "span", "x", "b")
	if len(got.Locations) != 3 || got.Locations[0] == nil || got.Locations[1] != nil || got.Locations[2] == nil {
		t.Fatalf("locations = %+v", got.Locations)
	}
	if first := *got.Locations[0]; first.File != "web/Badge.ktml" || first.Line != 1 || first.Column != 2 || first.Component != "Badge" {
		t.Errorf("first location = %+v", first)
	}
	if strings.Contains(fmt.Sprint(got), "/project") {
		t.Errorf("a location leaks its absolute path: %+v", got)
	}
	locate(content, "div")
	locate("@1 "+pkg+"Dashboard#c0", "html", "body")
	// The application names a frameless root.
	locate("@7", "span")
	want := []string{
		"Badge JobCard#c0 BoardView#c0 Dashboard#c1 | span x b",
		// BoardView, called inside Layout's outlet, is Dashboard's call:
		// its outlets show Dashboard's content, and Dashboard is a root.
		"BoardView Dashboard#c1 | div",
		"Layout Dashboard#c0 | html body",
		"Badge | span",
	}
	if !reflect.DeepEqual(fake.located, want) {
		t.Errorf("located %q\nwant %q", fake.located, want)
	}
	for _, body := range []string{`{"identity":"nonsense","path":[{"tag":"a"}]}`, `{"identity":"@8","path":[{"tag":"a"}]}`} {
		response := post(handler, Prefix+"locate", body, nil)
		var answer locateResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || len(answer.Locations) != 1 || answer.Locations[0] != nil {
			t.Errorf("%s: %d %s", body, response.Code, response.Body.String())
		}
	}
	if response := post(handler, Prefix+"locate", `{"identity":"@1","path":[`+strings.Repeat(`{"tag":"a"},`, maxPath)+`{"tag":"a"}]}`, nil); response.Code != http.StatusBadRequest {
		t.Errorf("an overlong path: %d", response.Code)
	}
}

// TestEdit_GIVENASelection_WHENEdited_THENOpensTheEditorAtItsLocatedSource
// opens the editor at a source ktml dev locates itself: the page names an
// identity and element path, or a component, never a file.
func TestEdit_GIVENASelection_WHENEdited_THENOpensTheEditorAtItsLocatedSource(t *testing.T) {
	handler, _, _ := newHandler(t)
	badge := `@1 ` + pkg + `Dashboard#c1 ` + pkg + `BoardView#c0 ` + pkg + `JobCard#c0`
	element := `{"identity":"` + strings.ReplaceAll(badge, `"`, `\"`) + `","path":[{"tag":"span"},{"tag":"b"}]}`
	if response := post(handler, Prefix+"edit", element, nil); response.Code != http.StatusNotImplemented {
		t.Errorf("edit without an editor: %d %s", response.Code, response.Body.String())
	}
	var opened []explorer.Location
	handler.SetEditor(func(location explorer.Location) error {
		opened = append(opened, location)
		return nil
	})
	response := post(handler, Prefix+"edit", element, nil)
	var got editResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil {
		t.Fatalf("edit: %d %s", response.Code, response.Body.String())
	}
	if got.Location.File != "web/Badge.ktml" || got.Location.Line != 2 {
		t.Errorf("edit answered %+v", got)
	}
	if response := post(handler, Prefix+"edit", `{"component":"`+pkg+`JobCard"}`, nil); response.Code != http.StatusOK {
		t.Errorf("edit a component: %d %s", response.Code, response.Body.String())
	}
	want := []explorer.Location{
		{File: "web/Badge.ktml", Path: "/project/web/Badge.ktml", Line: 2, Column: 2, Component: "Badge"},
		{File: "web/JobCard.ktml", Path: "/project/web/JobCard.ktml", Line: 9, Column: 1, Component: "JobCard"},
	}
	if !reflect.DeepEqual(opened, want) {
		t.Errorf("opened %+v\nwant %+v", opened, want)
	}
	for _, body := range []string{
		`{"identity":"` + strings.ReplaceAll(badge, `"`, `\"`) + `","path":[{"tag":"x"}]}`,
		`{"component":"elsewhere.Missing"}`,
		`{"file":"/etc/passwd","line":1}`,
		`{}`,
	} {
		if response := post(handler, Prefix+"edit", body, nil); response.Code == http.StatusOK {
			t.Errorf("%s opened the editor", body)
		}
	}
	if len(opened) != 2 {
		t.Errorf("opened %d times", len(opened))
	}
	handler.SetEditor(func(explorer.Location) error { return errors.New("no such editor") })
	if response := post(handler, Prefix+"edit", `{"component":"`+pkg+`JobCard"}`, nil); response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "no such editor") {
		t.Errorf("a failing editor: %d %s", response.Code, response.Body.String())
	}
	if response := post(handler, Prefix+"edit", `{"component":"`+pkg+`JobCard"}`, map[string]string{"Origin": "https://evil.example"}); response.Code != http.StatusForbidden {
		t.Errorf("a cross-site edit: %d", response.Code)
	}
}

// TestResolve_GIVENAComponentGoPassedIntoASlot_WHENResolved_THENItHasItsOwnLevel
// gives a component value built in Go and rendered at a layout's content
// outlet, such as a page passed to a layout, its own place in the chain:
// its instance's identity ends with the outlet frame, which the program
// cannot resolve. The application names it, or else the next frame does.
func TestResolve_GIVENAComponentGoPassedIntoASlot_WHENResolved_THENItHasItsOwnLevel(t *testing.T) {
	handler, fake, _ := newHandler(t)
	// Shell (a root rendered by Go) calls Frame at c0; Frame renders its
	// children, Shell's fill, which renders Shell's outlet o0, where Go
	// passed a Planner. Planner calls Row at c0, which calls Badge.
	slot := `@5 ` + pkg + `Shell#c0 ` + pkg + `Frame#o0 ` + pkg + `Shell#o0`
	badge := slot + ` ` + pkg + `Planner#r0="a" ` + pkg + `Planner#c0 ` + pkg + `Row#c0`
	// The same shape on a root the application has no record for.
	inferred := strings.ReplaceAll(badge, "@5 ", "@6 ")
	request, _ := json.Marshal(resolveRequest{Identities: []string{slot, badge, inferred}})
	response := post(handler, Prefix+"resolve", string(request), nil)
	var got resolveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	names := func(identity string) []string {
		var names []string
		for _, entry := range got.Chains[identity] {
			names = append(names, got.Components[entry.Component].Name)
		}
		return names
	}
	if got := names(slot); !reflect.DeepEqual(got, []string{"Shell", "Frame", "Planner"}) {
		t.Errorf("slot chain = %q", got)
	}
	for _, identity := range []string{badge, inferred} {
		if got := names(identity); !reflect.DeepEqual(got, []string{"Shell", "Frame", "Planner", "Row", "Badge"}) {
			t.Errorf("%s chain = %q", identity, got)
		}
	}
	if entry := got.Chains[badge][2]; entry.Identity != slot {
		t.Errorf("Planner's level is %q, want the slot's identity", entry.Identity)
	}

	// Locating inside Row follows Planner's call and stops at Planner, whose
	// own content comes from Go.
	located, _ := json.Marshal(locateRequest{Identity: slot + ` ` + pkg + `Planner#r0="a" ` + pkg + `Planner#c0`, Path: []compiler.ElementStep{{Tag: "li"}}})
	post(handler, Prefix+"locate", string(located), nil)
	located, _ = json.Marshal(locateRequest{Identity: slot, Path: []compiler.ElementStep{{Tag: "ul"}}})
	post(handler, Prefix+"locate", string(located), nil)
	if want := []string{"Row Planner#c0 | li", "Planner | ul"}; !reflect.DeepEqual(fake.located, want) {
		t.Errorf("located %q, want %q", fake.located, want)
	}
}
