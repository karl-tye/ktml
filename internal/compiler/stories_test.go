package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

const storySchema = `record Stage [
	label: string,
	note?: string,
]

state Progress {
	Idle,
	Running [callId: string, link: TrustedURL],
	Failed [message: string],
}
`

const storyComponents = `import { Stage, Progress } from "./card.ktmlschema"

Card [
	title: string,
	count: integer = 3,
	score?: number,
	open: boolean,
	stage: Stage,
	stages: [Stage],
	progress: Progress,
	home: URL,
	footer?: content,
] {
	section {
		h2 "{title}"
		children
		footer
	}
}
`

const validStories = `import { Card } from "./card.ktml"

fixture report: TrustedURL = "/reports/42"
fixture criterion: JSONValue = ` + "`" + `{"id": "c1", "label": "Greeting"}` + "`" + `

story Card.Running [
	title = "Running",
	open = true,
	stage = [label = "Scoring"],
	stages = [[label = "One", note = "first"], [label = "Two", note = None]],
	progress = Running [callId = "call-42", link = report],
	home = "/",
	score = 4.5,
] {
	"Body"
	slot footer {
		span "Footer"
	}
}

story Card.Idle [
	title = "Idle",
	count = 7,
	open = false,
	stage = [label = "Waiting"],
	stages = [],
	progress = Idle,
	home = "/home",
]
`

func TestStories_GIVENEveryValueShape_WHENIndexed_THENStoriesAreTypedAgainstTheComponent(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"card.ktmlschema":  storySchema,
		"card.ktml":        storyComponents,
		"card.story.ktml":  validStories,
		"other/plain.ktml": "Plain [] {\n\tp \"x\"\n}\n",
	})
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("snapshot invalid: %v", snapshot.Err())
	}
	stories := snapshot.StoriesIn(filepath.Join(dir, "card.story.ktml"))
	if len(stories) != 2 {
		t.Fatalf("stories = %d, want 2", len(stories))
	}
	running, idle := stories[0], stories[1]
	if running.Name != "Running" || running.ComponentName != "Card" || !running.Default || idle.Default || !running.HasBody || idle.HasBody {
		t.Fatalf("stories = %+v / %+v", running, idle)
	}
	progress, ok := running.Value("progress")
	if !ok || progress.Kind != compiler.VariantValueKind || progress.Variant != "Running" {
		t.Fatalf("progress = %+v", progress)
	}
	link, _ := progress.Field("link")
	if link.Kind != compiler.FixtureValueKind || link.Fixture != "report" || link.String != "/reports/42" {
		t.Errorf("link = %+v", link)
	}
	stages, _ := running.Value("stages")
	if len(stages.Items) != 2 {
		t.Fatalf("stages = %+v", stages)
	}
	if note, _ := stages.Items[1].Field("note"); note.Kind != compiler.NoneValueKind {
		t.Errorf("second note = %+v, want None", note)
	}
	if count, _ := idle.Value("count"); count.Integer != 7 {
		t.Errorf("count = %+v", count)
	}
	if _, given := running.Value("count"); given {
		t.Error("an omitted defaulted input has a story value")
	}
	if fixtures := snapshot.Fixtures(filepath.Join(dir, "card.story.ktml")); len(fixtures) != 2 || fixtures[0].Type.Name != "TrustedURL" {
		t.Errorf("fixtures = %+v", fixtures)
	}
	card, _ := snapshot.Resolve(filepath.Join(dir, "card.ktml"), "Card")
	storyRefs := 0
	for _, reference := range snapshot.References(card.ID) {
		if reference.Kind == compiler.StoryReference {
			storyRefs++
		}
	}
	if storyRefs != 2 {
		t.Errorf("story references = %d, want 2", storyRefs)
	}
}

func TestStories_GIVENInvalidStories_WHENIndexed_THENEachErrorHasItsCodeAndRange(t *testing.T) {
	tests := []struct {
		name   string
		story  string
		code   string
		marked string
	}{
		{"missing required input", `story Card.A [title = "a", open = true, stage = [label = "x"], stages = [], progress = Idle]`, "missing-input", "A"},
		{"unknown input", `story Card.A [nope = 1]`, "unknown-input", "nope"},
		{"scalar mismatch", `story Card.A [title = 3]`, "story-type-mismatch", "3"},
		{"integer fraction", `story Card.A [count = 1.5]`, "story-type-mismatch", "1.5"},
		{"none for required", `story Card.A [title = None]`, "story-type-mismatch", "None"},
		{"unknown variant", `story Card.A [progress = Paused]`, "unknown-variant", "Paused"},
		{"payload on bare variant", `story Card.A [progress = Idle [x = "y"]]`, "unexpected-payload", `Idle [x = "y"]`},
		{"missing payload field", `story Card.A [progress = Failed []]`, "missing-field", "Failed []"},
		{"missing record field", `story Card.A [stage = [note = "n"]]`, "missing-field", `[note = "n"]`},
		{"unknown record field", `story Card.A [stage = [label = "l", size = 2]]`, "unknown-field", "size"},
		{"trusted literal", `story Card.A [progress = Running [callId = "c", link = "/x"]]`, "trusted-value-literal", `"/x"`},
		{"unknown fixture", `story Card.A [progress = Running [callId = "c", link = missing]]`, "unknown-fixture", "missing"},
		{"collection as record", `story Card.A [stage = ["a"]]`, "story-type-mismatch", `["a"]`},
		{"content as input", `story Card.A [footer = "x"]`, "content-as-story-input", "footer"},
		{"unknown component", `story Nope.A`, "unknown-component", "Nope"},
		{"unknown slot", `story Card.A { slot header { "x" } }`, "unknown-slot", "header"},
		{"path in body", `story Card.A { "{title}" }`, "unknown-path-root", "title"},
		{"duplicate story", "story Card.A\nstory Card.A", "duplicate-story", "A"},
		{"fixture type", `fixture n: string = "x"`, "fixture-type", "string"},
		{"invalid json fixture", "fixture j: JSONValue = `{`", "invalid-json-fixture", "`{`"},
		{"component in story", "Extra [] {\n\tp \"x\"\n}", "component-in-story", "Extra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "import { Card } from \"./card.ktml\"\n" + tt.story + "\n"
			dir := writeWorkspace(t, map[string]string{
				"card.ktmlschema": storySchema,
				"card.ktml":       storyComponents,
				"card.story.ktml": source,
			})
			snapshot, err := compiler.NewWorkspace(dir).Index()
			if err != nil {
				t.Fatalf("Index() error = %v", err)
			}
			var found []string
			for _, diagnostic := range snapshot.Diagnostics(filepath.Join(dir, "card.story.ktml")) {
				found = append(found, diagnostic.Code+":"+source[diagnostic.Start:diagnostic.End])
				if diagnostic.Code == tt.code && source[diagnostic.Start:diagnostic.End] == tt.marked {
					return
				}
			}
			t.Errorf("diagnostics = %s, want %s at %q", strings.Join(found, ", "), tt.code, tt.marked)
		})
	}
}

func TestStories_GIVENStoryOutsideAStoryDocument_WHENIndexed_THENItIsRejected(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"card.ktmlschema": storySchema,
		"card.ktml":       storyComponents + "\nstory Card.A\n",
	})
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	for _, diagnostic := range snapshot.Diagnostics(filepath.Join(dir, "card.ktml")) {
		if diagnostic.Code == "story-outside-story-document" {
			return
		}
	}
	t.Errorf("diagnostics = %+v, want story-outside-story-document", snapshot.Diagnostics(filepath.Join(dir, "card.ktml")))
}

const bagSchema = `record Row [
	label: string,
	attrs: Attributes,
]
`

const bagComponents = `import { Row } from "./field.ktmlschema"

Field [attrs: Attributes, row: Row, rows: [Row], extra?: Attributes] {
	label ...row.attrs "{row.label}"
	input ...attrs
}
`

func TestStories_GIVENEmptyAttributeBags_WHENIndexed_THENTheyAreTheOnlyBagValue(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"field.ktmlschema": bagSchema,
		"field.ktml":       bagComponents,
		"field.story.ktml": "import { Field } from \"./field.ktml\"\n\nstory Field.Default [\n\tattrs = [],\n\trow = [label = \"Name\", attrs = []],\n\trows = [[label = \"A\", attrs = []]],\n\textra = [],\n]\n",
	})
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if !snapshot.Valid() {
		t.Fatalf("snapshot invalid: %v", snapshot.Err())
	}
	stories := snapshot.StoriesIn(filepath.Join(dir, "field.story.ktml"))
	if len(stories) != 1 {
		t.Fatalf("stories = %d, want 1", len(stories))
	}
	attrs, _ := stories[0].Value("attrs")
	row, _ := stories[0].Value("row")
	rowAttrs, _ := row.Field("attrs")
	extra, _ := stories[0].Value("extra")
	for name, value := range map[string]compiler.Value{"attrs": attrs, "row.attrs": rowAttrs, "extra": extra} {
		if value.Kind != compiler.AttributesValueKind || value.Type.Name != compiler.AttributesType {
			t.Errorf("%s = %+v, want an empty attribute bag", name, value)
		}
	}
}

func TestStories_GIVENNonEmptyAttributeBags_WHENIndexed_THENTheyAreUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		marked string
	}{
		{"attributes", `[class = "x"]`, `[class = "x"]`},
		{"list", `["x"]`, `["x"]`},
		{"string", `"x"`, `"x"`},
		{"fixture", `bag`, `bag`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "import { Field } from \"./field.ktml\"\nstory Field.A [attrs = " + tt.value + ", row = [label = \"l\", attrs = []], rows = []]\n"
			dir := writeWorkspace(t, map[string]string{"field.ktmlschema": bagSchema, "field.ktml": bagComponents, "field.story.ktml": source})
			snapshot, err := compiler.NewWorkspace(dir).Index()
			if err != nil {
				t.Fatalf("Index() error = %v", err)
			}
			var found []string
			for _, diagnostic := range snapshot.Diagnostics(filepath.Join(dir, "field.story.ktml")) {
				found = append(found, diagnostic.Code+":"+source[diagnostic.Start:diagnostic.End])
				if diagnostic.Code == "unsupported-story-input" && source[diagnostic.Start:diagnostic.End] == tt.marked {
					return
				}
			}
			t.Errorf("diagnostics = %s, want unsupported-story-input at %q", strings.Join(found, ", "), tt.marked)
		})
	}
}

// TestWorkspace_GIVENDeferredStories_WHENIndexed_THENStoryDocumentsJoinOnlyWhenOpenedOrLoaded
// defers story Documents, as the language server does: Index leaves them
// out, an opened one joins, and LoadStories brings in the rest.
func TestWorkspace_GIVENDeferredStories_WHENIndexed_THENStoryDocumentsJoinOnlyWhenOpenedOrLoaded(t *testing.T) {
	dir := writeWorkspace(t, map[string]string{
		"card.ktmlschema":        storySchema,
		"card.ktml":              storyComponents,
		"card.story.ktml":        validStories,
		"other/plain.ktml":       "Plain [] {\n\tp \"x\"\n}\n",
		"other/plain.story.ktml": "import { Plain } from \"./plain.ktml\"\n\nstory Plain.Default\n",
	})
	cardStories := filepath.Join(dir, "card.story.ktml")
	plainStories := filepath.Join(dir, "other", "plain.story.ktml")
	workspace := compiler.NewWorkspace(dir)
	workspace.DeferStories()
	snapshot, err := workspace.Index()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HasFile(cardStories) || snapshot.HasFile(plainStories) || len(snapshot.Stories()) != 0 || !snapshot.HasFile(filepath.Join(dir, "card.ktml")) {
		t.Fatalf("deferred index files = %v", snapshot.Files())
	}
	if workspace.Sync([]string{plainStories}) {
		t.Error("a story Document changed on disk joined a deferred workspace")
	}
	snapshot, err = workspace.Update(cardStories, 1, []byte(validStories))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasFile(cardStories) || snapshot.HasFile(plainStories) || len(snapshot.Stories()) != 2 {
		t.Fatalf("after opening card.story.ktml: files = %v, stories = %d", snapshot.Files(), len(snapshot.Stories()))
	}
	snapshot, err = workspace.LoadStories()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasFile(plainStories) || len(snapshot.Stories()) != 3 {
		t.Fatalf("after LoadStories: files = %v, stories = %d", snapshot.Files(), len(snapshot.Stories()))
	}
	if current, _ := workspace.Current(); current != snapshot {
		t.Error("Current is not the snapshot LoadStories built")
	}
}
