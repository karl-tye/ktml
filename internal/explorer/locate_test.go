package explorer

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

// TestLocate_GIVENAnElementPath_WHENLocated_THENNamesEachElementsDeclaration
// finds where the picker's elements are declared, by component Go ID and
// relative to the project.
func TestLocate_GIVENAnElementPath_WHENLocated_THENNamesEachElementsDeclaration(t *testing.T) {
	s, _, root := newTestServer(t, fixtureFiles, nil)
	got := s.Locate(cardID, nil, []compiler.ElementStep{{Tag: "section", Classes: []string{"card"}}, {Tag: "ul"}, {Tag: "li"}})
	want := []Location{
		{File: "ui/card.ktml", Line: 11, Column: 2, Component: "Card"},
		{File: "ui/card.ktml", Line: 16, Column: 3, Component: "Card"},
		{File: "ui/card.ktml", Line: 19, Column: 6, Component: "Card"},
	}
	if len(got) != len(want) {
		t.Fatalf("Locate() = %+v", got)
	}
	for index := range want {
		if got[index].File != want[index].File || got[index].Line != want[index].Line || got[index].Column != want[index].Column || got[index].Component != want[index].Component {
			t.Errorf("step %d = %+v, want %+v", index, got[index], want[index])
		}
		if got[index].Path == "" || got[index].Path[:len(root)] != root {
			t.Errorf("step %d path = %q, want one in %s", index, got[index].Path, root)
		}
	}
	// A Render Clause body of a signature.
	if got := s.Locate(statusID, nil, []compiler.ElementStep{{Tag: "strong", Classes: []string{"failed"}}}); len(got) != 1 || got[0].Line != 41 {
		t.Errorf("Locate(Status strong) = %+v", got)
	}
	if got := s.Locate("ktml.explorer/ui.Missing", nil, []compiler.ElementStep{{Tag: "div"}}); len(got) != 1 || got[0].File != "" {
		t.Errorf("Locate(Missing) = %+v", got)
	}
	// A link names its caller by Go ID.
	if got := s.Locate(statusID, []Link{{Caller: cardID, Site: "c0"}}, []compiler.ElementStep{{Tag: "em"}}); len(got) != 1 || got[0].Line != 33 {
		t.Errorf("Locate(Status em) = %+v", got)
	}

	declaration, ok := s.Declaration(cardID)
	if !ok || declaration.File != "ui/card.ktml" || declaration.Line != 3 || declaration.Column != 1 || declaration.Component != "Card" {
		t.Errorf("Declaration(Card) = %+v, %v", declaration, ok)
	}
	if _, ok := s.Declaration("ktml.explorer/ui.Missing"); ok {
		t.Error("Declaration() knows a missing component")
	}
}
