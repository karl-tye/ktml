// Package ktml provides the runtime contract used by generated KTML components.
package ktml

import (
	"context"
	"errors"
	"io"

	"github.com/karl-tye/ktml/internal/rtwriter"
)

// Component renders transport-neutral output to a writer.
type Component interface {
	Render(context.Context, io.Writer) error
}

// ComponentFunc adapts a render function into a Component.
type ComponentFunc func(context.Context, io.Writer) error

// Render implements Component.
func (f ComponentFunc) Render(ctx context.Context, w io.Writer) error {
	return f(ctx, w)
}

// Render writes components in order and stops at the first error, which it
// returns unchanged. The components share one writer, so a RenderError's
// Partial reports whether any of them wrote before the failure.
func Render(ctx context.Context, w io.Writer, components ...Component) error {
	writer, owned := rtwriter.Wrap(w)
	var err error
	for _, component := range components {
		if component == nil {
			continue
		}
		if err = component.Render(ctx, writer); err != nil {
			break
		}
	}
	if owned {
		if renderError, ok := errors.AsType[*RenderError](err); ok {
			renderError.Partial = writer.Written()
		}
		rtwriter.Release(writer)
	}
	return err
}

// Ptr returns a pointer to a copy of value for optional and defaulted inputs.
func Ptr[T any](value T) *T { return &value }

// Join returns one Component that renders components in order, for supplying
// several Go-built values as one content slot. Nil entries are skipped.
func Join(components ...Component) Component {
	joined := append([]Component(nil), components...)
	return ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return Render(ctx, w, joined...)
	})
}
