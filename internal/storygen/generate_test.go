package storygen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

// copyFixture copies testdata/<name> into a temporary module and returns its
// root.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	return copyTree(t, filepath.Join("testdata", name))
}

// copyTree copies a directory into a temporary module.
func copyTree(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		target := filepath.Join(root, relative)
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
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// generate plans and writes stories for every component of root.
func generate(t *testing.T, root string, comments func(compiler.Component) []string) Result {
	t.Helper()
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	if err := ComponentErrors(snapshot); err != nil {
		t.Fatalf("fixture has errors: %v", err)
	}
	result, err := Plan(root, snapshot, Options{Components: snapshot.Components(), Comments: comments})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := result.Write(); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestGenerate_GIVENTypedComponents_WHENGenerated_THENStoriesTypeCheckAndRender
// generates stories for a fixture with records, collections, states, optional
// values, trusted values, and content, and checks the written Document with
// the compiler: zero diagnostics.
func TestGenerate_GIVENTypedComponents_WHENGenerated_THENStoriesTypeCheckAndRender(t *testing.T) {
	root := copyFixture(t, "basic")
	result := generate(t, root, func(component compiler.Component) []string {
		return []string{component.Name + " is reached by GET /board"}
	})
	path := filepath.Join(root, "web", "board.story.ktml")
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range snapshot.Diagnostics(path) {
		t.Errorf("diagnostic in generated stories: [%s] %s", diagnostic.Code, diagnostic.Message)
	}
	if !snapshot.Valid() {
		t.Fatalf("workspace is not valid: %v", snapshot.Err())
	}
	got := readFile(t, path)
	t.Logf("generated:\n%s", got)
	if len(result.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none", result.Skipped)
	}
	if result.Covered != result.Variants || result.Variants == 0 {
		t.Errorf("covered %d of %d Variants, want all", result.Covered, result.Variants)
	}
	for _, want := range []string{
		"story BoardView.Default",
		"story BoardView.Empty",
		"story BoardView.NoOptionals",
		"story ProgressLine.Running",
		"story StatusLine.Open",
		"story StatusLine.Booked",
		"story StatusLine.Closed",
		"story Panel.Default",
		"story OutcomeLine.Done [\n\toutcome = Done [grade = Pass],\n]",
		"story GradeLine.Fail",
		"fixture placeholderJSON: JSONValue = `{}`",
		"fixture placeholderURL: TrustedURL = \"/_story/trusted-url\"",
		"// BoardView is reached by GET /board",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Document lacks %q", want)
		}
	}
	// JobPage's state and optional list are two fields deep: ProgressLine
	// owns them, so JobPage has one story.
	if !strings.Contains(got, "story JobPage.Default") || strings.Contains(got, "story JobPage.Running") || strings.Contains(got, "story JobPage.NoOptionals") {
		t.Errorf("JobPage has stories for values it does not own")
	}
	// BoardView passes its state to ProgressLine, which selects on it.
	if strings.Contains(got, "story BoardView.Running") || strings.Contains(got, "story OutcomeLine.GradeFail") {
		t.Errorf("a component has Variant stories for a state it does not select on")
	}
	// An attribute bag is given as the empty bag, the only one a story
	// can give.
	if !strings.Contains(got, "story Field.Default [\n\tlabel = \"Label\",\n\tattrs = [],\n]") {
		t.Errorf("Field has no story with an empty attribute bag")
	}
}

// TestGenerate_GIVENTheExplorerFixture_WHENGenerated_THENEveryStoryTypeChecksAndRenders
// generates stories for the Explorer gate's helpdesk call audits, reader
// reviews, and ui components, beside their hand-written stories.
func TestGenerate_GIVENTheExplorerFixture_WHENGenerated_THENEveryStoryTypeChecksAndRenders(t *testing.T) {
	root := copyTree(t, filepath.Join("..", "..", "e2e-test", "testdata", "explorerapp"))
	result := generate(t, root, nil)
	if result.Stories < 50 {
		t.Errorf("generated %d stories, want the fixture's components covered", result.Stories)
	}
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Valid() {
		t.Fatalf("the workspace has errors after generation: %v", snapshot.Err())
	}
	for _, skipped := range result.Skipped {
		t.Logf("skipped %s: %s", skipped.Component.Name, skipped.Reason)
	}
	t.Logf("%d stories, %d of %d Variants", result.Stories, result.Covered, result.Variants)
	if result.Covered != result.Variants {
		t.Errorf("covered %d of %d Variants", result.Covered, result.Variants)
	}
}

// TestGenerate_GIVENPages_WHENGenerated_THENPagesGetOnlyDefaultAndVariantStories
// marks BoardView and ProgressLine as pages: a page gets its Default story,
// or one per Variant it selects on, and no Empty, NoOptionals, or LongText.
func TestGenerate_GIVENPages_WHENGenerated_THENPagesGetOnlyDefaultAndVariantStories(t *testing.T) {
	root := copyFixture(t, "basic")
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]bool{"BoardView": true, "ProgressLine": true}
	result, err := Plan(root, snapshot, Options{Components: snapshot.Components(), Page: func(component compiler.Component) bool { return pages[component.Name] }})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := result.Write(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(root, "web", "board.story.ktml"))
	for _, want := range []string{"story BoardView.Default", "story ProgressLine.Idle", "story ProgressLine.Running", "story ProgressLine.Failed", "story JobCard.NoOptionals"} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Document lacks %q", want)
		}
	}
	for _, absent := range []string{"story BoardView.Empty", "story BoardView.NoOptionals", "story BoardView.LongText", "story ProgressLine.LongText"} {
		if strings.Contains(got, absent) {
			t.Errorf("a page has %q", absent)
		}
	}
}

// TestGenerate_GIVENURLFields_WHENGenerated_THENPlaceholdersNameComponentAndField
// checks the placeholder URLs: each names the story's component and the
// field under /_story/, items after the first add their position, and no
// URL is "/", which in the Explorer is the Explorer itself.
func TestGenerate_GIVENURLFields_WHENGenerated_THENPlaceholdersNameComponentAndField(t *testing.T) {
	root := copyFixture(t, "basic")
	generate(t, root, nil)
	got := readFile(t, filepath.Join(root, "web", "board.story.ktml"))
	for _, want := range []string{
		"fixture placeholderURL: TrustedURL = \"/_story/trusted-url\"",
		"link = \"/_story/job-card/link\", status = Open],\n]",
		"link = \"/_story/board-view/link\"",
		"link = \"/_story/board-view/link/2\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Document lacks %q", want)
		}
	}
	if strings.Contains(got, "= \"/\"") {
		t.Errorf("a generated URL is \"/\"")
	}
	// Generation is deterministic: a second run changes nothing.
	again := generate(t, root, nil)
	for _, file := range again.Files {
		if file.Changed {
			t.Errorf("%s changed on a second run", file.Path)
		}
	}
}

// TestPlan_GIVENAccumulate_WHENOneComponentIsPlanned_THENOtherGeneratedStoriesAreKept
// adds one component's stories, as the component picker does, without
// removing the generated stories of the Document's other components.
func TestPlan_GIVENAccumulate_WHENOneComponentIsPlanned_THENOtherGeneratedStoriesAreKept(t *testing.T) {
	root := copyFixture(t, "basic")
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	pick := func(snapshot *compiler.Snapshot, name string) compiler.Component {
		for _, component := range snapshot.Components() {
			if component.Name == name {
				return component
			}
		}
		t.Fatalf("no component %s", name)
		return compiler.Component{}
	}
	result, err := Plan(root, snapshot, Options{Components: []compiler.Component{pick(snapshot, "StatusLine")}, Accumulate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Write(); err != nil {
		t.Fatal(err)
	}
	snapshot, err = compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	result, err = Plan(root, snapshot, Options{Components: []compiler.Component{pick(snapshot, "GradeLine")}, Accumulate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Write(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(root, "web", "board.story.ktml"))
	for _, want := range []string{"story StatusLine.Open", "story GradeLine.Fail", "// ktml:generated "} {
		if !strings.Contains(got, want) {
			t.Errorf("Document lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "story BoardView.") {
		t.Errorf("stories were written for a component that was not planned:\n%s", got)
	}
}

// TestGenerate_GIVENCollectionsKeyedByAnyPath_WHENGenerated_THENKeysAreUniqueInEveryStory
// keys collections by a string field not named key, a nested field, an
// integer, the item itself, and a markup-shaped field whose synthesized
// value is empty; and passes a keyed collection to a child component. Plan
// renders every generated story, so a duplicate key in any of them, such
// as LongText filling every term with the same text, fails it.
func TestGenerate_GIVENCollectionsKeyedByAnyPath_WHENGenerated_THENKeysAreUniqueInEveryStory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "web", "model.ktmlschema"), `record Fact [
	term: string,
	definition: string,
]

record Meta [
	code: string,
]

record Row [
	meta: Meta,
	label: string,
]

record Score [
	points: integer,
	player: string,
]

record Swatch [
	colour: string,
	label: string,
]

record GlossaryView [
	heading: string,
	facts: [Fact],
]
`)
	writeFile(t, filepath.Join(root, "web", "lists.ktml"), `import { Fact, Row, Score, Swatch, GlossaryView } from "./model.ktmlschema"

FactList [facts: [Fact]] {
	dl {
		for fact in facts key fact.term {
			dt "{fact.term}"
			dd "{fact.definition}"
		}
	}
}

RowTable [rows: [Row]] {
	ul {
		for row in rows key row.meta.code {
			li "{row.label}"
		}
	}
}

ScoreBoard [scores: [Score]] {
	ol {
		for score in scores key score.points {
			li "{score.player}"
		}
	}
}

TagList [tags: [string]] {
	ul {
		for tag in tags key tag {
			li "{tag}"
		}
	}
}

SwatchList [swatches: [Swatch]] {
	ul {
		for swatch in swatches key swatch.colour {
			li "{swatch.label}"
		}
	}
}

GlossaryPage [view: GlossaryView] {
	h1 "{view.heading}"
	FactList facts="{view.facts}"
}
`)
	generate(t, root, nil)
	got := readFile(t, filepath.Join(root, "web", "lists.story.ktml"))
	t.Logf("generated:\n%s", got)
	for _, want := range []string{
		"story FactList.LongText",
		"story TagList.LongText",
		"story RowTable.Default",
		"story ScoreBoard.Default",
		"story SwatchList.Default",
		"story GlossaryPage.LongText",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Document lacks %q:\n%s", want, got)
		}
	}
	// A second run is stable.
	for _, file := range generate(t, root, nil).Files {
		if file.Changed {
			t.Errorf("%s changed on a second run", file.Path)
		}
	}
}
