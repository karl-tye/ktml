// Package manual holds the call sites the codemod must not rewrite.
package manual

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"example.com/inplace/widgets"
)

// Badges stores the template as a function value.
var Badges = widgets.Badge

// Greeting has no context to give the presenter.
func Greeting() templ.Component {
	return widgets.Greeting("Hi")
}

// Panel passes children through the context.
func Panel(ctx context.Context, w io.Writer, body templ.Component) error {
	return widgets.Panel(widgets.Item{ID: "p", Label: "Panel"}).Render(templ.WithChildren(ctx, body), w)
}
