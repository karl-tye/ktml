// Package rtwriter holds the writer shared by package ktml's Render and the
// generated-code runtime in package rt, which re-exports it as rt.Writer.
package rtwriter

import (
	"io"
	"sync"
	"unsafe"
)

// Writer is the non-buffering writer generated renderers write through. It
// records whether any byte reached the target, for RenderError.Partial, and
// takes the io.StringWriter fast path when the target has one.
type Writer struct {
	target  io.Writer
	strings io.StringWriter
	written bool
	scratch [32]byte
}

var writers = sync.Pool{New: func() any { return new(Writer) }}

// Wrap returns w unchanged when it is already a *Writer, so nested Render
// calls do not re-wrap. Otherwise it returns a pooled Writer for w and reports
// that the caller owns it and must Release it.
func Wrap(w io.Writer) (*Writer, bool) {
	if writer, ok := w.(*Writer); ok {
		return writer, false
	}
	writer := writers.Get().(*Writer)
	writer.target, writer.written = w, false
	writer.strings, _ = w.(io.StringWriter)
	return writer, true
}

// Release returns an owned writer to the pool.
func Release(w *Writer) {
	w.target, w.strings = nil, nil
	writers.Put(w)
}

// Written reports whether any byte reached the target.
func (w *Writer) Written() bool { return w.written }

// Scratch returns an empty slice over the writer's scratch space, for
// formatting numbers without allocating.
func (w *Writer) Scratch() []byte { return w.scratch[:0] }

// Write implements io.Writer for Go-supplied components rendered into slots.
func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	if n > 0 {
		w.written = true
	}
	return n, err
}

// WriteString implements io.StringWriter.
func (w *Writer) WriteString(s string) (int, error) {
	var n int
	var err error
	if w.strings != nil {
		n, err = w.strings.WriteString(s)
	} else {
		// io.Writer implementations must not modify or retain p, so the
		// string's bytes are passed without a copy.
		n, err = w.target.Write(unsafe.Slice(unsafe.StringData(s), len(s)))
	}
	if n > 0 {
		w.written = true
	}
	return n, err
}
