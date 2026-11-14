//go:build !ktmldev

package ui

import "testing"

func TestRoster_GIVENARepetitionAndSlotsInsideAFill_WHENRendered_THENRendersInOrder(t *testing.T) {
	got, err := render(t, Roster{Members: []Member{{Id: "1", Name: "Ada"}}, Heading: text("<h1>Team</h1>")})
	want := `<section class="modal"><h2>Roster</h2><h1>Team</h1><article><h3>Ada</h3><p>Member</p><p>No nickname</p><div id="a"></div><div id="b"></div></article><footer class="modal-footer"><button type="button" hx-push-url="true">Close</button></footer></section>`
	if err != nil || got != want {
		t.Fatalf("rendered\n%q, %v; want\n%q", got, err, want)
	}
}
