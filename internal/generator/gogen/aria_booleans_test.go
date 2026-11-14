package gogen_test

import "testing"

func TestGenerate_GIVENBooleanPathsInARIAAttributes_WHENRendered_THENWritesTrueOrFalse(t *testing.T) {
	generated := generateWorkspaceSource(t, `Field [attrs?: Attributes] {
	input (type="checkbox" if attrs? { ...attrs })
}

Menu [open: boolean, busy: boolean] {
	button (type="button" aria-expanded="{open}" ARIA-Pressed="{open}" disabled="{open}" aria-hidden="false" aria-current) "x"
	div (if busy { aria-busy="{open}" } else { aria-live="polite" })
	Field attrs=(aria-checked="{open}" required="{open}")
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestARIABooleans(t *testing.T) {
	for _, tt := range []struct{ menu Menu; want string }{
		{Menu{Open: true, Busy: true}, "<button type=\"button\" aria-expanded=\"true\" ARIA-Pressed=\"true\" disabled aria-hidden=\"false\" aria-current>x</button>" +
			"<div aria-busy=\"true\"></div><input type=\"checkbox\" aria-checked=\"true\" required>"},
		{Menu{Busy: true}, "<button type=\"button\" aria-expanded=\"false\" ARIA-Pressed=\"false\" aria-hidden=\"false\" aria-current>x</button>" +
			"<div aria-busy=\"false\"></div><input type=\"checkbox\" aria-checked=\"false\">"},
	} {
		var output strings.Builder
		if err := tt.menu.Render(context.Background(), &output); err != nil { t.Fatal(err) }
		if got := output.String(); got != tt.want {
			t.Errorf("rendered\n%s\nwant\n%s", got, tt.want)
		}
	}
}
`)
}
