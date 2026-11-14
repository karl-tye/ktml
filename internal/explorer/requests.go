package explorer

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/storygen"
)

// Previews have no application behind them. Every HTMX request a preview
// makes is answered 204 No Content, which htmx 2 and 4 swap nothing for,
// and listed in the interface's Requests panel. Without this, a request to
// a URL of the Explorer, such as "/", swaps the Explorer's own interface
// into the component.

// requestLimit bounds the requests listed for a story.
const requestLimit = 50

// The preview hook (assets/preview.js) names the element and trigger of
// each HTMX request in these headers. Without them, as in a story that
// renders its own document, the Requests panel falls back to the headers
// HTMX sends.
const (
	elementHeader = "X-Ktml-Explorer-Element"
	triggerHeader = "X-Ktml-Explorer-Trigger"
)

// previewRequest is one row of the Requests panel: a request, or Count
// repeats of it, last made at Time.
type previewRequest struct {
	Method  string    `json:"method"`
	URL     string    `json:"url"`
	Element string    `json:"element,omitempty"`
	Trigger string    `json:"trigger,omitempty"`
	Count   int       `json:"count"`
	Time    time.Time `json:"time"`
}

func (r previewRequest) same(other previewRequest) bool {
	return r.Method == other.Method && r.URL == other.URL && r.Element == other.Element && r.Trigger == other.Trigger
}

// requestLog holds each story's requests, newest first.
type requestLog struct {
	mu      sync.Mutex
	stories map[string][]previewRequest
}

// add lists a request first, folding an earlier identical one into it.
func (l *requestLog) add(story string, request previewRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stories == nil {
		l.stories = make(map[string][]previewRequest)
	}
	list := l.stories[story]
	request.Count = 1
	for index, earlier := range list {
		if earlier.same(request) {
			request.Count += earlier.Count
			list = append(list[:index:index], list[index+1:]...)
			break
		}
	}
	list = append([]previewRequest{request}, list...)
	if len(list) > requestLimit {
		list = list[:requestLimit]
	}
	l.stories[story] = list
}

func (l *requestLog) list(story string) []previewRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]previewRequest{}, l.stories[story]...)
}

func (l *requestLog) clear(story string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.stories, story)
}

// isHTMX reports a request HTMX made: htmx 2 and 4 send HX-Request, and
// the preview hook its own header.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" || r.Header.Get(triggerHeader) != "" || r.Header.Get(elementHeader) != ""
}

// neutralize answers a preview's HTMX requests, of any method and to any
// URL, with 204 No Content and lists them; requests to placeholder URLs
// without HTMX, such as a followed link, get 204 too. Everything else,
// the Explorer's own requests and the assets a preview loads, is served.
func (s *Server) neutralize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		htmx := isHTMX(r)
		if !htmx && !strings.HasPrefix(r.URL.Path, storygen.PlaceholderPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		if htmx {
			story := requestStory(r)
			s.requests.add(story, describeRequest(r))
			s.broadcast("requests", story)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

// requestStory is the story whose preview made a request: from the page
// URL HTMX sends, or the Referer.
func requestStory(r *http.Request) string {
	for _, page := range []string{r.Header.Get("HX-Current-URL"), r.Referer()} {
		parsed, err := url.Parse(page)
		if err == nil && parsed.Path == Prefix+"preview" {
			return parsed.Query().Get("story")
		}
	}
	return ""
}

// describeRequest names a request's element and trigger from the preview
// hook, else from htmx 4's HX-Source (tag#id) or htmx 2's HX-Trigger (the
// id) and HX-Trigger-Name.
func describeRequest(r *http.Request) previewRequest {
	request := previewRequest{Method: r.Method, URL: r.URL.RequestURI(), Trigger: r.Header.Get(triggerHeader), Time: time.Now()}
	switch {
	case r.Header.Get(elementHeader) != "":
		request.Element = r.Header.Get(elementHeader)
	case r.Header.Get("HX-Source") != "":
		request.Element = r.Header.Get("HX-Source")
	case r.Header.Get("HX-Trigger") != "":
		request.Element = "#" + r.Header.Get("HX-Trigger")
	case r.Header.Get("HX-Trigger-Name") != "":
		request.Element = `[name="` + r.Header.Get("HX-Trigger-Name") + `"]`
	}
	return request
}

// requestsResponse is the body of GET /_explorer/requests.
type requestsResponse struct {
	Requests []previewRequest `json:"requests"`
}

func (s *Server) serveRequests(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, requestsResponse{Requests: s.requests.list(r.URL.Query().Get("story"))})
}

func (s *Server) clearRequests(w http.ResponseWriter, r *http.Request) {
	story := r.URL.Query().Get("story")
	s.requests.clear(story)
	s.broadcast("requests", story)
	w.WriteHeader(http.StatusNoContent)
}
