package scanner

import (
	"strings"
	"unicode/utf8"

	"github.com/karl-tye/ktml/internal/attrname"
	"github.com/karl-tye/ktml/internal/token"
)

type Scanner struct {
	src      []byte
	ch       rune
	offset   int
	rdOffset int
}

func (s *Scanner) Init(src []byte) {
	s.src = src
	s.ch = ' '
	s.offset = 0
	s.rdOffset = 0
	s.next()
}

func (s *Scanner) next() {
	if s.rdOffset < len(s.src) {
		s.offset = s.rdOffset
		r, w := rune(s.src[s.rdOffset]), 1
		if r >= utf8.RuneSelf {
			r, w = utf8.DecodeRune(s.src[s.rdOffset:])
		}
		s.rdOffset += w
		s.ch = r
	} else {
		s.offset = len(s.src)
		s.ch = 0
	}
}

func (s *Scanner) Scan() (int, token.Token, string) {
	s.skipWhitespace()
	pos := s.offset

	var tok token.Token
	var lit string

	switch ch := s.ch; {
	case isLetter(ch):
		lit = s.scanIdentifier()
		tok = token.Lookup(lit)
	case isDigit(ch):
		tok, lit = s.scanNumber()
	default:
		s.next()
		switch ch {
		case 0:
			tok = token.EOF
		case '=':
			tok, lit = s.pair('=', token.ASSIGN, token.EQL, "==")
		case '!':
			tok, lit = s.pair('=', token.NOT, token.NEQ, "!=")
		case '&':
			tok, lit = s.pair('&', token.AMP, token.LAND, "&&")
		case '<':
			if s.ch == '!' && s.rdOffset+1 < len(s.src) && s.src[s.rdOffset] == '-' && s.src[s.rdOffset+1] == '-' {
				tok, lit = s.scanHTMLComment(pos)
				break
			}
			tok, lit = s.pair('=', token.LSS, token.LEQ, "<=")
		case '>':
			tok, lit = s.pair('=', token.GTR, token.GEQ, ">=")
		case ':':
			tok = token.COLON
		case '?':
			tok = token.QUESTION
		case '#':
			tok = token.HASH
		case '@':
			tok = token.AT
		case '|':
			tok, lit = s.pair('|', token.OR, token.LOR, "||")
		case ',':
			tok = token.COMMA
		case '.':
			tok = token.PERIOD
		case '[':
			tok = token.LBRACK
		case ']':
			tok = token.RBRACK
		case '{':
			tok = token.LBRACE
		case '}':
			tok = token.RBRACE
		case '(':
			tok = token.LPAREN
		case ')':
			tok = token.RPAREN
		case '`':
			if s.ch == '`' && s.rdOffset < len(s.src) && s.src[s.rdOffset] == '`' {
				tok, lit = s.scanRawText(pos)
				break
			}
			tok, lit = s.scanString(ch)
		case '"':
			tok, lit = s.scanString(ch)
		case '/':
			if s.ch == '/' {
				s.scanComment()
				return s.Scan()
			}
			tok = token.ILLEGAL
			lit = string(ch)
		default:
			tok = token.ILLEGAL
			lit = string(ch)
		}
	}

	return pos, tok, lit
}

// pair scans a one- or two-character operator. Two-character operators carry
// their spelling as the literal so their ranges have the right length.
func (s *Scanner) pair(second rune, single, double token.Token, spelling string) (token.Token, string) {
	if s.ch == second {
		s.next()
		return double, spelling
	}
	return single, ""
}

// Offset returns the end of the most recently scanned token.
func (s *Scanner) Offset() int {
	return s.offset
}

func (s *Scanner) skipWhitespace() {
	for s.ch == ' ' || s.ch == '\t' || s.ch == '\n' || s.ch == '\r' {
		s.next()
	}
}

func (s *Scanner) scanIdentifier() string {
	offs := s.offset
	for isLetter(s.ch) || isDigit(s.ch) || s.ch == '-' {
		s.next()
	}
	return string(s.src[offs:s.offset])
}

func (s *Scanner) scanNumber() (token.Token, string) {
	offs := s.offset
	for isDigit(s.ch) {
		s.next()
	}
	if s.ch == '.' {
		s.next()
		for isDigit(s.ch) {
			s.next()
		}
	}
	if s.ch == '.' {
		for isDigit(s.ch) || s.ch == '.' {
			s.next()
		}
		return token.ILLEGAL, string(s.src[offs:s.offset])
	}
	return token.NUMBER, string(s.src[offs:s.offset])
}

func (s *Scanner) scanString(quote rune) (token.Token, string) {
	offs := s.offset - 1
	for {
		ch := s.ch
		if ch == 0 || ch == '\n' {
			return token.ILLEGAL, string(s.src[offs:s.offset])
		}
		s.next()
		if ch == quote {
			break
		}
		if ch == '\\' {
			s.next()
		}
	}
	return token.STRING, string(s.src[offs:s.offset])
}

// scanHTMLComment scans <!-- … --> starting at start, whose < was consumed.
// The comment ends at the first --> after <!--. An unterminated comment is
// ILLEGAL and runs to the end of the source.
func (s *Scanner) scanHTMLComment(start int) (token.Token, string) {
	textStart := start + len("<!--")
	end := strings.Index(string(s.src[textStart:]), "-->")
	if end < 0 {
		s.Seek(len(s.src))
		return token.ILLEGAL, string(s.src[start:])
	}
	end += textStart + len("-->")
	s.Seek(end)
	return token.HTML_COMMENT, string(s.src[start:end])
}

// scanRawText scans a raw-text literal, ``` … ```, starting at start, whose
// first backtick was consumed. It ends at the first ``` after the opening
// fence and may span lines. An unterminated literal is ILLEGAL and runs to
// the end of the source.
func (s *Scanner) scanRawText(start int) (token.Token, string) {
	textStart := start + len(RawTextFence)
	end := strings.Index(string(s.src[textStart:]), RawTextFence)
	if end < 0 {
		s.Seek(len(s.src))
		return token.ILLEGAL, string(s.src[start:])
	}
	end += textStart + len(RawTextFence)
	s.Seek(end)
	return token.RAW_TEXT, string(s.src[start:end])
}

// RawTextFence opens and closes a raw-text literal.
const RawTextFence = "```"

// Seek repositions the scanner so that the next token starts at offset.
func (s *Scanner) Seek(offset int) {
	s.rdOffset = offset
	s.next()
}

// AttributeNameEnd returns the end of the attribute name starting at start:
// an optional @ or :, a letter or _, then letters, digits, _, -, :, ., and *
// It returns start when no name starts there.
func AttributeNameEnd(src []byte, start int) int {
	offset := start
	if offset < len(src) && (src[offset] == '@' || src[offset] == ':') {
		offset++
	}
	if offset >= len(src) || !isLetter(rune(src[offset])) {
		return start
	}
	for offset < len(src) && IsAttributeNameChar(src[offset]) {
		offset++
	}
	return offset
}

// IsAttributeNameChar reports whether b may continue an attribute name.
func IsAttributeNameChar(b byte) bool {
	return attrname.NameChar(b)
}

func (s *Scanner) scanComment() {
	for s.ch != '\n' && s.ch != 0 {
		s.next()
	}
}

func isLetter(ch rune) bool {
	return 'a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || ch == '_'
}

func isDigit(ch rune) bool {
	return '0' <= ch && ch <= '9'
}
