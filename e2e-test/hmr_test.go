package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

// hmrApp is a copy of testdata/hmrapp in its own module, served by `ktml dev`
// on ephemeral ports.
type hmrApp struct {
	dir string
	url string
}

// copyHMRApp copies testdata/hmrapp into a temporary module that requires
// this repository.
func copyHMRApp(t *testing.T) string {
	t.Helper()
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join("testdata", "hmrapp")
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/hmrapp\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\n\nreplace github.com/karl-tye/ktml => "+filepath.ToSlash(repository)+"\n")
	return dir
}

func newHMRApp(t *testing.T) *hmrApp {
	t.Helper()
	app, _ := newHMRAppWith(t, nil)
	return app
}

// newHMRAppWith starts ktml dev with args over a copy of the fixture that
// setup may change first, and returns ktml dev's output as it accumulates.
func newHMRAppWith(t *testing.T, setup func(dir string, devCfg map[string]any), args ...string) (*hmrApp, *lockedBuffer) {
	t.Helper()
	ktml, err := filepath.Abs("ktml")
	if err != nil {
		t.Fatal(err)
	}
	dir := copyHMRApp(t)
	devPort, backendPort := freePort(t), freePort(t)
	var cfg map[string]any
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, "ktml.config.json")), &cfg); err != nil {
		t.Fatalf("parse fixture config: %v", err)
	}
	devCfg := cfg["dev"].(map[string]any)
	devCfg["port"] = devPort
	devCfg["proxy_target"] = fmt.Sprintf("http://127.0.0.1:%d", backendPort)
	devCfg["command"] = []string{"{binary}", "-addr", fmt.Sprintf("127.0.0.1:%d", backendPort)}
	if setup != nil {
		setup(dir, devCfg)
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "ktml.config.json"), string(encoded))

	output := &lockedBuffer{}
	command := exec.Command(ktml, append([]string{"dev"}, args...)...)
	command.Dir = dir
	command.Stdout = io.MultiWriter(os.Stdout, output)
	command.Stderr = io.MultiWriter(os.Stderr, output)
	if err := command.Start(); err != nil {
		t.Fatalf("start ktml dev: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	t.Cleanup(func() { stopDevServer(t, command, exited) })

	app := &hmrApp{dir: dir, url: fmt.Sprintf("http://127.0.0.1:%d", devPort)}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		response, err := http.Get(app.url + "/")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return app, output
			}
		}
		select {
		case <-exited:
			t.Fatal("ktml dev exited before the app was ready")
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("the app never became ready")
	return nil, nil
}

// lockedBuffer collects a process's output for concurrent reads.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// edit replaces old with replacement in a fixture file, as an editor save.
func (app *hmrApp) edit(t *testing.T, file, old, replacement string) {
	t.Helper()
	path := filepath.Join(app.dir, filepath.FromSlash(file))
	content := string(readFile(t, path))
	if !strings.Contains(content, old) {
		t.Fatalf("%s does not contain %q", file, old)
	}
	writeFile(t, path, strings.Replace(content, old, replacement, 1))
}

// until polls a JavaScript predicate on the page.
func until(t *testing.T, page *rod.Page, what, predicate string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result, err := page.Eval(predicate)
		if err == nil && result.Value.Bool() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func evalString(t *testing.T, page *rod.Page, script string) string {
	t.Helper()
	return page.MustEval(script).String()
}

// TestHMR_GIVENADashboardApp_WHENComponentsChange_THENPatchesCompatibleChangesAndRebuildsIncompatibleOnes
// is the Phase 3 exit gate: compatible component changes patch a live
// dashboard page (a doctype layout, keyed cards, Alpine and HTMX islands, and
// HTMX fragment roots) without a browser reload, and incompatible schema and
// backend changes rebuild and reload.
func TestHMR_GIVENADashboardApp_WHENComponentsChange_THENPatchesCompatibleChangesAndRebuildsIncompatibleOnes(t *testing.T) {
	prepareBinaries(t)
	app := newHMRApp(t)
	browser := rod.New().MustConnect()
	t.Cleanup(browser.MustClose)
	page := browser.MustPage(app.url)
	page.MustWaitLoad()
	const patchTimeout = 20 * time.Second
	const rebuildTimeout = 2 * time.Minute

	until(t, page, "HTMX fragments and Alpine", `() => document.querySelectorAll("#activity-list li").length === 2 &&
		!!document.getElementById("notice-text") && !!window.Alpine &&
		document.getElementById("counter-label").textContent === "Count: 0"`, patchTimeout)
	// Browser state that patches must keep: a page flag a reload would lose,
	// tagged card nodes, Alpine state, a typed note, and the focused field.
	page.MustEval(`() => { window.__live = true; document.querySelectorAll("li.job").forEach(li => li.__tag = li.dataset.id); }`)
	for range 3 {
		page.MustElement("#counter-inc").MustClick()
	}
	page.MustElement("#note-j2").MustInput("fragile")
	page.MustElement("#customer").MustInput("Ada")
	page.MustEval(`() => { const field = document.getElementById("customer"); field.focus(); field.setSelectionRange(1, 2); }`)
	until(t, page, "counter at 3", `() => document.getElementById("counter-label").textContent === "Count: 3"`, patchTimeout)

	// live reports what a patch must preserve.
	live := func(t *testing.T) {
		t.Helper()
		got := evalString(t, page, `() => {
			const active = document.activeElement;
			return [String(window.__live), active && active.id, active && active.value, active && active.selectionStart + "-" + active.selectionEnd,
				document.getElementById("note-j2").value].join("|");
		}`)
		if want := "true|customer|Ada|1-2|fragile"; got != want {
			t.Fatalf("browser state after the patch = %s, want %s", got, want)
		}
	}
	cards := func() string {
		return evalString(t, page, `() => Array.from(document.querySelectorAll("#jobs > li.job")).map(li => li.dataset.id + (li.__tag === li.dataset.id ? "=" : "+")).join(",")`)
	}

	t.Run("patches every live instance and the parent roots of a changed child", func(t *testing.T) {
		app.edit(t, "web/layout.ktml", `span class="badge" "{label}"`, `span class="badge live" "{label}"`)
		until(t, page, "five live badges", `() => document.querySelectorAll(".badge.live").length === 5 && document.querySelectorAll(".badge").length === 5`, patchTimeout)
		live(t)
		if got := cards(); got != "j1=,j2=,j3=,j4=" {
			t.Errorf("cards = %s, want the same nodes", got)
		}
		if got := evalString(t, page, `() => document.getElementById("activity-list").children.length + "|" + document.getElementById("notice-text").textContent`); got != "2|Welcome" {
			t.Errorf("HTMX fragment roots after the page patch = %s", got)
		}
	})

	t.Run("reorders keyed cards by moving their nodes", func(t *testing.T) {
		app.edit(t, "web/board.ktml", "for job in board.jobs key job.id", "for job in board.reversed key job.id")
		until(t, page, "reversed cards", `() => Array.from(document.querySelectorAll("#jobs > li.job")).map(li => li.dataset.id).join() === "j4,j3,j2,j1"`, patchTimeout)
		live(t)
		if got := cards(); got != "j4=,j3=,j2=,j1=" {
			t.Errorf("cards = %s, want moved nodes", got)
		}
	})

	t.Run("removes a keyed card", func(t *testing.T) {
		app.edit(t, "web/board.ktml", "for job in board.reversed key job.id", "for job in board.open key job.id")
		until(t, page, "open cards", `() => Array.from(document.querySelectorAll("#jobs > li.job")).map(li => li.dataset.id).join() === "j1,j2,j4"`, patchTimeout)
		live(t)
		if got := cards(); got != "j1=,j2=,j4=" {
			t.Errorf("cards = %s", got)
		}
	})

	t.Run("inserts a keyed card", func(t *testing.T) {
		app.edit(t, "web/board.ktml", "for job in board.open key job.id", "for job in board.jobs key job.id")
		until(t, page, "all cards", `() => Array.from(document.querySelectorAll("#jobs > li.job")).map(li => li.dataset.id).join() === "j1,j2,j3,j4"`, patchTimeout)
		live(t)
		if got := cards(); got != "j1=,j2=,j3+,j4=" {
			t.Errorf("cards = %s, want only j3 new", got)
		}
	})

	t.Run("keeps Alpine state when its bound markup changes", func(t *testing.T) {
		app.edit(t, "web/board.ktml", `x-text="'Count: ' + count"`, `x-text="'Clicks: ' + count"`)
		until(t, page, "relabelled counter", `() => document.getElementById("counter-label").textContent === "Clicks: 3"`, patchTimeout)
		live(t)
		page.MustElement("#counter-inc").MustClick()
		until(t, page, "counter still reactive", `() => document.getElementById("counter-label").textContent === "Clicks: 4"`, patchTimeout)
		page.MustEval(`() => { const field = document.getElementById("customer"); field.focus(); field.setSelectionRange(1, 2); }`)
	})

	t.Run("disposes a removed Alpine scope and initializes an inserted one", func(t *testing.T) {
		if got := evalString(t, page, `() => window.tipInits + "|" + window.tipDestroyed`); got != "1|undefined" {
			t.Fatalf("tip lifecycle before = %s", got)
		}
		app.edit(t, "web/board.ktml", "\t\tTip\n", "")
		until(t, page, "tip removed", `() => !document.getElementById("tip") && window.tipDestroyed === 1`, patchTimeout)
		live(t)
		app.edit(t, "web/board.ktml", "\t\tCounter\n", "\t\tCounter\n\t\tTip\n")
		until(t, page, "tip inserted and initialized", `() => !!document.getElementById("tip") && window.tipInits === 2`, patchTimeout)
		live(t)
		if got := evalString(t, page, `() => window.tipInits + "|" + window.tipDestroyed + "|" + document.getElementById("counter-label").textContent`); got != "2|1|Clicks: 4" {
			t.Errorf("Alpine lifecycle = %s", got)
		}
	})

	t.Run("processes HTMX attributes inserted by a patch", func(t *testing.T) {
		app.edit(t, "web/board.ktml", `div id="extra" {}`, `div id="extra" {
			button id="load-extra" type="button" hx-get="/extra" hx-target="#extra" "Load"
		}`)
		until(t, page, "load button", `() => !!document.getElementById("load-extra")`, patchTimeout)
		live(t)
		page.MustElement("#load-extra").MustClick()
		until(t, page, "HTMX fragment", `() => (document.getElementById("extra-text") || {}).textContent === "Loaded by HTMX"`, patchTimeout)
		page.MustEval(`() => { const field = document.getElementById("customer"); field.focus(); field.setSelectionRange(1, 2); }`)
	})

	t.Run("patches an added defaulted input with its default", func(t *testing.T) {
		app.edit(t, "web/layout.ktml", "Badge [label: string] {\n\tspan class=\"badge live\" \"{label}\"", "Badge [label: string, tone: string = \"info\"] {\n\tspan class=\"badge live\" data-tone=\"{tone}\" \"{label}\"")
		until(t, page, "toned badges", `() => document.querySelectorAll('.badge[data-tone="info"]').length === 5`, patchTimeout)
		live(t)
		if got := evalString(t, page, `() => (document.getElementById("extra-text") || {}).textContent`); got != "Loaded by HTMX" {
			t.Errorf("HTMX-loaded fragment after a page patch = %q", got)
		}
	})

	t.Run("shows a diagnostic over the last valid page until it is fixed", func(t *testing.T) {
		app.edit(t, "web/board.ktml", `h2 id="board-title" "Open work"`, `h2 id="board-title" "{missing}"`)
		until(t, page, "error overlay", `() => !!document.getElementById("ktml-error-overlay") && document.getElementById("ktml-error-overlay").shadowRoot.textContent.includes("missing")`, patchTimeout)
		if got := evalString(t, page, `() => document.getElementById("board-title").textContent`); got != "Open work" {
			t.Errorf("page under the overlay = %q, want the last valid page", got)
		}
		app.edit(t, "web/board.ktml", `h2 id="board-title" "{missing}"`, `h2 id="board-title" "Open jobs"`)
		until(t, page, "fixed and patched", `() => !document.getElementById("ktml-error-overlay") && document.getElementById("board-title").textContent === "Open jobs"`, patchTimeout)
		live(t)
	})

	t.Run("rebuilds and reloads when a live root fails to rerender", func(t *testing.T) {
		app.edit(t, "web/activity.ktml", "key entry.id", "key entry.kind")
		until(t, page, "reload", `() => window.__live === undefined && !!document.getElementById("page-title")`, rebuildTimeout)
		page.MustWaitLoad()
		time.Sleep(time.Second)
		if got := evalString(t, page, `() => String(!!document.getElementById("activity-list"))`); got != "false" {
			t.Errorf("the duplicate-key fragment rendered after the rebuild")
		}
		app.edit(t, "web/activity.ktml", "key entry.kind", "key entry.id")
		time.Sleep(2 * time.Second)
		page.MustReload()
		page.MustWaitLoad()
		until(t, page, "fixed fragment", `() => document.querySelectorAll("#activity-list li").length === 2`, patchTimeout)
	})

	t.Run("rebuilds and reloads when a required input is added", func(t *testing.T) {
		page.MustEval(`() => { window.__live = true; }`)
		app.edit(t, "web/activity.ktml", "Notice [text: string] {\n\tp id=\"notice-text\" \"{text}\"", "Notice [text: string, level: string] {\n\tp id=\"notice-text\" \"[{level}] {text}\"")
		until(t, page, "reloaded notice", `() => window.__live === undefined && (document.getElementById("notice-text") || {}).textContent === "[] Welcome"`, rebuildTimeout)
	})

	t.Run("rebuilds and reloads on a Host Schema change", func(t *testing.T) {
		until(t, page, "page ready", `() => !!document.getElementById("board-title")`, patchTimeout)
		page.MustEval(`() => { window.__live = true; }`)
		app.edit(t, "web/model.ktmlschema", "\tstatus: string,\n]", "\tstatus: string,\n\tnote?: string,\n]")
		until(t, page, "reloaded page", `() => window.__live === undefined && document.querySelectorAll("#jobs > li.job").length === 4`, rebuildTimeout)
	})

	t.Run("restarts and reloads on a backend change", func(t *testing.T) {
		until(t, page, "page ready", `() => !!document.getElementById("board-title")`, patchTimeout)
		page.MustEval(`() => { window.__live = true; }`)
		app.edit(t, "internal/app/app.go", `Title: "Jobs"`, `Title: "Work"`)
		until(t, page, "reloaded title", `() => window.__live === undefined && document.getElementById("page-title").textContent === "Work"`, rebuildTimeout)
	})
}
