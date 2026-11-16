package livereload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/dev/sse"
)

// Server is the development reverse proxy and SSE hub. It injects the client
// script into proxied pages and streams reload, patch, and overlay events to
// each page session. It never holds component props: patches are HTML the
// application rendered from its own registry (ADR-0004).
type Server struct {
	mu        sync.Mutex
	clients   map[chan string]string
	proxy     http.Handler
	targetURL string
	lastError string
	onSession func(session string, connected bool)
	picker    *Picker
	// metaCSPWarning warns once about a page policy in a <meta> tag.
	metaCSPWarning sync.Once
}

// SessionHeader is the response header carrying a page's request token, which
// the application's development registry sets (ktml/dev/hmr.RequestHeader).
const SessionHeader = "X-KTML-Request"

// NoClientHeader marks a page that must not get the client script, such as
// the Component Explorer's own interface around its preview frames.
const NoClientHeader = "X-KTML-No-Client"

func NewServer(targetURL string) *Server {
	slog.Debug("Livereload NewServer", "targetURL", targetURL)
	s := &Server{
		clients:   make(map[chan string]string),
		targetURL: targetURL,
	}

	target, _ := url.Parse(targetURL)
	rp := httputil.NewSingleHostReverseProxy(target)

	rp.ModifyResponse = func(r *http.Response) error {
		session := r.Header.Get(SessionHeader)
		r.Header.Del(SessionHeader)
		if !validSession(session) {
			session = ""
		}
		slog.Debug("Livereload proxy modifying response", "contentType", r.Header.Get("Content-Type"))
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		if r.Header.Get(NoClientHeader) != "" {
			r.Header.Del(NoClientHeader)
			return nil
		}

		if encoding := r.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
			// An encoding the transport did not decode (one the application
			// sends unasked, such as br) cannot take the client; pass the
			// page through intact rather than corrupt it.
			slog.Debug("Livereload cannot inject into an encoded page", "contentEncoding", encoding)
			return nil
		}

		r.Header.Del("Accept-Ranges")

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			slog.Error("Livereload failed to read proxy response", "error", err)
			return err
		}
		r.Body.Close()

		bodyStr := string(bodyBytes)
		s.mu.Lock()
		picker := s.picker
		s.mu.Unlock()
		if strings.Contains(bodyStr, "</head>") {
			injected := clientTag(session) + pickerTag(picker)
			if nonce := newNonce(); allowNonce(r.Header, nonce) {
				injected = withNonce(injected, nonce)
			}
			if metaCSP.MatchString(bodyStr) {
				s.metaCSPWarning.Do(func() {
					slog.Warn("A page sets its Content-Security-Policy in a <meta> tag, which ktml dev cannot amend; if it restricts scripts, the browser blocks live reload, component patching, and the picker. Send the policy as a response header instead, and ktml dev allows its client with a nonce", "path", r.Request.URL.Path)
				})
			}
			bodyStr = strings.Replace(bodyStr, "</head>", injected+"\n</head>", 1)
		}

		r.Body = io.NopCloser(bytes.NewReader([]byte(bodyStr)))
		r.Header.Set("Content-Length", strconv.Itoa(len(bodyStr)))
		r.ContentLength = int64(len(bodyStr))

		return nil
	}

	s.proxy = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/__ktml/livereload" || req.URL.Path == "/.ktml/live-reload" {
			slog.Debug("Livereload handling SSE connection", "path", req.URL.Path)
			s.serveSSE(w, req)
			return
		}
		if strings.HasPrefix(req.URL.Path, "/__ktml/") {
			// The application's control channel is never reachable through
			// the proxy.
			http.NotFound(w, req)
			return
		}
		// The browser's Accept-Encoding would let the application compress
		// the page, which the proxy cannot inject into. Without it the
		// transport negotiates gzip itself and hands back a decoded body,
		// even from an application that compresses regardless.
		req.Header.Del("Accept-Encoding")
		rp.ServeHTTP(w, req)
	})

	return s
}

// clientTag is the injected client: the page's session token, then the
// client script.
func clientTag(session string) string {
	encoded, _ := json.Marshal(session)
	return "<script data-ktml-client>window.__ktmlSession = " + string(encoded) + ";</script>\n" + clientScript
}

// OnSession registers a function called when a page session's SSE stream
// connects or disconnects.
func (s *Server) OnSession(callback func(session string, connected bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSession = callback
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *Server) register(c chan string, session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slog.Debug("Livereload client registered", "total_clients", len(s.clients)+1, "session", session)
	s.clients[c] = session
	if s.lastError != "" {
		c <- event("diagnostic", s.lastError)
	}
}

func (s *Server) unregister(c chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slog.Debug("Livereload client unregistered", "total_clients", len(s.clients)-1)
	delete(s.clients, c)
}

func (s *Server) ActiveClients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	session := r.URL.Query().Get("session")
	if !validSession(session) {
		session = ""
	}
	c := make(chan string, 16)
	s.register(c, session)
	defer s.unregister(c)
	s.notifySession(session, true)
	defer s.notifySession(session, false)

	for {
		select {
		case msg := <-c:
			_, _ = fmt.Fprint(w, msg)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) notifySession(session string, connected bool) {
	s.mu.Lock()
	callback := s.onSession
	s.mu.Unlock()
	if callback != nil && session != "" {
		callback(session, connected)
	}
}

// event formats one SSE event; see sse.Encode.
func event(name, data string) string { return sse.Encode(name, data) }

// Broadcast sends an event to every connected page.
func (s *Server) Broadcast(name string, data string) {
	slog.Debug("Livereload broadcasting event", "event", name, "data", data)
	s.send(func(string) bool { return true }, event(name, data))
}

// SendTo sends an event to the pages of one session.
func (s *Server) SendTo(session, name, data string) {
	slog.Debug("Livereload sending event", "session", session, "event", name)
	s.send(func(client string) bool { return client == session }, event(name, data))
}

func (s *Server) send(match func(session string) bool, payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, session := range s.clients {
		if !match(session) {
			continue
		}
		select {
		case c <- payload:
		default:
		}
	}
}

// Patch sends rerendered HTML for one root instance to its page session.
func (s *Server) Patch(session, identity, html string, document bool) {
	payload, _ := json.Marshal(struct {
		Identity string `json:"identity"`
		HTML     string `json:"html"`
		Document bool   `json:"document,omitempty"`
	}{identity, html, document})
	s.SendTo(session, "patch", string(payload))
}

// ShowError shows the error overlay on every page, including pages that
// connect later, until ClearError.
func (s *Server) ShowError(message string) {
	s.mu.Lock()
	s.lastError = message
	s.mu.Unlock()
	s.Broadcast("diagnostic", message)
}

// ClearError removes the error overlay.
func (s *Server) ClearError() {
	s.mu.Lock()
	shown := s.lastError != ""
	s.lastError = ""
	s.mu.Unlock()
	if shown {
		s.Broadcast("clear", "")
	}
}

// ReloadAssets tells every page that static assets changed, by their
// project-relative slash paths: the client swaps changed stylesheets in place
// and reloads the page for anything else.
func (s *Server) ReloadAssets(paths []string) {
	payload, _ := json.Marshal(struct {
		Paths []string `json:"paths"`
	}{paths})
	s.Broadcast("assets", string(payload))
}

func (s *Server) FullReload() {
	slog.Debug("Livereload FullReload triggered")
	s.Broadcast("reload", "1")
}

func (s *Server) WaitForTarget(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.targetURL, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode < http.StatusInternalServerError {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// validSession accepts the opaque page tokens the application issues.
func validSession(session string) bool {
	if session == "" || len(session) > 64 {
		return false
	}
	for _, r := range session {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
