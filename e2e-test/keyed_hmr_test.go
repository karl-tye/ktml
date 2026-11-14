package e2e_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/internal/dev/livereload"
)

const keyedComponent = "example.com/app.TodoList"

type keyedItem struct {
	key   string
	label string
	inner []string
}

// keyedRoot is the Render Identity the registry gives the TodoList root.
const keyedRoot = "@1"

// keyedList renders the development output of a TodoList root instance with
// nested keyed repetitions, using the same marker helpers as generated
// ktmldev renderers.
func keyedList(t *testing.T, items ...keyedItem) string {
	t.Helper()
	var output strings.Builder
	output.WriteString(`<ul id="todos"`)
	if err := dev.InstanceAttr(&output, keyedRoot); err != nil {
		t.Fatal(err)
	}
	output.WriteString(`>`)
	for _, item := range items {
		identity := dev.StringFrame(keyedRoot, keyedComponent, "r0", item.key)
		if err := dev.Begin(&output, identity); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&output, `<li data-key="%s"><label>%s</label><input id="input-%s" value="%s">`, item.key, item.label, item.key, item.key)
		for _, inner := range item.inner {
			innerIdentity := dev.StringFrame(identity, keyedComponent, "r1", inner)
			_ = dev.Begin(&output, innerIdentity)
			fmt.Fprintf(&output, `<span data-key="%s">%s</span>`, inner, inner)
			_ = dev.End(&output, innerIdentity)
		}
		output.WriteString(`</li>`)
		if err := dev.End(&output, identity); err != nil {
			t.Fatal(err)
		}
	}
	output.WriteString(`</ul>`)
	return output.String()
}

// TestKeyedHMR_GIVENKeyedFragments_WHENPatchedThroughLivereload_THENMovesSurvivorsAndPreservesBrowserState
// drives the livereload SSE patch path of a page session in a real browser.
func TestKeyedHMR_GIVENKeyedFragments_WHENPatchedThroughLivereload_THENMovesSurvivorsAndPreservesBrowserState(t *testing.T) {
	var mu sync.Mutex
	page := keyedList(t, keyedItem{key: "a", label: "Alpha"}, keyedItem{key: "b", label: "Beta", inner: []string{"x", "y"}}, keyedItem{key: "c", label: "Gamma"})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set(livereload.SessionHeader, "keyed-page")
		fmt.Fprintf(w, "<!doctype html><html><head><title>keyed</title></head><body>%s</body></html>", page)
	}))
	t.Cleanup(backend.Close)
	server := livereload.NewServer(backend.URL)
	proxy := httptest.NewServer(server)
	t.Cleanup(proxy.Close)

	browser := rod.New().MustConnect()
	t.Cleanup(browser.MustClose)
	tab := browser.MustPage(proxy.URL)
	tab.MustWaitLoad()
	waitFor(t, "SSE client", func() bool { return server.ActiveClients() > 0 })

	// Tag every live li so node reuse is observable, edit and focus input b.
	tab.MustEval(`() => { window.__ktmlMarker = "loaded"; document.querySelectorAll("li").forEach(li => li.__tag = li.dataset.key); }`)
	input := tab.MustElement("#input-b")
	input.MustSelectAllText().MustInput("typed by user")
	tab.MustEval(`() => { const input = document.getElementById("input-b"); input.focus(); input.setSelectionRange(2, 5); }`)

	state := func() string {
		return tab.MustEval(`() => {
			const lis = Array.from(document.querySelectorAll("#todos > li"));
			const order = lis.map(li => li.dataset.key + (li.__tag === li.dataset.key ? "=" : "+") + Array.from(li.querySelectorAll("span")).map(s => s.dataset.key).join("")).join(",");
			const active = document.activeElement;
			const input = document.getElementById("input-b");
			return [order, active && active.id, input ? input.value : "", input ? input.selectionStart + "-" + input.selectionEnd : "", window.__ktmlMarker].join("|");
		}`).String()
	}
	patch := func(html string, want string) {
		t.Helper()
		server.Patch("keyed-page", keyedRoot, html, false)
		var got string
		waitFor(t, "patched state "+want, func() bool { got = state(); return got == want })
		if got != want {
			t.Fatalf("state = %s, want %s", got, want)
		}
	}

	t.Run("reorders surviving fragments without recreating them", func(t *testing.T) {
		patch(keyedList(t, keyedItem{key: "c", label: "Gamma"}, keyedItem{key: "a", label: "Alpha"}, keyedItem{key: "b", label: "Beta", inner: []string{"x", "y"}}),
			"c=,a=,b=xy|input-b|typed by user|2-5|loaded")
	})
	t.Run("inserts and removes fragments", func(t *testing.T) {
		patch(keyedList(t, keyedItem{key: "d", label: "Delta"}, keyedItem{key: "b", label: "Beta", inner: []string{"x", "y"}}, keyedItem{key: "c", label: "Gamma"}),
			"d+,b=xy,c=|input-b|typed by user|2-5|loaded")
	})
	t.Run("moves the focused fragment and reorders nested fragments", func(t *testing.T) {
		patch(keyedList(t, keyedItem{key: "c", label: "Gamma"}, keyedItem{key: "d", label: "Delta"}, keyedItem{key: "b", label: "Beta renamed", inner: []string{"z", "y", "x"}}),
			"c=,d+,b=zyx|input-b|typed by user|2-5|loaded")
		if label := tab.MustElement(`li[data-key="b"] label`).MustText(); label != "Beta renamed" {
			t.Errorf("surviving fragment label = %q, want the patched text", label)
		}
	})
	t.Run("reloads on an unsafe patch", func(t *testing.T) {
		mu.Lock()
		page = keyedList(t, keyedItem{key: "a", label: "Alpha"})
		mu.Unlock()
		unbalanced := strings.Replace(keyedList(t, keyedItem{key: "a", label: "Alpha"}), "<!--/ktml:item", "<!--ktml:item", 1)
		server.Patch("keyed-page", keyedRoot, unbalanced, false)
		var got string
		waitFor(t, "reload", func() bool {
			result, err := tab.Eval(`() => String(window.__ktmlMarker) + "|" + document.querySelectorAll("#todos > li").length`)
			if err != nil {
				return false
			}
			got = result.Value.String()
			return got == "undefined|1"
		})
		if got != "undefined|1" {
			t.Fatalf("after unsafe patch = %s, want a reloaded page", got)
		}
	})
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("timed out waiting for %s", what)
}
