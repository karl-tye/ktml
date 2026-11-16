package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// TestAssets_GIVENAStylesheetInReloadWatch_WHENItChanges_THENPagesSwapItWithoutReloadingOrRestarting
// edits a dev.reload_watch stylesheet: the page restyles in place, keeping
// its state, and the backend is not rebuilt. Another asset reloads the page.
func TestAssets_GIVENAStylesheetInReloadWatch_WHENItChanges_THENPagesSwapItWithoutReloadingOrRestarting(t *testing.T) {
	prepareBinaries(t)
	app, output := newHMRAppWith(t, func(dir string, devCfg map[string]any) {
		devCfg["reload_watch"] = []string{"*.css", "*.svg"}
		devCfg["restart_watch"] = []string{"*.go", "*.css"}
		writeFile(t, filepath.Join(dir, "static", "site.css"), "body { color: rgb(255, 0, 0); }\n")
		writeFile(t, filepath.Join(dir, "static", "logo.svg"), "<svg xmlns=\"http://www.w3.org/2000/svg\"/>\n")
		layout := filepath.Join(dir, "web", "layout.ktml")
		source := string(readFile(t, layout))
		const htmx = `script src="/static/htmx.min.js" {}`
		if !strings.Contains(source, htmx) {
			t.Fatalf("layout does not load HTMX as %q", htmx)
		}
		writeFile(t, layout, strings.Replace(source, htmx, `link rel="stylesheet" href="/static/site.css"`+"\n\t\t\t"+htmx, 1))
	})
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 20 * time.Second

	page := browser.MustPage(app.url + "/")
	waitLoad(t, page, timeout)
	until(t, page, "the client and the stylesheet", `() => typeof window.__ktmlSession === "string" && getComputedStyle(document.body).color === "rgb(255, 0, 0)"`, timeout)
	page.MustEval(`() => { window.__live = true; }`)
	builds := strings.Count(output.String(), "hmrapp listening")
	if builds == 0 {
		t.Fatalf("ktml dev output does not show the backend start:\n%s", output.String())
	}

	app.edit(t, "static/site.css", "rgb(255, 0, 0)", "rgb(0, 128, 0)")

	until(t, page, "the swapped stylesheet", `() => getComputedStyle(document.body).color === "rgb(0, 128, 0)"`, timeout)
	if got := evalString(t, page, `() => String(window.__live) + "|" + document.querySelectorAll('link[rel="stylesheet"]').length`); got != "true|1" {
		t.Errorf("after the stylesheet change: live|stylesheets = %s, want the same page with one stylesheet", got)
	}
	if got := strings.Count(output.String(), "hmrapp listening"); got != builds {
		t.Errorf("the stylesheet change restarted the backend (%d starts, was %d)", got, builds)
	}

	app.edit(t, "static/logo.svg", "<svg ", "<svg data-v=\"2\" ")

	until(t, page, "a reload for another asset", `() => window.__live === undefined`, timeout)
}
