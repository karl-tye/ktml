//go:build !ktmldev

package kanban

import (
	"context"
	"testing"
)

func TestRepetition_GIVENProductionVariant_WHENRendered_THENWritesOnlyBodyHTML(t *testing.T) {
	output, err := render(t, context.Background(), Columns{Columns: sampleColumns()})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	want := `<section><h2>Todo</h2><ul>` +
		`<li class="card"><h3>Write &lt;spec&gt;</h3><ul class="tags"><li>docs</li><li>spec</li></ul></li>` +
		`<li class="card"><h3>Parse</h3><ul class="tags"></ul></li>` +
		`</ul></section>` +
		`<section><h2>Done</h2><ul>` +
		`<li class="card"><h3>Ship</h3><ul class="tags"><li>release</li></ul></li>` +
		`</ul></section>`
	if output != want {
		t.Errorf("output =\n%s\nwant\n%s", output, want)
	}
}
