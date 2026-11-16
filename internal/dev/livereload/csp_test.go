package livereload

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestAmendPolicies_GIVENPolicyShapes_WHENAmended_THENAllowsTheNonceAndKeepsOtherDirectives(t *testing.T) {
	const n = "'nonce-N0nce'"
	for _, tt := range []struct {
		name    string
		header  string
		want    string
		amended bool
	}{
		{"script-src self", "script-src 'self'", "script-src 'self' " + n, true},
		{"other directives unchanged", "default-src 'self'; img-src * data:; script-src 'self' https://cdn.example; style-src 'self'", "default-src 'self'; img-src * data:; script-src 'self' https://cdn.example " + n + "; style-src 'self'", true},
		{"default-src only", "default-src 'self'; img-src *", "default-src 'self' " + n + "; img-src *", true},
		{"script-src-elem governs elements", "script-src 'self'; script-src-elem 'self'", "script-src 'self'; script-src-elem 'self' " + n, true},
		{"strict-dynamic", "script-src 'strict-dynamic' 'nonce-app' 'unsafe-inline' https:", "script-src 'strict-dynamic' 'nonce-app' 'unsafe-inline' https: " + n, true},
		{"strict-dynamic ignores unsafe-inline", "script-src 'strict-dynamic' 'unsafe-inline'", "script-src 'strict-dynamic' 'unsafe-inline' " + n, true},
		{"hash disables unsafe-inline", "script-src 'unsafe-inline' 'sha256-abc='", "script-src 'unsafe-inline' 'sha256-abc=' " + n, true},
		{"unsafe-inline already allows the client", "script-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline'", false},
		{"none", "script-src 'none'; object-src 'none'", "script-src " + n + "; object-src 'none'", true},
		{"no script restriction", "img-src 'self'; frame-ancestors 'none'", "img-src 'self'; frame-ancestors 'none'", false},
		{"multiple policies in one header", "script-src 'self', default-src 'none'; img-src *", "script-src 'self' " + n + ", default-src " + n + "; img-src *", true},
		{"case-insensitive directive names", "Script-Src 'self'", "Script-Src 'self' " + n, true},
		{"trailing semicolon", "script-src 'self';", "script-src 'self' " + n, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, amended := amendPolicies(tt.header, "N0nce")

			if got != tt.want || amended != tt.amended {
				t.Errorf("amendPolicies(%q) = %q, %v; want %q, %v", tt.header, got, amended, tt.want, tt.amended)
			}
		})
	}
}

var nonceAttribute = regexp.MustCompile(`<script nonce="([A-Za-z0-9+/=]+)" data-ktml-client>`)

func proxyPage(t *testing.T, server *Server, target *httptest.Server) (*http.Response, string) {
	t.Helper()
	proxy := httptest.NewServer(server)
	t.Cleanup(proxy.Close)
	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return response, string(body)
}

func TestProxy_GIVENScriptRestrictingCSPHeaders_WHENProxied_THENEveryInjectedScriptCarriesTheNonceEachPolicyAllows(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Add("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		w.Header().Add("Content-Security-Policy", "script-src 'self' https://cdn.example")
		w.Header().Set("Content-Security-Policy-Report-Only", "script-src 'strict-dynamic' 'nonce-app'; report-uri /csp")
		_, _ = io.WriteString(w, `<html><head><script nonce="app">app()</script></head><body></body></html>`)
	}))
	defer target.Close()
	server := NewServer(target.URL)
	server.SetPicker(Picker{Hotkey: "Alt+Shift+K", Explorer: "http://127.0.0.1:4200"})

	response, body := proxyPage(t, server, target)

	matches := nonceAttribute.FindAllStringSubmatch(body, -1)
	// Every script tag but the application's own is injected.
	if got := strings.Count(body, "<script") - 1; got < 4 || len(matches) != got {
		t.Fatalf("%d injected scripts, %d with a nonce; want every one:\n%s", got, len(matches), body)
	}
	nonce := matches[0][1]
	for _, match := range matches {
		if match[1] != nonce {
			t.Fatalf("injected scripts carry different nonces %q and %q", nonce, match[1])
		}
	}
	policies := response.Header.Values("Content-Security-Policy")
	want := []string{"default-src 'self'; script-src 'self' 'nonce-" + nonce + "'", "script-src 'self' https://cdn.example 'nonce-" + nonce + "'"}
	if len(policies) != 2 || policies[0] != want[0] || policies[1] != want[1] {
		t.Errorf("Content-Security-Policy = %q, want %q", policies, want)
	}
	if got, want := response.Header.Get("Content-Security-Policy-Report-Only"), "script-src 'strict-dynamic' 'nonce-app' 'nonce-"+nonce+"'; report-uri /csp"; got != want {
		t.Errorf("Content-Security-Policy-Report-Only = %q, want %q", got, want)
	}
	if !strings.Contains(body, `<script nonce="app">app()</script>`) {
		t.Error("the application's own script changed")
	}

	_, second := proxyPage(t, server, target)
	if again := nonceAttribute.FindStringSubmatch(second); again == nil || again[1] == nonce {
		t.Errorf("second response reused the nonce %q", nonce)
	}
}

func TestProxy_GIVENNoCSP_WHENProxied_THENInjectsScriptsWithoutANonce(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head></head><body></body></html>`)
	}))
	defer target.Close()

	response, body := proxyPage(t, NewServer(target.URL), target)

	if strings.Contains(body, "nonce=") || response.Header.Get("Content-Security-Policy") != "" {
		t.Errorf("page without a policy got a nonce: %s", body)
	}
}

func TestProxy_GIVENMetaTagCSP_WHENProxied_THENWarnsOnce(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head><META http-equiv="Content-Security-Policy" content="script-src 'self'"></head><body></body></html>`)
	}))
	defer target.Close()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	server := NewServer(target.URL)

	proxyPage(t, server, target)
	proxyPage(t, server, target)

	if got := strings.Count(logs.String(), "Content-Security-Policy"); got != 1 {
		t.Errorf("meta-tag policy warnings = %d, want one:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "<meta>") {
		t.Errorf("warning does not explain the meta tag:\n%s", logs.String())
	}
}
