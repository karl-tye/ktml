package explorer

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

// htmxRequest sends a request as HTMX in a story's preview frame would:
// headers is htmx 4's, htmx 2's, or the preview hook's set.
func htmxRequest(t *testing.T, method, address, story string, headers map[string]string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(address)
	request.Header.Set("HX-Request", "true")
	request.Header.Set("HX-Current-URL", base.Scheme+"://"+base.Host+Prefix+"preview?story="+url.QueryEscape(story))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}

func previewRequests(t *testing.T, address, story string) []previewRequest {
	t.Helper()
	_, body := get(t, address+Prefix+"requests?story="+url.QueryEscape(story))
	var result struct {
		Requests []previewRequest `json:"requests"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("requests: %v\n%s", err, body)
	}
	return result.Requests
}

// TestPreviewRequests_GIVENHTMXRequests_WHENSent_THENNoContentAndLogged
// sends a preview's HTMX requests to placeholders, the Explorer's index,
// and its own routes, with every method: each is answered 204 No Content,
// which htmx 2 and 4 swap nothing for, and listed newest first with its
// element and trigger, repeats collapsed into a count.
func TestPreviewRequests_GIVENHTMXRequests_WHENSent_THENNoContentAndLogged(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)
	htmx4 := map[string]string{"HX-Source": "div#history", "X-Ktml-Explorer-Element": "div#history", "X-Ktml-Explorer-Trigger": "every 1s"}
	for range 3 {
		if status, body := htmxRequest(t, http.MethodGet, backend.URL+"/_story/card/link", runningKey, htmx4); status != http.StatusNoContent || body != "" {
			t.Fatalf("polling: status %d body %q, want 204 and no body", status, body)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{"/", Prefix + "state?story=" + url.QueryEscape(runningKey), Prefix + "edit", "/anything"} {
			if status, body := htmxRequest(t, method, backend.URL+path, runningKey, nil); status != http.StatusNoContent || body != "" {
				t.Errorf("%s %s: status %d body %q, want 204 and no body", method, path, status, body)
			}
		}
	}
	// htmx 2 names the element by its id and name, and has no preview hook.
	htmx2 := map[string]string{"HX-Trigger": "save", "HX-Trigger-Name": "save-button", "HX-Target": "result"}
	htmxRequest(t, http.MethodPost, backend.URL+"/_story/card/save", runningKey, htmx2)

	got := previewRequests(t, backend.URL, runningKey)
	if len(got) != 2+5*4 {
		t.Fatalf("logged %d requests, want 22 rows: %+v", len(got), got)
	}
	newest := got[0]
	if newest.Method != "POST" || newest.URL != "/_story/card/save" || newest.Element != "#save" || newest.Count != 1 {
		t.Errorf("newest = %+v, want the htmx 2 POST from #save", newest)
	}
	polling := got[len(got)-1]
	if polling.Method != "GET" || polling.URL != "/_story/card/link" || polling.Element != "div#history" || polling.Trigger != "every 1s" || polling.Count != 3 {
		t.Errorf("oldest = %+v, want the polling GET counted 3 times", polling)
	}
	if polling.Time.IsZero() {
		t.Errorf("polling has no time")
	}
	if other := previewRequests(t, backend.URL, "ui/card.story.ktml#Card.Idle"); len(other) != 0 {
		t.Errorf("another story lists %d requests, want none", len(other))
	}

	request, _ := http.NewRequest(http.MethodDelete, backend.URL+Prefix+"requests?story="+url.QueryEscape(runningKey), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("clear: status %d", response.StatusCode)
	}
	if got := previewRequests(t, backend.URL, runningKey); len(got) != 0 {
		t.Errorf("after clear: %+v", got)
	}
}

// TestPreviewRequests_GIVENManyDistinctRequests_WHENLogged_THENTheNewestAreKept
// caps a story's list.
func TestPreviewRequests_GIVENManyDistinctRequests_WHENLogged_THENTheNewestAreKept(t *testing.T) {
	_, backend, _ := newTestServer(t, fixtureFiles, nil)
	for index := range requestLimit + 10 {
		htmxRequest(t, http.MethodGet, backend.URL+"/_story/card/item/"+string(rune('a'+index%26))+strings.Repeat("x", index/26), runningKey, nil)
	}
	got := previewRequests(t, backend.URL, runningKey)
	if len(got) != requestLimit {
		t.Fatalf("logged %d, want %d", len(got), requestLimit)
	}
	if got[0].URL != "/_story/card/item/hxx" {
		t.Errorf("newest = %s", got[0].URL)
	}
}

// TestPreviewRequests_GIVENOrdinaryRequests_WHENSent_THENExplorerAndAssetsServed
// checks that what is not HTMX is untouched: the index, previews, state,
// bundled and static assets, and the events stream, which announces a
// logged request to the interface. A placeholder URL loaded without HTMX,
// as a link or image, is 204 too but not listed.
func TestPreviewRequests_GIVENOrdinaryRequests_WHENSent_THENExplorerAndAssetsServed(t *testing.T) {
	files := map[string]string{"static/app.css": "body { color: teal }\n"}
	for name, source := range fixtureFiles {
		files[name] = source
	}
	_, backend, _ := newTestServer(t, files, &config.Explorer{Static: &config.ExplorerStatic{Dir: "static", URL: "/static"}, Stylesheets: []string{"/static/app.css"}})
	for path, want := range map[string]string{
		"/":                                      "KTML Explorer",
		"/?story=" + url.QueryEscape(runningKey): "data-requests",
		Prefix + "preview?story=" + url.QueryEscape(runningKey): Prefix + "assets/preview.js",
		Prefix + "state?story=" + url.QueryEscape(runningKey):   `"navigation"`,
		Prefix + "assets/htmx.min.js":                           "htmx",
		Prefix + "assets/preview.js":                            "htmx:config:request",
		"/static/app.css":                                       "color: teal",
	} {
		if status, body := get(t, backend.URL+path); status != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("GET %s: status %d, want 200 with %q", path, status, want)
		}
	}
	if status, _ := get(t, backend.URL+"/_story/card/link"); status != http.StatusNoContent {
		t.Errorf("GET /_story/card/link without HTMX: status %d, want 204", status)
	}
	if got := previewRequests(t, backend.URL, runningKey); len(got) != 0 {
		t.Errorf("ordinary requests were listed: %+v", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, backend.URL+Prefix+"events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: status %d type %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	htmxRequest(t, http.MethodGet, backend.URL+"/_story/card/link", runningKey, nil)
	lines := bufio.NewScanner(response.Body)
	var event []string
	for lines.Scan() && lines.Text() != "" {
		event = append(event, lines.Text())
	}
	if want := []string{"event: requests", "data: " + runningKey}; strings.Join(event, "\n") != strings.Join(want, "\n") {
		t.Errorf("event = %q, want %q", event, want)
	}
}
