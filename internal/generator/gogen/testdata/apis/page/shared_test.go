package page

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/karl-tye/ktml"
)

const want = `<main><section><article class="card" data-count="3"><h2>Hi</h2><p class="sub">&lt;Hi&gt;</p><div class="body"><div class="panel" aria-label="inner"><p>body &amp; more</p></div></div><footer><button type="button">Save</button></footer></article></section><div class="panel" aria-label="Hi"></div><small>n</small></main>`

var errClosed = errors.New("closed")

type failAfter struct{ writes int }

func (w *failAfter) Write(p []byte) (int, error) {
	if w.writes == 0 {
		return 0, errClosed
	}
	w.writes--
	return len(p), nil
}

func renderError(t *testing.T, err error, kind ktml.ErrorKind) *ktml.RenderError {
	t.Helper()
	typed, ok := errors.AsType[*ktml.RenderError](err)
	if !ok || typed.Kind != kind {
		t.Fatalf("error = %#v, want a %s RenderError", err, kind)
	}
	return typed
}

func TestAPI_GIVENPage_WHENRendered_THENWritesTheExpectedBytes(t *testing.T) {
	note := "n"
	var buffer bytes.Buffer
	if err := page("Hi", &note).Render(context.Background(), &buffer); err != nil {
		t.Fatal(err)
	}
	if buffer.String() != want {
		t.Errorf("got  %s\nwant %s", buffer.String(), want)
	}
}

func TestAPI_GIVENMissingChildren_WHENRendered_THENFailsBeforeTheComponentWrites(t *testing.T) {
	var buffer bytes.Buffer
	typed := renderError(t, frame(nil).Render(context.Background(), &buffer), ktml.ErrMissingContent)
	if typed.Component != "example.com/fixture/ui.Frame" || typed.Input != "children" || typed.Partial || buffer.Len() != 0 {
		t.Errorf("alone: %#v, %q", typed, buffer.String())
	}
	err := ktml.Render(context.Background(), &buffer, noteOf("x"), frame(nil))
	typed = renderError(t, err, ktml.ErrMissingContent)
	if !typed.Partial || buffer.String() != "<small>x</small>" {
		t.Errorf("after a sibling: %#v, %q", typed, buffer.String())
	}
}

func TestAPI_GIVENFailingWriter_WHENRendered_THENReturnsATypedPartialWriteError(t *testing.T) {
	note := "n"
	err := page("Hi", &note).Render(context.Background(), &failAfter{writes: 1})
	typed := renderError(t, err, ktml.ErrWrite)
	if !typed.Partial || !errors.Is(err, errClosed) || typed.Component != "example.com/fixture/ui.Frame" {
		t.Errorf("error = %#v", typed)
	}
}

func TestAPI_GIVENCanceledContext_WHENRendered_THENFailsBeforeWriting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buffer bytes.Buffer
	err := page("Hi", nil).Render(ctx, &buffer)
	typed := renderError(t, err, ktml.ErrCanceled)
	if !errors.Is(err, context.Canceled) || typed.Partial || buffer.Len() != 0 || typed.Component != "example.com/fixture/page.Page" {
		t.Errorf("error = %#v, output %q", typed, buffer.String())
	}
}

func TestAPI_GIVENGoComponentInSlot_WHENRendered_THENRendersItAndReturnsItsErrorUnwrapped(t *testing.T) {
	var buffer bytes.Buffer
	child := ktml.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<x>")
		return err
	})
	if err := card("t", ktml.Join(child, nil, child)).Render(context.Background(), &buffer); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != `<article class="card" data-count="0"><h2>t</h2><div class="body"><x><x></div></article>` {
		t.Errorf("got %s", got)
	}
	sentinel := errors.New("child failed")
	failing := ktml.ComponentFunc(func(context.Context, io.Writer) error { return sentinel })
	if err := card("t", failing).Render(context.Background(), io.Discard); err != sentinel {
		t.Errorf("error = %#v, want the child's error unchanged", err)
	}
}

func TestAPI_GIVENNestedPublicRender_WHENRendered_THENDoesNotAllocateMoreThanTheFills(t *testing.T) {
	var buffer bytes.Buffer
	buffer.Grow(4096)
	component := noteOf("x")
	allocations := testing.AllocsPerRun(100, func() {
		buffer.Reset()
		_ = component.Render(context.Background(), &buffer)
	})
	if allocations != 0 {
		t.Errorf("rendering a leaf allocated %v times, want 0", allocations)
	}
}
