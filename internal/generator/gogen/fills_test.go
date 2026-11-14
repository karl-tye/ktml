package gogen_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

const fillsSource = `Layout [children: content, title: content] { main { header { title } children } }
Inner { em { children } }
Page [label: string] {
	Layout {
		slot title { h1 "Title" }
		p "{label}"
		Inner { "{label}" }
	}
}`

func generateFills(t *testing.T, api gogen.API) string {
	t.Helper()
	result := compiler.CompileSyntax("page.ktml", []byte(fillsSource))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() = %v", result.Err())
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated", API: api})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	return string(generated)
}

// TestGenerate_GIVENFillsCapturingOnlyProps_WHENGenerated_THENTheyAreNamedTypesNotClosures
// covers the H fill lowering: a fill that captures only the caller's props is
// a one-pointer struct, and one that captures nothing is an empty struct, so
// neither allocates when stored in a ktml.Component field.
func TestGenerate_GIVENFillsCapturingOnlyProps_WHENGenerated_THENTheyAreNamedTypesNotClosures(t *testing.T) {
	generated := generateFills(t, gogen.APIStruct)
	for _, want := range []string{
		"type ktmlFill1Page struct{ props *Page }",
		"func (ktmlFill ktmlFill1Page) Render(ctx context.Context, ktmlW io.Writer) error {\n\tprops := ktmlFill.props",
		"ktmlContent1 := ktmlFill1Page{props}",
		"ktmlContent2 := ktmlFill2Page{props}",
		"type ktmlFill3Page struct{}",
		"ktmlContent3 := ktmlFill3Page{}",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated H code lacks %q:\n%s", want, generated)
		}
	}
	if strings.Contains(generated, "ktml.ComponentFunc") {
		t.Errorf("generated H code still has a fill closure:\n%s", generated)
	}
	if direct := generateFills(t, gogen.APIDirect); !strings.Contains(direct, "ktml.ComponentFunc(func") || strings.Contains(direct, "ktmlFill") {
		t.Errorf("APIDirect fills must stay ktml.ComponentFunc closures:\n%s", direct)
	}
}

// TestGenerate_GIVENNestedFillsCapturingProps_WHENRendered_THENOnlyThePropsAllocate
// is the allocation budget: closures cost one allocation per fill plus the
// escaped props; named fill types leave only the props.
func TestGenerate_GIVENNestedFillsCapturingProps_WHENRendered_THENOnlyThePropsAllocate(t *testing.T) {
	compileGenerated(t, []byte(generateFills(t, gogen.APIStruct)), `package generated

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestGeneratedPage(t *testing.T) {
	var output strings.Builder
	if err := (Page{Label: "<a>"}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	if got, want := output.String(), "<main><header><h1>Title</h1></header><p>&lt;a&gt;</p><em>&lt;a&gt;</em></main>"; got != want { t.Fatalf("rendered %q, want %q", got, want) }
	ctx := context.Background()
	allocs := testing.AllocsPerRun(100, func() {
		if err := (Page{Label: "x"}).Render(ctx, io.Discard); err != nil { t.Fatal(err) }
	})
	if allocs > 1 {
		t.Fatalf("Page.Render allocated %v times per run, want at most 1 (the escaped props)", allocs)
	}
}
`)
}
