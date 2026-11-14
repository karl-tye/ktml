package hmr_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/dev/interp"
)

func newCaptureFixture(t *testing.T, capture hmr.CaptureOptions) *fixture {
	t.Helper()
	root := t.TempDir()
	for name, source := range map[string]string{"badge.ktml": badgeSource, "page.ktml": pageSource, "layout.ktml": layoutSource} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if capture.KeyPath == "" {
		capture.KeyPath = filepath.Join(t.TempDir(), "capture.key")
	}
	pkg := reflect.TypeOf(Badge{}).PkgPath()
	h, err := hmr.New(hmr.Options{Root: root, Token: token, Capture: capture, Logf: t.Logf, Interp: interp.Options{Packages: func(string) (string, error) { return pkg, nil }}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(dev.SetRenderer(h))
	return &fixture{root: root, hmr: h, handler: h.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = Page{Title: r.URL.Query().Get("title")}.Render(r.Context(), w)
	})), pkg: pkg}
}

func (f *fixture) page(t *testing.T, title string) {
	t.Helper()
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?title="+title, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", response.Code, response.Body)
	}
}

func (f *fixture) captureStatus(t *testing.T) hmr.CaptureStatus {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, hmr.Prefix+"capture", nil)
	request.Header.Set(hmr.TokenHeader, token)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	var status hmr.CaptureStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("capture status %d %q: %v", response.Code, response.Body, err)
	}
	return status
}

// waitFor polls until the capture worker wrote n stories.
func (f *fixture) waitFor(t *testing.T, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.hmr.CaptureStatus().Stories < n {
		if time.Now().After(deadline) {
			t.Fatalf("capture wrote %d stories, want %d", f.hmr.CaptureStatus().Stories, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCapture_GIVENALocalDataApp_WHENAPageRenders_THENWritesARedactedStory
// renders a page twice with values of one shape: one story is written into
// the capture directory, redacted, and the status counts it.
func TestCapture_GIVENALocalDataApp_WHENAPageRenders_THENWritesARedactedStory(t *testing.T) {
	f := newCaptureFixture(t, hmr.CaptureOptions{Dir: "captured", LocalData: true})
	if status := f.captureStatus(t); status.Status != hmr.CaptureActive || status.Raw {
		t.Fatalf("status = %+v", status)
	}
	f.page(t, "Mrs+Jane+Doe")
	f.waitFor(t, 1)
	f.page(t, "Mr+John+Smith")
	time.Sleep(100 * time.Millisecond)
	path := filepath.Join(f.root, "captured", "page.story.ktml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	written := string(data)
	if !strings.Contains(written, "story Page.Captured [") || strings.Count(written, "story Page.") != 1 {
		t.Errorf("captured Document:\n%s", written)
	}
	for _, raw := range []string{"Jane", "Doe", "John", "Smith"} {
		if strings.Contains(written, raw) {
			t.Errorf("the captured story holds the raw %q:\n%s", raw, written)
		}
	}
	if status := f.captureStatus(t); status.Stories != 1 {
		t.Errorf("status = %+v, want 1 story", status)
	}
}

// TestCapture_GIVENAnAppWithoutLocalData_WHENAPageRenders_THENRefusesAndWritesNothing
// refuses capture unless the application declared local data.
func TestCapture_GIVENAnAppWithoutLocalData_WHENAPageRenders_THENRefusesAndWritesNothing(t *testing.T) {
	f := newCaptureFixture(t, hmr.CaptureOptions{Dir: "captured"})
	status := f.captureStatus(t)
	if status.Status != hmr.CaptureRefused || !strings.Contains(status.Reason, "hmr.LocalData(true)") {
		t.Fatalf("status = %+v, want refused", status)
	}
	f.page(t, "Jane")
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(f.root, "captured")); !os.IsNotExist(err) {
		t.Errorf("a refused capture wrote %s", filepath.Join(f.root, "captured"))
	}
}

// TestCapture_GIVENADirectoryOutsideTheWorkspace_WHENInstalled_THENRefuses
// keeps captured props inside the project.
func TestCapture_GIVENADirectoryOutsideTheWorkspace_WHENInstalled_THENRefuses(t *testing.T) {
	f := newCaptureFixture(t, hmr.CaptureOptions{Dir: t.TempDir(), LocalData: true})
	if status := f.captureStatus(t); status.Status != hmr.CaptureRefused {
		t.Errorf("status = %+v, want refused", status)
	}
}

// TestCapture_GIVENNoCaptureRequest_WHENInstalled_THENIsOff reports off.
func TestCapture_GIVENNoCaptureRequest_WHENInstalled_THENIsOff(t *testing.T) {
	f := newCaptureFixture(t, hmr.CaptureOptions{LocalData: true})
	if status := f.captureStatus(t); status.Status != hmr.CaptureOff {
		t.Errorf("status = %+v, want off", status)
	}
}
