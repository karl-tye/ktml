package gogen_test

import (
	"strings"
	"testing"
)

func TestGenerate_GIVENConditionalValuedAttributes_WHENRendered_THENWritesOnlyTheTakenBranch(t *testing.T) {
	generated := generateWorkspaceSource(t, `Tab [current: boolean, oob: boolean, form?: string, label: string, handler: TrustedScript, href: URL] {
	a (
		href="{href}"
		if current { aria-current="page" class="on" } else { class="off" }
		if form? { form="{form}" } else if oob { hx-swap-oob="true" }
		if current && oob { @click="{handler}" data-label="{label}" disabled="{oob}" }
	) "{label}"
}`)
	if !strings.Contains(string(stripDirectives(t, generated)), "if props.Form != nil {") {
		t.Errorf("generated source lacks the presence test:\n%s", generated)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestConditionalAttributes(t *testing.T) {
	form := "f\"><script>x</script>"
	tests := []struct {
		name string
		tab  Tab
		want string
	}{
		{"current oob", Tab{Current: true, Oob: true, Label: "<L>", Handler: ktml.TrustScript("go('a<b')"), Href: ktml.NewURL("javascript:x")},
			"<a href=\"about:invalid#ktml-unsafe-url\" aria-current=\"page\" class=\"on\" hx-swap-oob=\"true\" @click=\"go(&#39;a&lt;b&#39;)\" data-label=\"&lt;L&gt;\" disabled>&lt;L&gt;</a>"},
		{"form", Tab{Form: &form, Label: "x", Href: ktml.NewURL("/x?a=1&b=2")},
			"<a href=\"/x?a=1&amp;b=2\" class=\"off\" form=\"f&#34;&gt;&lt;script&gt;x&lt;/script&gt;\">x</a>"},
		{"none", Tab{Label: "x", Href: ktml.NewURL("/x")}, "<a href=\"/x\" class=\"off\">x</a>"},
	}
	for _, test := range tests {
		var output strings.Builder
		if err := test.tab.Render(context.Background(), &output); err != nil { t.Fatal(err) }
		if got := output.String(); got != test.want {
			t.Errorf("%s rendered\n%s\nwant\n%s", test.name, got, test.want)
		}
	}
}
`)
}
