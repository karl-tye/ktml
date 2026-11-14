package renderapi_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/karl-tye/ktml"
	bcross "github.com/karl-tye/ktml/bench/renderapi/b/cross"
	bform "github.com/karl-tye/ktml/bench/renderapi/b/form"
	blist "github.com/karl-tye/ktml/bench/renderapi/b/list"
	bnested "github.com/karl-tye/ktml/bench/renderapi/b/nested"
	bscript "github.com/karl-tye/ktml/bench/renderapi/b/script"
	bstatic "github.com/karl-tye/ktml/bench/renderapi/b/static"
	hcross "github.com/karl-tye/ktml/bench/renderapi/h/cross"
	hform "github.com/karl-tye/ktml/bench/renderapi/h/form"
	hlist "github.com/karl-tye/ktml/bench/renderapi/h/list"
	hnested "github.com/karl-tye/ktml/bench/renderapi/h/nested"
	hscript "github.com/karl-tye/ktml/bench/renderapi/h/script"
	hstatic "github.com/karl-tye/ktml/bench/renderapi/h/static"
	"github.com/karl-tye/ktml/bench/renderapi/handwritten"
	tpl "github.com/karl-tye/ktml/bench/renderapi/templ"
)

// implementation renders one benchmark page through one candidate. Each
// render builds its props or arguments at the call site, as a handler would.
type implementation struct {
	name   string
	render func(io.Writer) error
}

type benchmark struct {
	name            string
	implementations []implementation
}

// implementations lists the candidates in benchstat column order.
func benchmarks() []benchmark {
	ctx := context.Background()

	entries := listEntries(1000)
	hEntryList, bEntryList := hEntries(entries), bEntries(entries)

	values := formValues()
	hValues, bValues := hFormValues(values), bFormValues(values)
	action := ktml.NewURL(formAction)

	page := scriptPage()
	data := scriptData(page)
	boot := ktml.TrustScript(page.Boot)
	hLinkList, bLinkList := hLinks(page.Links), bLinks(page.Links)

	return []benchmark{
		{"Static", []implementation{
			{"H", func(w io.Writer) error { return hstatic.Static{}.Render(ctx, w) }},
			{"B", func(w io.Writer) error { return bstatic.RenderStatic(ctx, w, bstatic.StaticProps{}) }},
			{"templ", func(w io.Writer) error { return tpl.Static().Render(ctx, w) }},
			{"handwritten", handwritten.Static},
		}},
		{"NestedSame", []implementation{
			{"H", func(w io.Writer) error { return hnested.NestedSame{Label: nestedLabel}.Render(ctx, w) }},
			{"B", func(w io.Writer) error {
				return bnested.RenderNestedSame(ctx, w, bnested.NestedSameProps{Label: nestedLabel})
			}},
			{"templ", func(w io.Writer) error { return tpl.NestedSame(nestedLabel).Render(ctx, w) }},
			{"handwritten", func(w io.Writer) error { return handwritten.Nested(w, nestedLabel) }},
		}},
		{"NestedCross", []implementation{
			{"H", func(w io.Writer) error { return hcross.NestedCross{Label: nestedLabel}.Render(ctx, w) }},
			{"B", func(w io.Writer) error {
				return bcross.RenderNestedCross(ctx, w, bcross.NestedCrossProps{Label: nestedLabel})
			}},
			{"templ", func(w io.Writer) error { return tpl.NestedCross(nestedLabel).Render(ctx, w) }},
			{"handwritten", func(w io.Writer) error { return handwritten.Nested(w, nestedLabel) }},
		}},
		{"NestedSlots", []implementation{
			{"H", func(w io.Writer) error { return hnested.NestedSlots{Label: nestedLabel}.Render(ctx, w) }},
			{"B", func(w io.Writer) error {
				return bnested.RenderNestedSlots(ctx, w, bnested.NestedSlotsProps{Label: nestedLabel})
			}},
			{"templ", func(w io.Writer) error { return tpl.NestedSlots(nestedLabel).Render(ctx, w) }},
			{"handwritten", func(w io.Writer) error { return handwritten.NestedSlots(w, nestedLabel) }},
		}},
		{"ListHeavy", []implementation{
			{"H", func(w io.Writer) error { return hlist.ListHeavy{Entries: hEntryList}.Render(ctx, w) }},
			{"B", func(w io.Writer) error {
				return blist.RenderListHeavy(ctx, w, blist.ListHeavyProps{Entries: bEntryList})
			}},
			{"templ", func(w io.Writer) error { return tpl.ListHeavy(entries).Render(ctx, w) }},
			{"handwritten", func(w io.Writer) error { return handwritten.ListHeavy(w, entries) }},
		}},
		{"FormHeavy", []implementation{
			{"H", func(w io.Writer) error {
				return hform.FormHeavy{Action: action, Csrf: formCSRF, Values: hValues, Locked: true}.Render(ctx, w)
			}},
			{"B", func(w io.Writer) error {
				return bform.RenderFormHeavy(ctx, w, bform.FormHeavyProps{Action: action, Csrf: formCSRF, Values: bValues, Locked: true})
			}},
			{"templ", func(w io.Writer) error { return tpl.TemplFormHeavy(formAction, formCSRF, values, true).Render(ctx, w) }},
			{"handwritten", func(w io.Writer) error { return handwritten.FormHeavy(w, formAction, formCSRF, values, true) }},
		}},
		{"ScriptHeavy", []implementation{
			{"H", func(w io.Writer) error {
				return hscript.ScriptHeavy{Id: page.ID, Title: page.Title, Summary: page.Summary, Data: data, Links: hLinkList, Boot: boot}.Render(ctx, w)
			}},
			{"B", func(w io.Writer) error {
				return bscript.RenderScriptHeavy(ctx, w, bscript.ScriptHeavyProps{Id: page.ID, Title: page.Title, Summary: page.Summary, Data: data, Links: bLinkList, Boot: boot})
			}},
			{"templ", func(w io.Writer) error { return tpl.ScriptHeavy(page).Render(ctx, w) }},
			{"handwritten", func(w io.Writer) error { return handwritten.ScriptHeavy(w, page) }},
		}},
	}
}

// TestImplementations_GIVENEveryBenchmark_WHENRendered_THENOutputIsByteIdentical
// is the protocol's pre-timing check: every candidate writes the same bytes.
func TestImplementations_GIVENEveryBenchmark_WHENRendered_THENOutputIsByteIdentical(t *testing.T) {
	for _, bench := range benchmarks() {
		t.Run(bench.name, func(t *testing.T) {
			var want []byte
			for _, implementation := range bench.implementations {
				var output bytes.Buffer
				if err := implementation.render(&output); err != nil {
					t.Fatalf("%s: %v", implementation.name, err)
				}
				if want == nil {
					want = output.Bytes()
					t.Logf("%d bytes", len(want))
					continue
				}
				if !bytes.Equal(output.Bytes(), want) {
					t.Errorf("%s differs from %s at byte %d:\n got %q\nwant %q", implementation.name, bench.implementations[0].name, firstDifference(output.Bytes(), want), excerpt(output.Bytes(), want), excerpt(want, output.Bytes()))
				}
			}
		})
	}
}

// TestImplementations_GIVENNonStringWriter_WHENRendered_THENOutputMatches
// covers the io.Writer path without io.StringWriter.
func TestImplementations_GIVENNonStringWriter_WHENRendered_THENOutputMatches(t *testing.T) {
	for _, bench := range benchmarks() {
		var want bytes.Buffer
		if err := bench.implementations[0].render(&want); err != nil {
			t.Fatal(err)
		}
		for _, implementation := range bench.implementations {
			var output bytes.Buffer
			if err := implementation.render(plainWriter{&output}); err != nil {
				t.Fatalf("%s/%s: %v", bench.name, implementation.name, err)
			}
			if !bytes.Equal(output.Bytes(), want.Bytes()) {
				t.Errorf("%s/%s differs through a plain io.Writer", bench.name, implementation.name)
			}
		}
	}
}

type plainWriter struct{ w io.Writer }

func (w plainWriter) Write(p []byte) (int, error) { return w.w.Write(p) }

func firstDifference(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func excerpt(a, b []byte) []byte {
	start := max(firstDifference(a, b)-40, 0)
	return a[start:min(start+120, len(a))]
}

func run(b *testing.B, name string) {
	for _, bench := range benchmarks() {
		if bench.name != name {
			continue
		}
		for _, implementation := range bench.implementations {
			b.Run("impl="+implementation.name+"/w=discard", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := implementation.render(io.Discard); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("impl="+implementation.name+"/w=buffer", func(b *testing.B) {
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
		return
	}
	b.Fatalf("unknown benchmark %s", name)
}

func BenchmarkStatic(b *testing.B)      { run(b, "Static") }
func BenchmarkNestedSame(b *testing.B)  { run(b, "NestedSame") }
func BenchmarkNestedCross(b *testing.B) { run(b, "NestedCross") }
func BenchmarkNestedSlots(b *testing.B) { run(b, "NestedSlots") }
func BenchmarkListHeavy(b *testing.B)   { run(b, "ListHeavy") }
func BenchmarkFormHeavy(b *testing.B)   { run(b, "FormHeavy") }
func BenchmarkScriptHeavy(b *testing.B) { run(b, "ScriptHeavy") }
