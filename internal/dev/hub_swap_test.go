package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/internal/dev/control"
	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/sse"
	"github.com/karl-tye/ktml/internal/dev/watcher"
)

type fakeSwapper struct {
	mu       sync.Mutex
	requests []hmr.SwapRequest
	response hmr.SwapResponse
	err      error
}

func (f *fakeSwapper) Swap(_ context.Context, request hmr.SwapRequest) (hmr.SwapResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	return f.response, f.err
}

// sessionEvents connects a page session's SSE stream and returns its events
// as "name data" lines, overlay events included.
func sessionEvents(t *testing.T, liveReload *livereload.Server, session string) <-chan string {
	t.Helper()
	server := httptest.NewServer(liveReload)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); server.Close() })
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/__ktml/livereload?session="+session, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := liveReload.ActiveClients()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	deadline := time.Now().Add(time.Second)
	for liveReload.ActiveClients() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	events := make(chan string, 16)
	go func() {
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
	return events
}

func nextEvent(t *testing.T, events <-chan string) string {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		return "(none)"
	}
}

type swapHubFixture struct {
	hub     *Hub
	swapper *fakeSwapper
	server  *livereload.Server
	path    string
	listen  chan watcher.Event
	gens    chan Batch
}

func newSwapHubFixture(t *testing.T) *swapHubFixture {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "components.ktml")
	writeHubSource(t, path, "Count [value: string] {\n\tspan \"{value} active\"\n}\n")
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	f := &swapHubFixture{swapper: &fakeSwapper{}, server: livereload.NewServer(target.URL), path: path, listen: make(chan watcher.Event, 1), gens: make(chan Batch, 4)}
	f.hub = NewHub(f.server, nil, 0, func(batch Batch) error {
		f.gens <- batch
		return nil
	})
	f.hub.SetControl(f.swapper)
	if err := f.hub.TrackFile(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.hub.Listen(ctx, f.listen)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func (f *swapHubFixture) edit(t *testing.T, source string) {
	t.Helper()
	writeHubSource(t, f.path, source)
	f.listen <- watcher.Event{Type: watcher.EventReload, Files: []string{f.path}}
}

const compatibleCount = "Count [value: string] {\n\tspan \"{value} live\"\n}\n"

func TestHub_GIVENCompatibleChange_WHENSwapped_THENBroadcastsEachPatchToItsSessionOnly(t *testing.T) {
	f := newSwapHubFixture(t)
	f.swapper.response = hmr.SwapResponse{Generation: 1, Patches: []hmr.Patch{{Session: "page-a", Identity: "@1", HTML: "<span>3 live</span>"}}}
	pageA := sessionEvents(t, f.server, "page-a")
	pageB := sessionEvents(t, f.server, "page-b")
	f.edit(t, compatibleCount)

	if got := nextEvent(t, pageA); got != "patch "+`{"identity":"@1","html":"`+"\\u003cspan\\u003e3 live\\u003c/span\\u003e"+`"}` {
		t.Fatalf("page-a event = %q", got)
	}
	select {
	case event := <-pageB:
		t.Errorf("page-b received %q", event)
	case <-time.After(100 * time.Millisecond):
	}
	if batch := <-f.gens; len(batch.Generate) != 1 {
		t.Errorf("generated batch = %+v", batch)
	}
	f.swapper.mu.Lock()
	defer f.swapper.mu.Unlock()
	if len(f.swapper.requests) != 1 {
		t.Fatalf("swap requests = %d", len(f.swapper.requests))
	}
	request := f.swapper.requests[0]
	if request.Generation != 1 || len(request.Files) != 1 || request.Files[0].Path != f.path || request.Files[0].Source != compatibleCount {
		t.Errorf("swap request = %+v", request)
	}
}

func TestHub_GIVENSwapThatCannotPatch_WHENReloaded_THENFallsBackToAFullReload(t *testing.T) {
	tests := []struct {
		name     string
		response hmr.SwapResponse
		err      error
		overlay  bool
	}{
		{name: "no control channel", err: control.ErrUnavailable},
		{name: "failed rerender", response: hmr.SwapResponse{Reload: []string{"page-a"}, Patches: []hmr.Patch{{Session: "page-b", Identity: "@2", HTML: "<p></p>"}}}},
		{name: "interpreter diagnostic", err: &control.DiagnosticError{Message: "components.ktml:2: rejected"}, overlay: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSwapHubFixture(t)
			f.swapper.response, f.swapper.err = tt.response, tt.err
			page := sessionEvents(t, f.server, "page-b")
			f.edit(t, compatibleCount)
			want := []string{"reload 1"}
			if tt.overlay {
				want = []string{"diagnostic components.ktml:2: rejected", "clear", "reload 1"}
			}
			for _, expected := range want {
				if got := strings.TrimSpace(nextEvent(t, page)); got != expected {
					t.Fatalf("event = %q, want %q", got, expected)
				}
			}
		})
	}
}

func TestHub_GIVENInvalidEdit_WHENReloaded_THENShowsTheDiagnosticAndKeepsThePage(t *testing.T) {
	f := newSwapHubFixture(t)
	page := sessionEvents(t, f.server, "page-a")
	f.edit(t, "Count [value: string] {\n\tspan \"{missing}\"\n}\n")
	if got := nextEvent(t, page); !strings.HasPrefix(got, "diagnostic ") || !strings.Contains(got, "missing") {
		t.Fatalf("event = %q, want the diagnostic overlay", got)
	}
	f.swapper.response = hmr.SwapResponse{Patches: []hmr.Patch{{Session: "page-a", Identity: "@1", HTML: "<span>fixed</span>"}}}
	f.edit(t, compatibleCount)
	if got := nextEvent(t, page); !strings.HasPrefix(got, "patch ") {
		t.Fatalf("event after the fix = %q, want the patch", got)
	}
	if got := strings.TrimSpace(nextEvent(t, page)); got != "clear" {
		t.Errorf("event after the patch = %q, want the overlay cleared", got)
	}
}
