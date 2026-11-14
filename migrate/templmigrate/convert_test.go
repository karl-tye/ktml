package templmigrate

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ/generator"
	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
)

// fixtureModule copies the typed fixture module into a temporary directory
// that also requires KTML, so emitted presenters and generated Go compile
// there.
func fixtureModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir("testdata/typed", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("testdata/typed", path)
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

func convertFixture(t *testing.T, module string, scaffold bool, paths ...string) *Report {
	t.Helper()
	for i, path := range paths {
		paths[i] = filepath.Join(module, path)
	}
	files, err := ExpandPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Convert(files, ConvertOptions{Options: Options{Root: module}, Out: filepath.Join(module, "out"), ScaffoldPresenters: scaffold})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return report
}

func TestConvert_GIVENTheConversionFixture_WHENConverted_THENEveryNodeHasOneStatusAndAnOutput(t *testing.T) {
	module := fixtureModule(t)
	report := convertFixture(t, module, true, "convert/...")
	conversion := report.Conversion
	if conversion == nil {
		t.Fatal("no conversion summary")
	}
	if conversion.Failed() || report.Failed() {
		t.Fatalf("conversion failed: %d partial nodes, validation %v, diagnostics %v", conversion.Partial, conversion.Validation, conversion.Diagnostics)
	}
	if report.Totals.Total == 0 || len(report.Nodes) != report.Totals.Total {
		t.Fatalf("nodes %d, totals %d", len(report.Nodes), report.Totals.Total)
	}
	for _, n := range report.Nodes {
		if n.Status < Converted || n.Status > Blocked {
			t.Errorf("%s:%d:%d %s has no PLAN status", n.File, n.From.Line, n.From.Col, n.Kind)
		}
		if n.Partial {
			t.Errorf("%s:%d:%d %s is partial: %v", n.File, n.From.Line, n.From.Col, n.Kind, n.Notes)
		}
		if n.Output == nil || n.Output.File == "" {
			t.Errorf("%s:%d:%d %s (%s) has no output location", n.File, n.From.Line, n.From.Col, n.Kind, n.Code)
		}
	}
	for _, kind := range []string{"ktml", "ktmlschema", "presenter", "sourcemap", "helpers"} {
		found := false
		for _, output := range conversion.Outputs {
			found = found || output.Kind == kind
		}
		if !found {
			t.Errorf("no %s output", kind)
		}
	}
	if len(conversion.Security) == 0 {
		t.Error("no security markers for the trusted URL, JSON, and script values")
	}
}

func TestConvert_GIVENFilesThatCallEachOther_WHENConverted_THENTheyShareOneValidDocument(t *testing.T) {
	module := fixtureModule(t)
	report := convertFixture(t, module, true, "convert/...")
	if len(report.Conversion.Validation) > 0 {
		t.Fatalf("validation: %v", report.Conversion.Validation)
	}
	shared, err := os.ReadFile(filepath.Join(module, "out", "convert", "widgets", "ping.ktml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"Ping [", "Pong [", "Dot {"} {
		if !strings.Contains(string(shared), "\n"+component) {
			t.Errorf("ping.ktml lacks %s:\n%s", component, shared)
		}
	}
	if _, err := os.Stat(filepath.Join(module, "out", "convert", "widgets", "pong.ktml")); !os.IsNotExist(err) {
		t.Errorf("pong.ktml was written: %v", err)
	}
	merged := 0
	for _, d := range report.Conversion.Diagnostics {
		if d.Code == "merged-document" {
			merged++
		}
	}
	if merged != 2 {
		t.Errorf("merged-document diagnostics = %d, want one per file", merged)
	}
	for _, n := range report.Nodes {
		if strings.HasSuffix(n.File, "pong.templ") && n.Output != nil && n.Output.File != "convert/widgets/ping.ktml" {
			t.Errorf("%s %s maps to %s", n.File, n.Kind, n.Output.File)
		}
	}
}

func TestConvert_GIVENTheConversionFixture_WHENConverted_THENNamesStayDistinctAndOneSchemaPerPackage(t *testing.T) {
	module := fixtureModule(t)
	convertFixture(t, module, true, "convert/...")
	out := filepath.Join(module, "out", "convert")
	page, err := os.ReadFile(filepath.Join(out, "convert.ktml"))
	if err != nil {
		t.Fatal(err)
	}
	// KTML imports components by name with no aliases: the local Card is
	// qualified by its package next to the widgets Card it calls.
	for _, want := range []string{"\nConvertCard [", "\tConvertCard title=\"Local\"", "import { Badge, Card, Meter, Stamp, Toolbar }"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("convert.ktml lacks %q", want)
		}
	}
	// Literal text of Go string templates stays in KTML.
	for _, want := range []string{
		`p class="summary" data-ref="board-{view.boardTitle}-{view.dataRefItoaBoardCount}" "{view.boardCount} of {view.boardTitle}'s 100% items"`,
		// An integer value stays a path in a string argument.
		`Badge label="{view.boardCount} new"`,
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("convert.ktml lacks %s", want)
		}
	}
	schemas, _ := filepath.Glob(filepath.Join(out, "*.ktmlschema"))
	if len(schemas) != 1 || filepath.Base(schemas[0]) != "convert.ktmlschema" {
		t.Errorf("schemas = %v, want one per output package", schemas)
	}
	presenter, err := os.ReadFile(filepath.Join(out, "widgets", "widgets_present.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Stamp declares its own now, so its clock parameter is clockNow.
	if !strings.Contains(string(presenter), "func NewStampView(label string, clockNow time.Time) StampView") || !strings.Contains(string(presenter), "now := clockNow.UTC()") {
		t.Errorf("Stamp presenter:\n%s", presenter)
	}
	board, err := os.ReadFile(filepath.Join(out, "convert_present.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(board), "func NewBoardPageView(ctx context.Context, board convertsrc.Board, open bool, now time.Time) BoardPageView") {
		t.Errorf("BoardPage presenter does not take the request context and the clock:\n%s", board)
	}
	// templ.JSONString becomes KTML's own JSON constructor, not a helper.
	if !strings.Contains(string(board), "ktml.MustJSON(board.Tags)") || strings.Contains(string(board), "migrateJSON(") {
		t.Errorf("BoardPage presenter does not build its JSON with ktml.MustJSON:\n%s", board)
	}
	helpers, _ := filepath.Glob(filepath.Join(out, "*", "migrate_helpers.go"))
	root, _ := filepath.Glob(filepath.Join(out, "migrate_helpers.go"))
	for _, path := range append(helpers, root...) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "func migrateJSON(") {
			t.Errorf("%s still defines migrateJSON:\n%s", path, data)
		}
	}
}

func TestConvert_GIVENTheConversionFixture_WHENConvertedTwice_THENWritesIdenticalFiles(t *testing.T) {
	read := func() map[string]string {
		module := fixtureModule(t)
		convertFixture(t, module, true, "convert/...")
		files := map[string]string{}
		root := filepath.Join(module, "out")
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(path)
				rel, _ := filepath.Rel(root, path)
				files[rel] = strings.ReplaceAll(string(data), module, "<module>")
			}
			return nil
		})
		return files
	}
	first, second := read(), read()
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("file sets differ: %d and %d", len(first), len(second))
	}
	for name, content := range first {
		if second[name] != content {
			t.Errorf("%s differs between runs", name)
		}
	}
}

func TestConvert_GIVENPresenterTemplates_WHENNotScaffolding_THENTheyArePartial(t *testing.T) {
	module := fixtureModule(t)
	report := convertFixture(t, module, false, "convert/...")
	if !report.Conversion.Failed() || report.Conversion.Partial == 0 {
		t.Fatalf("templates that need presenters converted without --scaffold-presenters")
	}
	for _, d := range report.Conversion.Diagnostics {
		if d.Code == "presenter-required" {
			return
		}
	}
	t.Error("no presenter-required diagnostic")
}

func TestConvert_GIVENUnconvertibleConstructs_WHENConverted_THENNodesArePartialAndScaffoldsStillCompile(t *testing.T) {
	module := fixtureModule(t)
	report := convertFixture(t, module, true, "fixture/...")
	if !report.Conversion.Failed() {
		t.Fatal("a fixture with templ.Raw, a once handle, and an unexported helper converted cleanly")
	}
	want := map[string]bool{"function-returning-component": false, "once-handle": false, "presenter-reference": false}
	for _, d := range report.Conversion.Diagnostics {
		if _, ok := want[d.Code]; ok && d.Severity == "partial" {
			want[d.Code] = true
		}
	}
	for code, seen := range want {
		if !seen {
			t.Errorf("no partial %s diagnostic", code)
		}
	}
	generateKTML(t, module)
	goCommand(t, module, "build", "./...")
}

// TestConvert_GIVENTheConversionFixture_WHENGeneratedAndCompiled_THENRendersTheSameBytesAsTempl
// generates the converted KTML with KTML's own generator, compiles it with
// the presenters, and renders templ and KTML from the same inputs.
func TestConvert_GIVENTheConversionFixture_WHENGeneratedAndCompiled_THENRendersTheSameBytesAsTempl(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module")
	}
	module := fixtureModule(t)
	report := convertFixture(t, module, true, "convert/...")
	if report.Conversion.Failed() {
		t.Fatalf("conversion failed: %v %v", report.Conversion.Validation, report.Conversion.Diagnostics)
	}
	generateKTML(t, module)
	generateTempl(t, filepath.Join(module, "convert"))
	if err := os.WriteFile(filepath.Join(module, "out", "convert", "differential_test.go"), []byte(fixtureDifferential), 0o644); err != nil {
		t.Fatal(err)
	}
	goCommand(t, module, "vet", "./...")
	goCommand(t, module, "test", "./out/...")
}

const fixtureDifferential = `package convert_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	source "example.com/typed/convert"
	"example.com/typed/out/convert"
)

func TestDifferential(t *testing.T) {
	owner := "Sam <O'Neil>"
	due := time.Date(2026, time.March, 9, 0, 0, 0, 0, time.UTC)
	boards := []source.Board{
		{Title: "Ops & <Support>", Items: []source.Item{{ID: "a", Label: "First", Done: true, Due: &due, Phase: source.PhaseTodo}, {ID: "b", Label: "Second"}}, Tags: []string{"x", "y"}, Owner: &owner, Status: source.StatusOpen, Count: 2, Ratio: 0.25, Link: "/boards/ops"},
		{Title: "Empty", Status: source.StatusClosed, Link: "javascript:alert(1)"},
		{Title: "Closed", Items: []source.Item{{ID: "c", Label: "Third", Phase: source.PhaseDone}}, Status: source.StatusClosed, Ratio: 1},
		{Title: "Unsafe", Status: source.StatusOpen, Link: "javascript:alert(1)"},
	}
	for _, board := range boards {
		for _, open := range []bool{true, false} {
			var want, got bytes.Buffer
			ctx := context.WithValue(context.Background(), source.UserKey{}, "Avery <A>")
			if err := source.BoardPage(board, open).Render(ctx, &want); err != nil {
				t.Fatal(err)
			}
			// The presenter takes the request context and the clock
			// explicitly; the KTML render needs neither.
			if err := (convert.BoardPage{View: convert.NewBoardPageView(ctx, board, open, time.Now())}).Render(context.Background(), &got); err != nil {
				t.Fatal(err)
			}
			// The one intended difference: each runtime's own marker for a
			// URL its policy rejects.
			wantBytes := bytes.ReplaceAll(want.Bytes(), []byte("about:invalid#TemplFailedSanitizationURL"), []byte("about:invalid#ktml-unsafe-url"))
			if board.Title == "Unsafe" && open && !bytes.Contains(got.Bytes(), []byte("about:invalid#ktml-unsafe-url")) {
				t.Errorf("the unsafe link was not rejected: %s", got.String())
			}
			if !bytes.Equal(wantBytes, got.Bytes()) {
				t.Errorf("%s open=%v:\ntempl: %s\nktml:  %s", board.Title, open, want.String(), got.String())
			}
		}
	}
}
`

// generateKTML writes the Go of every emitted .ktml and .ktmlschema, as
// ktml generate does.
func generateKTML(t *testing.T, module string) {
	t.Helper()
	generateKTMLRoot(t, filepath.Join(module, "out"))
}

// generateKTMLRoot generates every .ktml and .ktmlschema under out.
func generateKTMLRoot(t *testing.T, out string) {
	t.Helper()
	var docs []string
	_ = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".ktml") || strings.HasSuffix(path, ".ktmlschema")) {
			docs = append(docs, path)
		}
		return nil
	})
	workspace := compiler.NewWorkspace(out)
	packages := func(dir string) (gogen.GoPackage, error) {
		resolved, err := gopkg.Resolve(dir)
		return gogen.GoPackage{ImportPath: resolved.ImportPath, Name: resolved.Name}, err
	}
	for _, doc := range docs {
		snapshot, err := workspace.Load(doc)
		if err != nil || !snapshot.Valid() {
			t.Fatalf("%s: %v %v", doc, err, snapshot.Diagnostics(doc))
		}
		pkg, err := gopkg.Resolve(filepath.Dir(doc))
		if err != nil {
			t.Fatal(err)
		}
		options := gogen.Options{Package: pkg.Name, Packages: packages}
		var code []byte
		target := strings.TrimSuffix(doc, ".ktml") + "_ktml.go"
		if strings.HasSuffix(doc, ".ktmlschema") {
			code, _, err = gogen.GenerateSchema(snapshot, doc, options)
			target = strings.TrimSuffix(doc, ".ktmlschema") + "_ktmlschema.go"
		} else {
			code, err = gogen.GenerateFile(snapshot, doc, options)
		}
		if err != nil {
			t.Fatalf("generate %s: %v", doc, err)
		}
		if err := os.WriteFile(target, code, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// generateTempl writes the templ Go of a source directory tree.
func generateTempl(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !strings.HasSuffix(path, ".templ") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseString(string(src))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := generator.Generate(file, &buf, generator.WithFileName(filepath.Base(path))); err != nil {
			t.Fatal(err)
		}
		return os.WriteFile(strings.TrimSuffix(path, ".templ")+"_templ.go", buf.Bytes(), 0o644)
	})
}

func goCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestConvert_GIVENSwitches_WHENConverted_THENClosedSetsBecomeRenderClausesAndOpenSetsStayBooleans(t *testing.T) {
	module := fixtureModule(t)
	report := convertFixture(t, module, true, "convert/...")
	if len(report.Conversion.Validation) > 0 {
		t.Fatalf("validation: %v", report.Conversion.Validation)
	}
	if got, want := report.Conversion.Switches, (SwitchCounts{Clauses: 2, Booleans: 1}); got != want {
		t.Errorf("switches %+v, want %+v", got, want)
	}
	var reviewed []int
	for _, d := range report.Conversion.Diagnostics {
		if d.Code == "closed-presentation-state" {
			reviewed = append(reviewed, d.From.Line)
		}
	}
	if len(reviewed) != 1 {
		t.Errorf("closed-presentation-state reviews at lines %v, want only the open switch over board.Title", reviewed)
	}
	schema := string(mustRead(t, filepath.Join(module, "out", "convert", "convert.ktmlschema")))
	document := string(mustRead(t, filepath.Join(module, "out", "convert", "convert.ktml")))
	for _, want := range []string{"state BoardPageStatusState {\n\tOpen,\n\tOther,\n}", "state BoardPagePhaseState {\n\tTodo,\n\tDoingDone,\n\tOther,\n}"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema lacks %q:\n%s", want, schema)
		}
	}
	for _, want := range []string{"BoardPageStatus [state = Open] {", "BoardPageStatus [state = Other] {", "BoardPagePhase [state = DoingDone] {", "BoardPagePhase [state = Other] {"} {
		if !strings.Contains(document, want) {
			t.Errorf("document lacks %q:\n%s", want, document)
		}
	}
}

// TestConvert_GIVENLocalsAssignedInsideBlocks_WHENConverted_THENTheyStayInThePresenterAndTheBytesMatchTempl
// covers {{ x = y }} inside if, else, switch cases, and for bodies: the
// statements stay presenter Go, a block left with nothing to render is not
// emitted as an empty KTML block, and the whitespace around it renders as
// templ renders it.
func TestConvert_GIVENLocalsAssignedInsideBlocks_WHENConverted_THENTheyStayInThePresenterAndTheBytesMatchTempl(t *testing.T) {
	module := fixtureModule(t)
	report := convertFixture(t, module, true, "locals/...")
	if report.Conversion.Failed() {
		t.Fatalf("conversion failed: %v %v", report.Conversion.Validation, report.Conversion.Diagnostics)
	}
	document := string(mustRead(t, filepath.Join(module, "out", "locals", "locals.ktml")))
	for _, empty := range []string{"{\n\t\t}", "{\n\t}", "} else {\n\t\t}", "} else {\n\t}"} {
		if strings.Contains(document, empty) {
			t.Errorf("document has an empty block %q:\n%s", empty, document)
		}
	}
	presenter := string(mustRead(t, filepath.Join(module, "out", "locals", "locals_present.go")))
	for _, want := range []string{`picked = "yes"`, `note = "none"`, `note = "some"`, `label = "one"`, `label = "many"`, "count++"} {
		if !strings.Contains(presenter, want) {
			t.Errorf("presenter lacks %q:\n%s", want, presenter)
		}
	}
	for _, n := range report.Nodes {
		if n.Output == nil || n.Output.File == "" {
			t.Errorf("%s:%d:%d %s has no output location", n.File, n.From.Line, n.From.Col, n.Kind)
		}
	}
	if testing.Short() {
		return
	}
	generateKTML(t, module)
	generateTempl(t, filepath.Join(module, "locals"))
	if err := os.WriteFile(filepath.Join(module, "out", "locals", "differential_test.go"), []byte(localsDifferential), 0o644); err != nil {
		t.Fatal(err)
	}
	goCommand(t, module, "vet", "./...")
	goCommand(t, module, "test", "./out/...")
}

const localsDifferential = `package locals_test

import (
	"bytes"
	"context"
	"testing"

	source "example.com/typed/locals"
	"example.com/typed/out/locals"
)

func TestDifferential(t *testing.T) {
	for _, chosen := range []string{"", "local", "production"} {
		for _, mode := range []int{1, 2} {
			levels := []string{"local", "production"}
			var want, got bytes.Buffer
			if err := source.Forms(levels, chosen, mode).Render(context.Background(), &want); err != nil {
				t.Fatal(err)
			}
			if err := (locals.Forms{View: locals.NewFormsView(levels, chosen, mode)}).Render(context.Background(), &got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want.Bytes(), got.Bytes()) {
				t.Errorf("chosen=%q mode=%d:\ntempl: %s\nktml:  %s", chosen, mode, want.String(), got.String())
			}
		}
	}
}
`
