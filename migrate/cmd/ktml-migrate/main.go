// Command ktml-migrate migrates templ sources to KTML. The ktml CLI runs it
// for `ktml migrate`; it is a separate module so that templ stays out of
// KTML's own dependency graph.
//
//	ktml-migrate templ <paths> --report <file.json> --dry-run [--summary <file.md>] [--categories <file.json>]
//	ktml-migrate templ <paths> --report <file.json> --out <dir> [--scaffold-presenters]
//	ktml-migrate templ <paths> --report <file.json> --in-place [--scaffold-presenters] [--force] [--bridges auto|all|none] [--remove-templ [--hand-finished]]
//	ktml-migrate codemod <packages> --map <conversion.json> --report <file.json> [--write] [--fragment-render <import path>.<Func>] [--root <dir>] [--tags <tag,...>]
//
// --in-place converts a batch (templ files or packages) into the source
// packages themselves: .ktml, .ktmlschema, presenters, helpers, and templ
// bridges go next to the templ files, and source maps under
// <root>/.ktml/migrate. Existing outputs are kept unless --force.
// --remove-templ then deletes the batch's .templ and _templ.go files, but
// only when the batch is complete (see templmigrate.ConvertOptions).
//
// codemod rewrites the Go callers of the converted templates (the
// conversion report's call mappings) to the Hybrid H API, and renames
// their calls in unconverted .templ files to the templ bridges. Without
// --write it only reports. It exits 1 when a site needs a hand edit.
//
// --dry-run writes only the inventory: every templ node receives one
// migration status. --out converts: it writes .ktml, .ktmlschema,
// presenter scaffolds, and source maps for each templ file under <dir>,
// mirroring the source tree, and never writes the .templ files. The command
// exits 1 when any node is blocked, dropped, or partially converted, any
// file fails to parse, or the emitted KTML fails validation, and 2 on usage
// errors.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/karl-tye/ktml/migrate/templmigrate"
)

const usage = "usage: ktml migrate templ <file|dir|dir/...>... --report <file.json> (--dry-run | --out <dir> [--scaffold-presenters]) [--summary <file.md>] [--categories <file.json>] [--root <dir>] [--no-types] [--nodes all|unconverted]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "codemod" {
		return codemod(args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "templ" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet("templ", flag.ContinueOnError)
	flags.SetOutput(stderr)
	report := flags.String("report", "", "write the JSON inventory report to this file")
	summary := flags.String("summary", "", "write the Markdown summary to this file, between generated markers if it has them")
	categories := flags.String("categories", "", "JSON file assigning files to migration categories and exclusions")
	root := flags.String("root", "", "directory report paths are relative to (default: working directory)")
	dryRun := flags.Bool("dry-run", false, "report only; write no KTML, Host Schema, or presenter files")
	out := flags.String("out", "", "convert into this directory (mirrors the source tree; .templ files are never written)")
	inPlace := flags.Bool("in-place", false, "convert into the source packages themselves")
	force := flags.Bool("force", false, "with --in-place: overwrite existing outputs")
	removeTempl := flags.Bool("remove-templ", false, "with --in-place: delete the batch's .templ and _templ.go files when the batch is complete")
	handFinished := flags.Bool("hand-finished", false, "with --remove-templ: the partial nodes are finished by hand in the kept outputs")
	bridges := flags.String("bridges", "auto", "with --in-place: templ bridges for unconverted templ callers: auto, all, or none")
	scaffold := flags.Bool("scaffold-presenters", false, "emit Host Schemas and presenter scaffolds for templates that need them")
	noTypes := flags.Bool("no-types", false, "classify syntactically without loading go/types")
	nodes := flags.String("nodes", "all", "nodes to list in the report: all, or unconverted")
	paths, err := parseInterspersed(flags, args[1:])
	if err != nil {
		return 2
	}
	modes := 0
	for _, set := range []bool{*dryRun, *out != "", *inPlace} {
		if set {
			modes++
		}
	}
	if modes != 1 {
		fmt.Fprintln(stderr, "ktml migrate templ: give exactly one of --dry-run, --out, and --in-place")
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if *report == "" || len(paths) == 0 || *nodes != "all" && *nodes != "unconverted" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	options := templmigrate.Options{Root: *root, NoTypes: *noTypes}
	if *categories != "" {
		if options.Categories, err = templmigrate.LoadCategories(*categories); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	files, err := templmigrate.ExpandPaths(paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var inventory *templmigrate.Report
	if *out != "" || *inPlace {
		inventory, err = templmigrate.Convert(files, templmigrate.ConvertOptions{Options: options, Out: *out, ScaffoldPresenters: *scaffold,
			InPlace: *inPlace, Force: *force, RemoveTempl: *removeTempl, HandFinished: *handFinished, Bridges: *bridges})
	} else {
		inventory, err = templmigrate.Inventory(files, options)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := inventory.WriteJSON(*report, *nodes == "all"); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *summary != "" {
		if err := templmigrate.WriteSummary(*summary, inventory, options.Categories); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	t := inventory.Totals
	fmt.Fprintf(stdout, "%d files (%d excluded), %d nodes: %d converted, %d presentation_scaffolded, %d review_required, %d blocked; %d dropped, %d parse failures\n",
		t.Files+t.ExcludedFiles, t.ExcludedFiles, t.Total, t.Converted, t.PresentationScaffolded, t.ReviewRequired, t.Blocked, inventory.Dropped, t.ParseFailures)
	for _, problem := range inventory.TypeProblems {
		fmt.Fprintln(stderr, "types:", problem)
	}
	if conversion := inventory.Conversion; conversion != nil {
		fields := conversion.Fields
		fmt.Fprintf(stdout, "converted into %s: %d files written, %d templates, %d presenter fields (%d pass-through, %d derived, %d wiring), %d security markers, %d partial nodes\n",
			conversion.Out, len(conversion.Outputs), len(conversion.Templates), fields.Total, fields.PassThrough, fields.Derived, fields.Wiring, len(conversion.Security), conversion.Partial)
		for _, diagnostic := range conversion.Diagnostics {
			fmt.Fprintln(stderr, diagnostic)
		}
		for _, problem := range conversion.Validation {
			fmt.Fprintln(stderr, "ktml:", problem)
		}
		if len(conversion.Kept) > 0 {
			fmt.Fprintf(stdout, "kept %d existing outputs (--force overwrites them)\n", len(conversion.Kept))
		}
		if *removeTempl {
			if len(conversion.RemoveRefused) > 0 {
				for _, reason := range conversion.RemoveRefused {
					fmt.Fprintln(stderr, "not removing the templ files:", reason)
				}
				return 1
			}
			fmt.Fprintf(stdout, "removed %d templ files\n", len(conversion.Removed))
			if *handFinished && len(conversion.Validation) == 0 {
				return 0
			}
		}
		if conversion.Failed() {
			return 1
		}
	}
	if inventory.Failed() {
		return 1
	}
	return 0
}

// parseInterspersed parses flags that may follow positional arguments.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		args = flags.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
		if len(args) == 0 {
			return positional, nil
		}
	}
}

const codemodUsage = "usage: ktml migrate codemod <package pattern>... --map <conversion.json> --report <file.json> [--write] [--fragment-render <import path>.<Func>] [--root <dir>] [--tags <tag,...>]"

func codemod(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("codemod", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mapPath := flags.String("map", "", "the conversion report whose call mappings to apply")
	report := flags.String("report", "", "write the JSON codemod report to this file")
	write := flags.Bool("write", false, "apply the rewrites (default: report only)")
	fragment := flags.String("fragment-render", "", "buffered fragment renderer for direct renders into an http.ResponseWriter")
	root := flags.String("root", ".", "module directory the package patterns are relative to")
	tags := flags.String("tags", "", "comma-separated build tags to load the packages with (e.g. e2e)")
	patterns, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if *mapPath == "" || *report == "" || len(patterns) == 0 {
		fmt.Fprintln(stderr, codemodUsage)
		return 2
	}
	mappings, err := templmigrate.LoadCallMappings(*mapPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	result, err := templmigrate.Codemod(*root, patterns, mappings, templmigrate.CodemodOptions{Write: *write, FragmentRender: *fragment, Tags: splitTags(*tags)})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := result.WriteJSON(*report); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	t := result.Totals
	fmt.Fprintf(stdout, "%d sites: %d rewritable, %d rewritten, %d bridged, %d manual, %d deferred; %d files changed\n",
		len(result.Sites), t["rewritable"], t["rewritten"], t["bridged"], t["manual"], t["deferred"], len(result.Files))
	for _, site := range result.Sites {
		if site.Status == templmigrate.SiteManual {
			fmt.Fprintf(stderr, "%s:%d:%d: manual [%s] %s: %s\n", site.File, site.From.Line, site.From.Col, site.Kind, site.Template, site.Reason)
		}
	}
	for _, problem := range result.Problems {
		fmt.Fprintln(stderr, "problem:", problem)
	}
	if result.Failed() {
		return 1
	}
	return 0
}

// splitTags splits a comma-separated build tag list.
func splitTags(list string) []string {
	var tags []string
	for _, tag := range strings.Split(list, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}
