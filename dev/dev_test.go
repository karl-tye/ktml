package dev

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestFrames_GIVENNestedScopes_WHENBuilt_THENJoinAncestorChainWithDistinctKeyEncodings(t *testing.T) {
	outer := StringFrame("", "example.com/app.Board", "r0", "todo")
	inner := IntegerFrame(outer, "example.com/app.Board", "r1", 42)
	if want := `example.com/app.Board#r0="todo" example.com/app.Board#r1=42`; inner != want {
		t.Errorf("identity = %s, want %s", inner, want)
	}
	if StringFrame("", "c.C", "r0", "42") == IntegerFrame("", "c.C", "r0", 42) {
		t.Error("string and integer frames collide")
	}
	ctx := WithScope(context.Background(), inner)
	if Scope(ctx) != inner || Scope(context.Background()) != "" {
		t.Errorf("Scope() = %q", Scope(ctx))
	}
}

func TestMarkers_GIVENHostileKey_WHENWritten_THENCannotCloseTheComment(t *testing.T) {
	identity := StringFrame("", "c.C", "r0", "--><script>%")
	var output bytes.Buffer
	if err := Begin(&output, identity); err != nil {
		t.Fatal(err)
	}
	if err := End(&output, identity); err != nil {
		t.Fatal(err)
	}
	want := `<!--ktml:item c.C#r0="--%3E%3Cscript%3E%25"--><!--/ktml:item c.C#r0="--%3E%3Cscript%3E%25"-->`
	if output.String() != want {
		t.Errorf("markers = %s, want %s", output.String(), want)
	}
}

func TestInstanceMarkers_GIVENIdentity_WHENWritten_THENAttributeIsEscapedAndBoundariesCannotCloseTheComment(t *testing.T) {
	identity := CallFrame(StringFrame("@7", "c.C", "r0", `"><!--`), "c.C", "c2")
	if want := `@7 c.C#r0="\"><!--" c.C#c2`; identity != want {
		t.Fatalf("identity = %s, want %s", identity, want)
	}
	var output bytes.Buffer
	if err := InstanceAttr(&output, identity); err != nil {
		t.Fatal(err)
	}
	if want := ` data-ktml-i="@7 c.C#r0=&#34;\&#34;&gt;&lt;!--&#34; c.C#c2"`; output.String() != want {
		t.Errorf("attribute = %s, want %s", output.String(), want)
	}
	output.Reset()
	if err := BeginInstance(&output, "@1 c.C#c0"); err != nil {
		t.Fatal(err)
	}
	if err := EndInstance(&output, "@1 c.C#c0"); err != nil {
		t.Fatal(err)
	}
	if want := `<!--ktml:c @1 c.C#c0--><!--/ktml:c @1 c.C#c0-->`; output.String() != want {
		t.Errorf("boundaries = %s, want %s", output.String(), want)
	}
}

func TestInstanceMarkers_GIVENNoIdentity_WHENWritten_THENWriteNothing(t *testing.T) {
	writes := 0
	w := writerFunc(func(p []byte) (int, error) { writes++; return len(p), nil })
	for _, write := range []func() error{
		func() error { return InstanceAttr(w, "") },
		func() error { return BeginInstance(w, "") },
		func() error { return EndInstance(w, "") },
	} {
		if err := write(); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 0 {
		t.Errorf("writes = %d, want 0", writes)
	}
	if CallFrame("", "c.C", "c0") != "c.C#c0" {
		t.Errorf("root call frame = %q", CallFrame("", "c.C", "c0"))
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type recordingRenderer struct {
	components []ktml.Component
}

func (r *recordingRenderer) RenderComponent(ctx context.Context, w io.Writer, component ktml.Component, generated func(context.Context, io.Writer) error) error {
	r.components = append(r.components, component)
	if _, err := io.WriteString(w, "[delegated]"); err != nil {
		return err
	}
	return generated(ctx, w)
}

func TestRender_GIVENNoRenderer_WHENRendered_THENRunsGeneratedCode(t *testing.T) {
	var output bytes.Buffer
	component := ktml.ComponentFunc(func(context.Context, io.Writer) error { return nil })
	err := Render(context.Background(), &output, component, func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "generated")
		return err
	})
	if err != nil || output.String() != "generated" {
		t.Errorf("Render() = %q, %v", output.String(), err)
	}
}

func TestRender_GIVENInstalledRenderer_WHENRendered_THENDelegatesUntilRestored(t *testing.T) {
	renderer := &recordingRenderer{}
	restore := SetRenderer(renderer)
	generated := func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "generated")
		return err
	}
	component := ktml.ComponentFunc(func(context.Context, io.Writer) error { return nil })
	var output bytes.Buffer
	if err := Render(context.Background(), &output, component, generated); err != nil {
		t.Fatal(err)
	}
	if output.String() != "[delegated]generated" || len(renderer.components) != 1 {
		t.Errorf("delegated output = %q, calls = %d", output.String(), len(renderer.components))
	}
	restore()
	output.Reset()
	if err := Render(context.Background(), &output, component, generated); err != nil {
		t.Fatal(err)
	}
	if output.String() != "generated" {
		t.Errorf("restored output = %q", output.String())
	}
}
