package explorer

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// routedApp is internal/reach's fixture application, a Go module whose Chi
// routes render ListPage and JobDetail (pages) and, through KTML calls,
// JobRow, Badge, Card, and Icon; Orphan and Unused are not reached. Its
// stories carry generated headers, one of them (Icon's) stale.
func routedApp(t *testing.T, module bool) map[string]string {
	t.Helper()
	files := make(map[string]string)
	source := filepath.Join("..", "reach", "testdata", "app")
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if relative == "go.mod.txt" {
			if !module {
				return nil
			}
			relative = "go.mod"
		}
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	files["jobs/jobs.story.ktml"] = `import { ListPage, JobRow, Orphan } from "./jobs.ktml"

// ktml:generated 000000000000
// ListPage is a page, reached by:
//   GET /jobs → h.List
story ListPage.Default [
	view = [rows = []],
]

// ktml:generated 000000000000
// JobRow is a component rendered through KTML calls, reached by:
//   GET /jobs → h.List
story JobRow.Default [
	row = [id = "1", status = "Open"],
]

story JobRow.Closed [
	row = [id = "2", status = "Closed"],
]

// ktml:generated 000000000000
// Orphan is not reached by any route.
story Orphan.Default []
`
	files["ui/ui.story.ktml"] = `import { Icon, Unused } from "./ui.ktml"

// ktml:generated 000000000000
// Icon is a page, reached by:
//   GET /feed → h.Feed
story Icon.Default []

story Unused.Default []
`
	return files
}

var groupPattern = regexp.MustCompile(`<details class="group [a-z]+" data-group="([^"]*)"( data-count="(\d+)")?[^>]*?( open)?>`)

// groups lists the navigation's groups in order as id, id=count for counted
// groups, with a trailing + when it is open.
func groups(body string) []string {
	var result []string
	for _, match := range groupPattern.FindAllStringSubmatch(body, -1) {
		group := match[1]
		if match[3] != "" {
			group += "=" + match[3]
		}
		if match[4] != "" {
			group += "+"
		}
		result = append(result, group)
	}
	return result
}

func TestNavigation_GIVENReachability_WHENListed_THENGroupsStoriesByKindPackageAndComponent(t *testing.T) {
	s, backend, _ := newTestServer(t, routedApp(t, true), nil)
	s.analyze()
	_, body := get(t, backend.URL+"/?story="+url.QueryEscape("jobs/jobs.story.ktml#JobRow.Closed"))
	got := strings.Join(groups(body), " ")
	// Reachability wins over Icon's stale header; unreached components and a
	// story without a header are Other. Groups are collapsed except the
	// selected story's path.
	want := "Pages=1 Pages:jobs=1 Pages:jobs:ListPage " +
		"Components=3+ Components:jobs=2+ Components:jobs:JobRow+ Components:ui=1 Components:ui:Icon " +
		"Other=2 Other:jobs=1 Other:jobs:Orphan Other:ui=1 Other:ui:Unused"
	if got != want {
		t.Errorf("groups:\n got %s\nwant %s", got, want)
	}
	for _, want := range []string{
		`<a href="/?story=jobs/jobs.story.ktml%23JobRow.Closed" aria-current="page" data-label>Closed</a>`,
		`data-route-index>["GET /jobs → h.List","GET /jobs/{id} → h.Show"]</script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("navigation lacks %s:\n%s", want, body)
		}
	}
	if !regexp.MustCompile(`data-group="Pages:jobs:ListPage" data-routes="0"`).MatchString(body) {
		t.Errorf("ListPage does not name its route GET /jobs:\n%s", body)
	}
}

func TestNavigation_GIVENNoReachability_WHENListed_THENFallsBackToGeneratedHeadersThenOther(t *testing.T) {
	s, backend, _ := newTestServer(t, routedApp(t, false), nil)
	s.analyze() // no Go module: reachability fails
	_, body := get(t, backend.URL+"/")
	got := strings.Join(groups(body), " ")
	// The first story is selected, so its path is open.
	want := "Pages=2+ Pages:jobs=1+ Pages:jobs:ListPage+ Pages:ui=1 Pages:ui:Icon " +
		"Components=2 Components:jobs=2 Components:jobs:JobRow " +
		"Other=2 Other:jobs=1 Other:jobs:Orphan Other:ui=1 Other:ui:Unused"
	if got != want {
		t.Errorf("groups:\n got %s\nwant %s", got, want)
	}
	if !strings.Contains(body, `data-route-index>["GET /feed → h.Feed","GET /jobs → h.List"]</script>`) {
		t.Errorf("the route index is not the headers' routes:\n%s", body)
	}
}

func TestNavigation_GIVENAComponentChange_WHENApplied_THENReachabilityIsComputedAgain(t *testing.T) {
	s, backend, root := newTestServer(t, routedApp(t, true), nil)
	s.analyze()
	path := filepath.Join(root, "jobs", "jobs.ktml")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// JobDetail stops calling Card, so Card and Icon are no longer reached.
	changed := strings.Replace(string(source), `Card title="{title}"`, `p "{title}"`, 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Apply(t.Context(), []string{path})
	s.analyze()
	_, body := get(t, backend.URL+"/")
	if !strings.Contains(strings.Join(groups(body), " "), "Other:ui:Icon") {
		t.Errorf("Icon is still reachable after the change: %v", groups(body))
	}
	if code, _ := get(t, backend.URL+"/"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
}

func TestPackageNames_GIVENDirectories_WHENNamed_THENDropsTheirCommonParent(t *testing.T) {
	tests := []struct {
		name string
		dirs []string
		want []string
	}{
		{"common parent", []string{"internal/web", "internal/web/admin", "internal/blog"}, []string{"web", "web/admin", "blog"}},
		{"one directory", []string{"internal/web", "internal/web"}, []string{"web", "web"}},
		{"parent is a package", []string{"internal", "internal/ui"}, []string{"internal", "internal/ui"}},
		{"root", []string{".", "ui"}, []string{".", "ui"}},
		{"no common parent", []string{"jobs", "ui"}, []string{"jobs", "ui"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := strings.Join(packageNames(test.dirs), ",")
			if want := strings.Join(test.want, ","); got != want {
				t.Errorf("packageNames(%v) = %s, want %s", test.dirs, got, want)
			}
		})
	}
}
