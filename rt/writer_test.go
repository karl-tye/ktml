package rt

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/karl-tye/ktml"
)

type failingWriter struct{ after int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.after <= 0 {
		return 0, errors.New("closed")
	}
	w.after--
	return len(p), nil
}

func TestWrap_GIVENWriter_WHENWrappedTwice_THENReusesTheFirstAndOnlyTheOwnerFinishes(t *testing.T) {
	var buffer bytes.Buffer
	outer, owned := Wrap(&buffer)
	inner, innerOwned := Wrap(outer)
	if !owned || innerOwned || inner != outer {
		t.Fatalf("Wrap() = %p/%v, %p/%v; want the outer writer reused", outer, owned, inner, innerOwned)
	}
	if err := Raw(inner, "a"); err != nil {
		t.Fatal(err)
	}
	if err := Finish(inner, innerOwned, nil); err != nil || !outerUsable(outer) {
		t.Fatalf("a non-owning Finish released the writer: %v", err)
	}
	if err := Finish(outer, owned, nil); err != nil || buffer.String() != "a" {
		t.Fatalf("Finish() = %v, output %q", err, buffer.String())
	}
}

func TestFinish_GIVENRenderError_WHENOwned_THENRecordsWhetherBytesReachedTheTarget(t *testing.T) {
	tests := []struct {
		name        string
		write       bool
		wantPartial bool
	}{
		{name: "nothing written", write: false, wantPartial: false},
		{name: "bytes written", write: true, wantPartial: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, owned := Wrap(io.Discard)
			if tt.write {
				if err := Raw(w, "x"); err != nil {
					t.Fatal(err)
				}
			}
			err := Finish(w, owned, InvalidState("app.Card", "state"))
			renderError, ok := errors.AsType[*ktml.RenderError](err)
			if !ok || renderError.Partial != tt.wantPartial {
				t.Errorf("Finish() = %#v, want Partial %v", err, tt.wantPartial)
			}
		})
	}
}

func TestWriter_GIVENFailingTarget_WHENWritten_THENReturnsTheWriterErrorForWriteError(t *testing.T) {
	w, owned := Wrap(&failingWriter{after: 3})
	if err := Text(w, "a<b"); err != nil {
		t.Fatalf("first run failed early: %v", err)
	}
	err := Text(w, "c")
	if err == nil || err.Error() != "closed" {
		t.Fatalf("Text() error = %v, want the writer error", err)
	}
	wrapped := Finish(w, owned, WriteError("app.Card", err))
	renderError, ok := errors.AsType[*ktml.RenderError](wrapped)
	if !ok || renderError.Kind != ktml.ErrWrite || renderError.Component != "app.Card" || !renderError.Partial || errors.Unwrap(wrapped) != err {
		t.Errorf("WriteError() = %#v", wrapped)
	}
}

func TestWriter_GIVENTargetWithoutWriteString_WHENWritten_THENWritesTheBytes(t *testing.T) {
	var target struct{ io.Writer }
	var buffer bytes.Buffer
	target.Writer = &buffer
	w, owned := Wrap(target)
	defer Finish(w, owned, nil)
	if err := Text(w, `"x"`); err != nil || buffer.String() != "&#34;x&#34;" {
		t.Fatalf("Text() = %v, output %q", err, buffer.String())
	}
}

func TestPrimitives_GIVENWrappedBuffer_WHENWritten_THENDoNotAllocate(t *testing.T) {
	var buffer bytes.Buffer
	buffer.Grow(1 << 16)
	w, owned := Wrap(&buffer)
	defer Finish(w, owned, nil)
	allocations := testing.AllocsPerRun(100, func() {
		buffer.Reset()
		_ = Text(w, `<a href="x">&'tail`)
		_ = Attr(w, "plain")
		_ = TextInt(w, 1234567890)
		_ = TextNumber(w, 12.25)
		_ = JSONScript(w, ktml.TrustJSON(`{"a":"</script>"}`))
		_ = ScriptAttr(w, ktml.TrustScript(`go('x')`))
		_ = ScriptBool(w, true)
	})
	if allocations != 0 {
		t.Errorf("primitives allocated %v times per run, want 0", allocations)
	}
}

func TestEscapeString_GIVENStaticMarkup_WHENEscaped_THENMatchesThePrimitives(t *testing.T) {
	for _, value := range []string{"", "plain", `<a href="x">&'`, "tail&"} {
		var buffer bytes.Buffer
		w, owned := Wrap(&buffer)
		if err := Text(w, value); err != nil {
			t.Fatal(err)
		}
		_ = Finish(w, owned, nil)
		if got := EscapeString(value); got != buffer.String() {
			t.Errorf("EscapeString(%q) = %q, Text wrote %q", value, got, buffer.String())
		}
	}
}

// outerUsable reports whether a writer still has its target.
func outerUsable(w *Writer) bool {
	_, err := w.WriteString("")
	return err == nil
}
