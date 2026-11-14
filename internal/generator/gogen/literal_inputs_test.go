package gogen_test

import (
	"strings"
	"testing"
)

func TestGenerate_GIVENLiteralOptionalAndDefaultedInputs_WHENRendered_THENDoesNotAllocate(t *testing.T) {
	generated := generateSource(t, `Form [] {
	Field name="a" kind="tel" maxlength=20 step=0.5 required=true hint="Use <b>"
	Field name="b" kind="tel" required=false
	Field name="c" required=true
}

Field [name: string, kind: string = "text", maxlength: integer = 120, step?: number, required: boolean = false, hint?: string] {
	input name="{name}" type="{kind}" maxlength="{maxlength}" required="{required}"
	if step? {
		span "{step}"
	}
	if hint? {
		small "{hint}"
	}
}`)
	if strings.Contains(string(generated), "ktml.Ptr(") {
		t.Errorf("literal inputs are passed through ktml.Ptr:\n%s", generated)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestLiteralInputs(t *testing.T) {
	var output strings.Builder
	if err := (Form{}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	want := "<input name=\"a\" type=\"tel\" maxlength=\"20\" required><span>0.5</span><small>Use &lt;b&gt;</small>" +
		"<input name=\"b\" type=\"tel\" maxlength=\"120\">" +
		"<input name=\"c\" type=\"text\" maxlength=\"120\" required>"
	if got := output.String(); got != want {
		t.Fatalf("rendered\n%s\nwant\n%s", got, want)
	}
	ctx := context.Background()
	if allocs := testing.AllocsPerRun(100, func() { _ = (Form{}).Render(ctx, io.Discard) }); allocs != 0 {
		t.Fatalf("Form allocates %v times per render, want 0", allocs)
	}
}
`)
}
