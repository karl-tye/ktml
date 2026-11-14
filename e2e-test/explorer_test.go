package e2e_test

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
)

// explorerApp is a copy of testdata/explorerapp, a bookshop's helpdesk call
// audits, reader reviews, and ui components with their stories, served by
// `ktml explore` on an ephemeral loopback port.
type explorerApp struct {
	dir    string
	url    string
	stderr *lockedBuffer
}

// newExplorerApp starts the Explorer on a copy of the fixture, after each
// prepare has changed the copy.
func newExplorerApp(t *testing.T, prepare ...func(dir string)) *explorerApp {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join("testdata", "explorerapp")
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
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
	for _, change := range prepare {
		change(dir)
	}
	return startExplorer(t, dir)
}

// startExplorer serves dir with `ktml explore` on an ephemeral loopback
// port.
func startExplorer(t *testing.T, dir string) *explorerApp {
	t.Helper()
	ktml, err := filepath.Abs("ktml")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(ktml, "explore", "-addr", "127.0.0.1:0", dir)
	stderr := &lockedBuffer{}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start ktml explore: %v", err)
	}
	exited := make(chan struct{})
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			default:
			}
		}
		_ = command.Wait()
		close(exited)
	}()
	t.Cleanup(func() { stopDevServer(t, command, exited) })
	select {
	case line := <-lines:
		address := strings.TrimSuffix(strings.TrimPrefix(line, "KTML Explorer on "), "/")
		if !strings.HasPrefix(address, "http://127.0.0.1:") {
			t.Fatalf("ktml explore printed %q", line)
		}
		return &explorerApp{dir: dir, url: address, stderr: stderr}
	case <-exited:
		t.Fatal("ktml explore exited before it was ready")
	case <-time.After(time.Minute):
		t.Fatal("ktml explore never became ready")
	}
	return nil
}

func (app *explorerApp) edit(t *testing.T, file, old, replacement string) {
	t.Helper()
	path := filepath.Join(app.dir, filepath.FromSlash(file))
	content := string(readFile(t, path))
	if !strings.Contains(content, old) {
		t.Fatalf("%s does not contain %q", file, old)
	}
	writeFile(t, path, strings.Replace(content, old, replacement, 1))
}

// waitLoad waits for a page's load event. A page that does not load fails
// the test with what it was waiting on, instead of panicking, which would
// end the run before TestMain removes the fixture's generated files.
func waitLoad(t *testing.T, page *rod.Page, timeout time.Duration) {
	t.Helper()
	err := page.Timeout(timeout).WaitLoad()
	if err == nil {
		return
	}
	state := "unavailable"
	if result, evalErr := page.Timeout(2 * time.Second).Eval(`() => JSON.stringify({
		url: location.href,
		ready: document.readyState,
		frames: [...document.querySelectorAll("iframe")].map(f => {
			try { return f.getAttribute("src") + " " + f.contentDocument.readyState; } catch (e) { return f.getAttribute("src") + " ?"; }
		}),
		loaded: performance.getEntriesByType("resource").map(r => r.name.replace(location.origin, "") + " " + Math.round(r.duration) + "ms"),
	})`); evalErr == nil {
		state = result.Value.Str()
	}
	t.Fatalf("the page did not load: %v\n%s", err, state)
}

func (app *explorerApp) story(key string) string {
	return app.url + "/?story=" + url.QueryEscape(key)
}

// TestExplorer_GIVENComponentStories_WHENDevelopedInTheExplorer_THENControlsRerenderAndEditsHotApply
// is the Phase 5 exit gate: representative components (a Presentation
// State pipeline, an evidence browser with collections and optionals, and ui
// primitives) are developed on their own with typed controls, and story and
// component edits reach the open preview through HMR without a reload.
func TestExplorer_GIVENComponentStories_WHENDevelopedInTheExplorer_THENControlsRerenderAndEditsHotApply(t *testing.T) {
	prepareBinaries(t)
	app := newExplorerApp(t)
	browser := rod.New().MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 20 * time.Second

	page := browser.MustPage(app.story("internal/helpdesk/pipeline.story.ktml#Pipeline.Running"))
	waitLoad(t, page, timeout)
	frame := func() *rod.Page {
		return page.Timeout(timeout).MustElement("iframe[data-preview]").Context(page.GetContext()).MustFrame()
	}
	inFrame := func(t *testing.T, what, predicate string) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			result, err := frame().Eval(predicate)
			if err == nil && result.Value.Bool() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting in the preview for %s", what)
	}

	t.Run("lists the components and stories", func(t *testing.T) {
		nav := page.MustEval(`() => document.querySelector("[data-navigation]").textContent`).String()
		for _, want := range []string{"Pipeline", "Running", "Failed", "InteractiveReview", "WithEvidence", "ReviewsGrid", "Several", "Badge", "WithIcon", "Banner"} {
			if !strings.Contains(nav, want) {
				t.Errorf("navigation lacks %s:\n%s", want, nav)
			}
		}
		inFrame(t, "the running pipeline with Alpine, HTMX, and the project stylesheet", `() =>
			document.querySelector("#analysis-pipeline[hx-get='/call-audits/call-42/pipeline']") !== null &&
			!!window.Alpine && !!window.htmx && window.__ktmlClient === true &&
			document.body.classList.contains("paper") &&
			[...document.styleSheets].some(s => s.href && s.href.endsWith("/static/css/app.css"))`)
	})

	t.Run("a Variant control re-renders the preview with its typed payload", func(t *testing.T) {
		page.Timeout(timeout).MustElement(`select[data-path='["progress"]']`).MustSelect("Failed")
		inFrame(t, "the failed clause", `() => document.body.textContent.includes("Analysis failed.")`)
		input := page.Timeout(timeout).MustElement(`input[data-path='["progress","message"]']`)
		input.MustSelectAllText().MustInput("Disk full")
		inFrame(t, "the edited payload", `() => document.body.textContent.includes("Disk full")`)
		page.Timeout(timeout).MustElement(`select[data-path='["stages","scoring"]']`).MustSelect("Done")
		inFrame(t, "the third stage done", `() => document.querySelectorAll(".fa-check").length === 3`)
		if got := page.MustEval(`() => document.querySelector("[data-path='[\"progress\",\"message\"]']").type`).String(); got != "text" {
			t.Errorf("message control type = %s", got)
		}
	})

	t.Run("a component edit hot-applies to the open preview and keeps its controls", func(t *testing.T) {
		frame().MustEval(`() => { window.__live = "component"; }`)
		app.edit(t, "internal/helpdesk/components.ktml", `strong "Analysis failed."`, `strong "Analysis halted."`)
		inFrame(t, "the patched clause", `() => document.body.textContent.includes("Analysis halted.") && document.body.textContent.includes("Disk full")`)
		if got := frame().MustEval(`() => window.__live`).String(); got != "component" {
			t.Fatalf("the preview reloaded instead of patching (flag %q)", got)
		}
	})

	t.Run("a story edit hot-applies to inputs the controls did not change", func(t *testing.T) {
		frame().MustEval(`() => { window.__live = "story"; }`)
		app.edit(t, "internal/helpdesk/pipeline.story.ktml", `recording = Done, transcription = Done, scoring = Active`, `recording = Done, transcription = Active, scoring = Active`)
		// stages was changed by a control, so the story edit must not move it;
		// a story edit to an untouched story is checked below.
		time.Sleep(500 * time.Millisecond)
		if got := frame().MustEval(`() => document.querySelectorAll(".fa-check").length`).Int(); got != 3 {
			t.Errorf("a story edit overrode a control: %d stages done", got)
		}
		if got := frame().MustEval(`() => window.__live`).String(); got != "story" {
			t.Fatalf("the preview reloaded instead of patching (flag %q)", got)
		}

		page.Timeout(timeout).MustNavigate(app.story("internal/ui/badge.story.ktml#Badge.Plain"))
		waitLoad(t, page, timeout)
		inFrame(t, "the plain badge", `() => document.body.textContent.includes("Approved")`)
		frame().MustEval(`() => { window.__live = "badge"; }`)
		app.edit(t, "internal/ui/badge.story.ktml", `text = "Approved"]`, `text = "Approved by QA"]`)
		inFrame(t, "the edited story value", `() => document.body.textContent.includes("Approved by QA")`)
		if got := frame().MustEval(`() => window.__live`).String(); got != "badge" {
			t.Fatalf("the preview reloaded instead of patching (flag %q)", got)
		}
	})

	t.Run("collections, optionals, and fixtures have typed controls", func(t *testing.T) {
		page.Timeout(timeout).MustNavigate(app.story("internal/helpdesk/interactive.story.ktml#InteractiveReview.WithEvidence"))
		waitLoad(t, page, timeout)
		inFrame(t, "two criteria", `() => document.querySelectorAll("h3").length === 2`)
		// The second criterion has no evidence, so its button is disabled.
		inFrame(t, "the disabled criterion", `() => document.querySelectorAll("button[disabled]").length === 1`)
		some := page.Timeout(timeout).MustElement(`input[data-op="some"][data-path='["view","results",1,"criterionResultView","evidence"]']`)
		some.MustClick()
		inFrame(t, "evidence for both criteria", `() => document.querySelectorAll("button[disabled]").length === 0`)
		if page.MustHas(`input[data-kind="string"][data-path*='"criterion"']`) {
			t.Error("a JSONValue has a free-text control")
		}
		fixtures := page.MustEval(`() => [...document.querySelectorAll("select[data-kind=fixture][data-path='[\"view\",\"results\",0,\"criterionResultView\",\"criterion\"]'] option")].map(o => o.value).join(",")`).String()
		if fixtures != "greeting,reservation,zero,twelve,forty" {
			t.Errorf("JSONValue fixture choices = %s", fixtures)
		}
		page.Timeout(timeout).MustElement(`button[data-op="remove"][data-path='["view","results",0]']`).MustClick()
		inFrame(t, "one criterion", `() => document.querySelectorAll("h3").length === 1`)
		page.Timeout(timeout).MustElement(`button[data-op="add"][data-path='["view","turns"]']`).MustClick()
		page.Timeout(timeout).MustWaitStable()
		inFrame(t, "two transcript turns", `() => document.querySelectorAll("[data-transcript-turn]").length === 2`)
	})

	t.Run("syntax and type diagnostics appear inline with their source ranges", func(t *testing.T) {
		page.Timeout(timeout).MustNavigate(app.story("internal/helpdesk/pipeline.story.ktml#Pipeline.Failed"))
		waitLoad(t, page, timeout)
		inFrame(t, "the failed story", `() => document.body.textContent.includes("Scoring timed out")`)
		app.edit(t, "internal/helpdesk/pipeline.story.ktml", `progress = NotRequested,`, `progress = Paused,`)
		until(t, page, "the type diagnostic", `() => {
			const d = document.querySelector("[data-diagnostics] [data-code='unknown-variant']");
			return !!d && d.textContent.includes("internal/helpdesk/pipeline.story.ktml:") && d.querySelector("mark").textContent === "Paused";
		}`, timeout)
		inFrame(t, "the overlay in the preview", `() => {
			const overlay = document.getElementById("ktml-error-overlay");
			return !!overlay && overlay.shadowRoot.textContent.includes("unknown-variant") && overlay.shadowRoot.textContent.includes("pipeline.story.ktml:");
		}`)

		app.edit(t, "internal/helpdesk/pipeline.story.ktml", `progress = Paused,`, `progress = NotRequested`)
		until(t, page, "the syntax diagnostic", `() => !!document.querySelector("[data-diagnostics] [data-code='syntax']")`, timeout)
		app.edit(t, "internal/helpdesk/pipeline.story.ktml", `progress = NotRequested`, `progress = NotRequested,`)
		until(t, page, "diagnostics cleared", `() => !!document.querySelector("[data-diagnostics] [data-diagnostic-count='0']")`, timeout)
		inFrame(t, "the overlay cleared", `() => !document.getElementById("ktml-error-overlay")`)
	})
}

// TestExplorer_GIVENANonLoopbackAddress_WHENStarted_THENRefusesToServe proves
// the Explorer binds only to loopback.
func TestExplorer_GIVENANonLoopbackAddress_WHENStarted_THENRefusesToServe(t *testing.T) {
	prepareBinaries(t)
	for _, address := range []string{"0.0.0.0:0", ":0", "[::]:0"} {
		command := exec.Command("./ktml", "explore", "-addr", address, filepath.Join("testdata", "explorerapp"))
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "only binds to a loopback address") {
			t.Errorf("ktml explore -addr %s: err %v, output:\n%s", address, err, output)
		}
	}
	app := newExplorerApp(t)
	request, _ := http.NewRequest(http.MethodGet, app.url+"/", nil)
	request.Host = "attacker.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("a foreign Host got status %d, want 403", response.StatusCode)
	}
}

// TestExplorer_GIVENManyStories_WHENBrowsed_THENTheTreeGroupsRemembersAndSearchesThem
// browses the grouped story tree: Pages, Components, and Other by the
// generated headers (the fixture is not a Go module, so reachability is
// unavailable), collapsed except the deep-linked story's path, groups the
// viewer opens remembered across loads, and a search that filters, marks,
// opens, and clears.
func TestExplorer_GIVENManyStories_WHENBrowsed_THENTheTreeGroupsRemembersAndSearchesThem(t *testing.T) {
	prepareBinaries(t)
	app := newExplorerApp(t, func(dir string) {
		header := func(file, story, comment string) {
			path := filepath.Join(dir, filepath.FromSlash(file))
			content := strings.Replace(string(readFile(t, path)), "story "+story+" [", comment+"\nstory "+story+" [", 1)
			writeFile(t, path, content)
		}
		header("internal/helpdesk/pipeline.story.ktml", "Pipeline.Running", "// ktml:generated 000000000000\n// Pipeline is a page, reached by:\n//   GET /call-audits/{id}/pipeline → helpdesk.Pipeline")
		header("internal/ui/badge.story.ktml", "Badge.Plain", "// ktml:generated 000000000000\n// Badge is a component rendered through KTML calls, reached by:\n//   GET /reviews → reviews.List")
	})
	browser := rod.New().MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 10 * time.Second

	page := browser.MustPage(app.story("internal/ui/badge.story.ktml#Badge.WithIcon"))
	waitLoad(t, page, timeout)
	// tree is each group as id(count), + when open, ! when hidden.
	tree := func() string {
		return page.MustEval(`() => [...document.querySelectorAll("details[data-group]")].map(d =>
			d.dataset.group + (d.dataset.count ? "(" + d.dataset.count + ")" : "") + (d.open ? "+" : "") + (d.hidden ? "!" : "")).join(" ")`).String()
	}
	expect := func(t *testing.T, what, want string) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		got := tree()
		for got != want && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			got = tree()
		}
		if got != want {
			t.Fatalf("%s:\n got %s\nwant %s", what, got, want)
		}
	}
	visibleStories := func() string {
		return page.MustEval(`() => [...document.querySelectorAll("[data-story-item]")].filter(li => !li.closest("[hidden]")).map(li => li.textContent).join(",")`).String()
	}
	searchBox := func() *rod.Element { return page.Timeout(timeout).MustElement("[data-search]") }
	click := func(group string) {
		page.Timeout(timeout).MustElement(`details[data-group="` + group + `"] > summary`).MustClick()
	}

	t.Run("a deep link opens only the selected story's path", func(t *testing.T) {
		expect(t, "the tree", "Pages(4) Pages:helpdesk(4) Pages:helpdesk:Pipeline "+
			"Components(2)+ Components:ui(2)+ Components:ui:Badge+ "+
			"Other(5) Other:helpdesk(2) Other:helpdesk:InteractiveReview Other:reviews(2) Other:reviews:ReviewsGrid Other:ui(1) Other:ui:Banner")
		if got := page.Timeout(timeout).MustElement("a[aria-current]").MustText(); got != "WithIcon" {
			t.Errorf("the current story is %q", got)
		}
	})

	t.Run("groups expand and collapse and are remembered across loads", func(t *testing.T) {
		click("Other")
		click("Other:reviews")
		click("Components:ui:Badge")
		expect(t, "after toggling", "Pages(4) Pages:helpdesk(4) Pages:helpdesk:Pipeline "+
			"Components(2)+ Components:ui(2)+ Components:ui:Badge "+
			"Other(5)+ Other:helpdesk(2) Other:helpdesk:InteractiveReview Other:reviews(2)+ Other:reviews:ReviewsGrid Other:ui(1) Other:ui:Banner")
		if got := visibleStories(); !strings.Contains(got, "Several") {
			t.Errorf("the opened group's stories are not listed: %s", got)
		}

		// Choosing a story opens it in place: the URL names it and the tree
		// keeps its open groups. A reload opens the remembered groups and the
		// new story's path.
		click("Other:reviews:ReviewsGrid")
		page.Timeout(timeout).MustElementR("a", "^Several$").MustClick()
		deadline := time.Now().Add(timeout)
		for !strings.Contains(page.MustInfo().URL, "ReviewsGrid.Several") && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if got := page.MustInfo().URL; !strings.Contains(got, "/?story=internal/reviews/reviews.story.ktml%23ReviewsGrid.Several") {
			t.Fatalf("the URL does not name the chosen story: %s", got)
		}
		expect(t, "after choosing a story", "Pages(4) Pages:helpdesk(4) Pages:helpdesk:Pipeline "+
			"Components(2)+ Components:ui(2)+ Components:ui:Badge "+
			"Other(5)+ Other:helpdesk(2) Other:helpdesk:InteractiveReview Other:reviews(2)+ Other:reviews:ReviewsGrid+ Other:ui(1) Other:ui:Banner")

		page.Timeout(timeout).MustReload()
		waitLoad(t, page, timeout)
		expect(t, "after a reload", "Pages(4) Pages:helpdesk(4) Pages:helpdesk:Pipeline "+
			"Components(2) Components:ui(2) Components:ui:Badge "+
			"Other(5)+ Other:helpdesk(2) Other:helpdesk:InteractiveReview Other:reviews(2)+ Other:reviews:ReviewsGrid+ Other:ui(1) Other:ui:Banner")
	})

	t.Run("search filters, marks, and opens the matches, and Escape clears it", func(t *testing.T) {
		page.MustEval(`() => document.activeElement && document.activeElement.blur()`)
		page.Keyboard.MustType(input.Slash)
		if focused := page.MustEval(`() => document.activeElement.matches("[data-search]")`).Bool(); !focused {
			t.Fatal("/ does not focus the search")
		}
		searchBox().MustInput("WARN")
		expect(t, "a story match", "Pages(4)! Pages:helpdesk(4)! Pages:helpdesk:Pipeline! "+
			"Components(2)! Components:ui(2)! Components:ui:Badge! "+
			"Other(5)+ Other:helpdesk(2)! Other:helpdesk:InteractiveReview! Other:reviews(2)! Other:reviews:ReviewsGrid! Other:ui(1)+ Other:ui:Banner+")
		if got := page.MustEval(`() => [...document.querySelectorAll("[data-navigation] mark")].map(m => m.textContent).join(",")`).String(); got != "Warn" {
			t.Errorf("marks = %q, want Warn", got)
		}

		searchBox().MustSelectAllText().MustInput("call-audits")
		if got := visibleStories(); got != "Running,NotRequested,Failed,Complete" {
			t.Errorf("a route match lists %s", got)
		}
		if got := page.MustEval(`() => [...document.querySelectorAll("[data-route]")].filter(r => !r.hidden).map(r => r.textContent).join(",")`).String(); got != "GET /call-audits/{id}/pipeline → helpdesk.Pipeline" {
			t.Errorf("the matched route shown is %q", got)
		}

		searchBox().MustSelectAllText().MustInput("ui")
		if got := visibleStories(); got != "Plain,WithIcon,Warning" {
			t.Errorf("a package match lists %s", got)
		}

		searchBox().MustSelectAllText().MustInput("nothing like it")
		if hidden := page.MustEval(`() => document.querySelector("[data-no-results]").hidden`).Bool(); hidden {
			t.Error("no message when nothing matches")
		}

		page.Keyboard.MustType(input.Escape)
		if got := page.MustEval(`() => document.querySelector("[data-search]").value`).String(); got != "" {
			t.Errorf("Escape left the search %q", got)
		}
		expect(t, "after clearing", "Pages(4) Pages:helpdesk(4) Pages:helpdesk:Pipeline "+
			"Components(2) Components:ui(2) Components:ui:Badge "+
			"Other(5)+ Other:helpdesk(2) Other:helpdesk:InteractiveReview Other:reviews(2)+ Other:reviews:ReviewsGrid+ Other:ui(1) Other:ui:Banner")
		if marks := page.MustEval(`() => document.querySelectorAll("[data-navigation] mark").length`).Int(); marks != 0 {
			t.Errorf("%d marks left after clearing", marks)
		}
	})
}

// TestExplorer_GIVENAHostApplicationsStories_WHENBrowsed_THENTheTreeRendersAndFiltersQuickly
// measures the story tree of a large host application, read-only, when
// KTML_HOST_DIR names its root: the Explorer's start, its reachability, the interface's load, and
// each search keystroke.
func TestExplorer_GIVENAHostApplicationsStories_WHENBrowsed_THENTheTreeRendersAndFiltersQuickly(t *testing.T) {
	root := os.Getenv("KTML_HOST_DIR")
	if root == "" {
		t.Skip("KTML_HOST_DIR is not set")
	}
	prepareBinaries(t)
	started := time.Now()
	app := startExplorer(t, root)
	ready := time.Since(started)

	// The Explorer reports its first reachability analysis.
	for time.Since(started) < time.Minute && !strings.Contains(app.stderr.String(), "stories grouped by reachability") {
		time.Sleep(20 * time.Millisecond)
	}
	reached := time.Since(started)
	if !strings.Contains(app.stderr.String(), "stories grouped by reachability") {
		t.Fatalf("reachability never finished:\n%s", app.stderr)
	}
	requested := time.Now()
	response, err := http.Get(app.url + "/")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	served := time.Since(requested)

	browser := rod.New().MustConnect()
	t.Cleanup(browser.MustClose)
	page := browser.MustPage(app.url + "/")
	waitLoad(t, page, time.Minute)
	loaded := page.MustEval(`() => {
		const timing = performance.getEntriesByType("navigation")[0];
		return timing.domContentLoadedEventEnd - timing.startTime;
	}`).Num()
	counts := page.MustEval(`() => [...document.querySelectorAll(".kind")].map(k =>
		k.dataset.group + " " + k.dataset.count + " stories, " + k.querySelectorAll(".package").length + " packages, " + k.querySelectorAll(".component").length + " components").join("; ")`).String()
	stories := page.MustEval(`() => document.querySelectorAll("[data-story-item]").length`).Int()
	// Each keystroke of a search, then clearing it, with the layout it forces.
	keystrokes := page.MustEval(`() => {
		const search = document.querySelector("[data-search]");
		const times = [];
		for (const value of ["b", "ba", "bad", "badg", "badge", "", "g", "ge", "get", "get ", "get /", "get /j", "get /jobs", "", "e", "en", "eng", ""]) {
			const start = performance.now();
			search.value = value;
			search.dispatchEvent(new Event("input"));
			const script = performance.now() - start;
			document.body.offsetHeight;
			times.push([Math.round(script), Math.round(performance.now() - start)]);
		}
		return times;
	}`).Arr()
	worst, total := 0.0, 0.0
	for _, keystroke := range keystrokes {
		total += keystroke.Arr()[1].Num()
		worst = max(worst, keystroke.Arr()[1].Num())
	}
	t.Logf("host: %d stories; %s", stories, counts)
	t.Logf("host: keystrokes [script ms, with layout ms] %v", keystrokes)
	for line := range strings.SplitSeq(app.stderr.String(), "\n") {
		if strings.Contains(line, "reachability") {
			t.Logf("host: %s", line)
		}
	}
	t.Logf("host: started in %v, reachability grouped after %v; / served in %v (%d KB); interface loaded in %.0f ms; search keystroke mean %.1f ms, worst %.1f ms",
		ready.Round(time.Millisecond), reached.Round(time.Millisecond), served.Round(time.Millisecond), len(raw)/1024, loaded, total/float64(len(keystrokes)), worst)
	if worst > 100 {
		t.Errorf("a search keystroke took %.1f ms", worst)
	}
}

// TestExplorer_GIVENSeveralStories_WHENClickedThroughInTurn_THENEachLoads
// guards against the hang where every story page left in the back/forward
// cache kept its two live-reload streams open, so the third click waited
// forever behind the browser's six connections per host.
func TestExplorer_GIVENSeveralStories_WHENClickedThroughInTurn_THENEachLoads(t *testing.T) {
	prepareBinaries(t)
	app := newExplorerApp(t)
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 10 * time.Second

	page := browser.MustPage(app.url + "/")
	waitLoad(t, page, timeout)
	hrefs := page.MustEval(`() => [...new Set([...document.querySelectorAll("a[href*='story=']")].map(a => a.getAttribute("href")))]`).Arr()
	if len(hrefs) < 6 {
		t.Fatalf("the fixture has %d stories, want at least 6", len(hrefs))
	}
	// loaded reports the page showing href's story in a loaded preview.
	loaded := func(href string) bool {
		result, err := page.Eval(`(href) => {
			const want = new URL(href, location.href).searchParams.get("story");
			const frame = document.querySelector("iframe[data-preview]");
			const doc = frame && frame.contentDocument;
			return new URLSearchParams(location.search).get("story") === want && document.readyState === "complete" &&
				!!doc && doc.readyState === "complete" && new URLSearchParams(doc.location.search).get("story") === want;
		}`, href)
		return err == nil && result.Value.Bool()
	}
	waitLoaded := func(what, href string) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for !loaded(href) {
			if time.Now().After(deadline) {
				t.Fatalf("%s (%s) did not load", what, href)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// A click opens each story in place, in a fresh preview frame whose
	// predecessor's streams close with its document.
	for i, href := range hrefs[:6] {
		page.MustEval(`(href) => {
			const link = [...document.querySelectorAll("a")].find(a => a.getAttribute("href") === href);
			for (let group = link.closest("details"); group; group = group.parentElement.closest("details")) group.open = true;
			link.click();
		}`, href.Str())
		waitLoaded(fmt.Sprintf("click %d", i+1), href.Str())
	}
	// Navigating to a story's URL, as a link opened with a modifier does,
	// leaves each page in the back/forward cache with its streams closed.
	for i, href := range hrefs[:6] {
		wait := page.Timeout(timeout).MustWaitNavigation()
		page.MustEval(`(href) => { location.assign(href); }`, href.Str())
		if err := rod.Try(func() {
			wait()
			waitLoad(t, page, timeout)
		}); err != nil {
			t.Fatalf("navigation %d (%s) did not load: %v", i+1, href.Str(), err)
		}
		waitLoaded(fmt.Sprintf("navigation %d", i+1), href.Str())
	}
}

// TestExplorer_GIVENAPreviewThatMakesHTMXRequests_WHENOpen_THENNothingIsSwappedAndTheRequestsAreListed
// opens a story whose component loads and polls URLs: a placeholder and
// "/", which is the Explorer's index. Previews have no application behind
// them, so the Explorer answers each HTMX request with 204 No Content: the
// component's DOM stays as rendered, with no Explorer interface inside
// it, and the Requests panel lists each request, a poll once with a count.
func TestExplorer_GIVENAPreviewThatMakesHTMXRequests_WHENOpen_THENNothingIsSwappedAndTheRequestsAreListed(t *testing.T) {
	prepareBinaries(t)
	app := newExplorerApp(t, func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, "internal", "poll"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "internal", "poll", "poller.ktml"), `Poller [label: string] {
	section id="poller" {
		div id="history" hx-get="/_story/poller/history" hx-trigger="every 1s" "History: {label}"
		div id="inbox" hx-get="/" hx-trigger="load" "Inbox"
		div id="once" hx-post="/_story/poller/once" hx-trigger="load" "Once"
	}
}
`)
		writeFile(t, filepath.Join(dir, "internal", "poll", "poller.story.ktml"), `import { Poller } from "./poller.ktml"

story Poller.Default [label = "steady"]
`)
	})
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 15 * time.Second

	page := browser.MustPage(app.story("internal/poll/poller.story.ktml#Poller.Default"))
	waitLoad(t, page, timeout)
	frame := page.Timeout(timeout).MustElement("iframe[data-preview]").Context(page.GetContext()).MustFrame()
	until(t, frame, "HTMX in the preview", `() => !!window.htmx && !!document.querySelector("#history")`, timeout)
	until(t, page, "the poll listed with a repeat count", `() => [...document.querySelectorAll("[data-request-list] [data-request]")].some(row =>
		row.querySelector(".url").textContent === "/_story/poller/history" && Number(row.dataset.count) >= 3)`, timeout)

	got := frame.MustEval(`() => ({
		history: document.querySelector("#history").textContent,
		inbox: document.querySelector("#inbox").textContent,
		once: document.querySelector("#once").textContent,
		explorer: !!document.querySelector("[data-navigation], [data-panel], [data-preview], .top"),
		markup: document.querySelector("#poller").innerHTML.length,
	})`)
	if got.Get("history").Str() != "History: steady" || got.Get("inbox").Str() != "Inbox" || got.Get("once").Str() != "Once" || got.Get("explorer").Bool() {
		t.Errorf("the preview's DOM changed: %s", got.JSON("", ""))
	}

	rows := page.MustEval(`() => [...document.querySelectorAll("[data-request-list] [data-request]")].map(row => ({
		method: row.querySelector(".method").textContent,
		url: row.querySelector(".url").textContent,
		by: row.querySelector(".by") ? row.querySelector(".by").textContent : "",
		count: Number(row.dataset.count),
	}))`).Arr()
	byURL := make(map[string]string)
	for _, row := range rows {
		byURL[row.Get("url").Str()] = row.Get("method").Str() + " " + row.Get("by").Str()
		if row.Get("url").Str() != "/_story/poller/history" && row.Get("count").Int() != 1 {
			t.Errorf("a load request repeated: %s", row.JSON("", ""))
		}
	}
	for url, want := range map[string]string{
		"/_story/poller/history": "GET div#history · every 1s",
		"/":                      "GET div#inbox · load",
		"/_story/poller/once":    "POST div#once · load",
	} {
		if byURL[url] != want {
			t.Errorf("request %s listed as %q, want %q (rows %v)", url, byURL[url], want, byURL)
		}
	}
	if len(rows) != 3 {
		t.Errorf("listed %d rows, want 3: %v", len(rows), byURL)
	}
	if first := rows[0].Get("url").Str(); first != "/_story/poller/history" {
		t.Errorf("newest row is %s, want the poll", first)
	}

	page.Timeout(timeout).MustElement("[data-requests-clear]").MustClick()
	until(t, page, "the list cleared or holding only new polls", `() => [...document.querySelectorAll("[data-request-list] [data-request]")].every(row =>
		row.querySelector(".url").textContent === "/_story/poller/history" && Number(row.dataset.count) <= 2)`, timeout)
}

// TestExplorer_GIVENASearchedTree_WHENAStoryIsClicked_THENItOpensWithoutAReload
// searches the story tree, scrolls it, and clicks a match: the page does not
// reload (a window marker survives), the URL names the story, the search
// and the tree's scroll position stay, and the preview, title, and
// selection show the new story. Back and Forward open their stories the
// same way, and a click with a modifier key is left to the browser.
func TestExplorer_GIVENASearchedTree_WHENAStoryIsClicked_THENItOpensWithoutAReload(t *testing.T) {
	prepareBinaries(t)
	app := newExplorerApp(t)
	browser := rod.New().ControlURL(launcher.New().Headless(true).Set("headless", "new").MustLaunch()).MustConnect()
	t.Cleanup(browser.MustClose)
	const timeout = 15 * time.Second
	const first = "internal/ui/badge.story.ktml#Badge.Plain"

	page := browser.MustPage("")
	page.MustSetViewport(1100, 260, 1, false)
	page.Timeout(timeout).MustNavigate(app.story(first))
	waitLoad(t, page, timeout)
	page.Timeout(timeout).MustElement("[data-search]").MustInput("e")
	page.MustEval(`() => {
		const nav = document.querySelector("[data-navigation]");
		nav.scrollTop = nav.scrollHeight;
		window.__marker = "kept";
	}`)
	type sidebar struct {
		Search    string `json:"search"`
		ScrollTop int    `json:"scrollTop"`
	}
	readSidebar := func() sidebar {
		var got sidebar
		page.MustEval(`() => ({search: document.querySelector("[data-search]").value, scrollTop: Math.round(document.querySelector("[data-navigation]").scrollTop)})`).Unmarshal(&got)
		return got
	}
	// Off-screen groups skip layout until shown, so the tree's height
	// settles after the scroll: scroll again until the position holds.
	before := readSidebar()
	for settled := time.Now().Add(timeout); time.Now().Before(settled); {
		time.Sleep(300 * time.Millisecond)
		page.MustEval(`() => { const nav = document.querySelector("[data-navigation]"); nav.scrollTop = nav.scrollHeight; }`)
		time.Sleep(100 * time.Millisecond)
		now := readSidebar()
		if now == before {
			break
		}
		before = now
	}
	if before.ScrollTop == 0 {
		t.Fatalf("the tree does not scroll at this size: %+v", before)
	}
	// The last visible match, which is not the current story.
	target := page.MustEval(`() => {
		const links = [...document.querySelectorAll("[data-story-item] a")].filter(a => !a.closest("[hidden]") && !a.hasAttribute("aria-current"));
		return new URL(links[links.length - 1].href).searchParams.get("story");
	}`).Str()

	// shows waits until the page shows a story: its URL, title, selection,
	// and a loaded preview of it.
	shows := func(t *testing.T, key string) {
		t.Helper()
		title := key[strings.Index(key, "#")+1:]
		const showing = `(key, title) => {
			const frame = document.querySelector("iframe[data-preview]");
			const doc = frame && frame.contentDocument;
			const current = document.querySelector("[data-navigation] a[aria-current]");
			return new URLSearchParams(location.search).get("story") === key &&
				document.querySelector(".title h1").textContent === title &&
				document.body.dataset.story === key &&
				!!current && new URL(current.href).searchParams.get("story") === key &&
				!!doc && doc.readyState === "complete" && new URLSearchParams(doc.location.search).get("story") === key &&
				doc.body.children.length > 0;
		}`
		deadline := time.Now().Add(timeout)
		for {
			result, err := page.Eval(showing, key, title)
			if err == nil && result.Value.Bool() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for the page to show %s (at %s)", key, page.MustInfo().URL)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if marker := page.MustEval(`() => window.__marker || ""`).Str(); marker != "kept" {
			t.Fatalf("the page reloaded showing %s", key)
		}
	}

	t.Run("a click opens the story and keeps the tree", func(t *testing.T) {
		page.MustEval(`(key) => [...document.querySelectorAll("[data-story-item] a")].find(a => new URL(a.href).searchParams.get("story") === key).click()`, target)
		shows(t, target)
		if after := readSidebar(); after != before {
			t.Errorf("the tree changed: %+v, want %+v", after, before)
		}
	})

	t.Run("Back and Forward open their stories in place", func(t *testing.T) {
		page.Timeout(timeout).MustNavigateBack()
		shows(t, first)
		page.Timeout(timeout).MustNavigateForward()
		shows(t, target)
		if after := readSidebar(); after.Search != before.Search {
			t.Errorf("the search changed: %+v", after)
		}
	})

	t.Run("a click with a modifier key is left to the browser", func(t *testing.T) {
		prevented := page.MustEval(`() => {
			let prevented = null;
			const record = (event) => { prevented = event.defaultPrevented; event.preventDefault(); };
			window.addEventListener("click", record);
			const link = [...document.querySelectorAll("[data-story-item] a")].find(a => !a.hasAttribute("aria-current"));
			link.dispatchEvent(new MouseEvent("click", {bubbles: true, cancelable: true, button: 0, ctrlKey: true}));
			window.removeEventListener("click", record);
			return prevented;
		}`).Bool()
		if prevented {
			t.Error("a Ctrl-click was taken over")
		}
		shows(t, target)
	})
}
