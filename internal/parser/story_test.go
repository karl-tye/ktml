package parser

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestParseStory_GIVENEveryValueShape_WHENParsed_THENStoryRecordsValuesAndRanges(t *testing.T) {
	source := `import { Pipeline } from "./pipeline.ktml"
fixture report: TrustedURL = "/reports/42"
story Pipeline.Running [
	progress = Running [callId = "call-42"],
	stages = [recording = Done, transcription = Active],
	tags = ["a", "b"],
	notes = [],
	score = 4.5,
	open = true,
	note = None,
	link = report,
] {
	"Body text"
	slot footer {
		span "Footer"
	}
}
story Pipeline.Empty
`
	p := New([]byte(source))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	if len(doc.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(doc.Nodes))
	}
	fixture, ok := doc.Nodes[1].(*ast.FixtureDecl)
	if !ok {
		t.Fatalf("node 1 = %T, want *ast.FixtureDecl", doc.Nodes[1])
	}
	if fixture.Name != "report" || fixture.TypeDecl != "TrustedURL" || fixture.Value.String != "/reports/42" || source[fixture.P:fixture.E] != `fixture report: TrustedURL = "/reports/42"` {
		t.Errorf("fixture = %+v", fixture)
	}
	story, ok := doc.Nodes[2].(*ast.StoryDecl)
	if !ok {
		t.Fatalf("node 2 = %T, want *ast.StoryDecl", doc.Nodes[2])
	}
	if story.Component != "Pipeline" || story.Name != "Running" || source[story.ComponentRange.Start:story.ComponentRange.End] != "Pipeline" || source[story.NameRange.Start:story.NameRange.End] != "Running" {
		t.Errorf("story header = %+v", story)
	}
	if story.ArgumentsRange == nil || story.BodyRange == nil || story.E != story.BodyRange.End || source[story.E-1] != '}' {
		t.Fatalf("story ranges = %+v %+v %d", story.ArgumentsRange, story.BodyRange, story.E)
	}
	if len(story.Body) != 2 {
		t.Errorf("body = %d nodes, want 2", len(story.Body))
	}
	want := []struct {
		name string
		kind ast.ValueKind
		text string
	}{
		{"progress", ast.NameValue, `Running [callId = "call-42"]`},
		{"stages", ast.FieldsValue, `[recording = Done, transcription = Active]`},
		{"tags", ast.ListValue, `["a", "b"]`},
		{"notes", ast.EmptyValue, `[]`},
		{"score", ast.ScalarValue, `4.5`},
		{"open", ast.ScalarValue, `true`},
		{"note", ast.NameValue, `None`},
		{"link", ast.NameValue, `report`},
	}
	if len(story.Arguments) != len(want) {
		t.Fatalf("arguments = %d, want %d", len(story.Arguments), len(want))
	}
	for index, tt := range want {
		argument := story.Arguments[index]
		if argument.Name != tt.name || argument.Value.Kind != tt.kind || source[argument.Value.P:argument.Value.E] != tt.text {
			t.Errorf("argument %d = %s %s %q, want %s %s %q", index, argument.Name, argument.Value.Kind, source[argument.Value.P:argument.Value.E], tt.name, tt.kind, tt.text)
		}
	}
	progress := story.Arguments[0].Value
	if progress.Name != "Running" || progress.PayloadRange == nil || len(progress.Fields) != 1 || progress.Fields[0].Value.Scalar.String != "call-42" {
		t.Errorf("progress = %+v", progress)
	}
	empty, ok := doc.Nodes[3].(*ast.StoryDecl)
	if !ok || empty.Name != "Empty" || empty.ArgumentsRange != nil || empty.BodyRange != nil {
		t.Errorf("bare story = %+v", doc.Nodes[3])
	}
}

func TestParseStory_GIVENBrokenEntry_WHENParsed_THENRecoversAtTheNextEntryAndStory(t *testing.T) {
	source := "story Card.A [title = , open = true]\nstory Card.B [title = \"b\"]\n"
	p := New([]byte(source))
	doc := p.ParseDocument()
	if len(p.Errors()) == 0 {
		t.Fatal("expected a syntax error")
	}
	var names []string
	for _, node := range doc.Nodes {
		if story, ok := node.(*ast.StoryDecl); ok {
			names = append(names, story.Name)
		}
	}
	if len(names) != 2 || names[1] != "B" {
		t.Errorf("stories = %v, want [A B]", names)
	}
}
