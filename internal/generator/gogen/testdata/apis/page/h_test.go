package page

import (
	"example.com/fixture/ui"
	"github.com/karl-tye/ktml"
)

func page(heading string, note *string) ktml.Component { return Page{Heading: heading, Note: note} }
func noteOf(text string) ktml.Component                { return Note{Text: text} }
func frame(children ktml.Component) ktml.Component     { return ui.Frame{Children: children} }
func card(title string, children ktml.Component) ktml.Component {
	return ui.Card{Title: title, Children: children}
}
