package interp_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
)

var storyFiles = map[string]string{
	"card.ktmlschema": `record Stage [
	key: string,
	label: string,
	note?: string,
]

state Progress {
	Idle,
	Running [callId: string, link: TrustedURL],
	Failed [message: string],
}
`,
	"badge.ktml": `Badge [label: string] {
	span class="badge" "{label}"
}
`,
	"card.ktml": `import { Stage, Progress } from "./card.ktmlschema"

Card [
	title: string,
	count: integer = 3,
	score?: number,
	open: boolean,
	stages: [Stage],
	progress: Progress,
	home: URL,
	data: JSONValue,
	footer?: content,
] {
	section data-open="{open}" data-state="{data}" {
		a href="{home}" "{title} ({count})"
		if score? {
			b "{score}"
		}
		for stage in stages key stage.key {
			if stage.note? {
				i "{stage.label}: {stage.note}"
			} else {
				i "{stage.label}"
			}
		}
		Status progress="{progress}"
		children
		footer
	}
}

Status [progress: Progress]

Status [progress = Idle] {
	em "idle"
}

Status [progress = Running, callId, link] {
	a href="{link}" "running {callId}"
}

Status [progress = Failed, message] {
	strong "{message}"
}
`,
	"card.story.ktml": `import { Card } from "./card.ktml"
import { Badge } from "./badge.ktml"

fixture report: TrustedURL = "javascript:report()"
fixture payload: JSONValue = ` + "`" + `{"a":"</script>"}` + "`" + `

story Card.Running [
	title = "<Run>",
	score = 4.5,
	open = true,
	stages = [[key = "a", label = "One", note = "first"], [key = "b", label = "Two"]],
	progress = Running [callId = "c-1", link = report],
	home = "javascript:alert(1)",
	data = payload,
] {
	"Body "
	Badge label="new"
	slot footer {
		small "Footer"
	}
}

story Card.Idle [
	title = "Idle",
	count = 9,
	open = false,
	stages = [],
	progress = Idle,
	home = "/home",
	data = payload,
]
`,
}

func renderStory(t *testing.T, program *interp.Program, ctx context.Context, name string, overrides map[string]compiler.Value) (string, error) {
	t.Helper()
	for _, story := range program.Stories() {
		if story.Name == name {
			var output bytes.Buffer
			err := program.RenderStory(ctx, &output, story.ID, overrides)
			return output.String(), err
		}
	}
	t.Fatalf("no story %s", name)
	return "", nil
}

func TestRenderStory_GIVENTypedStoryValues_WHENRendered_THENWritesTheComponentWithThoseValues(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, storyFiles)
	if !snapshot.Valid() {
		t.Fatalf("snapshot: %v", snapshot.Err())
	}
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name      string
		overrides map[string]compiler.Value
		want      string
	}{
		{
			name: "Running",
			want: `<section data-open data-state="{&#34;a&#34;:&#34;&lt;/script&gt;&#34;}"><a href="about:invalid#ktml-unsafe-url">&lt;Run&gt; (3)</a><b>4.5</b><i>One: first</i><i>Two</i><a href="javascript:report()">running c-1</a>Body <span class="badge">new</span><small>Footer</small></section>`,
		},
		{
			name: "Idle",
			want: `<section data-state="{&#34;a&#34;:&#34;&lt;/script&gt;&#34;}"><a href="/home">Idle (9)</a><em>idle</em></section>`,
		},
		{
			name: "Idle",
			overrides: map[string]compiler.Value{
				"count":    {Kind: compiler.NoneValueKind},
				"progress": {Kind: compiler.VariantValueKind, Variant: "Failed", Fields: []compiler.FieldValue{{Name: "message", Value: compiler.Value{Kind: compiler.ScalarValueKind, String: "<boom>"}}}},
			},
			want: `<section data-state="{&#34;a&#34;:&#34;&lt;/script&gt;&#34;}"><a href="/home">Idle (3)</a><strong>&lt;boom&gt;</strong></section>`,
		},
	}
	for _, tt := range tests {
		got, err := renderStory(t, program, context.Background(), tt.name, tt.overrides)
		if err != nil {
			t.Fatalf("RenderStory(%s) error = %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("RenderStory(%s) =\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

func TestRenderStory_GIVENDevelopmentAndARegistryRoot_WHENRendered_THENMarksTheInstanceAndContentCalls(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, storyFiles)
	options := testOptions
	options.Development = true
	program, err := interp.New(snapshot, options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := renderStory(t, program, dev.WithScope(context.Background(), "@7"), "Running", nil)
	if err != nil {
		t.Fatalf("RenderStory() error = %v", err)
	}
	for _, want := range []string{`<section data-ktml-i="@7"`, `<!--ktml:item @7 `, `data-ktml-i="@7 `, `.story.Running#c0"`} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	var props ktml.Component = program.StoryProps(program.Stories()[0].ID, nil)
	if id, ok := program.ComponentID(props); !ok || id != string(program.Stories()[0].ID) {
		t.Errorf("ComponentID(story) = %q, %v", id, ok)
	}
	depends := strings.Join(program.Dependencies(string(program.Stories()[0].ID)), " ")
	if !strings.Contains(depends, ".Badge") || !strings.Contains(depends, ".Card") || !strings.Contains(depends, ".Status") {
		t.Errorf("Dependencies(story) = %s", depends)
	}
}

func TestRenderStory_GIVENAnInvalidStateOverride_WHENRendered_THENReturnsTheComponentsRenderError(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, storyFiles)
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = renderStory(t, program, context.Background(), "Idle", map[string]compiler.Value{
		"stages": {Kind: compiler.CollectionValueKind, Items: []compiler.Value{
			{Kind: compiler.RecordValueKind, Fields: []compiler.FieldValue{{Name: "key", Value: compiler.Value{Kind: compiler.ScalarValueKind, String: "k"}}}},
			{Kind: compiler.RecordValueKind, Fields: []compiler.FieldValue{{Name: "key", Value: compiler.Value{Kind: compiler.ScalarValueKind, String: "k"}}}},
		}},
	})
	var renderError *ktml.RenderError
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrDuplicateKey {
		t.Fatalf("error = %v, want ErrDuplicateKey", err)
	}
}

func TestRenderStory_GIVENEmptyAttributeBags_WHENRendered_THENSpreadsWriteNothing(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{
		"field.ktmlschema": "record Row [\n\tlabel: string,\n\tattrs: Attributes,\n]\n",
		"field.ktml":       "import { Row } from \"./field.ktmlschema\"\n\nField [attrs: Attributes, row: Row, extra?: Attributes] {\n\tlabel ...row.attrs \"{row.label}\"\n\tinput name=\"n\" ...attrs\n}\n",
		"field.story.ktml": "import { Field } from \"./field.ktml\"\n\nstory Field.Default [\n\tattrs = [],\n\trow = [label = \"Name\", attrs = []],\n\textra = [],\n]\n",
	})
	if !snapshot.Valid() {
		t.Fatalf("snapshot: %v", snapshot.Err())
	}
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := renderStory(t, program, context.Background(), "Default", nil)
	if err != nil {
		t.Fatalf("RenderStory() error = %v", err)
	}
	if want := `<label>Name</label><input name="n">`; got != want {
		t.Errorf("RenderStory() = %s, want %s", got, want)
	}
}
