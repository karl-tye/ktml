package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyStoriesApp copies the reachability fixture into a temporary module
// and makes it the working directory.
func copyStoriesApp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("..", "..", "internal", "reach", "testdata", "app")
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
	if err := os.WriteFile(filepath.Join(root, "ktml.config.json"), []byte(`{"plugin": {"htmx": {"path": "htmx.wasm", "settings": {"chi": {"dir": "web"}}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func runStories(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := storiesCmd(&stdout, &stderr, args)
	return status, stdout.String(), stderr.String()
}

// TestStoriesGenerate_GIVENARoutedApp_WHENRun_THENWritesStoriesForReachableComponentsOnly
// runs ktml stories generate on the reachability fixture: stories are
// written beside the Documents of reachable components, each component's
// first story names its routes, unreachable components get none and are
// listed with -unreachable, and -check passes once the stories are current.
func TestStoriesGenerate_GIVENARoutedApp_WHENRun_THENWritesStoriesForReachableComponentsOnly(t *testing.T) {
	root := copyStoriesApp(t)

	status, stdout, stderr := runStories(t, "generate", "-check")
	if status != 1 {
		t.Fatalf("-check before generating: status %d, want 1\n%s%s", status, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "jobs", "jobs.story.ktml")); !os.IsNotExist(err) {
		t.Fatalf("-check wrote a story Document")
	}

	status, stdout, stderr = runStories(t, "generate", "-unreachable")
	if status != 0 {
		t.Fatalf("status %d\n%s%s", status, stdout, stderr)
	}
	for _, want := range []string{
		"reachability: 4 routes; 4 pages and 4 components reachable; 3 unreachable",
		"unreachable: Orphan (jobs/jobs.ktml)",
		"unreachable: UnusedChild (ui/ui.ktml)",
		"Variants: 0 of 0 covered",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	jobs, err := os.ReadFile(filepath.Join(root, "jobs", "jobs.story.ktml"))
	if err != nil {
		t.Fatal(err)
	}
	ui, err := os.ReadFile(filepath.Join(root, "ui", "ui.story.ktml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"story ListPage.Default", "story JobRow.Default", "story JobDetail.Default", "// JobDetail is a page, reached by:", "//   GET /jobs/{id} → h.Show"} {
		if !strings.Contains(string(jobs), want) {
			t.Errorf("jobs.story.ktml lacks %q:\n%s", want, jobs)
		}
	}
	if strings.Contains(string(jobs), "Orphan") {
		t.Errorf("jobs.story.ktml has a story for the unreachable Orphan")
	}
	// ListPage is a page: its Default story only.
	if strings.Contains(string(jobs), "story ListPage.Empty") {
		t.Errorf("the page ListPage has an Empty story:\n%s", jobs)
	}
	for _, want := range []string{"story Icon.Default", "// Icon is a component rendered through KTML calls, reached by:", "story FeedItem.Default"} {
		if !strings.Contains(string(ui), want) {
			t.Errorf("ui.story.ktml lacks %q:\n%s", want, ui)
		}
	}
	if strings.Contains(string(ui), "Unused") {
		t.Errorf("ui.story.ktml has a story for an unreachable component")
	}

	if status, stdout, stderr = runStories(t, "generate", "-check"); status != 0 {
		t.Errorf("-check after generating: status %d\n%s%s", status, stdout, stderr)
	}

	status, stdout, _ = runStories(t, "generate", "-json", "-check")
	if status != 0 {
		t.Fatalf("-json -check: status %d", status)
	}
	var report storiesReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("-json output: %v\n%s", err, stdout)
	}
	if report.Routes != 4 || len(report.Reachable) != 8 || len(report.Unreachable) != 3 {
		t.Errorf("JSON report = %d routes, %d reachable, %d unreachable", report.Routes, len(report.Reachable), len(report.Unreachable))
	}

	// -all writes stories for unreachable components too.
	if status, stdout, stderr = runStories(t, "generate", "-all", "jobs"); status != 0 {
		t.Fatalf("-all: status %d\n%s%s", status, stdout, stderr)
	}
	jobs, _ = os.ReadFile(filepath.Join(root, "jobs", "jobs.story.ktml"))
	if !strings.Contains(string(jobs), "// Orphan is not reached by any route.\nstory Orphan.Default") {
		t.Errorf("-all did not write Orphan's story:\n%s", jobs)
	}
	if !strings.Contains(string(jobs), "//   GET /jobs/{id} → h.Show") {
		t.Errorf("-all dropped the route comments")
	}
}

// TestStoriesGenerate_GIVENNoSubcommand_WHENRun_THENPrintsUsage reports a
// usage error.
func TestStoriesGenerate_GIVENNoSubcommand_WHENRun_THENPrintsUsage(t *testing.T) {
	status, _, stderr := runStories(t)
	if status != 2 || !strings.Contains(stderr, "usage: ktml stories generate") {
		t.Errorf("status %d, stderr %q", status, stderr)
	}
}
