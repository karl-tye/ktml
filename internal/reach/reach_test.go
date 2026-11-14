package reach

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/plugin/htmx"
)

// copyApp copies testdata/app into a temporary module.
func copyApp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("testdata", "app")
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		target := filepath.Join(root, strings.TrimSuffix(relative, ".txt"))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func analyze(t *testing.T, root string) *Result {
	t.Helper()
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Valid() {
		t.Fatalf("fixture has errors: %v", snapshot.Err())
	}
	result, err := Analyze(Options{Root: root, Chi: htmx.ChiSettings{Dir: "web"}, Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func names(components map[compiler.SymbolID]*Component, page bool) []string {
	var list []string
	for _, component := range components {
		if component.Page == page {
			list = append(list, component.Name)
		}
	}
	slices.Sort(list)
	return list
}

func routesOf(t *testing.T, result *Result, name string) []string {
	t.Helper()
	for _, component := range result.Reachable {
		if component.Name == name {
			var list []string
			for _, route := range component.Routes {
				list = append(list, route.String())
			}
			return list
		}
	}
	t.Fatalf("%s is not reachable", name)
	return nil
}

// TestAnalyze_GIVENChiRoutes_WHENAnalyzed_THENFollowsHandlersGoAndKTMLCalls
// follows each route's handler: a method value in another package, a
// closure, a helper function, and an interface method, to the components
// the Go code constructs, then their KTML calls. Icon is reached only
// through JobDetail's Card; Orphan, Unused, and UnusedChild are not reached.
func TestAnalyze_GIVENChiRoutes_WHENAnalyzed_THENFollowsHandlersGoAndKTMLCalls(t *testing.T) {
	result := analyze(t, copyApp(t))
	for _, warning := range result.Warnings {
		t.Errorf("warning: %s", warning)
	}
	if got, want := names(result.Reachable, true), []string{"FeedItem", "JobDetail", "ListPage", "Notice"}; !slices.Equal(got, want) {
		t.Errorf("pages = %v, want %v", got, want)
	}
	if got, want := names(result.Reachable, false), []string{"Badge", "Card", "Icon", "JobRow"}; !slices.Equal(got, want) {
		t.Errorf("components = %v, want %v", got, want)
	}
	var unreachable []string
	for _, component := range result.Unreachable {
		unreachable = append(unreachable, component.Name)
	}
	if want := []string{"Orphan", "Unused", "UnusedChild"}; !slices.Equal(unreachable, want) {
		t.Errorf("unreachable = %v, want %v", unreachable, want)
	}
	if got, want := routesOf(t, result, "Icon"), []string{"GET /jobs/{id}"}; !slices.Equal(got, want) {
		t.Errorf("Icon routes = %v, want %v", got, want)
	}
	if got, want := routesOf(t, result, "Badge"), []string{"GET /jobs"}; !slices.Equal(got, want) {
		t.Errorf("Badge routes = %v, want %v", got, want)
	}
	if got, want := routesOf(t, result, "Notice"), []string{"POST /jobs/{id}/close"}; !slices.Equal(got, want) {
		t.Errorf("Notice routes = %v, want %v", got, want)
	}
	if got, want := routesOf(t, result, "FeedItem"), []string{"GET /feed"}; !slices.Equal(got, want) {
		t.Errorf("FeedItem routes = %v, want %v", got, want)
	}
	for _, component := range result.Reachable {
		for _, route := range component.Routes {
			if route.Handler == "" {
				t.Errorf("%s: route %s has no handler", component.Name, route)
			}
		}
	}
}

// TestAnalyze_GIVENARouteRemoved_WHENAnalyzed_THENItsComponentsAreUnreachable
// removes the /feed route: FeedItem is only constructed by the feed's
// interface method, so it becomes unreachable.
func TestAnalyze_GIVENARouteRemoved_WHENAnalyzed_THENItsComponentsAreUnreachable(t *testing.T) {
	root := copyApp(t)
	router := filepath.Join(root, "web", "router.go")
	data, err := os.ReadFile(router)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(router, []byte(strings.Replace(string(data), "\tr.Get(\"/feed\", h.Feed)\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	result := analyze(t, root)
	for _, component := range result.Reachable {
		if component.Name == "FeedItem" {
			t.Errorf("FeedItem is reachable without its route: %v", component.Routes)
		}
	}
}
