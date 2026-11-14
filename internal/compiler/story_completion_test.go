package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestStoryCompletions_GIVENACursorInAStory_WHENCompleted_THENOffersTheTypedNamesAndValuesThere(t *testing.T) {
	prefix := "import { Card } from \"./card.ktml\"\n\nfixture report: TrustedURL = \"/r\"\n\n"
	tests := []struct {
		name   string
		source string // | marks the cursor
		want   []string
		absent []string
	}{
		{"component after story", "story |", []string{"Card"}, nil},
		{"partial component", "story Ca|", []string{"Card"}, nil},
		{"inputs", "story Card.A [|", []string{"title", "count", "score", "open", "stages", "progress", "home"}, []string{"footer"}},
		{"remaining inputs", "story Card.A [title = \"x\", |", []string{"open"}, []string{"title"}},
		{"boolean value", "story Card.A [open = |", []string{"true", "false"}, []string{"None"}},
		{"optional value", "story Card.A [score = |", []string{"None"}, nil},
		{"variants", "story Card.A [progress = |", []string{"Idle", "Running", "Failed"}, nil},
		{"partial variant", "story Card.A [progress = Ru|", []string{"Running"}, nil},
		{"payload fields", "story Card.A [progress = Running [|", []string{"callId", "link"}, []string{"title"}},
		{"payload fixture", "story Card.A [progress = Running [callId = \"c\", link = |", []string{"report"}, nil},
		{"record fields in a collection", "story Card.A [stages = [[|", []string{"label", "note"}, nil},
		{"optional field", "story Card.A [stages = [[label = \"x\", note = |", []string{"None"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cursor := strings.Index(tt.source, "|")
			source := prefix + strings.Replace(tt.source, "|", "", 1)
			dir := writeWorkspace(t, map[string]string{
				"card.ktmlschema": storySchema,
				"card.ktml":       storyComponents,
				"card.story.ktml": source,
			})
			snapshot, err := compiler.NewWorkspace(dir).Index()
			if err != nil {
				t.Fatal(err)
			}
			list, ok := snapshot.StoryCompletions(filepath.Join(dir, "card.story.ktml"), []byte(source), len(prefix)+cursor)
			if !ok {
				t.Fatal("no story completion context")
			}
			labels := make(map[string]bool)
			for _, item := range list.Items {
				labels[item.Label] = true
			}
			for _, want := range tt.want {
				if !labels[want] {
					t.Errorf("completions %v lack %s", labels, want)
				}
			}
			for _, absent := range tt.absent {
				if labels[absent] {
					t.Errorf("completions %v include %s", labels, absent)
				}
			}
		})
	}
}

func TestStoryCompletions_GIVENTheStoryBody_WHENCompleted_THENLeavesItToBodyCompletion(t *testing.T) {
	source := "import { Card } from \"./card.ktml\"\nstory Card.A [title = \"x\"] {\n\t\n}\n"
	dir := writeWorkspace(t, map[string]string{"card.ktmlschema": storySchema, "card.ktml": storyComponents, "card.story.ktml": source})
	snapshot, _ := compiler.NewWorkspace(dir).Index()
	if _, ok := snapshot.StoryCompletions(filepath.Join(dir, "card.story.ktml"), []byte(source), strings.Index(source, "\t\n")+1); ok {
		t.Error("the body offered story value completions")
	}
}

func TestStoryCompletions_GIVENAttributeBags_WHENCompleted_THENOffersTheInputAndTheEmptyBag(t *testing.T) {
	prefix := "import { Field } from \"./field.ktml\"\n\n"
	tests := []struct {
		name   string
		source string // | marks the cursor
		want   string
	}{
		{"bag input", "story Field.A [|", "attrs"},
		{"bag value", "story Field.A [attrs = |", "[]"},
		{"bag field value", "story Field.A [row = [label = \"x\", attrs = |", "[]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cursor := strings.Index(tt.source, "|")
			source := prefix + strings.Replace(tt.source, "|", "", 1)
			dir := writeWorkspace(t, map[string]string{"field.ktmlschema": bagSchema, "field.ktml": bagComponents, "field.story.ktml": source})
			snapshot, err := compiler.NewWorkspace(dir).Index()
			if err != nil {
				t.Fatal(err)
			}
			list, ok := snapshot.StoryCompletions(filepath.Join(dir, "field.story.ktml"), []byte(source), len(prefix)+cursor)
			if !ok {
				t.Fatal("no story completion context")
			}
			var labels []string
			for _, item := range list.Items {
				if item.Label == tt.want {
					return
				}
				labels = append(labels, item.Label)
			}
			t.Errorf("completions %v lack %s", labels, tt.want)
		})
	}
}
