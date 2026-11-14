package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/livereload"
)

// A backend that takes longer than the default readiness wait to start, as
// a server that seeds local fixtures at startup does (about 30 s), must not fail every rebuild.
func TestBackendReady_GIVENASlowStartingBackend_WHENTheReadyTimeoutCoversIt_THENItIsReady(t *testing.T) {
	var ready atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	time.AfterFunc(400*time.Millisecond, func() { ready.Store(true) })

	short := NewHub(livereload.NewServer(target.URL), nil, 0, nil)
	short.SetReadyTimeout(100 * time.Millisecond)
	if err := short.backendReady(context.Background()); err == nil {
		t.Fatal("backendReady() with a 100ms timeout succeeded before the backend was ready")
	}
	long := NewHub(livereload.NewServer(target.URL), nil, 0, nil)
	long.SetReadyTimeout(5 * time.Second)
	if err := long.backendReady(context.Background()); err != nil {
		t.Fatalf("backendReady() with a 5s timeout = %v, want ready", err)
	}
}

func TestBackendReady_GIVENNoReadyTimeout_WHENWaiting_THENDefaultsToFiveSeconds(t *testing.T) {
	if got := NewHub(nil, nil, 0, nil).readyTimeout(); got != DefaultReadyTimeout || DefaultReadyTimeout != 5*time.Second {
		t.Fatalf("readyTimeout() = %v, want the 5s default", got)
	}
}
