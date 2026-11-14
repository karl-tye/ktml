//go:build ktmldev

package kanban

import (
	"context"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/dev"
)

func TestRepetition_GIVENDevelopmentVariant_WHENRendered_THENMarksNestedRenderIdentityAcrossComponents(t *testing.T) {
	output, err := render(t, context.Background(), Columns{Columns: sampleColumns()})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const columns = "example.com/fixture/kanban.Columns"
	const cardView = "example.com/fixture/kanban.CardView"
	begin := func(identity string) string { return "<!--ktml:item " + identity + "-->" }
	end := func(identity string) string { return "<!--/ktml:item " + identity + "-->" }
	todo := columns + "#r0=1"
	done := columns + "#r0=2"
	write := todo + " " + columns + `#r1="c1"`
	parse := todo + " " + columns + `#r1="c2"`
	ship := done + " " + columns + `#r1="c1"`
	// The CardView instance at call c0 carries its identity on its root
	// element; its repetition items extend that identity.
	instance := func(item string) string {
		return ` data-ktml-i="` + strings.ReplaceAll(item, `"`, "&#34;") + " " + columns + `#c0"`
	}
	card := func(item string) string { return item + " " + columns + "#c0 " + cardView }
	want := begin(todo) + `<section><h2>Todo</h2><ul>` +
		begin(write) + `<li` + instance(write) + ` class="card"><h3>Write &lt;spec&gt;</h3><ul class="tags">` +
		begin(card(write)+"#r0=7") + `<li>docs</li>` + end(card(write)+"#r0=7") +
		begin(card(write)+"#r0=8") + `<li>spec</li>` + end(card(write)+"#r0=8") +
		`</ul></li>` + end(write) +
		begin(parse) + `<li` + instance(parse) + ` class="card"><h3>Parse</h3><ul class="tags"></ul></li>` + end(parse) +
		`</ul></section>` + end(todo) +
		begin(done) + `<section><h2>Done</h2><ul>` +
		begin(ship) + `<li` + instance(ship) + ` class="card"><h3>Ship</h3><ul class="tags">` +
		begin(card(ship)+"#r0=7") + `<li>release</li>` + end(card(ship)+"#r0=7") +
		`</ul></li>` + end(ship) +
		`</ul></section>` + end(done)
	if output != want {
		t.Errorf("output =\n%s\nwant\n%s", output, want)
	}
}

func TestRepetition_GIVENDevelopmentVariantAndDuplicateKey_WHENRendered_THENWritesNoMarkers(t *testing.T) {
	output, err := render(t, context.Background(), Labels{Labels: []string{"a", "a"}})
	if err == nil || output != "" {
		t.Errorf("Render() = %q, %v", output, err)
	}
}

func TestInstance_GIVENRegistryRootScope_WHENRendered_THENMarksTheRootInstance(t *testing.T) {
	ctx := dev.WithScope(context.Background(), "@7")
	output, err := render(t, ctx, Labels{Labels: []string{"a"}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.HasPrefix(output, "<!--ktml:c @7-->") || !strings.HasSuffix(output, "<!--/ktml:c @7-->") {
		t.Errorf("multi-root output = %s", output)
	}
	output, err = render(t, ctx, CardView{Card: Card{Id: "x", Title: "T"}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.HasPrefix(output, `<li data-ktml-i="@7" class="card">`) {
		t.Errorf("single-root output = %s", output)
	}
}
