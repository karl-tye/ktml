package alpine

import (
	"fmt"
	"strings"
)

// Kind is the kind of a JavaScript syntax node.
type Kind int

const (
	Ident Kind = iota
	This
	Literal
	TemplateLit
	RegexLit
	// Hole is a KTML interpolation, {path}, standing for one value.
	Hole
	ArrayLit
	ObjectLit
	// Property is an object literal entry; Name is its key. Its first
	// child is the value, or the function of a method, getter, or setter.
	Property
	Spread
	Member
	Index
	Call
	New
	Unary
	Update
	Binary
	Assign
	Conditional
	Sequence
	Arrow
	Function
	Paren
	// Statements.
	Block
	ExprStmt
	VarDecl
	// Declarator has the binding pattern and an optional initializer.
	Declarator
	If
	Return
	Loop
	Try
	Jump
	Empty
	// Patterns.
	ObjectPattern
	ArrayPattern
	DefaultPattern
	RestPattern
)

// LiteralKind refines a Literal node.
type LiteralKind int

const (
	NumberLiteral LiteralKind = iota
	StringLiteral
	BooleanLiteral
	NullLiteral
	UndefinedLiteral
)

// Node is a JavaScript syntax node. Offsets are into the parsed text.
type Node struct {
	Kind       Kind
	Start, End int
	// Name is the identifier, property name, or operator.
	Name string
	// NameStart and NameEnd locate Name for Ident, Member, and Property.
	NameStart, NameEnd int
	Children           []*Node
	// Params are the parameter patterns of Arrow and Function.
	Params  []*Node
	Literal LiteralKind
	// Method, Getter, Setter, Shorthand, and Computed refine Property.
	Method, Getter, Setter, Shorthand, Computed bool
	// Optional marks ?. member access and calls.
	Optional bool
	// Hole is the index of a KTML interpolation.
	Hole int
	// Value is the decoded value of a string literal.
	Value string
}

// SyntaxError is a parse failure at a text offset.
type SyntaxError struct {
	Start, End int
	Message    string
}

func (e *SyntaxError) Error() string { return e.Message }

type tokenKind int

const (
	tEOF tokenKind = iota
	tIdent
	tNumber
	tString
	tTemplate
	tRegex
	tPunct
	tHole
	tPrivate
)

type token struct {
	kind       tokenKind
	text       string
	start, end int
	// newline reports a line break before the token.
	newline bool
	value   string
}

// holeByte stands for a KTML interpolation in parsed text.
const holeByte = 0

type parser struct {
	src    string
	pos    int
	tok    token
	prev   token
	holes  int
	err    *SyntaxError
	regexp bool // whether a / starts a regular expression
}

type bailout struct{}

func (p *parser) fail(start, end int, format string, args ...any) {
	if p.err == nil {
		if end <= start {
			end = start + 1
		}
		p.err = &SyntaxError{Start: start, End: end, Message: fmt.Sprintf(format, args...)}
	}
	panic(bailout{})
}

var punctuators = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=", "*=", "/=", "%=",
	"&=", "|=", "^=", "**", "<<", ">>",
	"{", "}", "(", ")", "[", "]", ";", ",", "<", ">", "+", "-", "*", "/", "%", "&", "|", "^",
	"!", "~", "?", ":", "=", ".", "@",
}

func identStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '$' || c >= 0x80
}

func identPart(c byte) bool {
	return identStart(c) || c >= '0' && c <= '9'
}

func (p *parser) next() {
	p.prev = p.tok
	newline := false
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\n' || c == '\r' || c == 0xe2 && strings.HasPrefix(p.src[p.pos:], " ") {
			newline = true
			p.pos++
			continue
		}
		if c == ' ' || c == '\t' || c == '\f' || c == '\v' {
			p.pos++
			continue
		}
		if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*' {
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.fail(p.pos, len(p.src), "unterminated comment")
			}
			if strings.Contains(p.src[p.pos:p.pos+2+end], "\n") {
				newline = true
			}
			p.pos += end + 4
			continue
		}
		break
	}
	start := p.pos
	p.tok = token{start: start, newline: newline}
	if p.pos >= len(p.src) {
		p.tok.kind, p.tok.end = tEOF, p.pos
		return
	}
	c := p.src[p.pos]
	switch {
	case c == holeByte:
		p.pos++
		p.tok.kind = tHole
	case identStart(c) || c == '\\':
		for p.pos < len(p.src) && (identPart(p.src[p.pos]) || p.src[p.pos] == '\\') {
			p.pos++
		}
		p.tok.kind = tIdent
	case c == '#' && p.pos+1 < len(p.src) && identStart(p.src[p.pos+1]):
		p.pos++
		for p.pos < len(p.src) && identPart(p.src[p.pos]) {
			p.pos++
		}
		p.tok.kind = tPrivate
	case c >= '0' && c <= '9' || c == '.' && p.pos+1 < len(p.src) && p.src[p.pos+1] >= '0' && p.src[p.pos+1] <= '9':
		p.scanNumber()
		p.tok.kind = tNumber
	case c == '"' || c == '\'':
		p.tok.value = p.scanString(c)
		p.tok.kind = tString
	case c == '`':
		p.scanTemplate()
		p.tok.kind = tTemplate
	case c == '/' && p.regexp:
		p.scanRegex()
		p.tok.kind = tRegex
	default:
		for _, punct := range punctuators {
			if strings.HasPrefix(p.src[p.pos:], punct) {
				// ?. followed by a digit is ? and a number.
				if punct == "?." && p.pos+2 < len(p.src) && p.src[p.pos+2] >= '0' && p.src[p.pos+2] <= '9' {
					continue
				}
				p.pos += len(punct)
				p.tok.kind = tPunct
				break
			}
		}
		if p.tok.kind != tPunct {
			p.fail(p.pos, p.pos+1, "unexpected character %q", string(c))
		}
	}
	p.tok.end = p.pos
	p.tok.text = p.src[start:p.pos]
	if p.tok.kind == tHole {
		p.tok.text = ""
	}
	// A / after an operand is division; elsewhere it starts a regular
	// expression.
	switch p.tok.kind {
	case tIdent:
		p.regexp = keywordsBeforeExpression[p.tok.text]
	case tNumber, tString, tTemplate, tRegex, tHole, tPrivate:
		p.regexp = false
	case tPunct:
		p.regexp = p.tok.text != ")" && p.tok.text != "]" && p.tok.text != "}" && p.tok.text != "++" && p.tok.text != "--"
	}
}

var keywordsBeforeExpression = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true, "delete": true,
	"void": true, "throw": true, "case": true, "do": true, "else": true, "await": true, "yield": true,
}

func (p *parser) scanNumber() {
	if p.src[p.pos] == '0' && p.pos+1 < len(p.src) && strings.ContainsRune("xXoObB", rune(p.src[p.pos+1])) {
		p.pos += 2
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '_' || c == '.' || c == 'n' {
			p.pos++
			continue
		}
		if (c == '+' || c == '-') && (p.src[p.pos-1] == 'e' || p.src[p.pos-1] == 'E') {
			p.pos++
			continue
		}
		break
	}
}

func (p *parser) scanString(quote byte) string {
	start := p.pos
	p.pos++
	var value strings.Builder
	for {
		if p.pos >= len(p.src) || p.src[p.pos] == '\n' {
			p.fail(start, p.pos, "unterminated string")
		}
		c := p.src[p.pos]
		if c == quote {
			p.pos++
			return value.String()
		}
		if c == '\\' && p.pos+1 < len(p.src) {
			value.WriteByte(p.src[p.pos+1])
			p.pos += 2
			continue
		}
		value.WriteByte(c)
		p.pos++
	}
}

// scanTemplate skips a template literal; its substitutions are parsed by
// parseTemplate.
func (p *parser) scanTemplate() {
	start := p.pos
	p.pos++
	for {
		if p.pos >= len(p.src) {
			p.fail(start, p.pos, "unterminated template literal")
		}
		switch p.src[p.pos] {
		case '\\':
			p.pos += 2
			continue
		case '`':
			p.pos++
			return
		case '$':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '{' {
				p.pos = p.skipBalanced(p.pos+1, start)
				continue
			}
		}
		p.pos++
	}
}

// skipBalanced returns the offset after the brace group starting at open.
func (p *parser) skipBalanced(open, start int) int {
	depth := 0
	for i := open; i < len(p.src); i++ {
		switch p.src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"', '\'':
			quote := p.src[i]
			for i++; i < len(p.src) && p.src[i] != quote; i++ {
				if p.src[i] == '\\' {
					i++
				}
			}
		case '`':
			inner := &parser{src: p.src, pos: i}
			func() {
				defer func() {
					if r := recover(); r != nil {
						if _, ok := r.(bailout); !ok {
							panic(r)
						}
						p.fail(start, len(p.src), "unterminated template literal")
					}
				}()
				inner.scanTemplate()
			}()
			i = inner.pos - 1
		}
	}
	p.fail(start, len(p.src), "unterminated template literal")
	return 0
}

func (p *parser) scanRegex() {
	start := p.pos
	p.pos++
	class := false
	for {
		if p.pos >= len(p.src) || p.src[p.pos] == '\n' {
			p.fail(start, p.pos, "unterminated regular expression")
		}
		c := p.src[p.pos]
		p.pos++
		switch {
		case c == '\\':
			p.pos++
		case c == '[':
			class = true
		case c == ']':
			class = false
		case c == '/' && !class:
			for p.pos < len(p.src) && identPart(p.src[p.pos]) {
				p.pos++
			}
			return
		}
	}
}

func (p *parser) is(text string) bool {
	return (p.tok.kind == tPunct || p.tok.kind == tIdent) && p.tok.text == text
}

func (p *parser) eat(text string) bool {
	if p.is(text) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(text string) token {
	if !p.is(text) {
		p.unexpected("expected " + text)
	}
	tok := p.tok
	p.next()
	return tok
}

func (p *parser) unexpected(expected string) {
	if p.tok.kind == tEOF {
		p.fail(p.tok.start, p.tok.start, "unexpected end of expression; %s", expected)
	}
	p.fail(p.tok.start, p.tok.end, "unexpected %s; %s", describe(p.tok), expected)
}

func describe(tok token) string {
	switch tok.kind {
	case tHole:
		return "interpolation"
	case tString:
		return "string"
	case tNumber:
		return "number"
	}
	return fmt.Sprintf("%q", tok.text)
}

// ParseProgram parses an Alpine expression the way Alpine evaluates it: a
// list of statements whose first is usually an expression. A leading { is
// an object literal, as in x-data.
func ParseProgram(src string) (program *Node, err *SyntaxError) {
	p := &parser{src: src, regexp: true}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			program, err = nil, p.err
		}
	}()
	p.next()
	program = &Node{Kind: Block, Start: 0, End: len(src)}
	if p.is("{") {
		expression := p.parseExpression()
		program.Children = append(program.Children, &Node{Kind: ExprStmt, Start: expression.Start, End: expression.End, Children: []*Node{expression}})
		p.endStatement()
	}
	for p.tok.kind != tEOF {
		program.Children = append(program.Children, p.parseStatement())
	}
	return program, nil
}

// ParseExpression parses exactly one expression.
func ParseExpression(src string) (expression *Node, err *SyntaxError) {
	p := &parser{src: src, regexp: true}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			expression, err = nil, p.err
		}
	}()
	p.next()
	expression = p.parseExpression()
	if p.tok.kind != tEOF {
		p.unexpected("expected the end of the expression")
	}
	return expression, nil
}

// ParsePattern parses a binding pattern, such as an x-for item.
func ParsePattern(src string) (pattern *Node, err *SyntaxError) {
	p := &parser{src: src, regexp: true}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			pattern, err = nil, p.err
		}
	}()
	p.next()
	pattern = p.parseBindingTarget()
	if p.tok.kind != tEOF {
		p.unexpected("expected the end of the binding")
	}
	return pattern, nil
}

func (p *parser) endStatement() {
	switch {
	case p.eat(";"):
	case p.is("}") || p.tok.kind == tEOF || p.tok.newline:
	default:
		p.unexpected("expected ; or the end of the statement")
	}
}

func (p *parser) parseStatement() *Node {
	start := p.tok.start
	switch {
	case p.is(";"):
		p.next()
		return &Node{Kind: Empty, Start: start, End: p.prev.end}
	case p.is("{"):
		return p.parseBlock()
	case p.is("let") || p.is("const") || p.is("var"):
		node := p.parseVarDecl()
		p.endStatement()
		return node
	case p.is("if"):
		p.next()
		p.expect("(")
		test := p.parseExpression()
		p.expect(")")
		node := &Node{Kind: If, Start: start, Children: []*Node{test, p.parseStatement()}}
		if p.eat("else") {
			node.Children = append(node.Children, p.parseStatement())
		}
		node.End = p.prev.end
		return node
	case p.is("return") || p.is("throw"):
		keyword := p.tok.text
		p.next()
		node := &Node{Kind: Return, Start: start, Name: keyword}
		if !p.is(";") && !p.is("}") && p.tok.kind != tEOF && !p.tok.newline {
			node.Children = append(node.Children, p.parseExpression())
		}
		p.endStatement()
		node.End = p.prev.end
		return node
	case p.is("break") || p.is("continue"):
		p.next()
		if p.tok.kind == tIdent && !p.tok.newline {
			p.next()
		}
		p.endStatement()
		return &Node{Kind: Jump, Start: start, End: p.prev.end}
	case p.is("for"):
		return p.parseFor()
	case p.is("while"):
		p.next()
		p.expect("(")
		test := p.parseExpression()
		p.expect(")")
		body := p.parseStatement()
		return &Node{Kind: Loop, Start: start, End: p.prev.end, Children: []*Node{test, body}}
	case p.is("do"):
		p.next()
		body := p.parseStatement()
		p.expect("while")
		p.expect("(")
		test := p.parseExpression()
		p.expect(")")
		p.eat(";")
		return &Node{Kind: Loop, Start: start, End: p.prev.end, Children: []*Node{body, test}}
	case p.is("try"):
		p.next()
		node := &Node{Kind: Try, Start: start, Children: []*Node{p.parseBlock()}}
		if p.eat("catch") {
			var param *Node
			if p.eat("(") {
				param = p.parseBindingTarget()
				p.expect(")")
			}
			handler := p.parseBlock()
			if param != nil {
				handler.Params = []*Node{param}
			}
			node.Children = append(node.Children, handler)
		}
		if p.eat("finally") {
			node.Children = append(node.Children, p.parseBlock())
		}
		node.End = p.prev.end
		return node
	case p.is("function") || p.is("async") && p.peekFunction():
		function := p.parsePrimary()
		return &Node{Kind: ExprStmt, Start: start, End: function.End, Children: []*Node{function}}
	}
	expression := p.parseExpression()
	p.endStatement()
	return &Node{Kind: ExprStmt, Start: start, End: expression.End, Children: []*Node{expression}}
}

func (p *parser) peekFunction() bool {
	rest := strings.TrimLeft(p.src[p.tok.end:], " \t")
	return strings.HasPrefix(rest, "function")
}

func (p *parser) parseBlock() *Node {
	start := p.expect("{").start
	node := &Node{Kind: Block, Start: start}
	for !p.is("}") {
		if p.tok.kind == tEOF {
			p.unexpected("expected }")
		}
		node.Children = append(node.Children, p.parseStatement())
	}
	p.next()
	node.End = p.prev.end
	return node
}

func (p *parser) parseVarDecl() *Node {
	start := p.tok.start
	keyword := p.tok.text
	p.next()
	node := &Node{Kind: VarDecl, Start: start, Name: keyword}
	for {
		target := p.parseBindingTarget()
		declarator := &Node{Kind: Declarator, Start: target.Start, End: target.End, Children: []*Node{target}}
		if p.eat("=") {
			init := p.parseAssignment()
			declarator.Children = append(declarator.Children, init)
			declarator.End = init.End
		}
		node.Children = append(node.Children, declarator)
		if !p.eat(",") {
			break
		}
	}
	node.End = p.prev.end
	return node
}

func (p *parser) parseFor() *Node {
	start := p.expect("for").start
	p.eat("await")
	p.expect("(")
	node := &Node{Kind: Loop, Start: start}
	var init *Node
	switch {
	case p.is(";"):
	case p.is("let") || p.is("const") || p.is("var"):
		init = p.parseVarDecl()
	default:
		init = p.parseExpression()
	}
	if init != nil {
		node.Children = append(node.Children, init)
	}
	if p.eat("of") || p.eat("in") {
		node.Children = append(node.Children, p.parseExpression())
	} else {
		p.expect(";")
		if !p.is(";") {
			node.Children = append(node.Children, p.parseExpression())
		}
		p.expect(";")
		if !p.is(")") {
			node.Children = append(node.Children, p.parseExpression())
		}
	}
	p.expect(")")
	node.Children = append(node.Children, p.parseStatement())
	node.End = p.prev.end
	return node
}

func (p *parser) parseExpression() *Node {
	expression := p.parseAssignment()
	if !p.is(",") {
		return expression
	}
	node := &Node{Kind: Sequence, Start: expression.Start, Children: []*Node{expression}}
	for p.eat(",") {
		node.Children = append(node.Children, p.parseAssignment())
	}
	node.End = p.prev.end
	return node
}

var assignmentOperators = map[string]bool{
	"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true, "**=": true, "<<=": true,
	">>=": true, ">>>=": true, "&=": true, "|=": true, "^=": true, "&&=": true, "||=": true, "??=": true,
}

func (p *parser) parseAssignment() *Node {
	if arrow := p.tryArrow(); arrow != nil {
		return arrow
	}
	if p.is("yield") {
		start := p.tok.start
		p.next()
		node := &Node{Kind: Unary, Name: "yield", Start: start}
		if !p.is(")") && !p.is("]") && !p.is("}") && !p.is(",") && !p.is(";") && p.tok.kind != tEOF {
			node.Children = []*Node{p.parseAssignment()}
		}
		node.End = p.prev.end
		return node
	}
	left := p.parseConditional()
	if p.tok.kind == tPunct && assignmentOperators[p.tok.text] {
		operator := p.tok.text
		if !assignable(left) {
			p.fail(left.Start, left.End, "the left side of %s is not assignable", operator)
		}
		p.next()
		right := p.parseAssignment()
		return &Node{Kind: Assign, Name: operator, Start: left.Start, End: right.End, Children: []*Node{toPattern(left), right}}
	}
	return left
}

func assignable(node *Node) bool {
	switch node.Kind {
	case Ident, Member, Index, ObjectLit, ArrayLit:
		return true
	case Paren:
		return len(node.Children) == 1 && (node.Children[0].Kind == Ident || node.Children[0].Kind == Member || node.Children[0].Kind == Index)
	}
	return false
}

// toPattern turns the object and array literals on an assignment's left
// into destructuring patterns.
func toPattern(node *Node) *Node {
	switch node.Kind {
	case ObjectLit:
		node.Kind = ObjectPattern
		for _, property := range node.Children {
			if len(property.Children) > 0 {
				property.Children[0] = toPattern(property.Children[0])
			}
		}
	case ArrayLit:
		node.Kind = ArrayPattern
		for index, element := range node.Children {
			if element != nil {
				node.Children[index] = toPattern(element)
			}
		}
	case Spread:
		node.Kind = RestPattern
	case Assign:
		node.Kind = DefaultPattern
	}
	return node
}

// tryArrow parses an arrow function if one starts here.
func (p *parser) tryArrow() *Node {
	start := p.tok.start
	async := false
	if p.is("async") && !p.tok.newline {
		rest := strings.TrimLeft(p.src[p.tok.end:], " \t")
		if strings.HasPrefix(rest, "(") || len(rest) > 0 && identStart(rest[0]) && !strings.HasPrefix(rest, "function") {
			if end := p.arrowParamsEnd(p.tok.end); end >= 0 {
				async = true
				p.next()
			}
		}
	}
	switch {
	case p.tok.kind == tIdent && !reserved[p.tok.text]:
		rest := strings.TrimLeft(p.src[p.tok.end:], " \t")
		if !strings.HasPrefix(rest, "=>") {
			return nil
		}
		param := &Node{Kind: Ident, Name: p.tok.text, Start: p.tok.start, End: p.tok.end, NameStart: p.tok.start, NameEnd: p.tok.end}
		p.next()
		p.expect("=>")
		return p.arrowBody(start, []*Node{param}, async)
	case p.is("("):
		if p.arrowParamsEnd(p.tok.start) < 0 {
			return nil
		}
		p.next()
		var params []*Node
		for !p.is(")") {
			if p.eat("...") {
				target := p.parseBindingTarget()
				params = append(params, &Node{Kind: RestPattern, Start: target.Start, End: target.End, Children: []*Node{target}})
			} else {
				params = append(params, p.parseBindingElement())
			}
			if !p.eat(",") {
				break
			}
		}
		p.expect(")")
		p.expect("=>")
		return p.arrowBody(start, params, async)
	}
	return nil
}

// arrowParamsEnd returns the offset of => when the text at from is an
// arrow parameter list, or -1.
func (p *parser) arrowParamsEnd(from int) int {
	i := from
	for i < len(p.src) && (p.src[i] == ' ' || p.src[i] == '\t') {
		i++
	}
	if i < len(p.src) && identStart(p.src[i]) {
		for i < len(p.src) && identPart(p.src[i]) {
			i++
		}
	} else if i < len(p.src) && p.src[i] == '(' {
		depth := 0
		for ; i < len(p.src); i++ {
			c := p.src[i]
			if c == '"' || c == '\'' || c == '`' {
				for i++; i < len(p.src) && p.src[i] != c; i++ {
					if p.src[i] == '\\' {
						i++
					}
				}
				continue
			}
			if c == '(' || c == '[' || c == '{' {
				depth++
			}
			if c == ')' || c == ']' || c == '}' {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
		}
	} else {
		return -1
	}
	for i < len(p.src) && (p.src[i] == ' ' || p.src[i] == '\t') {
		i++
	}
	if strings.HasPrefix(p.src[i:], "=>") {
		return i
	}
	return -1
}

func (p *parser) arrowBody(start int, params []*Node, async bool) *Node {
	node := &Node{Kind: Arrow, Start: start, Params: params}
	if async {
		node.Name = "async"
	}
	if p.is("{") {
		node.Children = []*Node{p.parseBlock()}
	} else {
		node.Children = []*Node{p.parseAssignment()}
	}
	node.End = p.prev.end
	return node
}

var reserved = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "export": true,
	"extends": true, "finally": true, "for": true, "function": true, "if": true, "import": true,
	"in": true, "instanceof": true, "new": true, "return": true, "super": true, "switch": true,
	"this": true, "throw": true, "try": true, "typeof": true, "var": true, "void": true, "while": true,
	"with": true, "true": true, "false": true, "null": true,
}

func (p *parser) parseConditional() *Node {
	test := p.parseBinary(0)
	if !p.eat("?") {
		return test
	}
	consequent := p.parseAssignment()
	p.expect(":")
	alternate := p.parseAssignment()
	return &Node{Kind: Conditional, Start: test.Start, End: alternate.End, Children: []*Node{test, consequent, alternate}}
}

var precedence = map[string]int{
	"??": 1, "||": 2, "&&": 3, "|": 4, "^": 5, "&": 6,
	"==": 7, "!=": 7, "===": 7, "!==": 7,
	"<": 8, ">": 8, "<=": 8, ">=": 8, "instanceof": 8, "in": 8,
	"<<": 9, ">>": 9, ">>>": 9, "+": 10, "-": 10, "*": 11, "/": 11, "%": 11, "**": 12,
}

func (p *parser) parseBinary(minimum int) *Node {
	left := p.parseUnary()
	for {
		operator := p.tok.text
		level, ok := precedence[operator]
		if !ok || p.tok.kind != tPunct && p.tok.kind != tIdent || level <= minimum {
			return left
		}
		p.next()
		next := level
		if operator == "**" {
			next = level - 1
		}
		right := p.parseBinary(next)
		left = &Node{Kind: Binary, Name: operator, Start: left.Start, End: right.End, Children: []*Node{left, right}}
	}
}

func (p *parser) parseUnary() *Node {
	start := p.tok.start
	switch {
	case p.is("!") || p.is("~") || p.is("+") || p.is("-") || p.is("typeof") || p.is("void") || p.is("delete") || p.is("await"):
		operator := p.tok.text
		p.next()
		operand := p.parseUnary()
		return &Node{Kind: Unary, Name: operator, Start: start, End: operand.End, Children: []*Node{operand}}
	case p.is("++") || p.is("--"):
		operator := p.tok.text
		p.next()
		operand := p.parseUnary()
		return &Node{Kind: Update, Name: operator, Start: start, End: operand.End, Children: []*Node{operand}}
	}
	expression := p.parsePostfix()
	if (p.is("++") || p.is("--")) && !p.tok.newline {
		operator := p.tok.text
		p.next()
		return &Node{Kind: Update, Name: operator, Start: expression.Start, End: p.prev.end, Children: []*Node{expression}}
	}
	return expression
}

func (p *parser) parsePostfix() *Node {
	var expression *Node
	if p.is("new") {
		start := p.tok.start
		p.next()
		if p.eat(".") {
			p.next() // new.target
			expression = &Node{Kind: Ident, Name: "new.target", Start: start, End: p.prev.end}
		} else {
			callee := p.parseMemberOnly()
			node := &Node{Kind: New, Start: start, Children: []*Node{callee}}
			if p.is("(") {
				node.Children = append(node.Children, p.parseArguments()...)
			}
			node.End = p.prev.end
			expression = node
		}
	} else {
		expression = p.parsePrimary()
	}
	return p.parseSuffixes(expression, true)
}

// parseMemberOnly parses a new callee: a primary and its member accesses.
func (p *parser) parseMemberOnly() *Node {
	return p.parseSuffixes(p.parsePrimary(), false)
}

func (p *parser) parseSuffixes(expression *Node, calls bool) *Node {
	for {
		switch {
		case p.is(".") || p.is("?."):
			optional := p.is("?.")
			p.next()
			if optional && p.is("(") {
				if !calls {
					return expression
				}
				args := p.parseArguments()
				expression = &Node{Kind: Call, Optional: true, Start: expression.Start, End: p.prev.end, Children: append([]*Node{expression}, args...)}
				continue
			}
			if optional && p.is("[") {
				p.next()
				index := p.parseExpression()
				p.expect("]")
				expression = &Node{Kind: Index, Optional: true, Start: expression.Start, End: p.prev.end, Children: []*Node{expression, index}}
				continue
			}
			if p.tok.kind != tIdent && p.tok.kind != tPrivate {
				p.unexpected("expected a property name")
			}
			expression = &Node{Kind: Member, Optional: optional, Name: p.tok.text, NameStart: p.tok.start, NameEnd: p.tok.end, Start: expression.Start, End: p.tok.end, Children: []*Node{expression}}
			p.next()
		case p.is("["):
			p.next()
			index := p.parseExpression()
			p.expect("]")
			expression = &Node{Kind: Index, Start: expression.Start, End: p.prev.end, Children: []*Node{expression, index}}
		case p.is("(") && calls:
			args := p.parseArguments()
			expression = &Node{Kind: Call, Start: expression.Start, End: p.prev.end, Children: append([]*Node{expression}, args...)}
		case p.tok.kind == tTemplate:
			template := p.parseTemplate()
			expression = &Node{Kind: Call, Start: expression.Start, End: template.End, Children: []*Node{expression, template}}
		default:
			return expression
		}
	}
}

func (p *parser) parseArguments() []*Node {
	p.expect("(")
	var args []*Node
	for !p.is(")") {
		if p.is("...") {
			start := p.tok.start
			p.next()
			argument := p.parseAssignment()
			args = append(args, &Node{Kind: Spread, Start: start, End: argument.End, Children: []*Node{argument}})
		} else {
			args = append(args, p.parseAssignment())
		}
		if !p.eat(",") {
			break
		}
	}
	p.expect(")")
	return args
}

func (p *parser) parsePrimary() *Node {
	tok := p.tok
	switch tok.kind {
	case tHole:
		p.next()
		node := &Node{Kind: Hole, Start: tok.start, End: tok.end, Hole: p.holes}
		p.holes++
		return node
	case tNumber:
		p.next()
		return &Node{Kind: Literal, Literal: NumberLiteral, Start: tok.start, End: tok.end, Name: tok.text}
	case tString:
		p.next()
		return &Node{Kind: Literal, Literal: StringLiteral, Start: tok.start, End: tok.end, Name: tok.text, Value: tok.value}
	case tTemplate:
		return p.parseTemplate()
	case tRegex:
		p.next()
		return &Node{Kind: RegexLit, Start: tok.start, End: tok.end, Name: tok.text}
	case tPrivate:
		p.next()
		return &Node{Kind: Ident, Name: tok.text, Start: tok.start, End: tok.end, NameStart: tok.start, NameEnd: tok.end}
	case tIdent:
		switch tok.text {
		case "true", "false":
			p.next()
			return &Node{Kind: Literal, Literal: BooleanLiteral, Start: tok.start, End: tok.end, Name: tok.text}
		case "null":
			p.next()
			return &Node{Kind: Literal, Literal: NullLiteral, Start: tok.start, End: tok.end, Name: tok.text}
		case "this":
			p.next()
			return &Node{Kind: This, Start: tok.start, End: tok.end, Name: "this"}
		case "function":
			return p.parseFunction(tok.start, false)
		case "async":
			if p.peekFunction() {
				p.next()
				return p.parseFunction(tok.start, true)
			}
		case "class", "import", "super":
			p.fail(tok.start, tok.end, "%s is not supported in an Alpine expression; move the code to a JavaScript module", tok.text)
		}
		if reserved[tok.text] {
			p.unexpected("expected an expression")
		}
		p.next()
		return &Node{Kind: Ident, Name: tok.text, Start: tok.start, End: tok.end, NameStart: tok.start, NameEnd: tok.end}
	case tPunct:
		switch tok.text {
		case "(":
			p.next()
			inner := p.parseExpression()
			p.expect(")")
			return &Node{Kind: Paren, Start: tok.start, End: p.prev.end, Children: []*Node{inner}}
		case "[":
			return p.parseArray()
		case "{":
			return p.parseObject()
		}
	}
	p.unexpected("expected an expression")
	return nil
}

func (p *parser) parseFunction(start int, async bool) *Node {
	p.expect("function")
	p.eat("*")
	node := &Node{Kind: Function, Start: start}
	if async {
		node.Name = "async"
	}
	if p.tok.kind == tIdent {
		node.NameStart, node.NameEnd = p.tok.start, p.tok.end
		p.next()
	}
	node.Params = p.parseParams()
	node.Children = []*Node{p.parseBlock()}
	node.End = p.prev.end
	return node
}

func (p *parser) parseParams() []*Node {
	p.expect("(")
	var params []*Node
	for !p.is(")") {
		if p.eat("...") {
			target := p.parseBindingTarget()
			params = append(params, &Node{Kind: RestPattern, Start: target.Start, End: target.End, Children: []*Node{target}})
		} else {
			params = append(params, p.parseBindingElement())
		}
		if !p.eat(",") {
			break
		}
	}
	p.expect(")")
	return params
}

// parseBindingElement parses a binding target with an optional default.
func (p *parser) parseBindingElement() *Node {
	target := p.parseBindingTarget()
	if p.eat("=") {
		value := p.parseAssignment()
		return &Node{Kind: DefaultPattern, Start: target.Start, End: value.End, Children: []*Node{target, value}}
	}
	return target
}

func (p *parser) parseBindingTarget() *Node {
	tok := p.tok
	switch {
	case tok.kind == tIdent && !reserved[tok.text]:
		p.next()
		return &Node{Kind: Ident, Name: tok.text, Start: tok.start, End: tok.end, NameStart: tok.start, NameEnd: tok.end}
	case p.is("["):
		p.next()
		node := &Node{Kind: ArrayPattern, Start: tok.start}
		for !p.is("]") {
			if p.is(",") {
				p.next()
				node.Children = append(node.Children, nil)
				continue
			}
			if p.eat("...") {
				target := p.parseBindingTarget()
				node.Children = append(node.Children, &Node{Kind: RestPattern, Start: target.Start, End: target.End, Children: []*Node{target}})
			} else {
				node.Children = append(node.Children, p.parseBindingElement())
			}
			if !p.eat(",") {
				break
			}
		}
		p.expect("]")
		node.End = p.prev.end
		return node
	case p.is("{"):
		p.next()
		node := &Node{Kind: ObjectPattern, Start: tok.start}
		for !p.is("}") {
			if p.eat("...") {
				target := p.parseBindingTarget()
				node.Children = append(node.Children, &Node{Kind: RestPattern, Start: target.Start, End: target.End, Children: []*Node{target}})
			} else {
				key := p.tok
				if key.kind != tIdent && key.kind != tString && key.kind != tNumber {
					p.unexpected("expected a property name")
				}
				p.next()
				property := &Node{Kind: Property, Name: key.text, NameStart: key.start, NameEnd: key.end, Start: key.start}
				if p.eat(":") {
					property.Children = []*Node{p.parseBindingElement()}
				} else {
					property.Shorthand = true
					value := &Node{Kind: Ident, Name: key.text, Start: key.start, End: key.end, NameStart: key.start, NameEnd: key.end}
					if p.eat("=") {
						init := p.parseAssignment()
						value = &Node{Kind: DefaultPattern, Start: key.start, End: init.End, Children: []*Node{value, init}}
					}
					property.Children = []*Node{value}
				}
				property.End = p.prev.end
				node.Children = append(node.Children, property)
			}
			if !p.eat(",") {
				break
			}
		}
		p.expect("}")
		node.End = p.prev.end
		return node
	}
	p.unexpected("expected a binding name")
	return nil
}

func (p *parser) parseArray() *Node {
	start := p.expect("[").start
	node := &Node{Kind: ArrayLit, Start: start}
	for !p.is("]") {
		if p.is(",") {
			p.next()
			node.Children = append(node.Children, nil)
			continue
		}
		if p.is("...") {
			spreadStart := p.tok.start
			p.next()
			argument := p.parseAssignment()
			node.Children = append(node.Children, &Node{Kind: Spread, Start: spreadStart, End: argument.End, Children: []*Node{argument}})
		} else {
			node.Children = append(node.Children, p.parseAssignment())
		}
		if !p.eat(",") {
			break
		}
	}
	p.expect("]")
	node.End = p.prev.end
	return node
}

func (p *parser) parseObject() *Node {
	start := p.expect("{").start
	node := &Node{Kind: ObjectLit, Start: start}
	for !p.is("}") {
		node.Children = append(node.Children, p.parseProperty())
		if !p.eat(",") {
			break
		}
	}
	p.expect("}")
	node.End = p.prev.end
	return node
}

func (p *parser) parseProperty() *Node {
	start := p.tok.start
	if p.is("...") {
		p.next()
		argument := p.parseAssignment()
		return &Node{Kind: Spread, Start: start, End: argument.End, Children: []*Node{argument}}
	}
	property := &Node{Kind: Property, Start: start}
	async, generator := false, false
	if p.is("async") && !p.nextIs(":", ",", "(", "}") {
		async = true
		p.next()
	}
	if p.eat("*") {
		generator = true
	}
	if (p.is("get") || p.is("set")) && !p.nextIs(":", ",", "(", "}") {
		property.Getter, property.Setter = p.is("get"), p.is("set")
		p.next()
	}
	p.parsePropertyKey(property)
	switch {
	case p.is("("):
		property.Method = !property.Getter && !property.Setter
		function := &Node{Kind: Function, Start: p.tok.start}
		if async {
			function.Name = "async"
		}
		function.Params = p.parseParams()
		function.Children = []*Node{p.parseBlock()}
		function.End = p.prev.end
		property.Children = []*Node{function}
	case async || generator || property.Getter || property.Setter:
		p.unexpected("expected (")
	case p.eat(":"):
		property.Children = []*Node{p.parseAssignment()}
	case !property.Computed && p.isIdentifierKey(property):
		property.Shorthand = true
		value := &Node{Kind: Ident, Name: property.Name, Start: property.NameStart, End: property.NameEnd, NameStart: property.NameStart, NameEnd: property.NameEnd}
		if p.is("=") {
			// A cover initializer, valid only in a destructuring pattern.
			p.next()
			init := p.parseAssignment()
			value = &Node{Kind: Assign, Name: "=", Start: value.Start, End: init.End, Children: []*Node{value, init}}
		}
		property.Children = []*Node{value}
	default:
		p.unexpected("expected : after the property name")
	}
	property.End = p.prev.end
	return property
}

func (p *parser) isIdentifierKey(property *Node) bool {
	return property.Name != "" && identStart(property.Name[0]) && !reserved[property.Name]
}

func (p *parser) nextIs(texts ...string) bool {
	rest := strings.TrimLeft(p.src[p.tok.end:], " \t\r\n")
	for _, text := range texts {
		if strings.HasPrefix(rest, text) {
			return true
		}
	}
	return false
}

func (p *parser) parsePropertyKey(property *Node) {
	tok := p.tok
	switch tok.kind {
	case tIdent, tNumber, tPrivate:
		property.Name, property.NameStart, property.NameEnd = tok.text, tok.start, tok.end
		p.next()
	case tString:
		property.Name, property.NameStart, property.NameEnd = tok.value, tok.start, tok.end
		p.next()
	case tPunct:
		if tok.text == "[" {
			p.next()
			key := p.parseAssignment()
			p.expect("]")
			property.Computed = true
			property.NameStart, property.NameEnd = tok.start, p.prev.end
			property.Children = append(property.Children, key)
			return
		}
		p.unexpected("expected a property name")
	case tHole:
		p.fail(tok.start, tok.end, "an interpolation cannot be a property name")
	default:
		p.unexpected("expected a property name")
	}
}

// parseTemplate parses a template literal and its substitutions.
func (p *parser) parseTemplate() *Node {
	tok := p.tok
	node := &Node{Kind: TemplateLit, Start: tok.start, End: tok.end, Name: tok.text}
	for i := tok.start + 1; i < tok.end-1; i++ {
		if p.src[i] == '\\' {
			i++
			continue
		}
		if p.src[i] == '$' && p.src[i+1] == '{' {
			end := p.skipBalanced(i+1, tok.start)
			inner := &parser{src: p.src[:end-1], pos: i + 2, regexp: true, holes: p.holes}
			func() {
				defer func() {
					if r := recover(); r != nil {
						if _, ok := r.(bailout); !ok {
							panic(r)
						}
						p.err = inner.err
						panic(bailout{})
					}
				}()
				inner.next()
				expression := inner.parseExpression()
				if inner.tok.kind != tEOF {
					inner.unexpected("expected }")
				}
				node.Children = append(node.Children, expression)
			}()
			p.holes = inner.holes
			i = end - 1
		}
	}
	p.next()
	return node
}
