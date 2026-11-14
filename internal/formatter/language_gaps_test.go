package formatter

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/parser"
)

func TestFormatter_GIVENElementLines_WHENFormatted_THENHeadersAreCanonical(t *testing.T) {
	long := `form id="phone-card-form" action="{cardAction}" method="post" data-target="#global-modal-content" class="bg-base rounded-xl"`
	tests := []struct {
		name, input, want string
	}{
		{
			name:  "GIVEN childless siblings on separate lines WHEN formatted THEN they stay siblings",
			input: "Panel {\n\tdiv id=\"a\"\n\tdiv id=\"b\"\n\tAmountDue amount=\"{amount}\"\n}",
			want:  "Panel {\n\tdiv id=\"a\"\n\tdiv id=\"b\"\n\tAmountDue amount=\"{amount}\"\n}\n",
		},
		{
			name:  "GIVEN a short continuation group WHEN formatted THEN it is written on one line",
			input: "Panel {\n\tdiv (\n\t\tclass=\"hello\"\n\t\tid=\"world\"\n\t) {}\n}",
			want:  "Panel {\n\tdiv class=\"hello\" id=\"world\"\n}\n",
		},
		{
			name:  "GIVEN a header wider than 100 columns WHEN formatted THEN it uses the continuation form in source order",
			input: "Panel {\n\t" + long + " {\n\t\th2 \"Take Payment\"\n\t}\n}",
			want:  "Panel {\n\tform (\n\t\tid=\"phone-card-form\"\n\t\taction=\"{cardAction}\"\n\t\tmethod=\"post\"\n\t\tdata-target=\"#global-modal-content\"\n\t\tclass=\"bg-base rounded-xl\"\n\t) {\n\t\th2 {\n\t\t\t\"Take Payment\"\n\t\t}\n\t}\n}\n",
		},
		{
			name:  "GIVEN a long childless header WHEN formatted THEN the group closes without a body",
			input: "Panel {\n\t" + long + "\n\tp \"after\"\n}",
			want:  "Panel {\n\tform (\n\t\tid=\"phone-card-form\"\n\t\taction=\"{cardAction}\"\n\t\tmethod=\"post\"\n\t\tdata-target=\"#global-modal-content\"\n\t\tclass=\"bg-base rounded-xl\"\n\t)\n\tp {\n\t\t\"after\"\n\t}\n}\n",
		},
		{
			name:  "GIVEN literal true and false attribute values WHEN formatted THEN the values are kept",
			input: "Panel {\n\ta hx-push-url=\"true\" aria-hidden=false disabled\n}",
			want:  "Panel {\n\ta hx-push-url=\"true\" aria-hidden=false disabled\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertCanonical(t, tt.input, tt.want)
		})
	}
}

func TestFormatter_GIVENContentInputsAndConditions_WHENFormatted_THENCanonicalAndIdempotent(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{
			name:  "GIVEN a call with children and a named slot WHEN formatted THEN the fill is a braced slot block",
			input: "Page [] {\n\tModal title=\"Pay\" {\n\t\tp \"Choose\"\n\t\tslot   footer {   button \"Pay\" }\n\t\tslot actions {}\n\t}\n}",
			want:  "Page {\n\tModal title=\"Pay\" {\n\t\tp {\n\t\t\t\"Choose\"\n\t\t}\n\t\tslot footer {\n\t\t\tbutton {\n\t\t\t\t\"Pay\"\n\t\t\t}\n\t\t}\n\t\tslot actions {}\n\t}\n}\n",
		},
		{
			name:  "GIVEN content declarations and outlets WHEN formatted THEN they are kept",
			input: "Modal [children: content, footer?: content] {\n\tsection {\n\t\tchildren\n\t\tfooter\n\t}\n}",
			want:  "Modal [children: content, footer?: content] {\n\tsection {\n\t\tchildren\n\t\tfooter\n\t}\n}\n",
		},
		{
			name:  "GIVEN an if else-if else chain with messy spacing WHEN formatted THEN operators are spaced and groups are kept",
			input: "Badge [a: boolean, note?: string] {\n\tif a&&!b||(note?&&a) { span \"x\" }\n\telse if note? {\n\t\tp \"{note}\"\n\t}   else {}\n}",
			want:  "Badge [a: boolean, note?: string] {\n\tif a && !b || (note? && a) {\n\t\tspan {\n\t\t\t\"x\"\n\t\t}\n\t} else if note? {\n\t\tp {\n\t\t\t\"{note}\"\n\t\t}\n\t} else {}\n}\n",
		},
		{
			name:  "GIVEN an empty if body WHEN formatted THEN it is written as {}",
			input: "Badge [a: boolean] {\n\tif !a {\n\t}\n}",
			want:  "Badge [a: boolean] {\n\tif !a {}\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertCanonical(t, tt.input, tt.want)
		})
	}
}

// assertCanonical formats input, compares it with want, and checks that the
// output parses without errors and formats to itself.
func assertCanonical(t *testing.T, input, want string) {
	t.Helper()
	p := parser.New([]byte(input))
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		t.Fatalf("parse input: %v", err)
	}
	got := Format(doc)
	if got != want {
		t.Fatalf("Format() =\n%s\nwant\n%s", got, want)
	}
	again := parser.New([]byte(got))
	reformatted := Format(again.ParseDocument())
	if err := again.Err(); err != nil {
		t.Fatalf("parse formatted output: %v", err)
	}
	if reformatted != got {
		t.Fatalf("formatting is not idempotent:\n%s", strings.TrimSpace(reformatted))
	}
}
