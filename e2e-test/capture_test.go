package e2e_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/compiler"
)

// declareLocalData makes the fixture app declare local data, as an app
// started with local fixtures would.
func declareLocalData(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "cmd", "server", "dev.go")
	source := string(readFile(t, path))
	declared := strings.Replace(source, "hmr.Install(handler)", "hmr.Install(handler, hmr.LocalData(true))", 1)
	if declared == source {
		t.Fatal("the fixture's dev.go no longer calls hmr.Install(handler)")
	}
	writeFile(t, path, declared)
}

func allowCapture(devCfg map[string]any) {
	devCfg["capture"] = map[string]any{"allow": true}
}

func waitForOutput(t *testing.T, output *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(output.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("ktml dev never printed %q:\n%s", want, output)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestCapture_GIVENALocalDataApp_WHENBrowsed_THENWritesRedactedStoriesThatTypeCheck
// runs ktml dev --capture-stories on the HMR fixture app, which declares
// local data: browsing the dashboard and an HTMX fragment writes one story
// per rendered root into the capture directory, redacted, and the stories
// type-check.
func TestCapture_GIVENALocalDataApp_WHENBrowsed_THENWritesRedactedStoriesThatTypeCheck(t *testing.T) {
	prepareBinaries(t)
	app, output := newHMRAppWith(t, func(dir string, devCfg map[string]any) {
		declareLocalData(t, dir)
		allowCapture(devCfg)
	}, "--capture-stories", "captured")
	waitForOutput(t, output, "KTML story capture: capturing into ")
	for _, path := range []string{"/", "/activity", "/notice", "/"} {
		response, err := http.Get(app.url + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	waitForOutput(t, output, "ktml capture: wrote Activity.Captured")
	waitForOutput(t, output, "ktml capture: wrote Dashboard.Captured")

	path := filepath.Join(app.dir, "captured", "web", "board.story.ktml")
	board := string(readFile(t, path))
	activity := string(readFile(t, filepath.Join(app.dir, "captured", "web", "activity.story.ktml")))
	t.Logf("captured:\n%s\n%s", board, activity)
	if strings.Count(board, "story Dashboard.") != 1 {
		t.Errorf("two renders of one shape wrote %d Dashboard stories", strings.Count(board, "story Dashboard."))
	}
	for _, raw := range []string{`"j1"`, `"j2"`, "Customer rang", "Engineer rang back", `"a1"`, "Boiler service", "Leaking tap", "Fuse box", "Radiator bleed", "Welcome"} {
		if strings.Contains(board+activity, raw) {
			t.Errorf("a captured story holds the raw value %s", raw)
		}
	}
	snapshot, err := compiler.NewWorkspace(app.dir).Index()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, filepath.Join(app.dir, "captured", "web", "activity.story.ktml")} {
		for _, diagnostic := range snapshot.Diagnostics(file) {
			t.Errorf("%s: [%s] %s", file, diagnostic.Code, diagnostic.Message)
		}
	}
	if !snapshot.Valid() {
		t.Errorf("the workspace has errors with the captured stories: %v", snapshot.Err())
	}
}

// TestCapture_GIVENAnAppThatDoesNotDeclareLocalData_WHENCapturing_THENTheAppRefuses
// runs the unchanged fixture, whose hmr.Install call declares nothing: the
// app refuses and no story is written.
func TestCapture_GIVENAnAppThatDoesNotDeclareLocalData_WHENCapturing_THENTheAppRefuses(t *testing.T) {
	prepareBinaries(t)
	app, output := newHMRAppWith(t, func(_ string, devCfg map[string]any) { allowCapture(devCfg) }, "--capture-stories", "captured")
	waitForOutput(t, output, "KTML story capture: refused by the application: the application did not declare local data")
	response, err := http.Get(app.url + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(app.dir, "captured")); !os.IsNotExist(err) {
		t.Errorf("a refused capture wrote the capture directory")
	}
}

// TestCapture_GIVENAProjectWithoutConsent_WHENCapturing_THENKtmlDevRefuses
// exits before starting anything when dev.capture.allow is not set.
func TestCapture_GIVENAProjectWithoutConsent_WHENCapturing_THENKtmlDevRefuses(t *testing.T) {
	prepareBinaries(t)
	dir := copyHMRApp(t)
	command := exec.Command(filepath.Join(mustAbs(t, "."), "ktml"), "dev", "--capture-stories", "captured")
	command.Dir = dir
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = command.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Minute):
		_ = command.Process.Kill()
		t.Fatal("ktml dev kept running without dev.capture.allow")
	}
	if err == nil || !strings.Contains(string(out), `"capture": {"allow": true}`) {
		t.Errorf("ktml dev: %v\n%s", err, out)
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}
