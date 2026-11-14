package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// writeComponentProject writes a temporary module that requires this
// repository's ktml runtime.
func writeComponentProject(t *testing.T, files map[string]string) {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	writeSchemaProject(t, files)
	goMod := "module example.com/app\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\n\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"
	if err := os.WriteFile("go.mod", []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
}

var componentProject = map[string]string{
	"ui/card.ktml":          "Card [title: string, children?: content] {\n\tarticle {\n\t\th2 \"{title}\"\n\t\tchildren\n\t}\n}\n",
	"web/page.ktml":         "import { Card } from \"../ui/card.ktml\"\nimport { Person } from \"./person.ktmlschema\"\n\nPage [person: Person] {\n\tCard title=\"{person.name}\" {\n\t\tp \"hi\"\n\t}\n}\n",
	"web/person.ktmlschema": "record Person [name: string]",
	"web/handler.go":        "package handlers\n",
}

func TestGenerateCmd_GIVENComponentsWithoutConfig_WHENGeneratedRecursively_THENWritesCompilingProductionAndDevelopmentVariants(t *testing.T) {
	writeComponentProject(t, componentProject)
	files, err := expandGenerateArgs([]string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ui/card.ktml", "web/page.ktml", "web/person.ktmlschema"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("expandGenerateArgs() = %v, want %v", files, want)
	}
	for _, file := range files {
		if err := generateFile(file, false); err != nil {
			t.Fatalf("generateFile(%s) error = %v", file, err)
		}
	}
	production, err := gogen.StripLineDirectives(mustReadFile(t, "web/page_ktml.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(production), "//go:build !ktmldev\n") || !strings.Contains(string(production), "package handlers") || !strings.Contains(string(production), `(ui.Card{Title: props.Person.Name`) {
		t.Fatalf("generated page:\n%s", production)
	}
	if development := string(mustReadFile(t, "web/page_ktml_dev.go")); !strings.HasPrefix(development, "//go:build ktmldev\n") {
		t.Fatalf("generated development page:\n%s", development)
	}
	if err := os.WriteFile("web/handler_test.go", []byte(`package handlers

import (
	"context"
	"strings"
	"testing"
)

func TestPage(t *testing.T) {
	var output strings.Builder
	if err := (Page{Person: Person{Name: "<Ann>"}}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "<article><h2>&lt;Ann&gt;</h2><p>hi</p></article>" {
		t.Errorf("got %s", got)
	}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"vet", "./..."}, {"vet", "-tags", "ktmldev", "./..."}, {"test", "-count=1", "./..."}} {
		if output, err := exec.Command("go", args...).CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	state := string(mustReadFile(t, filepath.Join(".ktml", "state.json")))
	if !strings.Contains(state, "web/page_ktml.go") || !strings.Contains(state, "web/page_ktml_dev.go") {
		t.Fatalf("build state does not record both variants:\n%s", state)
	}
	before, err := os.Stat("web/page_ktml.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := generateFile("web/page.ktml", false); err != nil {
		t.Fatalf("second generateFile() error = %v", err)
	}
	if after, err := os.Stat("web/page_ktml.go"); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an unchanged document was regenerated: %v", err)
	}
}

func TestGenerateCmd_GIVENHandwrittenCollision_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	tests := []struct {
		name string
		go_  string
		want string
	}{
		{name: "component type", go_: "package ui\n\ntype Card struct{}\n", want: "Card (declared in card.go)"},
		{name: "internal renderer", go_: "package ui\n\nfunc ktmlRenderCard() {}\n", want: "ktmlRenderCard (declared in card.go)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeComponentProject(t, map[string]string{"ui/card.ktml": componentProject["ui/card.ktml"], "ui/card.go": tt.go_})
			err := generateFile("ui/card.ktml", false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("generateFile() error = %v, want %s", err, tt.want)
			}
			if _, statErr := os.Stat("ui/card_ktml.go"); !os.IsNotExist(statErr) {
				t.Errorf("generation wrote output despite the collision: %v", statErr)
			}
		})
	}
}

func TestExpandGenerateArgs_GIVENPatterns_WHENExpanded_THENSelectsKTMLSources(t *testing.T) {
	writeSchemaProject(t, map[string]string{
		"a.ktml":              "",
		"sub/b.ktml":          "",
		"sub/c.ktmlschema":    "",
		"sub/d.go":            "",
		".hidden/e.ktml":      "",
		"node_modules/f.ktml": "",
	})
	tests := []struct {
		args []string
		want []string
	}{
		{args: []string{"."}, want: []string{"a.ktml"}},
		{args: []string{"./..."}, want: []string{"a.ktml", "sub/b.ktml", "sub/c.ktmlschema"}},
		{args: []string{"sub/..."}, want: []string{"sub/b.ktml", "sub/c.ktmlschema"}},
		{args: []string{"sub/b.ktml"}, want: []string{"sub/b.ktml"}},
	}
	for _, tt := range tests {
		got, err := expandGenerateArgs(tt.args)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("expandGenerateArgs(%v) = %v, %v; want %v", tt.args, got, err, tt.want)
		}
	}
	if _, err := expandGenerateArgs([]string{"missing/..."}); err == nil {
		t.Error("expandGenerateArgs(missing/...) succeeded")
	}
}

func TestGenerateCmd_GIVENAStoryDocument_WHENGenerated_THENItIsSkippedAndWritesNothing(t *testing.T) {
	files := map[string]string{"ui/card.story.ktml": "import { Card } from \"./card.ktml\"\n\nstory Card.Plain [title = \"Plain\"]\n"}
	for name, source := range componentProject {
		files[name] = source
	}
	writeComponentProject(t, files)
	expanded, err := expandGenerateArgs([]string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range expanded {
		if strings.HasSuffix(file, ".story.ktml") {
			t.Fatalf("expandGenerateArgs() = %v includes a story document", expanded)
		}
	}
	if err := generateFile("ui/card.story.ktml", false); err != nil {
		t.Fatalf("generateFile(story) error = %v", err)
	}
	if matches, _ := filepath.Glob("ui/card.story*.go"); len(matches) != 0 {
		t.Fatalf("story generated %v", matches)
	}
}
