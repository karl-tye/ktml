// Package dev holds the development-only Render Identity helpers and the
// delegation hook used by the ktmldev variant of generated KTML renderers
// (ADR-0004). Production binaries must not link it: generated production
// files never import it.
package dev

import (
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/rt"
)

type scopeKey struct{}

// WithScope returns a context whose Render Identity scope is identity, so the
// repetitions of a component called from a repetition body extend the
// caller's ancestor-key chain.
func WithScope(ctx context.Context, identity string) context.Context {
	return context.WithValue(ctx, scopeKey{}, identity)
}

// Scope returns the ancestor Render Identity carried by ctx, or "" at the
// root.
func Scope(ctx context.Context) string {
	identity, _ := ctx.Value(scopeKey{}).(string)
	return identity
}

// StringFrame appends a string-keyed frame to scope.
func StringFrame(scope, component, source, key string) string {
	return frame(scope, component, source, strconv.Quote(key))
}

// IntegerFrame appends an integer-keyed frame to scope.
func IntegerFrame(scope, component, source string, key int64) string {
	return frame(scope, component, source, strconv.FormatInt(key, 10))
}

// CallFrame appends the frame of a component call (source c<n>) or content
// outlet (source o<n>) of component to scope. The result is the Render
// Identity of the instance rendered at that site.
func CallFrame(scope, component, source string) string {
	current := component + "#" + source
	if scope == "" {
		return current
	}
	return scope + " " + current
}

// frame is component#source=key, separated from the ancestor chain by a
// space.
func frame(scope, component, source, key string) string {
	current := component + "#" + source + "=" + key
	if scope == "" {
		return current
	}
	return scope + " " + current
}

var commentEscaper = strings.NewReplacer("%", "%25", "<", "%3C", ">", "%3E")

// Begin writes the opening fragment boundary of one repetition item.
func Begin(w io.Writer, identity string) error {
	_, err := io.WriteString(w, "<!--ktml:item "+commentEscaper.Replace(identity)+"-->")
	return err
}

// End writes the closing fragment boundary of one repetition item.
func End(w io.Writer, identity string) error {
	_, err := io.WriteString(w, "<!--/ktml:item "+commentEscaper.Replace(identity)+"-->")
	return err
}

// InstanceAttr writes the instance attribute of a single-root component's
// root element. An instance without an identity writes nothing.
func InstanceAttr(w io.Writer, identity string) error {
	if identity == "" {
		return nil
	}
	_, err := io.WriteString(w, ` data-ktml-i="`+rt.EscapeString(identity)+`"`)
	return err
}

// BeginInstance writes the opening boundary of a multi-root component
// instance. An instance without an identity writes nothing.
func BeginInstance(w io.Writer, identity string) error {
	if identity == "" {
		return nil
	}
	_, err := io.WriteString(w, "<!--ktml:c "+commentEscaper.Replace(identity)+"-->")
	return err
}

// EndInstance writes the closing boundary of a multi-root component instance.
func EndInstance(w io.Writer, identity string) error {
	if identity == "" {
		return nil
	}
	_, err := io.WriteString(w, "<!--/ktml:c "+commentEscaper.Replace(identity)+"-->")
	return err
}

// Renderer renders generated components in place of their compiled bodies.
// The development registry (ktml/dev/hmr) installs one to track root
// instances and to render through the current interpreter program. generated
// renders the component's compiled body.
type Renderer interface {
	RenderComponent(ctx context.Context, w io.Writer, component ktml.Component, generated func(context.Context, io.Writer) error) error
}

type installed struct{ renderer Renderer }

var current atomic.Pointer[installed]

// SetRenderer installs renderer for every generated public Render of the
// process and returns a function that restores the previous one.
func SetRenderer(renderer Renderer) (restore func()) {
	previous := current.Swap(&installed{renderer: renderer})
	return func() { current.Store(previous) }
}

// Render is the delegation hook of a ktmldev generated public Render: it
// hands the component to the installed Renderer, or runs generated when none
// is installed.
func Render(ctx context.Context, w io.Writer, component ktml.Component, generated func(context.Context, io.Writer) error) error {
	if hook := current.Load(); hook != nil && hook.renderer != nil {
		return hook.renderer.RenderComponent(ctx, w, component, generated)
	}
	return generated(ctx, w)
}
