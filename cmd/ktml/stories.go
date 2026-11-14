package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/reach"
	"github.com/karl-tye/ktml/internal/storygen"
)

const storiesUsage = "usage: ktml stories generate [-chi dir] [-all] [-unreachable] [-check] [-json] [paths...]"

// storiesCmd runs ktml stories <subcommand>. It returns the process exit
// status.
func storiesCmd(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 || args[0] != "generate" {
		fmt.Fprintln(stderr, storiesUsage)
		return 2
	}
	return storiesGenerateCmd(stdout, stderr, args[1:])
}

// routeCommentLimit bounds the routes a story's comment names.
const routeCommentLimit = 5

// storiesGenerateCmd writes a story Document beside each component Document
// with a component the application renders: reachable from a Chi route,
// unless -all.
func storiesGenerateCmd(stdout, stderr io.Writer, args []string) int {
	flags := flag.NewFlagSet("stories generate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	chiDir := flags.String("chi", "", "the Go `dir` of the Chi route registrations (default: plugin.htmx.settings.chi.dir, else the module)")
	all := flags.Bool("all", false, "write stories for every component, reachable or not")
	listUnreachable := flags.Bool("unreachable", false, "list the components no route reaches")
	check := flags.Bool("check", false, "write nothing; exit 1 when a story Document would change")
	asJSON := flags.Bool("json", false, "print the reachability and the outcome as JSON")
	flags.Usage = func() {
		fmt.Fprintln(stderr, storiesUsage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		paths = []string{"./..."}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	chi, err := reach.ChiSettings(root, *chiDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	selected, err := expandGenerateArgs(paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	documents := make(map[string]bool)
	for _, file := range selected {
		if strings.HasSuffix(file, ".ktml") && !compiler.IsStoryPath(file) {
			if absolute, err := filepath.Abs(file); err == nil {
				documents[filepath.Clean(absolute)] = true
			}
		}
	}

	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := storygen.ComponentErrors(snapshot); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	reachability, err := reach.Analyze(reach.Options{Root: root, Chi: chi, Snapshot: snapshot})
	if err != nil {
		if !*all {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// -all needs no reachability; it only names routes in comments.
		fmt.Fprintf(stderr, "warning: %v\n", err)
		reachability = nil
	}
	var components []compiler.Component
	for _, component := range snapshot.Components() {
		if compiler.IsStoryPath(component.File) || !documents[component.File] {
			continue
		}
		if _, reached := reachability.Lookup(component.ID); reached || *all {
			components = append(components, component)
		}
	}
	comments := func(component compiler.Component) []string {
		if reachability == nil {
			return nil
		}
		reached, ok := reachability.Lookup(component.ID)
		if !ok {
			return []string{component.Name + " is not reached by any route."}
		}
		return routeComment(reached)
	}
	page := func(component compiler.Component) bool {
		reached, ok := reachability.Lookup(component.ID)
		return ok && reached.Page
	}
	result, err := storygen.Plan(root, snapshot, storygen.Options{Components: components, Comments: comments, Page: page})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !*check {
		if err := result.Write(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	if *asJSON {
		data, err := json.MarshalIndent(storiesJSON(root, reachability, result), "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = stdout.Write(append(data, '\n'))
	} else {
		printStoriesReport(stdout, root, reachability, result, *listUnreachable, *check)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", relativeTo(root, warning))
	}
	if *check {
		for _, file := range result.Files {
			if file.Changed {
				return 1
			}
		}
	}
	return 0
}

// routeComment names the routes that reach a component.
func routeComment(component *reach.Component) []string {
	kind := "a component rendered through KTML calls"
	if component.Page {
		kind = "a page"
	}
	lines := []string{fmt.Sprintf("%s is %s, reached by:", component.Name, kind)}
	for index, route := range component.Routes {
		if index == routeCommentLimit {
			lines = append(lines, fmt.Sprintf("  and %d more", len(component.Routes)-routeCommentLimit))
			break
		}
		lines = append(lines, fmt.Sprintf("  %s → %s", route, route.Handler))
	}
	return lines
}

func relativeTo(root, text string) string {
	return strings.ReplaceAll(text, root+string(filepath.Separator), "")
}

func printStoriesReport(stdout io.Writer, root string, reachability *reach.Result, result storygen.Result, listUnreachable, check bool) {
	if reachability != nil {
		fmt.Fprintf(stdout, "reachability: %d routes; %d pages and %d components reachable; %d unreachable\n",
			len(reachability.Routes), reachability.Pages(), len(reachability.Reachable)-reachability.Pages(), len(reachability.Unreachable))
		if listUnreachable {
			for _, component := range reachability.Unreachable {
				fmt.Fprintf(stdout, "  unreachable: %s (%s)\n", component.Name, relativeTo(root, component.File))
			}
		}
	}
	verb := "written"
	if check {
		verb = "would change"
	}
	var added, updated, unchanged, removed int
	for _, file := range result.Files {
		report := file.Report
		added += len(report.Added)
		updated += len(report.Updated)
		unchanged += len(report.Unchanged)
		removed += len(report.Removed)
		if file.Changed {
			fmt.Fprintf(stdout, "%s %s: %d added, %d updated, %d removed\n", relativeTo(root, file.Path), verb, len(report.Added), len(report.Updated), len(report.Removed))
		}
		for _, key := range report.Edited {
			fmt.Fprintf(stdout, "  kept %s in %s: edited by hand\n", key, relativeTo(root, file.Path))
		}
		for _, key := range report.Own {
			fmt.Fprintf(stdout, "  kept %s in %s: written by hand, so the generated story is not written\n", key, relativeTo(root, file.Path))
		}
	}
	fmt.Fprintf(stdout, "stories: %d generated in %d Documents (%d added, %d updated, %d unchanged, %d removed)\n", result.Stories, len(result.Files), added, updated, unchanged, removed)
	fmt.Fprintf(stdout, "Variants: %d of %d covered\n", result.Covered, result.Variants)
	for _, skipped := range result.Skipped {
		fmt.Fprintf(stdout, "  skipped %s (%s): %s\n", skipped.Component.Name, relativeTo(root, skipped.Component.File), skipped.Reason)
	}
}

type storiesReport struct {
	Routes      int                `json:"routes,omitempty"`
	Reachable   []storiesComponent `json:"reachable,omitempty"`
	Unreachable []storiesComponent `json:"unreachable,omitempty"`
	Files       []storiesFile      `json:"files"`
	Skipped     []storiesComponent `json:"skipped,omitempty"`
	Stories     int                `json:"stories"`
	Variants    int                `json:"variants"`
	Covered     int                `json:"covered"`
}

type storiesComponent struct {
	Name   string   `json:"name"`
	File   string   `json:"file"`
	Page   bool     `json:"page,omitempty"`
	Routes []string `json:"routes,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

type storiesFile struct {
	Path      string   `json:"path"`
	Changed   bool     `json:"changed"`
	Added     []string `json:"added,omitempty"`
	Updated   []string `json:"updated,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"`
	Edited    []string `json:"edited,omitempty"`
	Own       []string `json:"own,omitempty"`
	Removed   []string `json:"removed,omitempty"`
}

func storiesJSON(root string, reachability *reach.Result, result storygen.Result) storiesReport {
	report := storiesReport{Files: []storiesFile{}, Stories: result.Stories, Variants: result.Variants, Covered: result.Covered}
	if reachability != nil {
		report.Routes = len(reachability.Routes)
		for _, component := range reachability.Reachable {
			entry := storiesComponent{Name: component.Name, File: relativeTo(root, component.File), Page: component.Page}
			for _, route := range component.Routes {
				entry.Routes = append(entry.Routes, route.String()+" → "+route.Handler)
			}
			report.Reachable = append(report.Reachable, entry)
		}
		sort.Slice(report.Reachable, func(i, j int) bool {
			if report.Reachable[i].File != report.Reachable[j].File {
				return report.Reachable[i].File < report.Reachable[j].File
			}
			return report.Reachable[i].Name < report.Reachable[j].Name
		})
		for _, component := range reachability.Unreachable {
			report.Unreachable = append(report.Unreachable, storiesComponent{Name: component.Name, File: relativeTo(root, component.File)})
		}
	}
	for _, file := range result.Files {
		report.Files = append(report.Files, storiesFile{
			Path: relativeTo(root, file.Path), Changed: file.Changed,
			Added: file.Report.Added, Updated: file.Report.Updated, Unchanged: file.Report.Unchanged,
			Edited: file.Report.Edited, Own: file.Report.Own, Removed: file.Report.Removed,
		})
	}
	for _, skipped := range result.Skipped {
		report.Skipped = append(report.Skipped, storiesComponent{Name: skipped.Component.Name, File: relativeTo(root, skipped.Component.File), Reason: skipped.Reason})
	}
	return report
}
