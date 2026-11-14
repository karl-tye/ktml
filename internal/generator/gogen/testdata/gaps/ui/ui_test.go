package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"testing"

	"github.com/karl-tye/ktml"
)

func text(value string) ktml.Component {
	return ktml.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, value)
		return err
	})
}

// markers matches the ktmldev Render Identity boundaries and instance
// attributes, so the shared assertions hold in both variants.
var markers = regexp.MustCompile(`<!--/?ktml:(item|c) [^>]*-->| data-ktml-i="[^"]*"`)

func render(t *testing.T, component ktml.Component) (string, error) {
	t.Helper()
	output, err := renderRaw(component)
	return markers.ReplaceAllString(output, ""), err
}

func renderRaw(component ktml.Component) (string, error) {
	var output bytes.Buffer
	err := component.Render(context.Background(), &output)
	return output.String(), err
}

func TestModal_GIVENChildrenAndSlots_WHENRendered_THENRendersEachInPlace(t *testing.T) {
	tests := []struct {
		name  string
		props Modal
		want  string
	}{
		{"children only", Modal{Title: "Pay", Children: text("<b>body</b>")}, `<section class="modal"><h2>Pay</h2><b>body</b></section>`},
		{"joined footer", Modal{Title: "Pay", Children: text("x"), Actions: ktml.Join(text("a"), nil, text("b"))}, `<section class="modal"><h2>Pay</h2>x<footer class="modal-footer">ab</footer></section>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(t, tt.props)
			if err != nil || got != tt.want {
				t.Fatalf("rendered %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestModal_GIVENNilRequiredChildren_WHENRendered_THENReturnsMissingContentBeforeWriting(t *testing.T) {
	got, err := render(t, Modal{Title: "Pay"})
	var renderError *ktml.RenderError
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrMissingContent || renderError.Input != "children" || renderError.Component != "example.com/fixture/ui.Modal" {
		t.Fatalf("error = %#v", err)
	}
	if got != "" {
		t.Errorf("wrote %q before the error", got)
	}
}

func TestPage_GIVENACalleeWithItsOwnOutletInsideAFill_WHENRendered_THENDoesNotRecurse(t *testing.T) {
	got, err := render(t, Page{})
	if want := "<main><em></em><hr><em>text</em></main>"; err != nil || got != want {
		t.Fatalf("rendered %q, %v; want %q", got, err, want)
	}
}

func TestMemberCard_GIVENConditions_WHENRendered_THENBranchesAndReadsNarrowedOptionals(t *testing.T) {
	note, nickname, flag, unflagged := "hello", "Jo", true, false
	full := Member{
		Id: "m1", Name: "Ada", Admin: true,
		Address:  &Address{City: "Leeds", Verified: true, Note: &note},
		Tags:     &[]string{"a", "b"},
		Flag:     &flag,
		Nickname: &nickname,
	}
	tests := []struct {
		name  string
		props MemberCard
		want  string
	}{
		{"admin with everything", MemberCard{Member: full},
			`<article><h3>Ada</h3><span class="badge">Admin</span><p>Leeds</p><small>hello</small><p>aka Jo</p><ul><li>a</li><li>b</li></ul><i>flagged</i><div id="a"></div><div id="b"></div></article>`},
		{"compact admin falls to the note", MemberCard{Member: Member{Id: "m2", Name: "Bo", Admin: true, Flag: &unflagged, Address: &Address{City: "York"}}, Compact: ktml.Ptr(true), Note: ktml.Ptr("n")},
			`<article><h3>Bo</h3><p>n</p><p>No nickname</p><div id="a"></div><div id="b"></div></article>`},
		{"plain member", MemberCard{Member: Member{Id: "m3", Name: "Cy", Tags: &[]string{}}},
			`<article><h3>Cy</h3><p>Member</p><p>No nickname</p><ul></ul><div id="a"></div><div id="b"></div></article>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(t, tt.props)
			if err != nil || got != tt.want {
				t.Fatalf("rendered\n%q, %v; want\n%q", got, err, tt.want)
			}
		})
	}
}

func TestStatusPanel_GIVENAClauseGroupWithChildren_WHENRendered_THENTheOpenClauseRendersThem(t *testing.T) {
	got, err := render(t, StatusPanel{Panel: NewPanelOpen(), Children: text("kids")})
	if want := `<div class="open">kids</div>`; err != nil || got != want {
		t.Fatalf("rendered %q, %v; want %q", got, err, want)
	}
	got, err = render(t, StatusPanel{Panel: NewPanelClosed(PanelClosed{Reason: "done"}), Children: text("kids")})
	if want := `<p>done</p>`; err != nil || got != want {
		t.Fatalf("rendered %q, %v; want %q", got, err, want)
	}
	if _, err := render(t, StatusPanel{Children: text("kids")}); err == nil {
		t.Fatal("nil state rendered")
	}
}

func TestNested_GIVENNestedFills_WHENRendered_THENEachFillReadsTheCallerScope(t *testing.T) {
	nickname := "Jo"
	got, err := render(t, Nested{Member: Member{Name: "Ada", Nickname: &nickname}})
	want := `<section class="modal"><h2>Outer</h2><section class="modal"><h2>Inner</h2><p>Ada</p><footer class="modal-footer"><span>Jo</span></footer></section></section>`
	if err != nil || got != want {
		t.Fatalf("rendered\n%q, %v; want\n%q", got, err, want)
	}
}
