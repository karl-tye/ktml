package token

import (
	"testing"
)

func TestTokenString(t *testing.T) {
	tests := []struct {
		tok  Token
		want string
	}{
		{ILLEGAL, "ILLEGAL"},
		{EOF, "EOF"},
		{COMMENT, "COMMENT"},
		{IDENT, "IDENT"},
		{STRING, "STRING"},
		{NUMBER, "NUMBER"},
		{ASSIGN, "="},
		{COLON, ":"},
		{HASH, "#"},
		{OR, "|"},
		{LPAREN, "("},
		{RPAREN, ")"},
		{LBRACK, "["},
		{RBRACK, "]"},
		{LBRACE, "{"},
		{RBRACE, "}"},
		{IMPORT, "import"},
		{FROM, "from"},
		{Token(999), "token(999)"},
		{Token(-1), "token(-1)"},
	}

	for _, tt := range tests {
		if got := tt.tok.String(); got != tt.want {
			t.Errorf("Token(%d).String() = %v, want %v", tt.tok, got, tt.want)
		}
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		ident string
		want  Token
	}{
		{"import", IMPORT},
		{"from", FROM},
		{"main", IDENT},
		{"div", IDENT},
		{"UNKNOWN", IDENT},
	}

	for _, tt := range tests {
		if got := Lookup(tt.ident); got != tt.want {
			t.Errorf("Lookup(%q) = %v, want %v", tt.ident, got, tt.want)
		}
	}
}
