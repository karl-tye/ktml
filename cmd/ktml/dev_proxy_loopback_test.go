package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/picker"
)

func TestDevProxy_GIVENAPort_WHENListening_THENBindsLoopbackOnly(t *testing.T) {
	if got := devProxyAddr(7330); got != "127.0.0.1:7330" {
		t.Fatalf("devProxyAddr(7330) = %q, want 127.0.0.1:7330", got)
	}
}

func TestDevProxy_GIVENAForeignHost_WHENRequested_THENForbidden(t *testing.T) {
	handler := devProxyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for host, want := range map[string]int{
		"localhost:7330":      http.StatusTeapot,
		"127.0.0.1:7330":      http.StatusTeapot,
		"[::1]:7330":          http.StatusTeapot,
		"attacker.test:7330":  http.StatusForbidden,
		"attacker.test":       http.StatusForbidden,
		"192.168.1.20:7330":   http.StatusForbidden,
		"localhost.evil.test": http.StatusForbidden,
	} {
		request := httptest.NewRequest(http.MethodGet, "/__ktml/events", nil)
		request.Host = host
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Errorf("Host %q: status %d, want %d", host, recorder.Code, want)
		}
	}
}

func TestDevProxy_GIVENASlowClient_WHENServed_THENHeadersHaveATimeout(t *testing.T) {
	server := newDevProxyServer("127.0.0.1:0", http.NotFoundHandler())
	if server.ReadHeaderTimeout <= 0 || server.ReadHeaderTimeout > 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want a bound of at most 30s", server.ReadHeaderTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want none: the livereload stream is long-lived", server.WriteTimeout)
	}
}

func TestKTMLDev_GIVENTheProxyPortIsTaken_WHENStarted_THENExitsWithTheListenError(t *testing.T) {
	executable := buildKTML(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	dir := devExplorerProject(t, taken.Addr().(*net.TCPAddr).Port, "")
	command := exec.Command(executable, "dev")
	command.Dir = dir
	var output lockedBuffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(output.String(), "address already in use") {
			t.Fatalf("ktml dev error = %v, want the listen error\n%s", err, output.String())
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Signal(os.Interrupt)
		<-done
		t.Fatalf("ktml dev kept running without its proxy\n%s", output.String())
	}
}

// TestDevRoutes_GIVENThePickerRoutes_WHENThePickerIsOffOrOn_THENOnlyAnEnabledPickerAnswers
// serves /__ktml/picker/ only once ktml dev enables the picker; until then,
// and for every other path, the proxy answers.
func TestDevRoutes_GIVENThePickerRoutes_WHENThePickerIsOffOrOn_THENOnlyAnEnabledPickerAnswers(t *testing.T) {
	var enabled atomic.Pointer[picker.Handler]
	handler := devProxyHandler(devRoutes(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), &enabled))
	request := func(path string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7330"+path, nil))
		return recorder.Code
	}
	if code := request(picker.Prefix + "resolve"); code != http.StatusNotFound {
		t.Errorf("picker off: status %d, want 404", code)
	}
	enabled.Store(picker.New(nil, nil, "http://127.0.0.1:7332"))
	if code := request(picker.Prefix + "resolve"); code != http.StatusMethodNotAllowed {
		t.Errorf("picker on, GET: status %d, want the picker's 405", code)
	}
	if code := request("/jobs"); code != http.StatusTeapot {
		t.Errorf("app path: status %d", code)
	}
}
