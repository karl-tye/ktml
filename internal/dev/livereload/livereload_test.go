package livereload

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/sse"
)

func TestProxy_GIVENPageWithRequestToken_WHENProxied_THENInjectsTheSessionAndHidesTheHeader(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set(SessionHeader, "tok_123")
		_, _ = io.WriteString(w, `<html><head></head><body><template class="__ktml_cache" data-ktml-id="x" data-ktml-component="App">{"Secret":"s"}</template></body></html>`)
	}))
	defer target.Close()
	proxy := httptest.NewServer(NewServer(target.URL))
	defer proxy.Close()

	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.Header.Get(SessionHeader) != "" {
		t.Error("the request token header reached the browser")
	}
	if !strings.Contains(string(body), `<script data-ktml-client>window.__ktmlSession = "tok_123";</script>`) {
		t.Errorf("page does not carry its session: %s", body)
	}
	if strings.Index(string(body), "data-ktml-client") > strings.Index(string(body), "</head>") {
		t.Error("client is not injected into the head")
	}
	for _, path := range []string{"/__ktml/cache", "/__ktml/dev/swap"} {
		response, err := http.Post(proxy.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s through the proxy: status %d, want 404", path, response.StatusCode)
		}
	}
}

func TestProxy_GIVENHostileRequestToken_WHENProxied_THENInjectsNoSession(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set(SessionHeader, `"</script><script>alert(1)//`)
		_, _ = io.WriteString(w, `<html><head></head><body></body></html>`)
	}))
	defer target.Close()
	proxy := httptest.NewServer(NewServer(target.URL))
	defer proxy.Close()
	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(body), "alert") || !strings.Contains(string(body), `window.__ktmlSession = "";`) {
		t.Errorf("body = %s", body)
	}
}

func TestProxy_GIVENAGzipBrowserRequest_WHENTheApplicationCompresses_THENThePageStillGetsTheClient(t *testing.T) {
	const page = `<html><head></head><body>hello</body></html>`
	tests := []struct {
		name   string
		always bool
	}{
		{name: "GIVENAnApplicationThatNegotiatesGzip_WHENProxied_THENInjectsTheClient", always: false},
		{name: "GIVENAnApplicationThatAlwaysGzips_WHENProxied_THENInjectsTheClient", always: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				if !tt.always && !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
					_, _ = io.WriteString(w, page)
					return
				}
				w.Header().Set("Content-Encoding", "gzip")
				zw := gzip.NewWriter(w)
				_, _ = io.WriteString(zw, page)
				_ = zw.Close()
			}))
			defer target.Close()
			proxy := httptest.NewServer(NewServer(target.URL))
			defer proxy.Close()

			// A browser asks for gzip itself and decodes whatever comes back,
			// so the client transport must not negotiate on its behalf.
			request, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept-Encoding", "gzip, deflate, br")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var reader io.Reader = response.Body
			if response.Header.Get("Content-Encoding") == "gzip" {
				zr, err := gzip.NewReader(response.Body)
				if err != nil {
					t.Fatalf("gzip response does not decode: %v", err)
				}
				reader = zr
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("reading the page: %v", err)
			}
			if !strings.Contains(string(body), "data-ktml-client") || !strings.Contains(string(body), "hello") {
				t.Errorf("Content-Encoding %q, page lacks the client: %q", response.Header.Get("Content-Encoding"), body)
			}
		})
	}
}

type sseClient struct {
	events chan string
	close  func()
}

func connect(t *testing.T, server *Server, base, session string) sseClient {
	t.Helper()
	before := server.ActiveClients()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/__ktml/livereload?session="+session, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for server.ActiveClients() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	events := make(chan string, 16)
	go func() {
		defer close(events)
		// A spec reader, as the page's EventSource reads the stream.
		reader := sse.NewReader(response.Body)
		for {
			event, err := reader.Next()
			if err != nil {
				return
			}
			events <- event.Name + " " + event.Data
		}
	}()
	return sseClient{events: events, close: func() { cancel(); _ = response.Body.Close() }}
}

func next(t *testing.T, client sseClient) string {
	t.Helper()
	select {
	case event := <-client.events:
		return event
	case <-time.After(time.Second):
		return "(none)"
	}
}

func TestSSE_GIVENTwoSessions_WHENPatched_THENOnlyTheOwningSessionReceivesItsPatch(t *testing.T) {
	server := NewServer("http://127.0.0.1:1")
	sessions := make(chan string, 8)
	server.OnSession(func(session string, connected bool) {
		sessions <- fmt.Sprintf("%s=%v", session, connected)
	})
	listener := httptest.NewServer(server)
	defer listener.Close()
	a := connect(t, server, listener.URL, "a")
	b := connect(t, server, listener.URL, "b")
	server.Patch("b", "@2", "<p>\"new\"</p>", false)
	server.FullReload()
	if got := next(t, a); got != "reload 1" {
		t.Errorf("session a got %q first, want the reload only", got)
	}
	if got := next(t, b); got != `patch {"identity":"@2","html":"\u003cp\u003e\"new\"\u003c/p\u003e"}` {
		t.Errorf("session b patch = %q", got)
	}
	if got := next(t, b); got != "reload 1" {
		t.Errorf("session b then got %q", got)
	}
	a.close()
	b.close()
	var reported []string
	for range 4 {
		select {
		case session := <-sessions:
			reported = append(reported, session)
		case <-time.After(time.Second):
			t.Fatalf("session callbacks = %v", reported)
		}
	}
	sort.Strings(reported)
	if got := strings.Join(reported, " "); got != "a=false a=true b=false b=true" {
		t.Errorf("session callbacks = %s", got)
	}
}

func TestSSE_GIVENErrorOverlay_WHENAPageConnectsLater_THENItSeesTheErrorUntilCleared(t *testing.T) {
	server := NewServer("http://127.0.0.1:1")
	listener := httptest.NewServer(server)
	defer listener.Close()
	server.ShowError("page.ktml:3: unknown input x")
	late := connect(t, server, listener.URL, "late")
	defer late.close()
	if got := next(t, late); got != "diagnostic page.ktml:3: unknown input x" {
		t.Errorf("late page got %q", got)
	}
	server.ClearError()
	if got := next(t, late); got != "clear " && got != "clear" {
		t.Errorf("after ClearError got %q", got)
	}
	server.ClearError()
	if got := next(t, late); got != "(none)" {
		t.Errorf("a second ClearError sent %q", got)
	}
}

func TestSSE_GIVENADiagnosticWithMixedLineEndings_WHENShown_THENThePageReadsOneEventWithLFLines(t *testing.T) {
	server := NewServer("http://127.0.0.1:1")
	front := httptest.NewServer(server)
	defer front.Close()
	page := connect(t, server, front.URL, "s1")
	defer page.close()

	server.ShowError("page.ktml:3: unknown input\r\n  at x\rdata: forged\revent: reload\r\r\n")
	server.Broadcast("ok", "1")

	if got := next(t, page); got != "diagnostic page.ktml:3: unknown input\n  at x\ndata: forged\nevent: reload\n\n" {
		t.Fatalf("event = %q, want the whole diagnostic as one event", got)
	}
	if got := next(t, page); got != "ok 1" {
		t.Fatalf("next event = %q, want ok 1 (no forged event)", got)
	}
}

func TestWaitForTarget_GIVENStartingBackend_WHENPolled_THENWaitsUntilReady(t *testing.T) {
	ready := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()
	server := NewServer(target.URL)
	go func() {
		time.Sleep(75 * time.Millisecond)
		close(ready)
	}()

	if err := server.WaitForTarget(context.Background(), time.Second); err != nil {
		t.Fatalf("WaitForTarget() error = %v", err)
	}
}

func TestProxy_GIVENTheRemovedElementsPaths_WHENRequested_THENTheyReachTheApplication(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "app:"+r.URL.Path)
	}))
	defer target.Close()
	proxy := httptest.NewServer(NewServer(target.URL))
	defer proxy.Close()

	// The legacy /ktml/elements browser parsed and executed every .html file
	// in the project as html/template on each request; the Component
	// Explorer replaces it, and the dev proxy serves none of its routes.
	for _, path := range []string{"/ktml/elements", "/ktml/elements/", "/ktml/elements/api/components", "/ktml/elements/render/Button"} {
		response, err := http.Post(proxy.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if string(body) != "app:"+path {
			t.Errorf("POST %s through the proxy = %q, want the application's response", path, body)
		}
	}
}

// TestProxy_GIVENThePicker_WHENSet_THENOnlyThenPagesGetItsConfigAndScript
// adds the component picker to pages only when ktml dev enables it, as the
// Explorer's own previews never do.
func TestProxy_GIVENThePicker_WHENSet_THENOnlyThenPagesGetItsConfigAndScript(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head></head><body></body></html>`)
	}))
	defer target.Close()
	server := NewServer(target.URL)
	proxy := httptest.NewServer(server)
	defer proxy.Close()
	page := func() string {
		response, err := http.Get(proxy.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return string(body)
	}
	if body := page(); strings.Contains(body, "__ktmlPicker") {
		t.Error("a page got the picker before it was set")
	}
	server.SetPicker(Picker{Hotkey: "Alt+Shift+K", Explorer: "http://127.0.0.1:7349</script>"})
	body := page()
	if !strings.Contains(body, `window.__ktmlPicker = {"hotkey":"Alt+Shift+K","explorer":"http://127.0.0.1:7349\u003c/script\u003e"};`) {
		t.Errorf("picker config is missing or unescaped:\n%s", body)
	}
	if !strings.Contains(body, `fetch("/__ktml/picker/"`) || strings.Index(body, "__ktmlPickerLoaded") > strings.Index(body, "</head>") {
		t.Error("the picker script is not in the head")
	}
}

func TestSSE_GIVENChangedAssets_WHENReloaded_THENEveryPageGetsTheirSlashPaths(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	server := NewServer(target.URL)
	proxy := httptest.NewServer(server)
	defer proxy.Close()
	response, err := http.Get(proxy.URL + "/__ktml/livereload?session=page-a")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for server.ActiveClients() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	server.ReloadAssets([]string{"static/css/site.css", "static/logo.svg"})

	reader := sse.NewReader(response.Body)
	event, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "assets" || event.Data != `{"paths":["static/css/site.css","static/logo.svg"]}` {
		t.Errorf("event = %s %s", event.Name, event.Data)
	}
}
