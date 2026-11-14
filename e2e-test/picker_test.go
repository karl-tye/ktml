package e2e_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/karl-tye/ktml/internal/compiler"
)

// counterStory is the fixture's one hand-written story.
const counterStory = "import { Counter } from \"./board.ktml\"\n\nstory Counter.Bare\n"

// TestPicker_GIVENTheHMRAppUnderKtmlDev_WHENAComponentIsPicked_THENItOpensInTheExplorer
// drives the component picker on a page ktml dev serves: the hotkey toggles
// pick mode, hovering labels the innermost instance with its component
// chain, Shift+wheel and Alt+Shift+Up/Down walk the chain one level per
// gesture without scrolling the page, and a click, which never reaches the
// app, opens the selected component in one reused Explorer tab, creating a
// type-checked story from its types when it has none.
func TestPicker_GIVENTheHMRAppUnderKtmlDev_WHENAComponentIsPicked_THENItOpensInTheExplorer(t *testing.T) {
	prepareBinaries(t)
	explorerPort := freePort(t)
	app, output := newHMRAppWith(t, func(dir string, devCfg map[string]any) {
		devCfg["explorer"] = map[string]any{"addr": fmt.Sprintf("127.0.0.1:%d", explorerPort)}
		writeFile(t, filepath.Join(dir, "web", "board.story.ktml"), counterStory)
	})
	explorerURL := fmt.Sprintf("http://127.0.0.1:%d", explorerPort)
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 15 * time.Second

	page := browser.MustPage(app.url + "/")
	waitLoad(t, page, timeout)
	page.MustSetViewport(1200, 800, 1, false)
	until(t, page, "the picker", `() => window.__ktmlPickerLoaded === true && !!document.querySelector("#jobs li.job span.badge")`, timeout)
	if !strings.Contains(output.String(), "press Alt+Shift+K") {
		t.Errorf("ktml dev does not announce the picker:\n%s", output.String())
	}
	// A tall page, so a Shift+wheel that scrolled would show.
	page.MustEval(`() => { document.body.style.minHeight = "6000px"; }`)

	hotkey := func() {
		t.Helper()
		if err := page.KeyActions().Press(input.AltLeft, input.ShiftLeft).Type(input.KeyK).Do(); err != nil {
			t.Fatal(err)
		}
	}
	center := func(selector string) (float64, float64) {
		t.Helper()
		box := page.MustEval(`(selector) => { const r = document.querySelector(selector).getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; }`, selector).Arr()
		return box[0].Num(), box[1].Num()
	}
	moveTo := func(selector string) {
		t.Helper()
		x, y := center(selector)
		page.Mouse.MustMoveTo(x-1, y)
		page.Mouse.MustMoveTo(x, y)
	}
	labelPath := `() => { const host = document.querySelector("[data-ktml-picker]"); return host ? host.shadowRoot.querySelector("[data-path]").textContent : ""; }`
	current := `() => { const host = document.querySelector("[data-ktml-picker]"); const b = host && host.shadowRoot.querySelector("[data-path] b"); return b ? b.textContent : ""; }`
	waitSelected := func(what, want string) {
		t.Helper()
		until(t, page, what+": "+want+" selected", `() => (`+current+`)() === `+fmt.Sprintf("%q", want), timeout)
	}
	shiftWheel := func(deltaY float64, steps int) {
		t.Helper()
		if err := page.Keyboard.Press(input.ShiftLeft); err != nil {
			t.Fatal(err)
		}
		if err := page.Mouse.Scroll(0, deltaY, steps); err != nil {
			t.Fatal(err)
		}
		if err := page.Keyboard.Release(input.ShiftLeft); err != nil {
			t.Fatal(err)
		}
		// The next wheel is a new gesture.
		time.Sleep(300 * time.Millisecond)
	}

	hotkey()
	until(t, page, "pick mode", `() => !!document.querySelector("[data-ktml-picker]")`, timeout)

	// The innermost instance under the pointer, with its component chain.
	moveTo("#jobs li.job span.badge")
	waitSelected("hover", "Badge")
	if got := evalString(t, page, labelPath); !strings.HasPrefix(got, "Dashboard › Layout › BoardView › JobCard › Badge") {
		t.Errorf("label = %q", got)
	}
	boxMatches := func(selector string) bool {
		return page.MustEval(`(selector) => {
			const host = document.querySelector("[data-ktml-picker]");
			const box = host.shadowRoot.querySelector("[data-box]").getBoundingClientRect();
			const r = document.querySelector(selector).getBoundingClientRect();
			return Math.abs(box.left - r.left) < 1 && Math.abs(box.top - r.top) < 1 && Math.abs(box.width - r.width) < 1 && Math.abs(box.height - r.height) < 1;
		}`, selector).Bool()
	}
	if !boxMatches("#jobs li.job span.badge") {
		t.Error("the highlight does not cover the hovered Badge")
	}

	// Shift+wheel walks the chain: up to the parents, down toward the
	// innermost, without scrolling the page.
	shiftWheel(-100, 1)
	waitSelected("wheel up", "JobCard")
	if !boxMatches("#jobs li.job") {
		t.Error("the highlight does not cover the JobCard")
	}
	shiftWheel(-100, 1)
	waitSelected("wheel up again", "BoardView")
	shiftWheel(100, 1)
	waitSelected("wheel down", "JobCard")
	// One trackpad-like gesture of many small events steps one level.
	shiftWheel(-240, 12)
	waitSelected("one gesture", "BoardView")
	shiftWheel(240, 12)
	waitSelected("one gesture down", "JobCard")
	if scroll := page.MustEval(`() => [window.scrollX, window.scrollY]`).Arr(); scroll[0].Num() != 0 || scroll[1].Num() != 0 {
		t.Errorf("Shift+wheel scrolled the page in pick mode to %v", scroll)
	}
	// The keyboard does the same.
	if err := page.KeyActions().Press(input.AltLeft, input.ShiftLeft).Type(input.ArrowUp).Do(); err != nil {
		t.Fatal(err)
	}
	waitSelected("Alt+Shift+Up", "BoardView")
	if err := page.KeyActions().Press(input.AltLeft, input.ShiftLeft).Type(input.ArrowDown).Do(); err != nil {
		t.Fatal(err)
	}
	waitSelected("Alt+Shift+Down", "JobCard")

	// Another instance resets the level; a frameless HTMX fragment root is
	// named by the application.
	moveTo("#tip")
	waitSelected("another instance", "Tip")
	moveTo("#notice-text")
	waitSelected("an HTMX fragment root", "Notice")
	if got := evalString(t, page, labelPath); !strings.HasPrefix(got, "Notice") {
		t.Errorf("fragment label = %q", got)
	}

	// A click on the selected parent opens it; JobCard has no story, so
	// ktml dev writes one from its types and the Explorer opens at it.
	moveTo("#jobs li.job span.badge")
	waitSelected("back on the Badge", "Badge")
	shiftWheel(-100, 1)
	waitSelected("parent", "JobCard")
	x, y := center("#jobs li.job span.badge")
	page.Mouse.MustMoveTo(x, y)
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	// explorerAt waits for a tab besides the app's to show the story.
	explorerAt := func(what, story string) *rod.Page {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			var tabs []string
			for _, open := range browser.MustPages() {
				if open.TargetID == page.TargetID {
					continue
				}
				info, err := open.Info()
				if err != nil {
					continue
				}
				tabs = append(tabs, info.URL)
				if strings.HasPrefix(info.URL, explorerURL+"/?story=") && strings.Contains(info.URL, story) {
					return open
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the other tabs are at %v, want the Explorer at %s; label %q", what, tabs, story, evalString(t, page, labelPath))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	explorer := explorerAt("JobCard", "web%2Fboard.story.ktml%23JobCard.")
	if name := explorer.MustEval(`() => window.name`).String(); name != "ktml-explorer" {
		t.Errorf("Explorer tab name = %q", name)
	}
	until(t, page, "pick mode ends after a pick", `() => !document.querySelector("[data-ktml-picker]")`, timeout)

	story := string(readFile(t, filepath.Join(app.dir, "web", "board.story.ktml")))
	if !strings.Contains(story, "// ktml:generated ") || !strings.Contains(story, "story JobCard.") || !strings.Contains(story, "story Counter.Bare") {
		t.Errorf("story Document:\n%s", story)
	}
	// Values come from the types, never from the page.
	for _, rendered := range []string{"Boiler service", "Leaking tap", "Fuse box", "Radiator bleed", `"j1"`} {
		if strings.Contains(story, rendered) {
			t.Errorf("the created story holds the page's value %q:\n%s", rendered, story)
		}
	}
	snapshot, err := compiler.NewWorkspace(app.dir).Index()
	if err != nil {
		t.Fatal(err)
	}
	storyPath, _ := filepath.Abs(filepath.Join(app.dir, "web", "board.story.ktml"))
	if diagnostics := snapshot.Diagnostics(storyPath); len(diagnostics) > 0 {
		t.Errorf("the created story has diagnostics: %+v", diagnostics)
	}

	// A click never reaches the app: the counter's Alpine @click does not
	// run. Counter has a story, so the same tab opens at it.
	hotkey()
	until(t, page, "pick mode again", `() => !!document.querySelector("[data-ktml-picker]")`, timeout)
	moveTo("#counter-inc")
	waitSelected("counter", "Counter")
	x, y = center("#counter-inc")
	page.Mouse.MustMoveTo(x, y)
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	explorerAt("Counter", "web%2Fboard.story.ktml%23Counter.Bare")
	if label := page.MustElement("#counter-label").MustText(); label != "Count: 0" {
		t.Errorf("the click reached the app: counter = %q", label)
	}
	pages := browser.MustPages()
	if len(pages) != 2 {
		t.Errorf("%d tabs are open, want the app and one reused Explorer tab", len(pages))
	}

	// Esc leaves pick mode, and the page works as before.
	hotkey()
	until(t, page, "pick mode for Esc", `() => !!document.querySelector("[data-ktml-picker]")`, timeout)
	page.Keyboard.MustType(input.Escape)
	until(t, page, "Esc", `() => !document.querySelector("[data-ktml-picker]")`, timeout)
	page.MustActivate()
	x, y = center("#counter-inc")
	page.Mouse.MustMoveTo(x, y)
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	until(t, page, "the counter after pick mode", `() => document.querySelector("#counter-label").textContent === "Count: 1"`, timeout)
}
