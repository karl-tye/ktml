package e2e_test

import (
	"fmt"
	"os"
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
	// The hovered element is selected; a click on it opens the innermost
	// component.
	moveTo("#counter-inc")
	waitSelected("counter", "button#counter-inc")
	if got := evalString(t, page, labelPath); !strings.Contains(got, "BoardView › Counter › button#counter-inc") {
		t.Errorf("counter label = %q", got)
	}
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

// TestPicker_GIVENAPageGoPassedIntoALayoutSlot_WHENElementsArePicked_THENShowsTheSlottedComponentAndEachElementsSource
// picks elements inside a page component that Go passes into a layout's
// content slot: the page has its own level and box in the chain, a hovered
// element inside a row is highlighted with its Element Path and .ktml
// source, the walk goes out element by element and then component by
// component, C copies the source, Ctrl+click opens it in dev.picker.editor,
// and a click opens the innermost component's story.
func TestPicker_GIVENAPageGoPassedIntoALayoutSlot_WHENElementsArePicked_THENShowsTheSlottedComponentAndEachElementsSource(t *testing.T) {
	prepareBinaries(t)
	explorerPort := freePort(t)
	var opened string
	app, _ := newHMRAppWith(t, func(dir string, devCfg map[string]any) {
		devCfg["explorer"] = map[string]any{"addr": fmt.Sprintf("127.0.0.1:%d", explorerPort)}
		opened = filepath.Join(dir, "opened.txt")
		devCfg["picker"] = map[string]any{"editor": []string{"sh", "-c", `printf '%s\n' "$1" >> "$2"`, "editor", "{file}:{line}:{column}", opened}}
	})
	explorerURL := fmt.Sprintf("http://127.0.0.1:%d", explorerPort)
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 15 * time.Second

	page := browser.MustPage(app.url + "/planner")
	waitLoad(t, page, timeout)
	page.MustSetViewport(1200, 800, 1, false)
	_ = proto.BrowserGrantPermissions{Origin: app.url, Permissions: []proto.BrowserPermissionType{proto.BrowserPermissionTypeClipboardReadWrite, proto.BrowserPermissionTypeClipboardSanitizedWrite}}.Call(browser)
	const status = `#planner .planner-row[data-id="j1"] span.status`
	until(t, page, "the picker", `() => window.__ktmlPickerLoaded === true && !!document.querySelector('`+status+`')`, timeout)

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
	shadow := func(part string) string {
		return `() => { const host = document.querySelector("[data-ktml-picker]"); const node = host && host.shadowRoot.querySelector("` + part + `"); return node ? node.textContent : ""; }`
	}
	labelPath, source := shadow("[data-path]"), shadow("[data-source]")
	current := shadow("[data-path] b")
	waitSelected := func(what, want string) {
		t.Helper()
		until(t, page, what+": "+want+" selected", `() => (`+current+`)() === `+fmt.Sprintf("%q", want), timeout)
	}
	waitSource := func(what, want string) {
		t.Helper()
		until(t, page, what+": source "+want, `() => (`+source+`)().startsWith(`+fmt.Sprintf("%q", want)+`)`, timeout)
	}
	// box is the highlight's rectangle; rect a selector's, or the union of
	// several.
	box := func() []float64 {
		values := page.MustEval(`() => { const r = document.querySelector("[data-ktml-picker]").shadowRoot.querySelector("[data-box]").getBoundingClientRect(); return [r.left, r.top, r.right, r.bottom]; }`).Arr()
		return []float64{values[0].Num(), values[1].Num(), values[2].Num(), values[3].Num()}
	}
	rect := func(selectors ...string) []float64 {
		values := page.MustEval(`(selectors) => {
			const rects = selectors.map((s) => document.querySelector(s).getBoundingClientRect());
			return [Math.min(...rects.map((r) => r.left)), Math.min(...rects.map((r) => r.top)), Math.max(...rects.map((r) => r.right)), Math.max(...rects.map((r) => r.bottom))];
		}`, selectors).Arr()
		return []float64{values[0].Num(), values[1].Num(), values[2].Num(), values[3].Num()}
	}
	boxCovers := func(what string, selectors ...string) {
		t.Helper()
		got, want := box(), rect(selectors...)
		for index := range got {
			if diff := got[index] - want[index]; diff > 1 || diff < -1 {
				t.Errorf("%s: the highlight is %v, want %v", what, got, want)
				return
			}
		}
	}
	keys := func(keys ...input.Key) {
		t.Helper()
		if err := page.KeyActions().Press(keys[:len(keys)-1]...).Type(keys[len(keys)-1]).Do(); err != nil {
			t.Fatal(err)
		}
	}

	keys(input.AltLeft, input.ShiftLeft, input.KeyK)
	until(t, page, "pick mode", `() => !!document.querySelector("[data-ktml-picker]")`, timeout)

	// A plain-markup badge in a row: the slotted page has its own level.
	moveTo(status)
	waitSelected("hover", "span.status.state-open")
	if got := evalString(t, page, labelPath); !strings.HasPrefix(got, "Layout › Planner › section#planner.planner › div.planner-row › span.status.state-open") {
		t.Errorf("label = %q", got)
	}
	waitSource("hover", "web/planner.ktml:10:5  in Planner")
	boxCovers("the status badge", status)

	// Out element by element, then component by component, and back.
	keys(input.AltLeft, input.ShiftLeft, input.ArrowUp)
	waitSelected("up", "div.planner-row")
	waitSource("up", "web/planner.ktml:8:4")
	boxCovers("the row", `#planner .planner-row[data-id="j1"]`)
	keys(input.AltLeft, input.ShiftLeft, input.ArrowUp)
	waitSelected("up again", "section#planner.planner")
	keys(input.AltLeft, input.ShiftLeft, input.ArrowUp)
	waitSelected("up to the page", "Planner")
	waitSource("the page's declaration", "web/planner.ktml:4:1")
	// The slotted page's box is its whole content range.
	boxCovers("the slotted page", "#planner-title", "#planner")
	keys(input.AltLeft, input.ShiftLeft, input.ArrowUp)
	waitSelected("up to the layout", "Layout")
	keys(input.AltLeft, input.ShiftLeft, input.ArrowUp)
	waitSelected("the outermost stays", "Layout")
	for range 3 {
		keys(input.AltLeft, input.ShiftLeft, input.ArrowDown)
	}
	waitSelected("down", "div.planner-row")
	if err := page.Keyboard.Press(input.ShiftLeft); err != nil {
		t.Fatal(err)
	}
	if err := page.Mouse.Scroll(0, 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := page.Keyboard.Release(input.ShiftLeft); err != nil {
		t.Fatal(err)
	}
	waitSelected("Shift+wheel down", "span.status.state-open")

	// A component instance inside the slotted page keeps the page's level.
	moveTo(`#planner .planner-row[data-id="j1"] span.badge`)
	waitSelected("a Badge in a row", "Badge")
	if got := evalString(t, page, labelPath); !strings.HasPrefix(got, "Layout › Planner › Badge") {
		t.Errorf("badge label = %q", got)
	}
	// The layout's own markup, outside the slot.
	moveTo("#page-title")
	waitSelected("the layout title", "h1#page-title")
	if got := evalString(t, page, labelPath); !strings.HasPrefix(got, "Layout › html › body › header › h1#page-title") {
		t.Errorf("layout label = %q", got)
	}
	waitSource("the layout title", "web/layout.ktml:11:5  in Layout")

	// C copies the selection's source.
	moveTo(status)
	waitSource("back on the badge", "web/planner.ktml:10:5")
	page.Keyboard.MustType(input.KeyC)
	until(t, page, "copied", `() => (`+labelPath+`)().includes("web/planner.ktml:10:5")`, timeout)
	if copied, err := page.Eval(`() => navigator.clipboard.readText()`); err == nil && copied.Value.String() != "web/planner.ktml:10:5" {
		t.Errorf("clipboard = %q", copied.Value.String())
	}

	// Ctrl+click opens the source in the configured editor; pick mode stays.
	x, y := center(status)
	page.Mouse.MustMoveTo(x, y)
	if err := page.Keyboard.Press(input.ControlLeft); err != nil {
		t.Fatal(err)
	}
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	if err := page.Keyboard.Release(input.ControlLeft); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(app.dir, "web", "planner.ktml") + ":10:5"
	deadline := time.Now().Add(timeout)
	for {
		content, _ := os.ReadFile(opened)
		if strings.TrimSpace(string(content)) != "" {
			if got := strings.TrimSpace(string(content)); !strings.HasSuffix(got, string(filepath.Separator)+filepath.Join("web", "planner.ktml")+":10:5") {
				t.Errorf("the editor opened %q, want %q", got, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the editor never ran; label %q", evalString(t, page, labelPath))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !page.MustEval(`() => !!document.querySelector("[data-ktml-picker]")`).Bool() {
		t.Error("Ctrl+click left pick mode")
	}

	// A click opens the innermost component, the slotted page, creating its
	// story from its types.
	page.Mouse.MustMoveTo(x+1, y)
	page.Mouse.MustMoveTo(x, y)
	waitSelected("before the click", "span.status.state-open")
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	deadline = time.Now().Add(timeout)
	for {
		found := false
		for _, other := range browser.MustPages() {
			if other.TargetID == page.TargetID {
				continue
			}
			if info, err := other.Info(); err == nil && strings.HasPrefix(info.URL, explorerURL+"/?story=") && strings.Contains(info.URL, "Planner.") {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Explorer never opened Planner; label %q", evalString(t, page, labelPath))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPicker_GIVENEDITORAndNoConfiguredEditor_WHENCtrlClicked_THENOpensTheSourceInTheEDITOR
// resolves Ctrl+click's editor from ktml dev's own $EDITOR when
// dev.picker.editor is unset: the hint names it, Ctrl+click runs it with its
// goto syntax and without its blocking flag. The editor is a fake on PATH.
func TestPicker_GIVENEDITORAndNoConfiguredEditor_WHENCtrlClicked_THENOpensTheSourceInTheEDITOR(t *testing.T) {
	prepareBinaries(t)
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "args")
	writeFile(t, filepath.Join(bin, "code"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+record+".tmp' && mv '"+record+".tmp' '"+record+"'\n")
	if err := os.Chmod(filepath.Join(bin, "code"), 0o755); err != nil {
		t.Fatal(err)
	}
	explorerPort := freePort(t)
	app, output := newHMRAppEnv(t, []string{"EDITOR=code --wait", "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}, func(dir string, devCfg map[string]any) {
		devCfg["explorer"] = map[string]any{"addr": fmt.Sprintf("127.0.0.1:%d", explorerPort)}
	})
	if !strings.Contains(output.String(), "Ctrl+click it to open its source in code ($EDITOR)") {
		t.Errorf("ktml dev did not name the editor:\n%s", output.String())
	}
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 15 * time.Second
	page := browser.MustPage(app.url + "/planner")
	waitLoad(t, page, timeout)
	page.MustSetViewport(1200, 800, 1, false)
	_ = proto.BrowserGrantPermissions{Origin: app.url, Permissions: []proto.BrowserPermissionType{proto.BrowserPermissionTypeClipboardReadWrite, proto.BrowserPermissionTypeClipboardSanitizedWrite}}.Call(browser)
	const status = `#planner .planner-row[data-id="j1"] span.status`
	until(t, page, "the picker", `() => window.__ktmlPickerLoaded === true && !!document.querySelector('`+status+`')`, timeout)
	shadow := func(part string) string {
		return `() => { const host = document.querySelector("[data-ktml-picker]"); const node = host && host.shadowRoot.querySelector("` + part + `"); return node ? node.textContent : ""; }`
	}
	if err := page.KeyActions().Press(input.AltLeft, input.ShiftLeft).Type(input.KeyK).Do(); err != nil {
		t.Fatal(err)
	}
	until(t, page, "pick mode", `() => !!document.querySelector("[data-ktml-picker]")`, timeout)
	if hint := evalString(t, page, shadow("[data-hint]")); !strings.Contains(hint, "Ctrl+click to open the source in code") {
		t.Errorf("hint = %q", hint)
	}
	box := page.MustEval(`(selector) => { const r = document.querySelector(selector).getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; }`, status).Arr()
	x, y := box[0].Num(), box[1].Num()
	page.Mouse.MustMoveTo(x-1, y)
	page.Mouse.MustMoveTo(x, y)
	until(t, page, "the badge's source", `() => (`+shadow("[data-source]")+`)().startsWith("web/planner.ktml:10:5")`, timeout)
	if err := page.Keyboard.Press(input.ControlLeft); err != nil {
		t.Fatal(err)
	}
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	if err := page.Keyboard.Release(input.ControlLeft); err != nil {
		t.Fatal(err)
	}
	want := []string{"-g", filepath.Join(app.dir, "web", "planner.ktml") + ":10:5"}
	deadline := time.Now().Add(timeout)
	for {
		if content, err := os.ReadFile(record); err == nil {
			got := strings.Split(strings.TrimSpace(string(content)), "\n")
			if len(got) != 2 || got[0] != want[0] || !strings.HasSuffix(got[1], string(filepath.Separator)+filepath.Join("web", "planner.ktml")+":10:5") {
				t.Errorf("code ran with %q, want %q", got, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("code never ran; label %q", evalString(t, page, shadow("[data-path]")))
		}
		time.Sleep(50 * time.Millisecond)
	}
	until(t, page, "the opened label", `() => (`+shadow("[data-path]")+`)().includes("opened web/planner.ktml:10:5 in code")`, timeout)
}
