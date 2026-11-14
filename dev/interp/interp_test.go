package interp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/compiler"
)

// Greeting, Banner, and Stranger stand in for generated Hybrid H props: the
// interpreter finds a component by its Go package path and type name, and
// reads inputs by their generated field names.
type Greeting struct {
	Name string
	Note *string
}

func (Greeting) Render(context.Context, io.Writer) error { return nil }

type Banner struct {
	Title string
}

func (Banner) Render(context.Context, io.Writer) error { return nil }

type Stranger struct{}

func (Stranger) Render(context.Context, io.Writer) error { return nil }

var testOptions = interp.Options{Packages: func(string) (string, error) {
	return reflect.TypeOf(Greeting{}).PkgPath(), nil
}}

func indexWorkspace(t *testing.T, files map[string]string) (string, *compiler.Workspace, *compiler.Snapshot) {
	t.Helper()
	root := t.TempDir()
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspace := compiler.NewWorkspace(root)
	snapshot, err := workspace.Index()
	if err != nil {
		t.Fatal(err)
	}
	return root, workspace, snapshot
}

func renderString(t *testing.T, program *interp.Program, props ktml.Component) (string, error) {
	t.Helper()
	var output bytes.Buffer
	err := program.Render(context.Background(), &output, props)
	return output.String(), err
}

func TestRender_GIVENWorkspaceProps_WHENRendered_THENWritesEscapedOutput(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": `Greeting [name: string, note?: string] {
	p title="{name}" "Hello {name}"
	if note? {
		small "{note}"
	}
}
`})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	note := "<b>"
	output, err := renderString(t, program, Greeting{Name: `"Ann" & <Bo>`, Note: &note})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if want := `<p title="&#34;Ann&#34; &amp; &lt;Bo&gt;">Hello &#34;Ann&#34; &amp; &lt;Bo&gt;</p><small>&lt;b&gt;</small>`; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestRender_GIVENOptionalAndDefaultedInputsAddedAfterTheBuild_WHENPropsLackTheirFields_THENRendersThemAbsentAndDefaulted(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": `Greeting [name: string, note?: string, tone?: string, greeting: string = "Hi"] {
	p "{greeting} {name}"
	if tone? {
		i "{tone}"
	} else {
		i "plain"
	}
}
`})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := renderString(t, program, Greeting{Name: "Ann"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if want := `<p>Hi Ann</p><i>plain</i>`; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestRender_GIVENRequiredInputAddedAfterTheBuild_WHENPropsLackItsField_THENFailsBeforeWriting(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": `Greeting [name: string, title: string] {
	p "{title} {name}"
}
`})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := renderString(t, program, Greeting{Name: "Ann"})
	if err == nil || !strings.Contains(err.Error(), "required input title") || output != "" {
		t.Fatalf("Render() = %q, %v; want a rebuild error before any write", output, err)
	}
}

func TestRender_GIVENValueOfAnotherType_WHENRendered_THENFailsBeforeWriting(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": "Greeting [name: string] {\n\tp \"{name}\"\n}\n"})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, props := range []ktml.Component{Stranger{}, ktml.ComponentFunc(func(context.Context, io.Writer) error { return nil }), (*Greeting)(nil)} {
		output, err := renderString(t, program, props)
		if err == nil || output != "" {
			t.Errorf("Render(%T) = %q, %v; want an error", props, output, err)
		}
	}
}

func TestRender_GIVENPointerProps_WHENRendered_THENMatchesValueProps(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": "Greeting [name: string] {\n\tp \"{name}\"\n}\n"})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := renderString(t, program, &Greeting{Name: "Ann"})
	if err != nil || output != "<p>Ann</p>" {
		t.Fatalf("Render() = %q, %v", output, err)
	}
}

func TestRender_GIVENFailingWriter_WHENRenderedThroughComponent_THENReturnsTypedWriteErrorWithoutPartial(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": "Greeting [name: string] {\n\tp \"{name}\"\n}\n"})
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	failure := errors.New("disk full")
	err = program.Component(Greeting{Name: "Ann"}).Render(context.Background(), failingWriter{failure})
	var typed *ktml.RenderError
	if !errors.As(err, &typed) || typed.Kind != ktml.ErrWrite || !errors.Is(err, failure) || typed.Partial {
		t.Fatalf("Render() error = %#v, want ErrWrite without Partial", err)
	}
	if want := reflect.TypeOf(Greeting{}).PkgPath() + ".Greeting"; typed.Component != want {
		t.Errorf("Component = %q, want %q", typed.Component, want)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestNew_GIVENSnapshotWithErrors_WHENCompiled_THENFails(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{"greeting.ktml": "Greeting [name: string] {\n\tp \"{missing}\"\n}\n"})
	if program, err := interp.New(snapshot, testOptions); err == nil || program != nil {
		t.Fatalf("New() = %v, %v; want an error", program, err)
	}
}

func TestSwap_GIVENOneChangedDocument_WHENSwapped_THENRendersItsNewBodyAndKeepsTheOldProgram(t *testing.T) {
	root, workspace, snapshot := indexWorkspace(t, map[string]string{
		"greeting.ktml": "Greeting [name: string] {\n\tp \"Hello {name}\"\n}\n",
		"banner.ktml":   "import { Greeting } from \"./greeting.ktml\"\n\nBanner [title: string] {\n\th1 \"{title}\"\n\tGreeting name=\"{title}\"\n}\n",
	})
	before, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	greeting := filepath.Join(root, "greeting.ktml")
	changed, err := workspace.Update(greeting, 2, []byte("Greeting [name: string] {\n\tp class=\"new\" \"Welcome {name}\"\n}\n"))
	if err != nil || changed.Err() != nil {
		t.Fatalf("Update() = %v, %v", err, changed.Err())
	}
	after, err := before.Swap(changed, []string{greeting})
	if err != nil {
		t.Fatalf("Swap() error = %v", err)
	}
	// The unchanged caller's compiled body reaches the swapped callee.
	output, err := renderString(t, after, Banner{Title: "Ann"})
	if err != nil || output != `<h1>Ann</h1><p class="new">Welcome Ann</p>` {
		t.Errorf("after Swap: %q, %v", output, err)
	}
	output, err = renderString(t, before, Banner{Title: "Ann"})
	if err != nil || output != `<h1>Ann</h1><p>Hello Ann</p>` {
		t.Errorf("old program after Swap: %q, %v", output, err)
	}
	if !after.Reuses(before, filepath.Join(root, "banner.ktml")) || after.Reuses(before, greeting) {
		t.Errorf("Swap recompiled the wrong Documents")
	}
}

func TestDependencies_GIVENNestedCalls_WHENQueried_THENReturnsTheTransitiveClosure(t *testing.T) {
	root, _, snapshot := indexWorkspace(t, map[string]string{
		"page.ktml": `import { Banner } from "./banner.ktml"

Greeting [name: string, note?: string] {
	if note? {
		Banner title="{name}"
	}
}
`,
		"banner.ktml": `Banner [title: string] {
	div {
		Stranger
	}
}

Stranger [] {
	hr
}
`,
	})
	if !snapshot.Valid() {
		t.Fatalf("snapshot: %v", snapshot.Err())
	}
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	pkg := reflect.TypeOf(Greeting{}).PkgPath()
	id, ok := program.ComponentID(Greeting{})
	if !ok || id != pkg+".Greeting" {
		t.Errorf("ComponentID() = %q, %v", id, ok)
	}
	if _, ok := program.ComponentID(&struct{ ktml.Component }{}); ok {
		t.Error("ComponentID() knows an unrelated type")
	}
	got := strings.Join(program.Dependencies(pkg+".Greeting"), ",")
	if want := pkg + ".Banner," + pkg + ".Greeting," + pkg + ".Stranger"; got != want {
		t.Errorf("Dependencies() = %s, want %s", got, want)
	}
	if got := strings.Join(program.FileComponents(filepath.Join(root, "banner.ktml")), ","); got != pkg+".Banner,"+pkg+".Stranger" {
		t.Errorf("FileComponents() = %s", got)
	}
}

func TestCallTarget_GIVENCallSites_WHENQueried_THENNamesTheComponentEachRenders(t *testing.T) {
	_, _, snapshot := indexWorkspace(t, map[string]string{
		"page.ktml": `import { Banner } from "./banner.ktml"

Greeting [name: string, note?: string] {
	if note? {
		Banner title="{name}"
	} else {
		Stranger
	}
}

Stranger [] {
	Banner title="inner"
}
`,
		"banner.ktml": `Banner [title: string] {
	div {
		"{title}"
	}
}
`,
	})
	if !snapshot.Valid() {
		t.Fatalf("snapshot: %v", snapshot.Err())
	}
	program, err := interp.New(snapshot, testOptions)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	pkg := reflect.TypeOf(Greeting{}).PkgPath()
	for _, test := range []struct{ caller, site, want string }{
		{pkg + ".Greeting", "c0", pkg + ".Banner"},
		{pkg + ".Greeting", "c1", pkg + ".Stranger"},
		{pkg + ".Stranger", "c0", pkg + ".Banner"},
		{pkg + ".Greeting", "c2", ""},
		{pkg + ".Greeting", "o0", ""},
		{pkg + ".Missing", "c1", ""},
	} {
		got, ok := program.CallTarget(test.caller, test.site)
		if got != test.want || ok != (test.want != "") {
			t.Errorf("CallTarget(%s, %s) = %q, %v; want %q", test.caller, test.site, got, ok, test.want)
		}
	}
}
