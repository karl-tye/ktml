// Package app holds the Go callers of the widgets: handlers, a fragment
// renderer, an SSE-style render, and components passed as values.
package app

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/a-h/templ"

	"example.com/inplace/widgets"
)

// Renderer is the application's renderer boundary.
type Renderer struct{}

// Component renders a fragment.
func (Renderer) Component(c templ.Component, w http.ResponseWriter, r *http.Request) {
	_ = Fragment(r.Context(), w, c)
}

// Fragment renders into a buffer and writes only a complete response.
func Fragment(ctx context.Context, w http.ResponseWriter, c templ.Component) error {
	var b bytes.Buffer
	if err := c.Render(ctx, &b); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return err
	}
	_, err := w.Write(b.Bytes())
	return err
}

// Page is a page body held as a component value.
type Page struct {
	Content templ.Component
	Title   string
}

var items = []widgets.Item{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta", Done: true}}

// ListHandler renders a fragment through the renderer.
func ListHandler(render Renderer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render.Component(widgets.List(items), w, r)
	}
}

// BadgeHandler renders straight into the response.
func BadgeHandler(w http.ResponseWriter, r *http.Request) {
	widgets.Badge("count", len(items)).Render(r.Context(), w)
}

// GreetingHandler needs the request context for its presenter.
func GreetingHandler(w http.ResponseWriter, r *http.Request) {
	_ = Fragment(r.Context(), w, widgets.Greeting("Hello"))
}

// Handler serves a component with templ.Handler.
func Handler() http.Handler {
	return templ.Handler(widgets.List(items))
}

// Event renders an SSE payload.
func Event(ctx context.Context, w io.Writer) error {
	return widgets.Stamp("tick").Render(ctx, w)
}

// Home is a page whose content is a component value.
func Home() Page {
	return Page{Content: widgets.Group(widgets.Badge("a", 1), widgets.Badge("b", 2)), Title: "Home"}
}
