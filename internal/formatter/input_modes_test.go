package formatter_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/formatter"
)

func TestFormat_GIVENCanonicalInputModes_WHENFormatted_THENPreservesOperatorsAndZeroDefaults(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card[title:string,note?:string,enabled:boolean=false,count:number=0,label:string=""]{div}`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	want := "Card [\n\ttitle:   string,\n\tnote?:   string,\n\tenabled: boolean = false,\n\tcount:   number = 0,\n\tlabel:   string = \"\",\n] {\n\tdiv\n}\n"
	if got := formatter.Format(result.Document); got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
}
