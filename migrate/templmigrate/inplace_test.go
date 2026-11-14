package templmigrate

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// copyModule copies a fixture module into a temporary directory that also
// requires KTML, so converted packages compile there.
func copyModule(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir(fixture, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, path)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
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
	ktmlRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.OpenFile(filepath.Join(dir, "go.mod"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mod.WriteString("\nrequire github.com/karl-tye/ktml v0.0.0-00010101000000-000000000000\n\nreplace github.com/karl-tye/ktml => " + ktmlRoot + "\n")
	if closeErr := mod.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func convertInPlace(t *testing.T, module string, options ConvertOptions, paths ...string) *Report {
	t.Helper()
	for i, path := range paths {
		paths[i] = filepath.Join(module, path)
	}
	files, err := ExpandPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	options.Root, options.InPlace, options.ScaffoldPresenters = module, true, true
	report, err := Convert(files, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return report
}

const fragmentRender = "example.com/inplace/app.Fragment"

// handFinished is the hand edit of the sites the codemod reports as
// manual.
const handFinished = `// Package manual holds the call sites the codemod must not rewrite,
// finished by hand.
package manual

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"example.com/inplace/widgets"
)

// Badges builds the component from the old template's arguments.
var Badges = func(text string, count int) templ.Component {
	return widgets.Badge{Text: text, Count: int64(count)}
}

// Greeting takes the context its presenter needs.
func Greeting(ctx context.Context) templ.Component {
	return widgets.Greeting{View: widgets.NewGreetingView(ctx, "Hi")}
}

// Panel passes children in the Children field.
func Panel(ctx context.Context, w io.Writer, body templ.Component) error {
	return widgets.Panel{View: widgets.NewPanelView(widgets.Item{ID: "p", Label: "Panel"}), Children: body}.Render(ctx, w)
}
`

func TestInPlace_GIVENTemplatesUsingUnexportedHelpers_WHENConvertedInPlace_THENNoNodeIsPartial(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	out, err := Convert(mustExpand(t, filepath.Join(module, "widgets")), ConvertOptions{Options: Options{Root: module}, Out: filepath.Join(module, "out"), ScaffoldPresenters: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Conversion.Partial == 0 {
		t.Fatal("--out: the unexported helper label should leave partial nodes")
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets")
	if report.Conversion.Partial != 0 || len(report.Conversion.Validation) > 0 {
		t.Fatalf("in place: %d partial nodes, validation %v, diagnostics %v", report.Conversion.Partial, report.Conversion.Validation, report.Conversion.Diagnostics)
	}
	for _, name := range []string{"widgets.ktml", "widgets.ktmlschema", "widgets_present.go", "widgets_bridge.go"} {
		if _, err := os.Stat(filepath.Join(module, "widgets", name)); err != nil {
			t.Errorf("in place: %s not written into the source package: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(module, ".ktml", "migrate", "widgets", "widgets.ktmlmap.json")); err != nil {
		t.Errorf("in place: no source map under .ktml/migrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(module, "widgets", "widgets.templ")); err != nil {
		t.Errorf("the templ file was removed without --remove-templ: %v", err)
	}
}

func TestInPlace_GIVENHandEditedOutputs_WHENConvertedAgain_THENTheyAreKeptUnlessForced(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	convertInPlace(t, module, ConvertOptions{}, "widgets")
	present := filepath.Join(module, "widgets", "widgets_present.go")
	edited := []byte("// edited by hand; ktml migrate templ\npackage widgets\n")
	if err := os.WriteFile(present, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets")
	if got, _ := os.ReadFile(present); !bytes.Equal(got, edited) {
		t.Error("an existing presenter was overwritten without --force")
	}
	if !slices.Contains(report.Conversion.Kept, "widgets/widgets_present.go") {
		t.Errorf("kept %v, want the presenter listed", report.Conversion.Kept)
	}
	convertInPlace(t, module, ConvertOptions{Force: true}, "widgets")
	if got, _ := os.ReadFile(present); bytes.Equal(got, edited) {
		t.Error("--force did not overwrite the presenter")
	}
}

func TestInPlace_GIVENABatchCallingAnUnconvertedTemplate_WHENConverted_THENTheCallIsAPartialDependencyNode(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	report := convertInPlace(t, module, ConvertOptions{}, "pages")
	found := 0
	for _, d := range report.Conversion.Diagnostics {
		if d.Code == "unconverted-dependency" && d.Severity == "partial" {
			found++
		}
	}
	if found != 4 {
		t.Errorf("unconverted-dependency diagnostics: %d, want 4 (Badge, List, Actions, Panel): %v", found, report.Conversion.Diagnostics)
	}
	report = convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "pages")
	if len(report.Conversion.Removed) > 0 || len(report.Conversion.RemoveRefused) == 0 {
		t.Errorf("an incomplete batch was removed: removed %v, refused %v", report.Conversion.Removed, report.Conversion.RemoveRefused)
	}
}

// TestInPlace_GIVENTheFixtureApp_WHENConvertedRewrittenAndRemoved_THENItBuildsAndRendersAsBefore
// runs a whole batch: in-place conversion, the codemod in report and write
// modes against golden files, the hand edit of the manual sites, templ
// removal, KTML generation, vet, and the app's own render tests, which
// assert templ's bytes. A second codemod run changes nothing.
func TestInPlace_GIVENTheFixtureApp_WHENConvertedRewrittenAndRemoved_THENItBuildsAndRendersAsBefore(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module")
	}
	module := copyModule(t, "testdata/inplace")
	generateTempl(t, module)
	goCommand(t, module, "test", "./...") // the app's render assertions hold for templ
	conversion := convertInPlace(t, module, ConvertOptions{}, "widgets")
	if conversion.Conversion.Failed() {
		t.Fatalf("conversion failed: %v %v", conversion.Conversion.Validation, conversion.Conversion.Diagnostics)
	}
	mappings := conversion.Conversion.Calls
	bridges := map[string]string{}
	for _, m := range mappings {
		bridges[m.Template] = m.Bridge
	}
	if bridges["Badge"] != "BadgeTempl" || bridges["List"] != "ListTempl" || bridges["Panel"] != "PanelTempl" || bridges["Actions"] != "ActionsTempl" || bridges["Stamp"] != "" {
		t.Errorf("bridges %v: want one for each template pages.templ calls, and none other", bridges)
	}

	before := snapshot(t, module)
	report, err := Codemod(module, []string{"./..."}, mappings, CodemodOptions{FragmentRender: fragmentRender})
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, module); !equalSnapshots(before, after) {
		t.Error("report mode changed files")
	}
	wantTotals := map[string]int{"rewritable": 10, "bridged": 4, "manual": 4, "deferred": 6}
	for status, want := range wantTotals {
		if report.Totals[status] != want {
			t.Errorf("report mode: %d %s sites, want %d (%v)", report.Totals[status], status, want, report.Totals)
		}
	}
	manual := map[string]string{}
	for _, site := range report.Sites {
		if site.Status == SiteManual {
			manual[site.Kind+" "+site.Template] = site.Reason
			if site.File != "app/manual/manual.go" || site.From.Line == 0 || site.To.Col == 0 {
				t.Errorf("manual site without its source range in manual.go: %+v", site)
			}
		}
	}
	for _, key := range []string{"value example.com/inplace/widgets.Badge", "call example.com/inplace/widgets.Greeting", "call example.com/inplace/widgets.Panel", "with-children "} {
		if manual[key] == "" {
			t.Errorf("no manual site %q: %v", key, manual)
		}
	}

	report, err = Codemod(module, []string{"./..."}, mappings, CodemodOptions{Write: true, FragmentRender: fragmentRender})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals["rewritten"] != 10 || len(report.Problems) > 0 {
		t.Fatalf("write mode: %v, problems %v", report.Totals, report.Problems)
	}
	compareGolden(t, module, "testdata/inplace.golden")
	if got, _ := os.ReadFile(filepath.Join(module, "app", "manual", "manual.go")); !bytes.Equal(got, mustRead(t, "testdata/inplace/app/manual/manual.go")) {
		t.Error("a manual site was edited")
	}

	// The batch is not complete until the manual sites are finished.
	refused := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets")
	if len(refused.Conversion.Removed) > 0 || len(refused.Conversion.RemoveRefused) != 3 {
		t.Fatalf("removal with manual sites left: removed %v, refused %v", refused.Conversion.Removed, refused.Conversion.RemoveRefused)
	}
	if err := os.WriteFile(filepath.Join(module, "app", "manual", "manual.go"), []byte(handFinished), 0o644); err != nil {
		t.Fatal(err)
	}
	removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets")
	if len(removed.Conversion.RemoveRefused) > 0 || len(removed.Conversion.Removed) != 2 {
		t.Fatalf("removal: removed %v, refused %v", removed.Conversion.Removed, removed.Conversion.RemoveRefused)
	}

	generateKTMLIn(t, module)
	goCommand(t, module, "vet", "./...")
	goCommand(t, module, "test", "./...")
	gofmtClean(t, module)

	// The next wave converts the templ page that calls the widgets through
	// their bridges: the calls become KTML component calls again.
	wave := convertInPlace(t, module, ConvertOptions{}, "pages")
	if wave.Conversion.Failed() {
		t.Fatalf("second wave: %d partial nodes, validation %v, diagnostics %v", wave.Conversion.Partial, wave.Conversion.Validation, wave.Conversion.Diagnostics)
	}
	document := string(mustRead(t, filepath.Join(module, "pages", "pages.ktml")))
	for _, want := range []string{`import { Actions, Badge, List, Panel } from "../widgets/widgets.ktml"`, "Badge text=", "List view=", "Panel view="} {
		if !strings.Contains(document, want) {
			t.Errorf("pages.ktml lacks %q:\n%s", want, document)
		}
	}
	waveCalls := wave.Conversion.Calls
	if _, err := Codemod(module, []string{"./..."}, waveCalls, CodemodOptions{Write: true, FragmentRender: fragmentRender}); err != nil {
		t.Fatal(err)
	}
	if removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "pages"); len(removed.Conversion.Removed) != 2 {
		t.Fatalf("second wave removal: removed %v, refused %v", removed.Conversion.Removed, removed.Conversion.RemoveRefused)
	}
	generateKTMLIn(t, module)
	goCommand(t, module, "vet", "./...")
	goCommand(t, module, "test", "./...")
	mappings = append(mappings, waveCalls...)

	settled := snapshot(t, module)
	again, err := Codemod(module, []string{"./..."}, mappings, CodemodOptions{Write: true, FragmentRender: fragmentRender})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Files) > 0 || again.Totals["rewritten"] > 0 || !equalSnapshots(settled, snapshot(t, module)) {
		t.Errorf("the codemod is not idempotent: changed %v (%v)", again.Files, again.Totals)
	}
}

func TestCallMapping_GIVENArguments_WHENSpelled_THENBuildsTheHybridHLiteral(t *testing.T) {
	view := CallMapping{Component: "Card", Mode: "view", ViewField: "View", Presenter: "NewCardView", Context: true, Clock: true,
		Params: []CallParam{{Name: "title", Kind: "presenter"}, {Name: "icon", Kind: "content", Field: "Icon"}, {Name: "attrs", Kind: "attributes", Field: "Attrs"}}}
	direct := CallMapping{Component: "Badge", Mode: "direct", Variadic: true,
		Params: []CallParam{{Name: "n", Kind: "value", Field: "N", Scalar: "integer"}, {Name: "parts", Kind: "contents", Field: "Parts"}}}
	join := func(parts []string) string { return "ktml.Join(" + strings.Join(parts, ", ") + ")" }
	tests := []struct {
		name    string
		m       CallMapping
		args    []string
		spread  bool
		ctx     string
		want    string
		wantErr string
	}{
		{"view with context, clock, and nil content", view, []string{"t", "nil", "nil"}, false, "r.Context()", `ui.Card{View: ui.NewCardView(r.Context(), t, time.Now())}`, ""},
		{"view with content", view, []string{"t", "icon", "nil"}, false, "ctx", `ui.Card{View: ui.NewCardView(ctx, t, time.Now()), Icon: icon}`, ""},
		{"no context in scope", view, []string{"t", "nil", "nil"}, false, "", "", "request context"},
		{"templ attributes", view, []string{"t", "nil", "attrs"}, false, "ctx", "", "templ.Attributes"},
		{"integer literal and joined content", direct, []string{"3", "a", "b"}, false, "", `ui.Badge{N: 3, Parts: ktml.Join(a, b)}`, ""},
		{"integer expression and one content", direct, []string{"len(xs)", "a"}, false, "", `ui.Badge{N: int64(len(xs)), Parts: a}`, ""},
		{"no content", direct, []string{"1"}, false, "", `ui.Badge{N: 1}`, ""},
		{"spread content", direct, []string{"1", "parts"}, true, "", "", "spread"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.m.Literal("ui.", "", tt.args, tt.spread, tt.ctx, "time.Now()", join, nil)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %q, %v; want an error about %s", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func mustExpand(t *testing.T, paths ...string) []string {
	t.Helper()
	files, err := ExpandPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// snapshot reads every file of a module except build state.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[rel] = string(mustRead(t, path))
		return nil
	})
	return files
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, content := range a {
		if b[name] != content {
			return false
		}
	}
	return true
}

// compareGolden checks every <path>.golden file against the module's
// <path>. KTML_UPDATE_GOLDEN=1 rewrites them.
func compareGolden(t *testing.T, module, golden string) {
	t.Helper()
	var names []string
	_ = filepath.WalkDir(golden, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".golden") {
			names = append(names, path)
		}
		return err
	})
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no golden files")
	}
	for _, name := range names {
		rel, _ := filepath.Rel(golden, strings.TrimSuffix(name, ".golden"))
		got, err := os.ReadFile(filepath.Join(module, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if os.Getenv("KTML_UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(name, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if want := mustRead(t, name); !bytes.Equal(got, want) {
			t.Errorf("%s differs from its golden file:\n--- got\n%s\n--- want\n%s", rel, got, want)
		}
	}
}

// generateKTMLIn generates the Go of every .ktml and .ktmlschema under dir.
func generateKTMLIn(t *testing.T, dir string) {
	t.Helper()
	generateKTMLRoot(t, dir)
}

func formatGo(data []byte) ([]byte, error) { return format.Source(data) }

func gofmtClean(t *testing.T, dir string) {
	t.Helper()
	var unformatted []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_templ.go") {
			return err
		}
		data := mustRead(t, path)
		if formatted, err := formatGo(data); err != nil || !bytes.Equal(formatted, data) {
			rel, _ := filepath.Rel(dir, path)
			unformatted = append(unformatted, rel)
		}
		return nil
	})
	if len(unformatted) > 0 {
		t.Errorf("not gofmt'd: %v", unformatted)
	}
}

func TestCodemod_GIVENComponentInputsChangedByHand_WHENScanned_THENTheirCallSitesAreManual(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	conversion := convertInPlace(t, module, ConvertOptions{}, "widgets")
	document := filepath.Join(module, "widgets", "widgets.ktml")
	edited := strings.NewReplacer(
		"Stamp [view: StampView]", "Stamp [view: StampView, extra?: content]",
		"Badge [text: string, count: integer]", "Badge [label: string, count: integer]",
	).Replace(string(mustRead(t, document)))
	if err := os.WriteFile(document, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Codemod(module, []string{"./app"}, conversion.Conversion.Calls, CodemodOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, site := range report.Sites {
		if site.Kind == "call" {
			if site.Status == SiteManual {
				reasons[site.Template] = site.Reason
			} else if _, seen := reasons[site.Template]; !seen {
				reasons[site.Template] = ""
			}
		}
	}
	if reason := reasons["example.com/inplace/widgets.Stamp"]; !strings.Contains(reason, "extra") {
		t.Errorf("Stamp gained an input by hand; reason %q", reason)
	}
	if reason := reasons["example.com/inplace/widgets.Badge"]; !strings.Contains(reason, "text") || !strings.Contains(reason, "label") {
		t.Errorf("Badge's input was renamed by hand; reason %q", reason)
	}
	if reason := reasons["example.com/inplace/widgets.List"]; reason != "" {
		t.Errorf("List is unchanged, but its site is manual: %s", reason)
	}
}

func TestHideConverterOutputs_GIVENAPartlyConvertedPackage_WHENLoaded_THENOnlyOutputsOfRemainingTemplFilesAreHidden(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.templ":            "package p\n",
		"a_present.go":       "// Presenters converted from p/a.templ by ktml migrate templ.\npackage p\n",
		"a_bridge.go":        "// Bridges converted from p/a.templ by ktml migrate templ.\npackage p\n",
		"a_ktml.go":          "// Code generated by KTML. DO NOT EDIT.\npackage p\n",
		"b_present.go":       "// Presenters converted from p/b.templ by ktml migrate templ.\npackage p\n",
		"b_ktml.go":          "// Code generated by KTML. DO NOT EDIT.\npackage p\n",
		"p_ktmlschema.go":    "// Code generated by KTML from p.ktmlschema. DO NOT EDIT.\npackage p\n",
		"migrate_helpers.go": "// Presenter helpers written by ktml migrate templ.\npackage p\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	overlay := map[string][]byte{}
	hideConverterOutputs(map[string]bool{dir: true}, overlay)
	var hidden []string
	for path := range overlay {
		hidden = append(hidden, filepath.Base(path))
	}
	sort.Strings(hidden)
	if want := []string{"a_bridge.go", "a_ktml.go", "a_present.go"}; !slices.Equal(hidden, want) {
		t.Errorf("hidden %v, want %v: b.templ is converted and gone, and the schema and helpers collide with no templ file", hidden, want)
	}
}

func TestInPlace_GIVENAPackageConvertedInAnEarlierBatch_WHENAnotherOfItsFilesConverts_THENItsSchemaAndHelpersGainTheNewDeclarations(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	convertInPlace(t, module, ConvertOptions{}, "widgets/widgets.templ")
	schema := filepath.Join(module, "widgets", "widgets.ktmlschema")
	earlier := mustRead(t, schema)
	tags := "package widgets\n\n// Tags needs a record and the class helper.\ntempl Tags(items []Item, active bool) {\n\t<ul class={ \"tags\", templ.KV(\"active\", active), templ.KV(\"empty\", len(items) == 0) } x-data={ templ.JSONString(items) } data-active?={ active }>\n\t\tfor _, item := range items {\n\t\t\t<li id={ item.ID }>{ item.Label }</li>\n\t\t}\n\t</ul>\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "tags.templ"), []byte(tags), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/tags.templ")
	if len(report.Conversion.Validation) > 0 {
		t.Fatalf("the second batch does not validate: %v", report.Conversion.Validation)
	}
	merged := string(mustRead(t, schema))
	if !strings.HasPrefix(merged, string(earlier)) {
		t.Errorf("the earlier batch's schema was not kept as written:\n%s", merged)
	}
	if !strings.Contains(merged, "record TagsView [") {
		t.Errorf("the schema lacks the second batch's records:\n%s", merged)
	}
	if strings.Count(merged, "record ListView [") != 1 {
		t.Errorf("an earlier record was repeated:\n%s", merged)
	}
	helpers := string(mustRead(t, filepath.Join(module, "widgets", "migrate_helpers.go")))
	if !strings.Contains(helpers, "func migrateClasses(") || !strings.Contains(helpers, "func migrateJoin[") {
		t.Errorf("the helpers lack migrateClasses or the earlier migrateJoin:\n%s", helpers)
	}
	// A record renamed by hand is not added again when the batch runs again.
	handEdited := strings.ReplaceAll(merged, "TagsItem", "TagsRow")
	if err := os.WriteFile(schema, []byte(handEdited), 0o644); err != nil {
		t.Fatal(err)
	}
	document := filepath.Join(module, "widgets", "tags.ktml")
	if err := os.WriteFile(document, []byte(strings.ReplaceAll(string(mustRead(t, document)), "TagsItem", "TagsRow")), 0o644); err != nil {
		t.Fatal(err)
	}
	merged = handEdited
	again := convertInPlace(t, module, ConvertOptions{}, "widgets/tags.templ")
	if got := string(mustRead(t, schema)); got != merged {
		t.Errorf("a second run changed the merged schema:\n%s", got)
	}
	if len(again.Conversion.Validation) > 0 {
		t.Fatalf("a second run does not validate: %v", again.Conversion.Validation)
	}
}

func TestCallMapping_GIVENANilAttributesArgument_WHENTheCodemodSpellsIt_THENTheBagIsGivenEmpty(t *testing.T) {
	m := CallMapping{Component: "Bar", Mode: "direct", Params: []CallParam{{Name: "label", Kind: "value", Field: "Label"}, {Name: "attrs", Kind: "attributes", Field: "Attrs"}}}
	empty := func(arg string) (string, error) {
		if arg != "nil" {
			return "", fmt.Errorf("the templ.Attributes argument needs a ktml.Attributes")
		}
		return "ktml.Attributes{}", nil
	}
	got, err := m.Literal("ui.", "", []string{`"x"`, "nil"}, false, "", "", nil, empty)
	if want := `ui.Bar{Label: "x", Attrs: ktml.Attributes{}}`; err != nil || got != want {
		t.Errorf("got %q, %v; want %q (ktml vet requires every input, attribute bags included)", got, err, want)
	}
	if _, err := m.Literal("ui.", "", []string{`"x"`, "attrs"}, false, "", "", nil, empty); err == nil {
		t.Error("a non-nil templ.Attributes argument was rewritten")
	}
}

func TestCodemod_GIVENACallInABuildTaggedFile_WHENRunWithItsTag_THENItIsRewritten(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	report := convertInPlace(t, module, ConvertOptions{}, "widgets")
	tagged := filepath.Join(module, "app", "e2e_test.go")
	source := "//go:build e2e\n\npackage app\n\nimport (\n\t\"context\"\n\t\"io\"\n\n\t\"example.com/inplace/widgets\"\n)\n\nfunc badge(w io.Writer) error { return widgets.Badge(\"e2e\", 1).Render(context.Background(), w) }\n"
	if err := os.WriteFile(tagged, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Codemod(module, []string{"./..."}, report.Conversion.Calls, CodemodOptions{Write: true}); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, tagged)); got != source {
		t.Fatalf("a file outside the default build was rewritten without its tag:\n%s", got)
	}
	if _, err := Codemod(module, []string{"./..."}, report.Conversion.Calls, CodemodOptions{Write: true, Tags: []string{"e2e"}}); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, tagged)); !strings.Contains(got, `widgets.Badge{Text: "e2e", Count: 1}`) {
		t.Errorf("the tagged call was not rewritten:\n%s", got)
	}
}

func TestInPlace_GIVENAnUnexportedTemplateASamePackageTemplFileCalls_WHENConverted_THENItGetsABridge(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"secret.templ": "package widgets\n\ntempl secret(text string) {\n\t<i>{ text }</i>\n}\n",
		"user.templ":   "package widgets\n\ntempl UsesSecret() {\n\t@secret(\"a\")\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/secret.templ")
	bridge := ""
	for _, m := range report.Conversion.Calls {
		if m.Template == "secret" {
			bridge = m.Bridge
		}
	}
	if bridge != "secretTempl" {
		t.Fatalf("bridge %q, want secretTempl: user.templ in the same package still calls secret", bridge)
	}
	if got := string(mustRead(t, filepath.Join(module, "widgets", "secret_bridge.go"))); !strings.Contains(got, "func secretTempl(text string) templ.Component") {
		t.Errorf("no unexported bridge:\n%s", got)
	}
}

func TestInPlace_GIVENATemplFileDeclaringAGoTypeNamedLikeAView_WHENConverted_THENTheRecordTakesAnotherName(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\ntype NoteView struct {\n\tTitle string\n}\n\ntempl Note(view NoteView) {\n\t<p title={ view.Title + \"!\" }>{ view.Title }</p>\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "note.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/note.templ")
	schema := string(mustRead(t, filepath.Join(module, "widgets", "widgets.ktmlschema")))
	if strings.Contains(schema, "record NoteView [") {
		t.Fatalf("the record NoteView collides with the Go type the presenter keeps:\n%s", schema)
	}
	if len(report.Conversion.Validation) > 0 {
		t.Errorf("validation: %v", report.Conversion.Validation)
	}
}

func TestInPlace_GIVENIfInitializersThatReuseLocalNames_WHENConverted_THENEachBranchGetsItsOwnFields(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\ntempl Errors(errs map[string]string) {\n\tif err, ok := errs[\"name\"]; ok {\n\t\t<p>{ err }</p>\n\t}\n\tif err, ok := errs[\"phone\"]; ok {\n\t\t<p>{ err }</p>\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "errors.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/errors.templ")
	document := string(mustRead(t, filepath.Join(module, "widgets", "errors.ktml")))
	if strings.Count(document, "if view.ok {") == 2 {
		t.Fatalf("both branches read one field, so the last initializer wins:\n%s", document)
	}
}

func TestInPlace_GIVENABridgedTemplateWithATimeParameter_WHENConverted_THENItsBridgeImportsTime(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"when.templ":   "package widgets\n\nimport \"time\"\n\ntempl When(at time.Time) {\n\t<time>{ at.Format(\"2006\") }</time>\n}\n",
		"caller.templ": "package widgets\n\nimport \"time\"\n\ntempl CallsWhen() {\n\t@When(time.Time{})\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/when.templ")
	if got := string(mustRead(t, filepath.Join(module, "widgets", "when_bridge.go"))); !strings.Contains(got, "\t\"time\"\n") {
		t.Errorf("the bridge's time.Time parameter has no import:\n%s", got)
	}
}

func TestInPlace_GIVENLoopsKeyedByIDFields_WHENConverted_THENEachIdentityKeyIsFlaggedAndNoPointerIsPrinted(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\ntype Ref struct {\n\tID *string\n\tName string\n}\n\ntempl Rows(items []Item, refs []Ref) {\n\tfor _, item := range items {\n\t\t<p>{ item.Label }</p>\n\t}\n\tfor _, ref := range refs {\n\t\t<p>{ ref.Name }</p>\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "rows.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/rows.templ")
	presenter := string(mustRead(t, filepath.Join(module, "widgets", "rows_present.go")))
	if strings.Contains(presenter, "Sprint(ref.ID)") {
		t.Errorf("the pointer ID is keyed by fmt.Sprint, which prints <nil> for every nil ID:\n%s", presenter)
	}
	var identity, pointer int
	for _, d := range report.Conversion.Diagnostics {
		if d.File != "widgets/rows.templ" || d.Severity != "review" {
			continue
		}
		switch d.Code {
		case "identity-key":
			identity++
			if !strings.Contains(d.Message, "item.ID") || !strings.Contains(d.Message, "unique and non-empty") {
				t.Errorf("identity-key message %q names neither the key nor what to confirm", d.Message)
			}
		case "position-key":
			pointer++
		}
	}
	if identity != 1 || pointer != 1 {
		t.Errorf("review diagnostics: %d identity-key (want 1, item.ID), %d position-key (want 1, ref.ID is a pointer)\n%v", identity, pointer, report.Conversion.Diagnostics)
	}
	if !strings.Contains(presenter, "TODO(ktml-migrate): confirm item.ID is unique and non-empty") {
		t.Errorf("the presenter does not mark the identity key for review:\n%s", presenter)
	}
	if len(report.Conversion.Validation) > 0 {
		t.Errorf("validation: %v", report.Conversion.Validation)
	}
}

func TestInPlace_GIVENAKeptOutputTheConverterNowEmitsDifferently_WHENConvertedAgain_THENItIsReportedAndRemovalWaits(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	convertInPlace(t, module, ConvertOptions{}, "widgets")
	present := filepath.Join(module, "widgets", "widgets_present.go")
	pristine := filepath.Join(module, ".ktml", "migrate", "kept", "widgets", "widgets_present.go.pristine")
	fresh := filepath.Join(module, ".ktml", "migrate", "kept", "widgets", "widgets_present.go.fresh")
	if !bytes.Equal(mustRead(t, pristine), mustRead(t, present)) {
		t.Fatal("the first conversion did not record what it emitted")
	}
	// A hand-finished presenter, converted by an older converter.
	handFinished := append(mustRead(t, present), "\n// finished by hand\n"...)
	if err := os.WriteFile(present, handFinished, 0o644); err != nil {
		t.Fatal(err)
	}
	unchanged := convertInPlace(t, module, ConvertOptions{}, "widgets")
	if len(unchanged.Conversion.Outdated) > 0 {
		t.Fatalf("a hand edit alone is reported outdated: %v", unchanged.Conversion.Outdated)
	}
	older := bytes.Replace(mustRead(t, pristine), []byte("var v ListView"), []byte("var v ListView // older"), 1)
	if err := os.WriteFile(pristine, older, 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{RemoveTempl: true, HandFinished: true}, "widgets")
	if !slices.Contains(report.Conversion.Outdated, "widgets/widgets_present.go") {
		t.Fatalf("outdated %v, want the presenter", report.Conversion.Outdated)
	}
	if !bytes.Equal(mustRead(t, present), handFinished) {
		t.Error("the kept presenter was overwritten")
	}
	if strings.Contains(string(mustRead(t, fresh)), "// older") {
		t.Error("the fresh copy is not the converter's current output")
	}
	if len(report.Conversion.Removed) > 0 || !slices.ContainsFunc(report.Conversion.RemoveRefused, func(s string) bool { return strings.Contains(s, "widgets/widgets_present.go") }) {
		t.Errorf("removed %v, refused %v: removal must wait for the outdated output", report.Conversion.Removed, report.Conversion.RemoveRefused)
	}
	found := false
	for _, d := range report.Conversion.Diagnostics {
		found = found || d.Code == "kept-output-outdated"
	}
	if !found {
		t.Error("no kept-output-outdated diagnostic")
	}
	// Merged by hand: the fresh copy becomes the pristine one.
	if err := os.WriteFile(pristine, mustRead(t, fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := convertInPlace(t, module, ConvertOptions{}, "widgets"); len(again.Conversion.Outdated) > 0 {
		t.Errorf("still outdated after the merge: %v", again.Conversion.Outdated)
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Error("the fresh copy of a merged output is left behind")
	}
}

func TestInPlace_GIVENALaterFileCallingAnUnexportedBridge_WHENConverted_THENItCallsTheKTMLComponent(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"secret.templ": "package widgets\n\ntempl secret(text string) {\n\t<i>{ text }</i>\n}\n",
		"user.templ":   "package widgets\n\ntempl UsesSecret() {\n\t@secret(\"a\")\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first := convertInPlace(t, module, ConvertOptions{}, "widgets/secret.templ")
	if _, err := Codemod(module, []string{"./..."}, first.Conversion.Calls, CodemodOptions{Write: true, FragmentRender: fragmentRender}); err != nil {
		t.Fatal(err)
	}
	if removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets/secret.templ"); len(removed.Conversion.Removed) == 0 {
		t.Fatalf("removal: removed %v, refused %v", removed.Conversion.Removed, removed.Conversion.RemoveRefused)
	}
	generateKTMLIn(t, module)
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/user.templ")
	for _, d := range report.Conversion.Diagnostics {
		if d.Severity == "partial" {
			t.Errorf("partial: %s", d)
		}
	}
	if document := string(mustRead(t, filepath.Join(module, "widgets", "user.ktml"))); !strings.Contains(document, "secret") || strings.Contains(document, "TODO") {
		t.Errorf("the call of the unexported bridge is not a KTML call:\n%s", document)
	}
}

func TestInPlace_GIVENABridgedTemplateWithAParameterOfAnotherPackage_WHENConverted_THENItsBridgeImportsIt(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"line.templ":   "package pages\n\nimport \"example.com/inplace/widgets\"\n\ntempl ItemLine(item widgets.Item) {\n\t<p>{ item.Label }</p>\n}\n",
		"caller.templ": "package pages\n\nimport \"example.com/inplace/widgets\"\n\ntempl CallsItemLine() {\n\t@ItemLine(widgets.Item{})\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "pages", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	convertInPlace(t, module, ConvertOptions{}, "pages/line.templ")
	if got := string(mustRead(t, filepath.Join(module, "pages", "line_bridge.go"))); !strings.Contains(got, "\t\"example.com/inplace/widgets\"\n") {
		t.Errorf("the bridge's widgets.Item parameter has no import:\n%s", got)
	}
}

func TestInPlace_GIVENABatchAlreadyConvertedAndGenerated_WHENConvertedAgain_THENItsRecordsKeepTheirNames(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	if err := os.WriteFile(filepath.Join(module, "widgets", "note.templ"), []byte("package widgets\n\ntempl Note(text string) {\n\t<p title={ text + \"!\" }>{ text }</p>\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/note.templ")
	generateKTMLIn(t, module)
	again := convertInPlace(t, module, ConvertOptions{}, "widgets/note.templ")
	if len(again.Conversion.Outdated) > 0 {
		t.Errorf("a rerun with the same converter reports outdated outputs: %v", again.Conversion.Outdated)
	}
	for _, f := range []string{"note.ktml.fresh", "note_present.go.fresh"} {
		if data, err := os.ReadFile(filepath.Join(module, ".ktml", "migrate", "kept", "widgets", f)); err == nil {
			t.Errorf("%s:\n%s", f, data)
		}
	}
}

func TestInPlace_GIVENTemplCallersAlreadyRenamedToTheBridge_WHENConvertedAgain_THENTheBridgeIsStillEmittedAndChecked(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"widgets/line.templ": "package widgets\n\ntempl ItemLine(item Item) {\n\t<p>{ item.Label }</p>\n}\n",
		"pages/caller.templ": "package pages\n\nimport \"example.com/inplace/widgets\"\n\ntempl CallsLine() {\n\t@widgets.ItemLine(widgets.Item{})\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, filepath.FromSlash(name)), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first := convertInPlace(t, module, ConvertOptions{}, "widgets/line.templ")
	if _, err := Codemod(module, []string{"./..."}, first.Conversion.Calls, CodemodOptions{Write: true, FragmentRender: fragmentRender}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(module, "pages", "caller.templ"))), "@widgets.ItemLineTempl(") {
		t.Fatal("the codemod did not rename the templ call site")
	}
	again := convertInPlace(t, module, ConvertOptions{}, "widgets/line.templ")
	if !slices.Contains(again.Conversion.Kept, "widgets/line_bridge.go") {
		t.Errorf("the bridge the renamed caller still uses is not emitted (and so not checked) again: kept %v", again.Conversion.Kept)
	}
}

func TestInPlace_GIVENAValuelessAttributeBeforeInlineText_WHENConverted_THENTheTextStaysAChild(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	if err := os.WriteFile(filepath.Join(module, "widgets", "status.templ"), []byte("package widgets\n\ntempl Status() {\n\t<select><option value=\"paid\" selected>Paid</option></select>\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/status.templ")
	if document := string(mustRead(t, filepath.Join(module, "widgets", "status.ktml"))); strings.Contains(document, `selected "Paid"`) {
		t.Errorf("name \"value\" is an attribute value in KTML, so the option renders selected=\"Paid\" and no text:\n%s", document)
	}
}

func TestInPlace_GIVENAGoBlockWithAMultiLineRawString_WHENMovedIntoThePresenter_THENTheStringKeepsItsText(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	// The block sits deeper in the templ markup (3 tabs) than its
	// presenter statement will (1 tab).
	source := "package widgets\n\nimport \"fmt\"\n\ntempl Data(id string) {\n\t<section>\n\t\t<div>\n\t\t\t{{ data := fmt.Sprintf(`{\n\t\t\tid: '%s',\n\t\t\tready: false,\n\t\t}`, id) }}\n\t\t\t<div x-data={ data }></div>\n\t\t</div>\n\t</section>\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "data.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/data.templ")
	presenter := string(mustRead(t, filepath.Join(module, "widgets", "data_present.go")))
	if !strings.Contains(presenter, "fmt.Sprintf(`{\n\t\t\tid: '%s',\n\t\t\tready: false,\n\t\t}`, id)") {
		t.Errorf("the raw string's lines were re-indented, which changes the rendered text:\n%s", presenter)
	}
}

func TestInPlace_GIVENAKeptDocumentEditedIntoAnInvalidOne_WHENTheBatchRunsAgain_THENValidationFailsAndTheTemplFilesStay(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	convertInPlace(t, module, ConvertOptions{}, "widgets")
	document := filepath.Join(module, "widgets", "widgets.ktml")
	source := string(mustRead(t, document))
	field := regexp.MustCompile(`\{view\.[A-Za-z0-9]+\}`).FindString(source)
	if field == "" {
		t.Fatalf("no view field in:\n%s", source)
	}
	if err := os.WriteFile(document, []byte(strings.Replace(source, field, "{view.removedByHand}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{RemoveTempl: true, HandFinished: true}, "widgets")
	if !slices.ContainsFunc(report.Conversion.Validation, func(s string) bool { return strings.Contains(s, "removedByHand") }) {
		t.Errorf("validation %v does not report the kept document's unknown field", report.Conversion.Validation)
	}
	if len(report.Conversion.Removed) > 0 {
		t.Errorf("removed %v although the kept KTML does not validate", report.Conversion.Removed)
	}
}

func TestInPlace_GIVENAClockReadingTemplateWithAVariadicParameter_WHENConverted_THENTheClockComesBeforeTheVariadic(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"ago.templ":    "package widgets\n\nimport \"time\"\n\ntempl Ago(label string, offsets ...int) {\n\t<time>{ label } { time.Now().Add(time.Duration(len(offsets))).UTC().Format(\"2006\") }</time>\n}\n\ntempl CallsAgo() {\n\t@Ago(\"a\", 1, 2)\n}\n",
		"caller.templ": "package widgets\n\ntempl StillTempl() {\n\t@Ago(\"b\", 3)\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/ago.templ")
	for _, name := range []string{"ago_present.go", "ago_bridge.go"} {
		source := mustRead(t, filepath.Join(module, "widgets", name))
		if _, err := formatGo(source); err != nil {
			t.Errorf("%s is not valid Go: %v\n%s", name, err, source)
		}
	}
	if presenter := string(mustRead(t, filepath.Join(module, "widgets", "ago_present.go"))); !strings.Contains(presenter, "func NewAgoView(label string, now time.Time, offsets ...int) AgoView") || !strings.Contains(presenter, `NewAgoView("a", now, 1, 2)`) {
		t.Errorf("the presenter or its caller does not take the clock before the variadic:\n%s", presenter)
	}
	var mapping CallMapping
	for _, m := range report.Conversion.Calls {
		if m.Template == "Ago" {
			mapping = m
		}
	}
	got, err := mapping.Literal("widgets.", "", []string{`"c"`, "4", "5"}, false, "", "time.Now()", nil, nil)
	if want := `widgets.Ago{View: widgets.NewAgoView("c", time.Now(), 4, 5)}`; err != nil || got != want {
		t.Errorf("codemod literal %q, %v; want %q", got, err, want)
	}
	got, err = mapping.Literal("widgets.", "", []string{`"c"`, "xs"}, true, "", "time.Now()", nil, nil)
	if want := `widgets.Ago{View: widgets.NewAgoView("c", time.Now(), xs...)}`; err != nil || got != want {
		t.Errorf("codemod spread literal %q, %v; want %q", got, err, want)
	}
}

func TestInPlace_GIVENAnEarlierBatchWhoseKTMLGoIsNotGenerated_WHENACallerConverts_THENTheBridgeCallIsAKTMLCall(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			module := copyModule(t, "testdata/inplace")
			for name, source := range map[string]string{
				"secret.templ": "package widgets\n\nimport \"strings\"\n\ntempl Secret(text string) {\n\t<i>{ strings.ToUpper(text) }</i>\n}\n",
				"user.templ":   "package widgets\n\ntempl UsesSecret() {\n\t@Secret(\"a\")\n}\n",
			} {
				if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			first := convertInPlace(t, module, ConvertOptions{}, "widgets/secret.templ")
			if _, err := Codemod(module, []string{"./..."}, first.Conversion.Calls, CodemodOptions{Write: true, FragmentRender: fragmentRender}); err != nil {
				t.Fatal(err)
			}
			if removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets/secret.templ"); len(removed.Conversion.Removed) == 0 {
				t.Fatalf("removal: refused %v", removed.Conversion.RemoveRefused)
			}
			// No ktml generate: the earlier batch's records have no Go yet.
			report := convertInPlace(t, module, ConvertOptions{Force: force}, "widgets/user.templ")
			for _, d := range report.Conversion.Diagnostics {
				if d.Severity == "partial" {
					t.Errorf("partial: %s", d)
				}
			}
			if document := string(mustRead(t, filepath.Join(module, "widgets", "user.ktml"))); !strings.Contains(document, "Secret view=") || strings.Contains(document, "TODO") {
				t.Errorf("the bridge call is not a KTML call:\n%s", document)
			}
		})
	}
}

func TestInPlace_GIVENAParameterNamedV_WHENItsPresenterIsWritten_THENTheViewVariableDoesNotShadowIt(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\ntempl Dash(v Item) {\n\t<p>{ label(v) }</p>\n\tif v.Done {\n\t\t<i>done</i>\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "dash.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/dash.templ")
	if report.Conversion.Failed() {
		t.Fatalf("conversion failed: %v", report.Conversion.Diagnostics)
	}
	if removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets/dash.templ"); len(removed.Conversion.Removed) == 0 {
		t.Fatalf("removal: refused %v", removed.Conversion.RemoveRefused)
	}
	generateKTMLIn(t, module)
	goCommand(t, module, "vet", "./widgets")
}

func TestInPlace_GIVENAnEarlierBatchWithAStoryDocument_WHENACallerConverts_THENTheStoryAddsNoTypeProblem(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"secret.templ": "package widgets\n\nimport \"strings\"\n\ntempl Secret(text string) {\n\t<i>{ strings.ToUpper(text) }</i>\n}\n",
		"user.templ":   "package widgets\n\ntempl UsesSecret() {\n\t@Secret(\"a\")\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first := convertInPlace(t, module, ConvertOptions{}, "widgets/secret.templ")
	if _, err := Codemod(module, []string{"./..."}, first.Conversion.Calls, CodemodOptions{Write: true, FragmentRender: fragmentRender}); err != nil {
		t.Fatal(err)
	}
	if removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets/secret.templ"); len(removed.Conversion.Removed) == 0 {
		t.Fatalf("removal: refused %v", removed.Conversion.RemoveRefused)
	}
	// ktml generate writes no Go for a story Document (ktml stories
	// generate writes them beside the components).
	story := "import { Secret } from \"./secret.ktml\"\n\nstory Secret.Plain [view = [text = \"a\"]]\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "secret.story.ktml"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/user.templ")
	if len(report.TypeProblems) > 0 {
		t.Errorf("type problems: %v", report.TypeProblems)
	}
}

func TestInPlace_GIVENABridgeFillingAnExtraSlot_WHENItsTemplCallerConverts_THENTheSlotIsReportedNotDropped(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	for name, source := range map[string]string{
		"secret.templ": "package widgets\n\nimport \"strings\"\n\ntempl Secret(text string) {\n\t<i>{ strings.ToUpper(text) }</i>\n}\n",
		"user.templ":   "package widgets\n\ntempl UsesSecret() {\n\t@Secret(\"a\")\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, "widgets", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first := convertInPlace(t, module, ConvertOptions{}, "widgets/secret.templ")
	if _, err := Codemod(module, []string{"./..."}, first.Conversion.Calls, CodemodOptions{Write: true, FragmentRender: fragmentRender}); err != nil {
		t.Fatal(err)
	}
	if removed := convertInPlace(t, module, ConvertOptions{RemoveTempl: true}, "widgets/secret.templ"); len(removed.Conversion.Removed) == 0 {
		t.Fatalf("removal: refused %v", removed.Conversion.RemoveRefused)
	}
	// Finished by hand: the component gained a slot its bridge fills from
	// a parameter (say a MessagingPanel and its Associations).
	edit := func(name, old, new string) {
		path := filepath.Join(module, "widgets", name)
		source := string(mustRead(t, path))
		if !strings.Contains(source, old) {
			t.Fatalf("%s lacks %q:\n%s", name, old, source)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(source, old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("secret.ktml", "Secret [view: SecretView]", "Secret [view: SecretView, note?: content]")
	edit("secret_bridge.go", "Secret{View: NewSecretView(text)}", "Secret{View: NewSecretView(text), Note: templ.Raw(text)}")
	generateKTMLIn(t, module)
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/user.templ")
	found := false
	for _, d := range report.Conversion.Diagnostics {
		if d.Severity == "partial" && strings.Contains(d.Message, "Note: templ.Raw(text)") {
			found = true
		}
	}
	if !found || !report.Conversion.Failed() {
		t.Errorf("the bridge's Note slot is dropped silently: diagnostics %v", report.Conversion.Diagnostics)
	}
	if document := string(mustRead(t, filepath.Join(module, "widgets", "user.ktml"))); !strings.Contains(document, "Secret view=") || !strings.Contains(document, "TODO") {
		t.Errorf("the call should stay a KTML call with a TODO for the slot:\n%s", document)
	}
}

func TestInPlace_GIVENASpreadIntoAVariadicContentParameter_WHENTheCallerConverts_THENNoPresenterArgumentIsSpread(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\nimport \"strings\"\n\ntempl Frame(title string, parts ...templ.Component) {\n\t<h1>{ strings.ToUpper(title) }</h1>\n\tfor _, part := range parts {\n\t\t@part\n\t}\n}\n\ntempl UsesFrame(title string, parts ...templ.Component) {\n\t@Frame(title, parts...)\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "frame.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/frame.templ")
	presenter := string(mustRead(t, filepath.Join(module, "widgets", "frame_present.go")))
	if strings.Contains(presenter, "title...") || !strings.Contains(presenter, "NewFrameView(title)") {
		t.Errorf("the content spread went to the presenter's last argument:\n%s", presenter)
	}
}

func TestInPlace_GIVENANegatedBooleanAttributeInADirectTemplate_WHENConverted_THENTheKTMLValidates(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\ntempl Choice(name string, enabled bool) {\n\t<input type=\"radio\" name={ name } value=\"false\" checked?={ !enabled }/>\n\t<input type=\"radio\" name={ name } value=\"true\" checked?={ enabled }/>\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "choice.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/choice.templ")
	if len(report.Conversion.Validation) > 0 {
		t.Errorf("validation: %v\n%s", report.Conversion.Validation, mustRead(t, filepath.Join(module, "widgets", "choice.ktml")))
	}
}

func TestInPlace_GIVENAContentConditionBesideAPresenterField_WHENConverted_THENThePresenterHasNoEmptyBlock(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\nimport \"strings\"\n\ntempl Panel(title string, panel templ.Component) {\n\t<h1>{ strings.ToUpper(title) }</h1>\n\tif panel != nil {\n\t\t@panel\n\t} else {\n\t\t<p>None</p>\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "panel.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	convertInPlace(t, module, ConvertOptions{}, "widgets/panel.templ")
	presenter := string(mustRead(t, filepath.Join(module, "widgets", "panel_present.go")))
	lines := strings.Split(presenter, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "{" && strings.TrimSpace(lines[i+1]) == "}" {
			t.Fatalf("the presenter keeps an empty block at line %d:\n%s", i+1, presenter)
		}
	}
}

func TestInPlace_GIVENAComputedClassListTestingAContentInput_WHENConverted_THENThePresenterDoesNotReadTheContentInput(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package widgets\n\ntempl Bubble(text string, isNew bool, actions ...templ.Component) {\n\t<div class={ \"bubble\", templ.KV(\"gap-2\", len(actions) > 0), templ.KV(\"new\", isNew) }>\n\t\tfor _, action := range actions {\n\t\t\t@action\n\t\t}\n\t\t<p>{ text }</p>\n\t</div>\n}\n"
	if err := os.WriteFile(filepath.Join(module, "widgets", "bubble.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	report := convertInPlace(t, module, ConvertOptions{}, "widgets/bubble.templ")
	presenter := string(mustRead(t, filepath.Join(module, "widgets", "bubble_present.go")))
	for _, line := range strings.Split(presenter, "\n") {
		if strings.Contains(line, "len(actions)") && !strings.HasPrefix(strings.TrimSpace(line), "//") {
			t.Fatalf("the presenter reads the content input actions, which it does not receive, so it does not compile:\n%s", presenter)
		}
	}
	partial := false
	for _, n := range report.Nodes {
		if n.Construct == "class-list" && n.Partial {
			partial = true
		}
	}
	if !partial {
		t.Errorf("the class list is not reported partial")
	}
}
