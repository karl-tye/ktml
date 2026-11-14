package scanner

import (
	"testing"

	"github.com/karl-tye/ktml/internal/token"
)

func TestScanner(t *testing.T) {
	input := `import { Button } from "@components"
	
	main {
		div#container {
			Button[disabled: boolean] {
				"Click Me"
			}
		}
	}
	// This is a comment
	`

	tests := []struct {
		expectedType    token.Token
		expectedLiteral string
	}{
		{token.IMPORT, "import"},
		{token.LBRACE, "{"},
		{token.IDENT, "Button"},
		{token.RBRACE, "}"},
		{token.FROM, "from"},
		{token.STRING, "\"@components\""},
		{token.IDENT, "main"},
		{token.LBRACE, "{"},
		{token.IDENT, "div"},
		{token.HASH, "#"},
		{token.IDENT, "container"},
		{token.LBRACE, "{"},
		{token.IDENT, "Button"},
		{token.LBRACK, "["},
		{token.IDENT, "disabled"},
		{token.COLON, ":"},
		{token.IDENT, "boolean"},
		{token.RBRACK, "]"},
		{token.LBRACE, "{"},
		{token.STRING, "\"Click Me\""},
		{token.RBRACE, "}"},
		{token.RBRACE, "}"},
		{token.RBRACE, "}"},
		{token.EOF, ""},
	}

	s := &Scanner{}
	s.Init([]byte(input))

	for i, tt := range tests {
		_, tok, lit := s.Scan()

		if tok != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok)
		}

		if lit != tt.expectedLiteral && tok != token.EOF && tok != token.LBRACE && tok != token.RBRACE && tok != token.LBRACK && tok != token.RBRACK && tok != token.HASH && tok != token.COLON && tok != token.ASSIGN && tok != token.OR && tok != token.LPAREN && tok != token.RPAREN {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, lit)
		}
	}
}

func TestScannerNumbers(t *testing.T) {
	input := `123 45.67`
	tests := []struct {
		expectedType    token.Token
		expectedLiteral string
	}{
		{token.NUMBER, "123"},
		{token.NUMBER, "45.67"},
		{token.EOF, ""},
	}

	s := &Scanner{}
	s.Init([]byte(input))

	for i, tt := range tests {
		_, tok, lit := s.Scan()
		if tok != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok)
		}
		if lit != tt.expectedLiteral {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, lit)
		}
	}
}

func TestScannerIllegal(t *testing.T) {
	input := `$ ?`
	tests := []struct {
		expectedType    token.Token
		expectedLiteral string
	}{
		{token.ILLEGAL, "$"},
		{token.QUESTION, ""},
		{token.EOF, ""},
	}

	s := &Scanner{}
	s.Init([]byte(input))

	for i, tt := range tests {
		_, tok, lit := s.Scan()
		if tok != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok)
		}
		if lit != tt.expectedLiteral && tok != token.EOF {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, lit)
		}
	}
}

func TestScannerUnclosedString(t *testing.T) {
	input := "\"unclosed\n"
	s := &Scanner{}
	s.Init([]byte(input))
	_, tok, _ := s.Scan()
	if tok != token.ILLEGAL {
		t.Fatalf("expected ILLEGAL token for unclosed string, got %v", tok)
	}
}

func TestScannerMultipleDecimals(t *testing.T) {
	input := "12.34.56"
	s := &Scanner{}
	s.Init([]byte(input))
	_, tok, _ := s.Scan()
	if tok != token.ILLEGAL {
		t.Fatalf("expected ILLEGAL token for multiple decimals, got %v", tok)
	}
}

func TestScanner_GIVENAttributeAndCommentSyntax_WHENScanned_THENReturnsTokens(t *testing.T) {
	tests := []struct {
		input string
		tok   token.Token
		lit   string
	}{
		{input: "@", tok: token.AT},
		{input: "<!-- a -- b {x} -->", tok: token.HTML_COMMENT, lit: "<!-- a -- b {x} -->"},
		{input: "<!---->", tok: token.HTML_COMMENT, lit: "<!---->"},
		{input: "<!-- a --->", tok: token.HTML_COMMENT, lit: "<!-- a --->"},
		{input: "<!-- open", tok: token.ILLEGAL, lit: "<!-- open"},
		{input: "<!x", tok: token.LSS},
	}
	for _, test := range tests {
		s := &Scanner{}
		s.Init([]byte(test.input))
		if _, tok, lit := s.Scan(); tok != test.tok || lit != test.lit {
			t.Errorf("Scan(%q) = %s %q, want %s %q", test.input, tok, lit, test.tok, test.lit)
		}
	}
}

func TestAttributeNameEnd_GIVENNames_WHENMeasured_THENFollowsTheLexicalRule(t *testing.T) {
	tests := []struct {
		input string
		end   int
	}{
		{input: "x-on:click.outside=", end: 18},
		{input: "@keydown.slash.window ", end: 21},
		{input: ":class=", end: 6},
		{input: "hx-on::after-request=", end: 20},
		{input: "x-transition.opacity.duration.100ms", end: 35},
		{input: "@", end: 0},
		{input: "::x", end: 0},
		{input: "@1", end: 0},
		{input: "1a", end: 0},
		{input: "a@b", end: 1},
	}
	for _, test := range tests {
		if got := AttributeNameEnd([]byte(test.input), 0); got != test.end {
			t.Errorf("AttributeNameEnd(%q) = %d, want %d", test.input, got, test.end)
		}
	}
}
