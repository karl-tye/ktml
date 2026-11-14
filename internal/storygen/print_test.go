package storygen

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

// TestQuote_GIVENAnyText_WHENParsedBack_THENIsExact round-trips strings that
// need care in KTML: braces read as interpolation in double quotes, and
// backtick literals hold neither backticks nor newlines.
func TestQuote_GIVENAnyText_WHENParsedBack_THENIsExact(t *testing.T) {
	tests := []string{
		"", "plain", `say "hi"`, "{}", `{"a": 1}`, "a } b", "back`tick {x}", "line\nbreak {y}",
		"tab\there", `back\slash`, "café ✓", "\x01control", "{ spaced }", "`",
	}
	for _, text := range tests {
		source := "story A.B [v = " + quote(text) + "]\n"
		p := parser.New([]byte(source))
		document := p.ParseDocument()
		if errs := p.Errors(); len(errs) > 0 {
			t.Errorf("quote(%q) = %s does not parse: %v", text, quote(text), errs[0])
			continue
		}
		story := document.Nodes[0].(*ast.StoryDecl)
		if got := story.Arguments[0].Value.Scalar.String; got != text {
			t.Errorf("quote(%q) = %s reads back as %q", text, quote(text), got)
		}
	}
}
