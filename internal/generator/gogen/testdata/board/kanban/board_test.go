package kanban

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func sampleColumns() []Column {
	return []Column{
		{Id: 1, Title: "Todo", Cards: []Card{
			{Id: "c1", Title: "Write <spec>", Tags: []Tag{{Id: 7, Label: "docs"}, {Id: 8, Label: "spec"}}},
			{Id: "c2", Title: "Parse", Tags: nil},
		}},
		{Id: 2, Title: "Done", Cards: []Card{
			{Id: "c1", Title: "Ship", Tags: []Tag{{Id: 7, Label: "release"}}},
		}},
	}
}

func render(t *testing.T, ctx context.Context, component ktml.Component) (string, error) {
	t.Helper()
	var output bytes.Buffer
	err := component.Render(ctx, &output)
	return output.String(), err
}

func renderError(t *testing.T, err error, kind ktml.ErrorKind) *ktml.RenderError {
	t.Helper()
	var typed *ktml.RenderError
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("error = %#v, want RenderError kind %s", err, kind)
	}
	return typed
}

func TestRepetition_GIVENDuplicateStringKeys_WHENRendered_THENFailsBeforeAnyBodyWrites(t *testing.T) {
	output, err := render(t, context.Background(), Labels{Labels: []string{"a", "b", "a"}})
	typed := renderError(t, err, ktml.ErrDuplicateKey)
	if typed.Component != "example.com/fixture/kanban.Labels" || typed.Input != "labels" || typed.Key != `"a"` {
		t.Errorf("error = %#v", typed)
	}
	if output != "" {
		t.Errorf("output = %q, want nothing written", output)
	}
}

func TestRepetition_GIVENDuplicateIntegerKeys_WHENRendered_THENReportsCanonicalIntegerKey(t *testing.T) {
	columns := sampleColumns()
	columns[1].Id = 1
	output, err := render(t, context.Background(), Columns{Columns: columns})
	typed := renderError(t, err, ktml.ErrDuplicateKey)
	if typed.Key != "1" || typed.Input != "columns" || output != "" {
		t.Errorf("error = %#v, output = %q", typed, output)
	}
}

func TestRepetition_GIVENNestedDuplicate_WHENRendered_THENAncestorsMayWriteButTheRepetitionDoesNot(t *testing.T) {
	columns := sampleColumns()
	columns[1].Cards = append(columns[1].Cards, Card{Id: "c1", Title: "Again"})
	output, err := render(t, context.Background(), Columns{Columns: columns})
	typed := renderError(t, err, ktml.ErrDuplicateKey)
	if typed.Input != "column.cards" || typed.Key != `"c1"` {
		t.Errorf("error = %#v", typed)
	}
	last := output[strings.LastIndex(output, "<h2>"):]
	if !strings.HasPrefix(last, "<h2>Done</h2>") || !strings.HasSuffix(output, "<ul>") || strings.Contains(last, "Ship") {
		t.Errorf("output after the failing parent = %q", last)
	}
}

func TestRepetition_GIVENEqualChildKeysUnderDifferentParents_WHENRendered_THENSucceeds(t *testing.T) {
	output, err := render(t, context.Background(), Columns{Columns: sampleColumns()})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Count(output, `class="card">`) != 3 || !strings.Contains(output, "Write &lt;spec&gt;") {
		t.Errorf("output = %q", output)
	}
}

func TestRepetition_GIVENEmptyOrNilCollections_WHENRendered_THENWritesNothing(t *testing.T) {
	for _, component := range []ktml.Component{
		Columns{},
		Columns{Columns: []Column{}},
		Labels{Labels: []string{}},
	} {
		output, err := render(t, context.Background(), component)
		if err != nil || output != "" {
			t.Errorf("Render() = %q, %v", output, err)
		}
	}
}

// canceledAfter reports cancellation from its nth Err call on.
type canceledAfter struct {
	context.Context
	calls, n int
}

func (ctx *canceledAfter) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.n {
		return context.Canceled
	}
	return nil
}

func TestRepetition_GIVENCanceledContext_WHENRendered_THENReturnsTypedCancellationBeforeTheFirstItem(t *testing.T) {
	ctx := &canceledAfter{Context: context.Background(), n: 2}
	output, err := render(t, ctx, Labels{Labels: []string{"a"}})
	typed := renderError(t, err, ktml.ErrCanceled)
	if !errors.Is(err, context.Canceled) || typed.Input != "labels" || output != "" {
		t.Errorf("error = %#v, output = %q", typed, output)
	}
}

func TestComponent_GIVENCanceledContext_WHENRendered_THENReturnsTypedCancellationBeforeWriting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output, err := render(t, ctx, Labels{Labels: []string{"a"}})
	typed := renderError(t, err, ktml.ErrCanceled)
	if !errors.Is(err, context.Canceled) || typed.Input != "" || typed.Partial || output != "" {
		t.Errorf("error = %#v, output = %q", typed, output)
	}
}

func TestRepetition_GIVENClauseBindings_WHENRendered_THENRepeatsBoundCollections(t *testing.T) {
	ready := NewBoardStateReady(BoardStateReady{Columns: sampleColumns()[:1]})
	output, err := render(t, context.Background(), Board{State: ready})
	if err != nil || !strings.Contains(output, "<h2>Todo</h2>") {
		t.Errorf("Board(Ready) = %q, %v", output, err)
	}
	labels := []string{"x", "y"}
	output, err = render(t, context.Background(), Picker{Labels: &labels})
	if err != nil || strings.Count(output, "<option") != 2 {
		t.Errorf("Picker(Some) = %q, %v", output, err)
	}
	output, err = render(t, context.Background(), Picker{})
	if err != nil || output != "<p>No labels</p>" {
		t.Errorf("Picker(None) = %q, %v", output, err)
	}
}
