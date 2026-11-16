package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
)

// TestCSP_GIVENAnAppThatAllowsOnlyItsOwnScripts_WHENServedByKtmlDev_THENTheInjectedClientStillRunsAndPatches
// serves the HMR app with script-src 'self' (and default-src 'self' for
// styles): ktml dev must allow its inline client with a per-response nonce,
// or the browser blocks live reload, patching, and the picker.
func TestCSP_GIVENAnAppThatAllowsOnlyItsOwnScripts_WHENServedByKtmlDev_THENTheInjectedClientStillRunsAndPatches(t *testing.T) {
	prepareBinaries(t)
	explorerPort := freePort(t)
	app, _ := newHMRAppWith(t, func(dir string, devCfg map[string]any) {
		devCfg["command"] = append(devCfg["command"].([]string), "-csp", "default-src 'self'; script-src 'self'")
		devCfg["explorer"] = map[string]any{"addr": fmt.Sprintf("127.0.0.1:%d", explorerPort)}
	})
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 20 * time.Second

	page := browser.MustPage(app.url + "/")
	waitLoad(t, page, timeout)
	until(t, page, "the injected client, the picker, and the app's own scripts", `() => typeof window.__ktmlSession === "string" && window.__ktmlSession !== "" && window.__ktmlPickerLoaded === true && !!window.htmx`, timeout)
	page.MustEval(`() => {
		window.__live = true;
		window.__violations = [];
		document.addEventListener("securitypolicyviolation", (event) => window.__violations.push(event.violatedDirective + " " + event.sample));
	}`)

	if err := page.KeyActions().Press(input.AltLeft, input.ShiftLeft).Type(input.KeyK).Do(); err != nil {
		t.Fatal(err)
	}
	until(t, page, "the picker overlay styled under the policy", `() => {
		const host = document.querySelector("[data-ktml-picker]");
		const label = host && host.shadowRoot.querySelector("[data-label]");
		return !!label && getComputedStyle(label).position === "fixed" && getComputedStyle(document.body).cursor === "crosshair";
	}`, timeout)
	if err := page.Keyboard.Type(input.Escape); err != nil {
		t.Fatal(err)
	}

	app.edit(t, "web/layout.ktml", `span class="badge" "{label}"`, `span class="badge csp" "{label}"`)

	until(t, page, "the patched badges", `() => document.querySelectorAll(".badge.csp").length > 0`, timeout)
	if got := evalString(t, page, `() => String(window.__live) + "|" + window.__violations.join(",")`); got != "true|" {
		t.Errorf("after the patch: live|violations = %s, want a patched page and no policy violations", got)
	}
}
