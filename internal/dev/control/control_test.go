package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/karl-tye/ktml/dev/hmr"
)

func TestSwap_GIVENApplicationResponses_WHENCalled_THENClassifiesPatchesDiagnosticsAndMissingChannels(t *testing.T) {
	var status int
	var body string
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(hmr.TokenHeader) != "secret" || r.URL.Path != "/__ktml/dev/swap" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer app.Close()
	client := New(app.URL, "secret")

	status, body = http.StatusOK, `{"generation":2,"patches":[{"session":"s","identity":"@1","html":"<p>x</p>"}],"reload":[]}`
	response, err := client.Swap(context.Background(), hmr.SwapRequest{Generation: 2})
	if err != nil || len(response.Patches) != 1 || response.Patches[0].HTML != "<p>x</p>" {
		t.Errorf("Swap() = %#v, %v", response, err)
	}

	status, body = http.StatusUnprocessableEntity, `{"diagnostic":"page.ktml:1: unknown input"}`
	_, err = client.Swap(context.Background(), hmr.SwapRequest{})
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Message != "page.ktml:1: unknown input" {
		t.Errorf("diagnostic error = %v", err)
	}

	if _, err := New(app.URL, "wrong").Swap(context.Background(), hmr.SwapRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("wrong token error = %v", err)
	}
	app.Close()
	if _, err := client.Swap(context.Background(), hmr.SwapRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("stopped application error = %v", err)
	}
}

type recordingTransport struct{ requests []*http.Request }

func (r *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, request)
	return nil, errors.New("recorded")
}

func TestPost_GIVENNonLoopbackApplication_WHENNotOptedIn_THENNeverSendsTheToken(t *testing.T) {
	transport := &recordingTransport{}
	client := New("https://app.example.com", "secret")
	client.HTTP.Transport = transport

	if _, err := client.Swap(context.Background(), hmr.SwapRequest{}); err == nil {
		t.Fatal("Swap() to a remote application without the opt-in succeeded")
	}
	if err := client.Session(context.Background(), "s", true); err == nil {
		t.Fatal("Session() to a remote application without the opt-in succeeded")
	}
	if len(transport.requests) != 0 {
		t.Fatalf("sent %d requests to a remote application without the opt-in", len(transport.requests))
	}

	client.AllowRemote = true
	_, _ = client.Swap(context.Background(), hmr.SwapRequest{})
	if len(transport.requests) != 1 || transport.requests[0].Header.Get(hmr.TokenHeader) != "secret" {
		t.Fatalf("opted-in requests = %d, want one carrying the token", len(transport.requests))
	}
}

func TestPost_GIVENTheApplicationRedirects_WHENCalled_THENDoesNotFollowWithTheToken(t *testing.T) {
	var leaked []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = append(leaked, r.Header.Get(hmr.TokenHeader))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer other.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer app.Close()

	if _, err := New(app.URL, "secret").Swap(context.Background(), hmr.SwapRequest{}); err == nil {
		t.Error("Swap() through a redirect succeeded")
	}
	if len(leaked) != 0 {
		t.Errorf("the redirect target received %d requests (tokens %q)", len(leaked), leaked)
	}
}

func TestComponents_GIVENApplicationResponses_WHENCalled_THENReturnsTheNamesOrUnavailable(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(hmr.TokenHeader) != "secret" || r.URL.Path != "/__ktml/dev/components" {
			http.NotFound(w, r)
			return
		}
		var request hmr.ComponentsRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Identities) == 1 && request.Identities[0] == "@1 example.com/web.Layout#o0" {
			_, _ = w.Write([]byte(`{"components":{"1":"example.com/web.Layout"},"outlets":{"@1 example.com/web.Layout#o0":"example.com/web.Page"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"components":{"1":"example.com/web.Page"}}`))
	}))
	defer app.Close()
	components, outlets, err := New(app.URL, "secret").Components(context.Background(), []string{"1"}, nil)
	if err != nil || components["1"] != "example.com/web.Page" || len(outlets) != 0 {
		t.Errorf("Components() = %v, %v, %v", components, outlets, err)
	}
	components, outlets, err = New(app.URL, "secret").Components(context.Background(), []string{"1"}, []string{"@1 example.com/web.Layout#o0"})
	if err != nil || components["1"] != "example.com/web.Layout" || outlets["@1 example.com/web.Layout#o0"] != "example.com/web.Page" {
		t.Errorf("Components() with an outlet = %v, %v, %v", components, outlets, err)
	}
	if _, _, err := New(app.URL, "wrong").Components(context.Background(), []string{"1"}, nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("without the channel: %v, want ErrUnavailable", err)
	}
}
