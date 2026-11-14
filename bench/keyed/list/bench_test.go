package list

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"testing"
)

func flatItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{Id: "item-" + strconv.Itoa(i), Title: "Title <" + strconv.Itoa(i) + "> & co", Count: int64(i)}
	}
	return items
}

func nestedRows(n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		note := "note " + strconv.Itoa(i)
		rows[i] = Row{Id: int64(i), Title: "Row \"" + strconv.Itoa(i) + "\"", Children: []Child{{Id: 1, Label: "one"}, {Id: 2, Label: "two & a half"}, {Id: 3, Label: "<three>"}}}
		if i%2 == 0 {
			rows[i].Note = &note
		}
	}
	return rows
}

type implementation struct {
	name   string
	render func(io.Writer) error
}

type shape struct {
	name            string
	implementations []implementation
}

func shapes() []shape {
	ctx := context.Background()
	flat := func(name string, items []Item) shape {
		return shape{name: name, implementations: []implementation{
			{"KTML", func(w io.Writer) error { return (FlatList{Items: items}).Render(ctx, w) }},
			{"Templ", func(w io.Writer) error { return TemplFlatList(items).Render(ctx, w) }},
			{"HandwrittenPreflight", func(w io.Writer) error { return HandwrittenFlatListPreflight(w, items) }},
			{"Handwritten", func(w io.Writer) error { return HandwrittenFlatList(w, items) }},
		}}
	}
	nested := func(name string, rows []Row) shape {
		return shape{name: name, implementations: []implementation{
			{"KTML", func(w io.Writer) error { return (NestedList{Rows: rows}).Render(ctx, w) }},
			{"Templ", func(w io.Writer) error { return TemplNestedList(rows).Render(ctx, w) }},
			{"HandwrittenPreflight", func(w io.Writer) error { return HandwrittenNestedListPreflight(w, rows) }},
			{"Handwritten", func(w io.Writer) error { return HandwrittenNestedList(w, rows) }},
		}}
	}
	return []shape{
		flat("Flat", flatItems(1000)),
		nested("Nested", nestedRows(1000)),
		flat("Small", flatItems(5)),
		flat("Empty", nil),
	}
}

// TestImplementations_GIVENEveryShape_WHENRendered_THENOutputIsByteIdentical is
// checked before any timing.
func TestImplementations_GIVENEveryShape_WHENRendered_THENOutputIsByteIdentical(t *testing.T) {
	for _, s := range shapes() {
		var want []byte
		for _, implementation := range s.implementations {
			var output bytes.Buffer
			if err := implementation.render(&output); err != nil {
				t.Fatalf("%s/%s: %v", s.name, implementation.name, err)
			}
			if want == nil {
				want = output.Bytes()
				continue
			}
			if !bytes.Equal(output.Bytes(), want) {
				t.Errorf("%s/%s differs:\n%s\nwant\n%s", s.name, implementation.name, output.Bytes(), want)
			}
		}
	}
}

func TestKTML_GIVENDuplicateKey_WHENRendered_THENFails(t *testing.T) {
	items := flatItems(20)
	items[19].Id = items[3].Id
	var output bytes.Buffer
	if err := (FlatList{Items: items}).Render(context.Background(), &output); err == nil {
		t.Fatal("duplicate key rendered")
	}
}

func BenchmarkKeyedRepetition(b *testing.B) {
	for _, s := range shapes() {
		for _, implementation := range s.implementations {
			b.Run(s.name+"/"+implementation.name+"/Discard", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := implementation.render(io.Discard); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(s.name+"/"+implementation.name+"/Buffer", func(b *testing.B) {
				var output bytes.Buffer
				output.Grow(1 << 20)
				b.ReportAllocs()
				for b.Loop() {
					output.Reset()
					if err := implementation.render(&output); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
