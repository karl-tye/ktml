package gogen_test

import "testing"

func TestGenerate_GIVENSpreadsAndBagLiterals_WHENRendered_THENForwardsEachEntryInItsContext(t *testing.T) {
	generated := generateWorkspaceSource(t, `Field [label: string, attrs?: Attributes] {
	input (type="text" class="field" if attrs? { ...attrs })
}

Form [busy: boolean, count: integer, ratio: number, label: string, handler: TrustedScript, data: JSONValue, href: URL, mine: Attributes] {
	Field label="a"
	Field (
		label="b"
		attrs=(required title="{label} #{count} {ratio}" @click="{handler}" :disabled="{busy}" x-data="{data}" hx-get="{href}" @keyup="go('<x>')" style="display: none" CLASS="dropped" if busy { disabled } else { data-idle } ...mine)
	)
	Field label="c" attrs="{mine}"
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestForwarding(t *testing.T) {
	form := Form{
		Busy: true, Count: -3, Ratio: 0.5, Label: "\"><b>",
		Handler: ktml.TrustScript("go(\"a<b\")"), Data: ktml.TrustJSON("{\"k\":\"</script>\"}"),
		Href: ktml.NewURL("javascript:alert(1)"),
		Mine: ktml.JoinAttributes(ktml.MustAttr("name", "n"), ktml.MustAttr("title", "shadowed"), ktml.MustAttr("type", "dropped")),
	}
	var output strings.Builder
	if err := form.Render(context.Background(), &output); err != nil { t.Fatal(err) }
	want := "<input type=\"text\" class=\"field\">" +
		"<input type=\"text\" class=\"field\" required title=\"&#34;&gt;&lt;b&gt; #-3 0.5\" @click=\"go(&#34;a&lt;b&#34;)\" :disabled=\"true\"" +
		" x-data=\"{&#34;k&#34;:&#34;&lt;/script&gt;&#34;}\" hx-get=\"about:invalid#ktml-unsafe-url\" @keyup=\"go(&#39;&lt;x&gt;&#39;)\" style=\"display: none\" disabled name=\"n\">" +
		"<input type=\"text\" class=\"field\" name=\"n\" title=\"shadowed\">"
	if got := output.String(); got != want {
		t.Fatalf("rendered\n%s\nwant\n%s", got, want)
	}
}
`)
}

func TestGenerate_GIVENConditionalBagEntryReadingAnOptional_WHENAbsent_THENDoesNotReadIt(t *testing.T) {
	generated := generateWorkspaceSource(t, `Field [attrs?: Attributes] {
	input (if attrs? { ...attrs })
}

Form [busy: boolean, note?: string] {
	Field attrs=(if busy { disabled } else if note? { data-note="{note}" } else { data-none })
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestLazyBag(t *testing.T) {
	note := "<n>"
	for form, want := range map[*Form]string{
		{}:                       "<input data-none>",
		{Note: &note}:            "<input data-note=\"&lt;n&gt;\">",
		{Busy: true, Note: &note}: "<input disabled>",
	} {
		var output strings.Builder
		if err := form.Render(context.Background(), &output); err != nil { t.Fatal(err) }
		if got := output.String(); got != want {
			t.Errorf("rendered %s, want %s", got, want)
		}
	}
}
`)
}
