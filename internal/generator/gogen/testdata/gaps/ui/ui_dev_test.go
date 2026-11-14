//go:build ktmldev

package ui

import (
	"strings"
	"testing"
)

func TestRoster_GIVENTheDevelopmentVariant_WHENRendered_THENMarksTheRepetitionInsideTheFill(t *testing.T) {
	got, err := renderRaw(Roster{Members: []Member{{Id: "1", Name: "Ada"}}})
	if err != nil || !strings.Contains(got, `<!--ktml:item example.com/fixture/ui.Roster#c0 example.com/fixture/ui.Modal#o0 example.com/fixture/ui.Roster#r0="1"-->`) {
		t.Fatalf("rendered %q, %v", got, err)
	}
}

func TestNested_GIVENTheDevelopmentVariant_WHENRendered_THENMarksEachInstanceByItsCallAndOutletFrames(t *testing.T) {
	nickname := "Jo"
	got, err := renderRaw(Nested{Member: Member{Name: "Ada", Nickname: &nickname}})
	want := `<section data-ktml-i="example.com/fixture/ui.Nested#c0" class="modal"><h2>Outer</h2>` +
		`<section data-ktml-i="example.com/fixture/ui.Nested#c0 example.com/fixture/ui.Modal#o0 example.com/fixture/ui.Nested#c1" class="modal">`
	if err != nil || !strings.HasPrefix(got, want) {
		t.Fatalf("rendered %q, %v", got, err)
	}
}
