package token

import "strconv"

// Token is the set of lexical tokens of the KTML programming language.
type Token int

const (
	// Special tokens
	ILLEGAL Token = iota
	EOF
	COMMENT
	// HTML_COMMENT is an output comment, <!-- … -->, including its
	// delimiters.
	HTML_COMMENT
	// RAW_TEXT is a raw-text literal, ``` … ```, including its fences.
	RAW_TEXT

	// Identifiers and basic type literals
	// (these stand for classes of literals)
	IDENT  // main, Button, div, label
	STRING // "abc", `abc`
	NUMBER // 1234.5678

	// Operators and delimiters
	ASSIGN   // =
	COLON    // :
	QUESTION // ?
	HASH     // #
	AT       // @ (attribute names only)
	OR       // | (Union types)
	COMMA    // ,
	PERIOD   // . (qualified Patterns)
	NOT      // !
	LAND     // &&
	LOR      // ||

	// Operators that are not conditions. They are scanned only so the parser
	// can reject them precisely.
	EQL // ==
	NEQ // !=
	LSS // <
	GTR // >
	LEQ // <=
	GEQ // >=
	AMP // &

	LPAREN // (
	RPAREN // )
	LBRACK // [
	RBRACK // ]
	LBRACE // {
	RBRACE // }

	// Keywords
	IMPORT // import
	FROM   // from
)

var tokens = [...]string{
	ILLEGAL: "ILLEGAL",
	EOF:     "EOF",
	COMMENT: "COMMENT",

	HTML_COMMENT: "HTML_COMMENT",
	RAW_TEXT:     "RAW_TEXT",

	IDENT:  "IDENT",
	STRING: "STRING",
	NUMBER: "NUMBER",

	ASSIGN:   "=",
	COLON:    ":",
	QUESTION: "?",
	HASH:     "#",
	AT:       "@",
	OR:       "|",
	COMMA:    ",",
	PERIOD:   ".",
	NOT:      "!",
	LAND:     "&&",
	LOR:      "||",

	EQL: "==",
	NEQ: "!=",
	LSS: "<",
	GTR: ">",
	LEQ: "<=",
	GEQ: ">=",
	AMP: "&",

	LPAREN: "(",
	RPAREN: ")",
	LBRACK: "[",
	RBRACK: "]",
	LBRACE: "{",
	RBRACE: "}",

	IMPORT: "import",
	FROM:   "from",
}

// String returns the string corresponding to the token tok.
func (tok Token) String() string {
	s := ""
	if 0 <= tok && tok < Token(len(tokens)) {
		s = tokens[tok]
	}
	if s == "" {
		s = "token(" + strconv.Itoa(int(tok)) + ")"
	}
	return s
}

var keywords = map[string]Token{
	"import": IMPORT,
	"from":   FROM,
}

// Lookup maps an identifier to its keyword token or IDENT (if not a keyword).
func Lookup(ident string) Token {
	if tok, isKeyword := keywords[ident]; isKeyword {
		return tok
	}
	return IDENT
}
