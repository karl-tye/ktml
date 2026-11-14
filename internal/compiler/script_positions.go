package compiler

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// ScriptPosition is where an interpolation falls in the static JavaScript
// around it.
type ScriptPosition int

const (
	// ScriptExpression is an expression position in code: the only position
	// where an interpolated value may appear.
	ScriptExpression ScriptPosition = iota
	ScriptString
	ScriptTemplate
	ScriptComment
	ScriptRegex
	// ScriptUncertain follows a / that the lexer cannot classify as a regex
	// or a division, so it fails closed for every later interpolation.
	ScriptUncertain
)

// Describe returns the position for a diagnostic.
func (position ScriptPosition) Describe() string {
	switch position {
	case ScriptString:
		return "inside a JavaScript string literal"
	case ScriptTemplate:
		return "inside a JavaScript template literal"
	case ScriptComment:
		return "inside a JavaScript comment"
	case ScriptRegex:
		return "inside a JavaScript regular expression"
	case ScriptUncertain:
		return "after a / that may start a regular expression"
	}
	return "in an expression position"
}

// scriptLexer tracks enough JavaScript lexical state to tell whether a
// position is in code (an expression position) or inside a string,
// template literal, comment, or regular expression. It is deliberately
// conservative: where it cannot decide, it reports ScriptUncertain.
type scriptLexer struct {
	position ScriptPosition
	quote    byte
	// previous is the last significant code character, or 0 at the start;
	// word is set when it ended an identifier, number, or keyword.
	previous byte
	word     string
	escaped  bool
	// classOpen is set inside a regular expression character class.
	classOpen bool
	// substitutions holds one brace depth for each open ${ … } of a
	// template literal; code inside one still counts as the template.
	substitutions []int
}

// regexKeywords may precede a regular expression literal.
var regexKeywords = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "throw": true, "case": true, "do": true, "else": true, "yield": true, "await": true,
}

// feed advances the lexer over static text.
func (l *scriptLexer) feed(text string) {
	for index := 0; index < len(text); index++ {
		c := text[index]
		switch l.position {
		case ScriptUncertain:
			return
		case ScriptString, ScriptTemplate:
			switch {
			case l.escaped:
				l.escaped = false
			case c == '\\':
				l.escaped = true
			case l.position == ScriptTemplate && c == '$' && index+1 < len(text) && text[index+1] == '{':
				index++
				l.substitutions = append(l.substitutions, 0)
				l.position, l.previous, l.word = ScriptExpression, '{', ""
			case c == l.quote:
				l.position, l.previous, l.word = ScriptExpression, '"', ""
			case l.position == ScriptString && (c == '\n' || c == '\r'):
				// An unterminated string ends at the line in JavaScript.
				l.position, l.previous, l.word = ScriptExpression, '"', ""
			}
		case ScriptComment:
			if l.quote == '/' && (c == '\n' || c == '\r') {
				l.position = ScriptExpression
			} else if l.quote == '*' && c == '*' && index+1 < len(text) && text[index+1] == '/' {
				index++
				l.position = ScriptExpression
			}
		case ScriptRegex:
			switch {
			case l.escaped:
				l.escaped = false
			case c == '\\':
				l.escaped = true
			case c == '[':
				l.classOpen = true
			case c == ']':
				l.classOpen = false
			case c == '/' && !l.classOpen:
				l.position, l.previous, l.word = ScriptExpression, ')', ""
			case c == '\n' || c == '\r':
				l.position = ScriptUncertain
			}
		default:
			index = l.code(text, index)
		}
	}
}

// code lexes one character in an expression position and returns the index
// of the last character consumed.
func (l *scriptLexer) code(text string, index int) int {
	c := text[index]
	switch {
	case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		return index
	case c == '\'' || c == '"' || c == '`':
		l.position, l.quote = ScriptString, c
		if c == '`' {
			l.position = ScriptTemplate
		}
		return index
	case c == '/' && index+1 < len(text) && (text[index+1] == '/' || text[index+1] == '*'):
		l.position, l.quote = ScriptComment, text[index+1]
		return index + 1
	case c == '{' && len(l.substitutions) > 0:
		l.substitutions[len(l.substitutions)-1]++
	case c == '}' && len(l.substitutions) > 0:
		top := len(l.substitutions) - 1
		if l.substitutions[top] == 0 {
			l.substitutions = l.substitutions[:top]
			l.position, l.quote = ScriptTemplate, '`'
			return index
		}
		l.substitutions[top]--
	case c == '/':
		switch {
		case l.regexAllowed():
			l.position, l.classOpen = ScriptRegex, false
		case l.previous == '}' || l.previous == ')':
			// A block or an if (…) header ends before a regular expression;
			// an object literal or a parenthesized expression ends before a
			// division.
			l.position = ScriptUncertain
		default:
			l.previous, l.word = '/', ""
		}
		return index
	case isScriptWordByte(c):
		end := index
		for end < len(text) && isScriptWordByte(text[end]) {
			end++
		}
		l.word, l.previous = text[index:end], 'a'
		return end - 1
	}
	l.previous, l.word = c, ""
	return index
}

func (l *scriptLexer) regexAllowed() bool {
	switch {
	case l.previous == 0:
		return true
	case l.word != "":
		return regexKeywords[l.word]
	}
	return strings.IndexByte("(,=:[!&|?{;+-*%<>~^", l.previous) >= 0
}

// interpolate records an interpolated value: an expression, after which a
// / is a division.
func (l *scriptLexer) interpolate() {
	l.previous, l.word = 'a', ""
}

// current returns the position of the next character. Code inside a
// template literal substitution still counts as the template.
func (l *scriptLexer) current() ScriptPosition {
	if l.position == ScriptExpression && len(l.substitutions) > 0 {
		return ScriptTemplate
	}
	return l.position
}

func isScriptWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c >= 0x80
}

// ScriptInterpolations checks where the paths of a script value fall in its
// static JavaScript. It returns, for each segment, the position of a path
// segment, or ScriptExpression for literal segments.
func ScriptInterpolations(literals []string, isPath []bool) []ScriptPosition {
	var lexer scriptLexer
	positions := make([]ScriptPosition, len(literals))
	for index, literal := range literals {
		if !isPath[index] {
			lexer.feed(literal)
			continue
		}
		positions[index] = lexer.current()
		lexer.interpolate()
	}
	return positions
}

// scriptPositions returns the position of each interpolated path of a
// script value in the static JavaScript around it.
func scriptPositions(quoted *ast.QuotedValue) map[*ast.PathExpression]ScriptPosition {
	if quoted == nil {
		return nil
	}
	literals := make([]string, len(quoted.Segments))
	isPath := make([]bool, len(quoted.Segments))
	for index, segment := range quoted.Segments {
		literals[index], isPath[index] = segment.Literal, segment.Kind == ast.PathSegment
	}
	positions := make(map[*ast.PathExpression]ScriptPosition)
	for index, position := range ScriptInterpolations(literals, isPath) {
		if isPath[index] {
			positions[quoted.Segments[index].Path] = position
		}
	}
	return positions
}

// reportScriptPosition reports an interpolated path of a script value
// (a script attribute or a script element body) whose type is accepted but
// which is not in an expression position of the static JavaScript around
// it (ADR-0001 amendment of 2026-10-13).
func (s *Snapshot) reportScriptPosition(path string, expression *ast.PathExpression, where string) {
	position, ok := s.scriptPositions[expression]
	if !ok || position == ScriptExpression {
		return
	}
	s.addDiagnostic(path, "script-interpolation-position", fmt.Sprintf("{%s} is %s in %s; an interpolated value may appear only where a JavaScript expression may, such as a call argument, an array or object literal, or the right side of an assignment", expression.String(), position.Describe(), where), expression.P, expression.E)
}
