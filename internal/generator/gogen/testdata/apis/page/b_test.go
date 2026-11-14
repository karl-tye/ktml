package page

import (
	"context"
	"io"

	"example.com/fixture/ui"
	"github.com/karl-tye/ktml"
)

func funcOf(component ktml.Component) ktml.ComponentFunc {
	if component == nil {
		return nil
	}
	return component.Render
}
func page(heading string, note *string) ktml.Component {
	return ktml.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return RenderPage(ctx, w, PageProps{Heading: heading, Note: note})
	})
}
func noteOf(text string) ktml.Component {
	return ktml.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return RenderNote(ctx, w, NoteProps{Text: text})
	})
}
func frame(children ktml.Component) ktml.Component {
	return ktml.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return ui.RenderFrame(ctx, w, ui.FrameProps{Children: funcOf(children)})
	})
}
func card(title string, children ktml.Component) ktml.Component {
	return ktml.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return ui.RenderCard(ctx, w, ui.CardProps{Title: title, Children: funcOf(children)})
	})
}
