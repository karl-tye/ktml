package rt

import (
	"errors"
	"io"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/internal/rtwriter"
)

// Writer is the non-buffering writer generated renderers write through. It
// records whether any byte reached the target, for RenderError.Partial, and
// takes the io.StringWriter fast path when the target has one. Generated code
// adds no bufio layer, because net/http already buffers responses.
type Writer = rtwriter.Writer

// Wrap returns w unchanged when it is already a *Writer, so nested public
// Render calls do not re-wrap. Otherwise it returns a pooled Writer for w and
// reports that the caller owns it and must pass it to Finish.
func Wrap(w io.Writer) (*Writer, bool) {
	return rtwriter.Wrap(w)
}

// Finish ends a public Render call. For the call that owns the writer it
// records Partial on a RenderError and returns the writer to the pool; err is
// returned unchanged either way.
func Finish(w *Writer, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		if renderError, ok := errors.AsType[*ktml.RenderError](err); ok {
			renderError.Partial = w.Written()
		}
	}
	rtwriter.Release(w)
	return err
}

func raw(w *Writer, s string) error {
	_, err := w.WriteString(s)
	return err
}

func writeBytes(w *Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}

// WriteError returns the typed error for a failed write in component. It
// unwraps to the writer's error.
func WriteError(component string, err error) error {
	return &ktml.RenderError{Kind: ktml.ErrWrite, Component: component, Err: err}
}
