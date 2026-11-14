package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/scanner"
	"github.com/karl-tye/ktml/internal/token"
)

type ParseError struct {
	Code string
	Msg  string
	Pos  int
	End  int
}

func (e ParseError) Error() string {
	return fmt.Sprintf("%s at byte %d", e.Msg, e.Pos)
}

type Parser struct {
	s       *scanner.Scanner
	src     []byte
	pos     int
	tok     token.Token
	lit     string
	prevEnd int
	errors  []ParseError
}

type bailout struct{}

func New(src []byte) *Parser {
	s := &scanner.Scanner{}
	s.Init(src)
	p := &Parser{s: s, src: src}
	p.next()
	return p
}

func (p *Parser) next() {
	p.prevEnd = p.s.Offset()
	p.pos, p.tok, p.lit = p.s.Scan()
}

// newlineBefore reports whether a newline separates the current token from
// the previous one. A line comment ends its line.
func (p *Parser) newlineBefore() bool {
	end := p.pos
	if end > len(p.src) {
		end = len(p.src)
	}
	return p.prevEnd < end && strings.IndexByte(string(p.src[p.prevEnd:end]), '\n') >= 0
}

func (p *Parser) errorf(format string, args ...interface{}) {
	p.codeErrorf("syntax", format, args...)
}

// codeErrorf reports a syntax error with a specific diagnostic code at the
// current token and bails out to the nearest recovery point.
func (p *Parser) codeErrorf(code, format string, args ...interface{}) {
	p.record(code, p.pos, p.tokenEnd(), format, args...)
	panic(bailout{})
}

// record appends a syntax error without bailing out.
func (p *Parser) record(code string, start, end int, format string, args ...interface{}) {
	if end <= start {
		end = start + 1
	}
	p.errors = append(p.errors, ParseError{Code: code, Msg: fmt.Sprintf(format, args...), Pos: start, End: end})
}

func (p *Parser) tokenEnd() int {
	end := p.pos + len(p.lit)
	if end <= p.pos {
		end = p.pos + 1
	}
	return end
}

func (p *Parser) expect(t token.Token) string {
	if p.tok != t {
		p.errorf("expected %s, got %s", t, p.tok)
	}
	lit := p.lit
	p.next()
	return lit
}

func (p *Parser) syncTo(bodyTokens ...token.Token) {
	for p.tok != token.EOF && !tokenInSet(p.tok, bodyTokens) {
		p.next()
	}
}

func tokenInSet(t token.Token, set []token.Token) bool {
	for _, s := range set {
		if t == s {
			return true
		}
	}
	return false
}

func (p *Parser) ParseDocument() *ast.Document {
	doc := &ast.Document{P: p.pos}
	for p.tok != token.EOF {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); ok {
						p.syncTo(token.IDENT, token.IMPORT, token.EOF)
					} else {
						panic(r)
					}
				}
			}()
			if p.tok == token.IMPORT {
				doc.Nodes = append(doc.Nodes, p.parseImportStmt())
			} else if p.tok == token.IDENT && p.lit == "doctype" || p.tok == token.HTML_COMMENT {
				// Consume the node so recovery makes progress.
				p.record("syntax", p.pos, p.tokenEnd(), "%s must be inside a component body", p.describe())
				doctype := p.tok == token.IDENT
				p.next()
				if doctype && p.tok == token.IDENT && !p.newlineBefore() {
					p.next()
				}
			} else if p.tok == token.IDENT && p.lit == "record" {
				doc.Nodes = append(doc.Nodes, p.parseRecordDecl())
			} else if p.tok == token.IDENT && p.lit == "state" {
				doc.Nodes = append(doc.Nodes, p.parseStateDecl())
			} else if p.atStoryDeclaration() && p.lit == "story" {
				doc.Nodes = append(doc.Nodes, p.parseStoryDecl())
			} else if p.atStoryDeclaration() {
				doc.Nodes = append(doc.Nodes, p.parseFixtureDecl())
			} else {
				doc.Nodes = append(doc.Nodes, p.parseComponentDeclaration())
			}
		}()
	}
	doc.E = p.pos
	return doc
}

func (p *Parser) parseImportStmt() *ast.ImportStmt {
	pos := p.pos
	p.expect(token.IMPORT)
	p.expect(token.LBRACE)

	var items []string
	var specifiers []ast.ImportSpecifier
	for p.tok == token.IDENT {
		specifier := ast.ImportSpecifier{Name: p.lit, NameRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
		p.next()
		if p.tok == token.IDENT && p.lit == "as" {
			p.next()
			aliasRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
			specifier.Alias = p.expect(token.IDENT)
			specifier.AliasRange = &aliasRange
		}
		items = append(items, specifier.Name)
		specifiers = append(specifiers, specifier)
		if p.tok == token.COMMA {
			p.next()
		} else if p.tok != token.RBRACE {
			p.errorf("expected , or } in import, got %s", p.tok)
		}
	}
	p.expect(token.RBRACE)
	p.expect(token.FROM)

	path := p.lit
	pathRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	p.expect(token.STRING)

	return &ast.ImportStmt{
		P:          pos,
		E:          p.pos,
		Items:      items,
		Path:       path,
		PathRange:  pathRange,
		Specifiers: specifiers,
	}
}

// parseRecordDecl parses a Host Schema record. Document-kind placement is a
// semantic rule, so records parse in every document.
func (p *Parser) parseRecordDecl() *ast.RecordDecl {
	pos := p.pos
	record := &ast.RecordDecl{P: pos, KeywordRange: ast.SourceRange{Start: pos, End: pos + len(p.lit)}}
	p.next()
	record.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	record.Name = p.expect(token.IDENT)
	p.expect(token.LBRACK)
	record.Fields = p.parseFieldList("record field")
	record.E = p.pos + 1
	p.expect(token.RBRACK)
	return record
}

// parseFieldList parses record or payload fields up to, but not including, the
// closing ]. Recovery skips one field at a time and never crosses the start of
// the next top-level declaration or a closing brace.
func (p *Parser) parseFieldList(context string) []*ast.FieldDecl {
	var fields []*ast.FieldDecl
	for p.tok != token.RBRACK && p.tok != token.RBRACE && p.tok != token.EOF && !p.atTopLevelDeclaration() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); ok {
						for p.tok != token.EOF && p.tok != token.RBRACK && p.tok != token.RBRACE && p.tok != token.COMMA && !p.atTopLevelDeclaration() {
							p.next()
						}
						if p.tok == token.COMMA {
							p.next()
						}
					} else {
						panic(r)
					}
				}
			}()
			field := &ast.FieldDecl{P: p.pos, NameRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
			field.Name = p.expect(token.IDENT)
			if p.tok == token.QUESTION {
				field.Optional = true
				field.OptionalMarker = &ast.SourceRange{Start: p.pos, End: p.pos + 1}
				p.next()
			}
			p.expect(token.COLON)
			field.TypeExpr = p.parseType()
			field.TypeDecl = field.TypeExpr.String()
			field.TypeRange = ast.SourceRange{Start: field.TypeExpr.P, End: field.TypeExpr.E}
			field.E = field.TypeExpr.E
			fields = append(fields, field)
			if p.tok == token.COMMA {
				p.next()
			} else if p.tok != token.RBRACK {
				p.errorf("expected , or ] after %s, got %s", context, p.tok)
			}
		}()
	}
	return fields
}

// parseStateDecl parses a closed Presentation State. Like records, states
// parse in every document and their placement is semantic.
func (p *Parser) parseStateDecl() *ast.StateDecl {
	pos := p.pos
	state := &ast.StateDecl{P: pos, KeywordRange: ast.SourceRange{Start: pos, End: pos + len(p.lit)}}
	p.next()
	state.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	state.Name = p.expect(token.IDENT)
	p.expect(token.LBRACE)
	for p.tok != token.RBRACE && p.tok != token.EOF && !p.atTopLevelDeclaration() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); ok {
						for p.tok != token.EOF && p.tok != token.RBRACE && p.tok != token.COMMA && !p.atTopLevelDeclaration() {
							p.next()
						}
						if p.tok == token.COMMA {
							p.next()
						}
					} else {
						panic(r)
					}
				}
			}()
			variant := &ast.VariantDecl{P: p.pos, NameRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
			variant.Name = p.expect(token.IDENT)
			variant.E = variant.NameRange.End
			if p.tok == token.LBRACK {
				payloadStart := p.pos
				p.next()
				variant.Payload = true
				variant.Fields = p.parseFieldList("payload field")
				variant.E = p.pos + 1
				p.expect(token.RBRACK)
				variant.PayloadRange = &ast.SourceRange{Start: payloadStart, End: variant.E}
			}
			state.Variants = append(state.Variants, variant)
			if p.tok == token.COMMA {
				p.next()
			} else if p.tok != token.RBRACE {
				p.errorf("expected , or } after variant, got %s", p.tok)
			}
		}()
	}
	state.E = p.pos + 1
	p.expect(token.RBRACE)
	return state
}

// atTopLevelDeclaration reports whether a record or state body has run into
// the start of the next top-level declaration, which bounds recovery from a
// missing closing bracket. A field may itself be named record or state, so the
// keyword only counts when a declaration name follows it.
func (p *Parser) atTopLevelDeclaration() bool {
	if p.tok == token.IMPORT {
		return true
	}
	if p.tok != token.IDENT || (p.lit != "record" && p.lit != "state" && p.lit != "story" && p.lit != "fixture") {
		return false
	}
	next := p.peek(1)
	return len(next) == 1 && next[0] == token.IDENT
}

// atRepetition reports whether the current body node starts a Keyed
// Repetition. for is a keyword only when an item name and in follow it, so an
// element or attribute may still be named for.
func (p *Parser) atRepetition() bool {
	if p.tok != token.IDENT || p.lit != "for" {
		return false
	}
	scanner := *p.s
	_, itemTok, _ := scanner.Scan()
	_, inTok, inLit := scanner.Scan()
	return itemTok == token.IDENT && inTok == token.IDENT && inLit == "in"
}

// parseRepetition parses for item in collection key key { body }.
func (p *Parser) parseRepetition() *ast.RepetitionNode {
	repetition := &ast.RepetitionNode{P: p.pos, KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	p.next()
	repetition.ItemRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	repetition.Item = p.expect(token.IDENT)
	repetition.InRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	p.next()
	repetition.Collection = p.parsePath("collection")
	if p.tok != token.IDENT || p.lit != "key" {
		p.errorf("expected key after repetition collection, got %s", p.tok)
	}
	repetition.KeyKeywordRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	p.next()
	repetition.Key = p.parsePath("key")
	repetition.Body, repetition.E = p.parseBody()
	return repetition
}

// parsePath parses a bare dotted identifier path. The separators may not be
// surrounded by whitespace, so a path is one contiguous spelling.
func (p *Parser) parsePath(context string) *ast.PathExpression {
	if p.tok != token.IDENT {
		p.errorf("expected repetition %s path, got %s", context, p.tok)
	}
	return p.parseDottedPath("syntax", "repetition "+context)
}

// parseDottedPath parses a path starting at the current identifier.
func (p *Parser) parseDottedPath(code, context string) *ast.PathExpression {
	path := &ast.PathExpression{P: p.pos}
	for {
		part := ast.PathPart{P: p.pos, E: p.pos + len(p.lit), Name: p.lit}
		p.expect(token.IDENT)
		path.Parts = append(path.Parts, part)
		path.E = part.E
		if p.tok != token.PERIOD || p.pos != part.E {
			return path
		}
		p.next()
		if p.tok != token.IDENT || p.pos != part.E+1 {
			p.codeErrorf(code, "expected identifier after . in %s path", context)
		}
	}
}

// peek returns up to n tokens after the current one without consuming them.
func (p *Parser) peek(n int) []token.Token {
	scanner := *p.s
	tokens := make([]token.Token, 0, n)
	for len(tokens) < n {
		_, tok, _ := scanner.Scan()
		if tok == token.EOF {
			break
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// parseType parses a named type or a bracketed collection type.
func (p *Parser) parseType() *ast.TypeExpr {
	start := p.pos
	if p.tok == token.LBRACK {
		p.next()
		element := p.parseType()
		end := p.pos + 1
		p.expect(token.RBRACK)
		return &ast.TypeExpr{P: start, E: end, Kind: ast.CollectionTypeExpr, Element: element}
	}
	name := p.expect(token.IDENT)
	nameRange := ast.SourceRange{Start: start, End: start + len(name)}
	return &ast.TypeExpr{P: start, E: nameRange.End, Kind: ast.NamedTypeExpr, Name: name, NameRange: &nameRange}
}

// parseComponentDeclaration parses a component declaration, Component
// Signature, or Render Clause. A bracket whose first entry is name = identifier
// is a clause header; a string or number value keeps the legacy override form.
func (p *Parser) parseComponentDeclaration() ast.Node {
	if p.tok == token.IDENT {
		if next := p.peek(4); len(next) == 4 && next[0] == token.LBRACK && next[1] == token.IDENT && next[2] == token.ASSIGN && next[3] == token.IDENT {
			return p.parseRenderClause()
		}
	}
	return p.parseElementDecl()
}

// parseRenderClause parses name [selector = Pattern, binding as local] body.
func (p *Parser) parseRenderClause() *ast.RenderClause {
	clause := &ast.RenderClause{P: p.pos, NameRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	clause.Name = p.expect(token.IDENT)
	p.expect(token.LBRACK)
	clause.SelectorRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	clause.Selector = p.expect(token.IDENT)
	p.expect(token.ASSIGN)
	clause.Pattern = ast.Pattern{P: p.pos, NameRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	clause.Pattern.Name = p.expect(token.IDENT)
	clause.Pattern.E = clause.Pattern.NameRange.End
	if p.tok == token.PERIOD {
		qualifierRange := clause.Pattern.NameRange
		clause.Pattern.Qualifier, clause.Pattern.QualifierRange = clause.Pattern.Name, &qualifierRange
		p.next()
		clause.Pattern.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
		clause.Pattern.Name = p.expect(token.IDENT)
		clause.Pattern.E = clause.Pattern.NameRange.End
	}
	for p.tok == token.COMMA {
		p.next()
		binding := ast.PayloadBinding{P: p.pos, FieldRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
		binding.Field = p.expect(token.IDENT)
		binding.E = binding.FieldRange.End
		if p.tok == token.IDENT && p.lit == "as" {
			p.next()
			aliasRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
			binding.Alias = p.expect(token.IDENT)
			binding.AliasRange = &aliasRange
			binding.E = aliasRange.End
		}
		clause.Bindings = append(clause.Bindings, binding)
	}
	if p.tok != token.RBRACK {
		p.errorf("expected , or ] in render clause, got %s", p.tok)
	}
	p.next()
	clause.Body, clause.E = p.parseBody()
	return clause
}

// parseBody parses a braced declaration body and returns the end of its
// closing brace.
func (p *Parser) parseBody() ([]ast.Node, int) {
	p.expect(token.LBRACE)
	var body []ast.Node
	for p.tok != token.RBRACE && p.tok != token.EOF {
		start := p.pos
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); ok {
						if p.pos == start {
							// Always make progress past a rejected body token.
							p.next()
						}
						p.syncTo(token.IDENT, token.STRING, token.HTML_COMMENT, token.RAW_TEXT, token.RBRACE, token.EOF)
					} else {
						panic(r)
					}
				}
			}()
			body = append(body, p.parseNode())
		}()
	}
	end := p.pos + 1
	p.expect(token.RBRACE)
	return body, end
}

func (p *Parser) parseElementDecl() *ast.ElementDecl {
	pos := p.pos
	name := p.lit
	p.expect(token.IDENT)

	var attrs []*ast.AttributeDecl
	hasInputs := p.tok == token.LBRACK
	if hasInputs {
		p.next()
		for p.tok != token.RBRACK && p.tok != token.EOF {
			func() {
				defer func() {
					if r := recover(); r != nil {
						if _, ok := r.(bailout); ok {
							p.syncTo(token.IDENT, token.RBRACK, token.COMMA, token.EOF)
							if p.tok == token.COMMA {
								p.next()
							}
						} else {
							panic(r)
						}
					}
				}()
				attrPos := p.pos
				attrName := p.expect(token.IDENT)
				nameRange := ast.SourceRange{Start: attrPos, End: attrPos + len(attrName)}
				var optionalMarker *ast.SourceRange
				if p.tok == token.QUESTION {
					marker := ast.SourceRange{Start: p.pos, End: p.pos + 1}
					optionalMarker = &marker
					p.next()
				}
				var attrType, attrDefault, attrVal string
				var typeRange ast.SourceRange
				var typeExpr *ast.TypeExpr
				var defaultOperator *ast.SourceRange
				var defaultLiteral, overrideLiteral *ast.ScalarLiteral
				kind := ast.InputDeclaration
				var mode ast.InputMode
				if p.tok == token.COLON {
					mode = ast.RequiredInput
					p.next()
					typeExpr = p.parseType()
					attrType = typeExpr.String()
					typeRange = ast.SourceRange{Start: typeExpr.P, End: typeExpr.E}
					if optionalMarker != nil {
						mode = ast.OptionalInput
					}
					if p.tok == token.ASSIGN {
						if optionalMarker != nil {
							p.errorf("optional input cannot have a default")
						}
						operator := ast.SourceRange{Start: p.pos, End: p.pos + 1}
						defaultOperator = &operator
						p.next()
						defaultLiteral = p.parseScalarLiteral("default")
						attrDefault = defaultLiteral.Raw
						mode = ast.DefaultedInput
					}
				} else if p.tok == token.ASSIGN {
					if optionalMarker != nil {
						p.errorf("override declaration cannot use ?")
					}
					kind = ast.OverrideDeclaration
					p.next()
					overrideLiteral = p.parseScalarLiteral("override")
					attrVal = overrideLiteral.Raw
				} else {
					p.errorf("expected : or = in attribute declaration, got %s", p.tok)
				}
				attrEnd := typeRange.End
				if defaultLiteral != nil {
					attrEnd = defaultLiteral.E
				}
				if overrideLiteral != nil {
					attrEnd = overrideLiteral.E
				}

				attrs = append(attrs, &ast.AttributeDecl{
					P: attrPos, E: attrEnd, Kind: kind,
					Name: attrName, NameRange: nameRange,
					TypeDecl: attrType, TypeRange: typeRange, TypeExpr: typeExpr, Mode: mode,
					OptionalMarker: optionalMarker, DefaultOperator: defaultOperator,
					Default: defaultLiteral, Override: overrideLiteral,
					DefaultValue: attrDefault, Value: attrVal,
				})

				if p.tok == token.COMMA {
					p.next()
				}
			}()
		}
		headerEnd := p.pos + 1
		p.expect(token.RBRACK)
		if p.tok == token.IDENT && p.lit == "default" {
			defaultRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
			p.next()
			body, _ := p.parseBody()
			return &ast.ElementDecl{P: pos, E: p.pos, Name: name, Attributes: attrs, Body: body, DefaultRange: &defaultRange}
		}
		if p.tok != token.LBRACE {
			return &ast.ElementDecl{P: pos, E: headerEnd, Name: name, Attributes: attrs, Bodyless: true}
		}
	}

	body, _ := p.parseBody()
	return &ast.ElementDecl{
		P:          pos,
		E:          p.pos,
		Name:       name,
		Attributes: attrs,
		Body:       body,
	}
}

func (p *Parser) parseScalarLiteral(context string) *ast.ScalarLiteral {
	literal := &ast.ScalarLiteral{P: p.pos, E: p.pos + len(p.lit), Raw: p.lit}
	switch p.tok {
	case token.STRING:
		literal.Kind = ast.StringLiteral
		if !validScalarStringLiteral(p.lit) {
			p.errorf("invalid string %s value", context)
		}
		var err error
		literal.String, err = strconv.Unquote(p.lit)
		if err != nil {
			p.errorf("invalid string %s value", context)
		}
		quoted, parseError := parseQuotedValue(p.pos, p.lit)
		if parseError != nil {
			p.errors = append(p.errors, *parseError)
			panic(bailout{})
		}
		for _, segment := range quoted.Segments {
			if segment.Kind == ast.PathSegment {
				p.errorf("scalar %s string cannot contain interpolation", context)
			}
		}
	case token.NUMBER:
		literal.Kind = ast.NumberLiteral
		var err error
		literal.Number, err = strconv.ParseFloat(p.lit, 64)
		if err != nil {
			p.errorf("invalid number %s value", context)
		}
	case token.IDENT:
		literal.Kind = ast.BooleanLiteral
		if p.lit == "true" {
			literal.Boolean = true
		} else if p.lit != "false" {
			p.errorf("expected scalar %s value, got %s", context, p.lit)
		}
	default:
		p.errorf("expected scalar %s value, got %s", context, p.tok)
	}
	p.next()
	return literal
}

func validScalarStringLiteral(raw string) bool {
	if len(raw) < 2 || raw[0] == '`' {
		return true
	}
	for index := 1; index < len(raw)-1; index++ {
		if raw[index] != '\\' {
			continue
		}
		index++
		if index >= len(raw)-1 {
			return false
		}
		switch raw[index] {
		case 'b', 'f', 'n', 'r', 't', '\\', '"':
			continue
		case 'u':
			if index+4 >= len(raw)-1 {
				return false
			}
			for digitIndex := index + 1; digitIndex < index+5; digitIndex++ {
				if !isHexDigit(raw[digitIndex]) {
					return false
				}
			}
			index += 4
		default:
			return false
		}
	}
	return true
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func (p *Parser) parseNode() ast.Node {
	switch p.tok {
	case token.STRING:
		return p.parseTextNode()
	case token.HTML_COMMENT:
		comment := &ast.HTMLCommentNode{P: p.pos, E: p.pos + len(p.lit), Text: p.lit[len("<!--") : len(p.lit)-len("-->")]}
		p.next()
		return comment
	case token.RAW_TEXT:
		return p.parseRawText()
	case token.ILLEGAL:
		if strings.HasPrefix(p.lit, "<!--") {
			p.codeErrorf("unterminated-html-comment", "HTML comment is not closed; expected -->")
		}
		if strings.HasPrefix(p.lit, scanner.RawTextFence) {
			p.record("unterminated-raw-text", p.pos, p.pos+len(scanner.RawTextFence), "raw text is not closed; expected %s", scanner.RawTextFence)
			panic(bailout{})
		}
		p.errorf("unexpected token in element body: %s", p.tok)
		return nil
	case token.IDENT:
		if p.atRepetition() {
			return p.parseRepetition()
		}
		switch p.lit {
		case "doctype":
			return p.parseDoctype()
		case "if":
			return p.parseCondition()
		case "else":
			p.codeErrorf("else-without-if", "else must directly follow the body of an if")
		}
		if p.atSlotFill() {
			return p.parseSlotFill()
		}
		return p.parseElementNode()
	default:
		p.errorf("unexpected token in element body: %s", p.tok)
		return nil
	}
}

// parseRawText parses a raw-text literal.
func (p *Parser) parseRawText() *ast.RawTextNode {
	fence := len(scanner.RawTextFence)
	node := &ast.RawTextNode{P: p.pos, E: p.pos + len(p.lit), Text: p.lit[fence : len(p.lit)-fence]}
	p.next()
	return node
}

// parseDoctype parses doctype name. The name must be on the keyword's line.
func (p *Parser) parseDoctype() *ast.DoctypeNode {
	doctype := &ast.DoctypeNode{P: p.pos, KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	p.next()
	if p.tok != token.IDENT || p.newlineBefore() {
		p.record("syntax", doctype.KeywordRange.Start, doctype.KeywordRange.End, "doctype requires a name on the same line, as in doctype html")
		panic(bailout{})
	}
	doctype.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	doctype.Name, doctype.E = p.lit, doctype.NameRange.End
	p.next()
	return doctype
}

// atSlotFill reports whether the current body node is slot name { … }. slot
// is a keyword only when an identifier follows it on the same line with no
// attribute value after it, so the HTML slot element (slot name="x") and the
// slot attribute keep working.
func (p *Parser) atSlotFill() bool {
	if p.tok != token.IDENT || p.lit != "slot" {
		return false
	}
	scanner := *p.s
	keywordEnd := scanner.Offset()
	namePos, nameTok, nameLit := scanner.Scan()
	if nameTok != token.IDENT || strings.IndexByte(string(p.src[keywordEnd:namePos]), '\n') >= 0 || p.nameContinuesAt(namePos+len(nameLit)) {
		return false
	}
	_, after, _ := scanner.Scan()
	return after != token.ASSIGN && after != token.STRING && after != token.NUMBER
}

// parseSlotFill parses slot name { body }.
func (p *Parser) parseSlotFill() *ast.SlotNode {
	slot := &ast.SlotNode{P: p.pos, KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	p.next()
	slot.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	slot.Name = p.expect(token.IDENT)
	if p.tok != token.LBRACE || p.newlineBefore() {
		p.record("slot-fill-body", slot.P, slot.NameRange.End, "slot fill %s requires a braced body on the same line", slot.Name)
		panic(bailout{})
	}
	slot.Body, slot.E = p.parseBody()
	return slot
}

// parseCondition parses if condition { then } [else { … } | else if …].
func (p *Parser) parseCondition() *ast.ConditionNode {
	node := &ast.ConditionNode{P: p.pos, KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	p.next()
	if p.tok == token.LBRACE {
		p.codeErrorf("condition-expected", "if requires a condition")
	}
	node.Condition = p.parseOrCondition()
	if p.tok != token.LBRACE {
		p.rejectConditionToken()
		p.codeErrorf("condition-unexpected", "unexpected %s in condition; expected { to start the if body", p.describe())
	}
	node.Then, node.E = p.parseBody()
	if p.tok == token.IDENT && p.lit == "else" {
		elseRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
		node.ElseKeywordRange = &elseRange
		p.next()
		if p.tok == token.IDENT && p.lit == "if" {
			nested := p.parseCondition()
			node.ElseIf, node.Else, node.E = true, []ast.Node{nested}, nested.E
			return node
		}
		if p.tok != token.LBRACE {
			p.codeErrorf("syntax", "expected { or if after else, got %s", p.describe())
		}
		node.Else, node.E = p.parseBody()
	}
	return node
}

func (p *Parser) parseOrCondition() *ast.ConditionExpr {
	first := p.parseAndCondition()
	if p.tok != token.LOR {
		return first
	}
	or := &ast.ConditionExpr{P: first.P, Kind: ast.OrCondition, Operands: []*ast.ConditionExpr{first}}
	for p.tok == token.LOR {
		or.OperatorRanges = append(or.OperatorRanges, ast.SourceRange{Start: p.pos, End: p.pos + 2})
		p.next()
		or.Operands = append(or.Operands, p.parseAndCondition())
	}
	or.E = or.Operands[len(or.Operands)-1].E
	return or
}

func (p *Parser) parseAndCondition() *ast.ConditionExpr {
	first := p.parseUnaryCondition()
	if p.tok != token.LAND {
		return first
	}
	and := &ast.ConditionExpr{P: first.P, Kind: ast.AndCondition, Operands: []*ast.ConditionExpr{first}}
	for p.tok == token.LAND {
		and.OperatorRanges = append(and.OperatorRanges, ast.SourceRange{Start: p.pos, End: p.pos + 2})
		p.next()
		and.Operands = append(and.Operands, p.parseUnaryCondition())
	}
	and.E = and.Operands[len(and.Operands)-1].E
	return and
}

func (p *Parser) parseUnaryCondition() *ast.ConditionExpr {
	if p.tok != token.NOT {
		return p.parsePrimaryCondition()
	}
	operator := ast.SourceRange{Start: p.pos, End: p.pos + 1}
	p.next()
	if p.tok == token.NOT {
		p.record("condition-double-negation", operator.Start, p.pos+1, "!! is not a condition; ! applies to one operand")
		panic(bailout{})
	}
	operand := p.parsePrimaryCondition()
	return &ast.ConditionExpr{P: operator.Start, E: operand.E, Kind: ast.NotCondition, OperatorRange: &operator, Operands: []*ast.ConditionExpr{operand}}
}

func (p *Parser) parsePrimaryCondition() *ast.ConditionExpr {
	switch p.tok {
	case token.LPAREN:
		start := p.pos
		p.next()
		inner := p.parseOrCondition()
		if p.tok != token.RPAREN {
			p.rejectConditionToken()
			p.codeErrorf("condition-unexpected", "unexpected %s in condition; expected )", p.describe())
		}
		end := p.pos + 1
		p.next()
		group := &ast.ConditionExpr{P: start, E: end, Kind: ast.GroupCondition, Operands: []*ast.ConditionExpr{inner}}
		p.rejectConditionToken()
		return group
	case token.IDENT:
		if p.lit == "true" || p.lit == "false" {
			p.codeErrorf("condition-literal", "literal %s is not a condition; use a boolean input or field", p.lit)
		}
		path := p.parseDottedPath("condition-unexpected", "condition")
		if p.tok == token.QUESTION && p.pos == path.E {
			operator := ast.SourceRange{Start: p.pos, End: p.pos + 1}
			p.next()
			presence := &ast.ConditionExpr{P: path.P, E: operator.End, Kind: ast.PresenceCondition, Path: path, OperatorRange: &operator}
			p.rejectConditionToken()
			return presence
		}
		p.rejectConditionToken()
		return &ast.ConditionExpr{P: path.P, E: path.E, Kind: ast.PathCondition, Path: path}
	case token.STRING, token.NUMBER:
		p.codeErrorf("condition-literal", "literal %s is not a condition; Go derives conditions into booleans or optionals", p.lit)
	}
	p.codeErrorf("condition-expected", "expected a condition, got %s", p.describe())
	return nil
}

// rejectConditionToken reports the precise error for a token that cannot
// follow a condition operand.
func (p *Parser) rejectConditionToken() {
	switch p.tok {
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		p.codeErrorf("condition-comparison", "comparison %s is not a condition; derive a boolean in Go", p.tok)
	case token.ASSIGN:
		p.codeErrorf("condition-assignment", "= is not a condition; conditions cannot assign or compare")
	case token.AMP:
		p.codeErrorf("condition-operator", "& is not a condition operator; use &&")
	case token.OR:
		p.codeErrorf("condition-operator", "| is not a condition operator; use ||")
	case token.LPAREN:
		p.codeErrorf("condition-call", "calls are not conditions; derive a boolean in Go")
	case token.LBRACK:
		p.codeErrorf("condition-index", "indexing is not a condition; derive a boolean in Go")
	case token.QUESTION:
		p.codeErrorf("condition-unexpected", "? must directly follow a path")
	case token.NOT:
		p.codeErrorf("condition-unexpected", "! must start an operand")
	}
}

func (p *Parser) describe() string {
	if p.lit != "" {
		return p.lit
	}
	return p.tok.String()
}

func (p *Parser) parseTextNode() *ast.TextNode {
	pos := p.pos
	val := p.lit
	quoted := p.quotedValue(pos, val)
	p.expect(token.STRING)
	return &ast.TextNode{
		P:      pos,
		E:      p.prevEnd,
		Value:  val,
		Quoted: quoted,
	}
}

func (p *Parser) quotedValue(pos int, raw string) *ast.QuotedValue {
	quoted, parseError := parseQuotedValue(pos, raw)
	if parseError != nil {
		p.errors = append(p.errors, *parseError)
	}
	return quoted
}

func parseQuotedValue(pos int, raw string) (*ast.QuotedValue, *ParseError) {
	quoted := &ast.QuotedValue{P: pos, E: pos + len(raw)}
	if len(raw) < 2 {
		return quoted, nil
	}
	content := raw[1 : len(raw)-1]
	contentPos := pos + 1
	if raw[0] == '`' {
		quoted.Segments = append(quoted.Segments, literalValueSegment(raw[0], content, contentPos))
		return quoted, nil
	}
	literalStart := 0
	for offset := 0; offset < len(content); {
		if content[offset] == '}' {
			return quoted, interpolationError("unmatched } in quoted value", contentPos+offset, contentPos+offset+1)
		}
		if content[offset] != '{' {
			offset++
			continue
		}
		closeOffset := strings.IndexByte(content[offset+1:], '}')
		if closeOffset < 0 {
			return quoted, interpolationError("unclosed interpolation path", contentPos+offset, contentPos+len(content))
		}
		closeOffset += offset + 1
		if offset+1 < closeOffset && (content[offset+1] == ' ' || content[offset+1] == '\t') {
			offset = closeOffset + 1
			continue
		}
		if offset > literalStart {
			quoted.Segments = append(quoted.Segments, literalValueSegment(raw[0], content[literalStart:offset], contentPos+literalStart))
		}
		if nested := strings.IndexByte(content[offset+1:closeOffset], '{'); nested >= 0 {
			return quoted, interpolationError("nested { in interpolation path", contentPos+offset, contentPos+closeOffset+1)
		}
		path := &ast.PathExpression{P: contentPos + offset, E: contentPos + closeOffset + 1}
		partStart := offset + 1
		for partStart <= closeOffset {
			partEnd := partStart
			for partEnd < closeOffset && content[partEnd] != '.' {
				partEnd++
			}
			part := content[partStart:partEnd]
			if !validPathIdentifier(part) {
				return quoted, interpolationError("interpolation must contain only an identifier path", path.P, path.E)
			}
			path.Parts = append(path.Parts, ast.PathPart{P: contentPos + partStart, E: contentPos + partEnd, Name: part})
			partStart = partEnd + 1
		}
		quoted.Segments = append(quoted.Segments, ast.ValueSegment{P: path.P, E: path.E, Kind: ast.PathSegment, Path: path})
		offset = closeOffset + 1
		literalStart = offset
	}
	if literalStart < len(content) {
		quoted.Segments = append(quoted.Segments, literalValueSegment(raw[0], content[literalStart:], contentPos+literalStart))
	}
	return quoted, nil
}

func interpolationError(message string, start, end int) *ParseError {
	return &ParseError{Code: "invalid-interpolation", Msg: message, Pos: start, End: end}
}

func validPathIdentifier(value string) bool {
	if value == "" || !isIdentifierStart(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		if !isIdentifierStart(value[i]) && (value[i] < '0' || value[i] > '9') && value[i] != '-' {
			return false
		}
	}
	return true
}

func isIdentifierStart(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value == '_'
}

func literalValueSegment(quote byte, raw string, pos int) ast.ValueSegment {
	literal := raw
	if unquoted, err := strconv.Unquote(string(quote) + raw + string(quote)); err == nil {
		literal = unquoted
	}
	return ast.ValueSegment{P: pos, E: pos + len(raw), Kind: ast.LiteralSegment, Literal: literal}
}

func (p *Parser) parseElementNode() *ast.ElementNode {
	element := &ast.ElementNode{P: p.pos, Name: p.lit}
	p.expect(token.IDENT)
	element.E = p.prevEnd

	if p.tok == token.LPAREN && !p.newlineBefore() {
		group := ast.SourceRange{Start: p.pos}
		p.next()
		for p.tok != token.RPAREN && p.tok != token.LBRACE && p.tok != token.RBRACE && p.tok != token.EOF {
			p.parseAttribute(element, true)
		}
		if p.tok != token.RPAREN {
			p.record("syntax", group.Start, group.Start+1, "unclosed attribute list of %s; expected )", element.Name)
			panic(bailout{})
		}
		group.End = p.pos + 1
		p.next()
		element.E = group.End
		element.Multiline, element.AttributeGroup = true, &group
	} else {
		for !p.newlineBefore() && p.atSameLineAttribute() {
			if (p.tok == token.COLON || p.tok == token.AT || p.tok == token.PERIOD) && p.pos == p.prevEnd {
				end := scanner.AttributeNameEnd(p.src, p.pos)
				if end == p.pos {
					end = p.pos + 1
				}
				p.record("colon-chain", p.pos, end, "%s cannot directly follow %s: colon chains and dotted shorthand are not KTML; separate the attribute with a space", p.src[p.pos:end], p.src[element.P:p.pos])
				panic(bailout{})
			}
			p.parseAttribute(element, false)
		}
		if p.tok == token.LPAREN && !p.newlineBefore() {
			p.record("mixed-attribute-list", p.pos, p.pos+1, "a parenthesized attribute list must directly follow the element name %s", element.Name)
			panic(bailout{})
		}
	}

	switch {
	case p.tok == token.LBRACE && p.newlineBefore():
		// A newline already ended the element. Keep the body as its children
		// so recovery does not close the enclosing body early.
		p.record("detached-element-body", p.pos, p.pos+1, "the body of %s must start on the same line as the element; a newline ended its attribute list", element.Name)
		element.Children, element.E = p.parseBody()
	case p.tok == token.LBRACE:
		element.Children, element.E = p.parseBody()
	case p.tok == token.STRING && !p.newlineBefore():
		text := p.parseTextNode()
		element.Children = append(element.Children, text)
		element.E = text.E
	case p.tok == token.RAW_TEXT && !p.newlineBefore():
		text := p.parseRawText()
		element.Children = append(element.Children, text)
		element.E = text.E
	}
	return element
}

// atSameLineAttribute reports whether the current token can start an
// attribute on a header line. An if or for directly followed by : or . is an
// attribute name, not a keyword.
func (p *Parser) atSameLineAttribute() bool {
	switch p.tok {
	case token.HASH, token.COLON, token.AT:
		return true
	case token.PERIOD:
		return p.pos == p.prevEnd || p.atSpread()
	case token.IDENT:
		return p.nameContinuesAt(p.pos+len(p.lit)) || !p.atRepetition() && !p.atInlineCondition()
	}
	return false
}

// nameContinuesAt reports whether an attribute name continues past an
// identifier ending at offset.
func (p *Parser) nameContinuesAt(offset int) bool {
	return offset < len(p.src) && (p.src[offset] == ':' || p.src[offset] == '.' || p.src[offset] == '*')
}

// attributeName consumes an attribute name (see scanner.AttributeNameEnd)
// starting at the current token, which is an identifier, @, or :.
func (p *Parser) attributeName() string {
	start := p.pos
	end := scanner.AttributeNameEnd(p.src, start)
	if end == start {
		p.record("invalid-attribute-name", start, start+1, "%s must be followed by an attribute name", p.tok)
		panic(bailout{})
	}
	p.s.Seek(end)
	p.next()
	return string(p.src[start:end])
}

// parseAttribute parses one attribute list entry of element. Inside a
// continuation group, newlines are whitespace; otherwise a value must be on
// the attribute's line.
func (p *Parser) parseAttribute(element *ast.ElementNode, grouped bool) {
	if entry := p.parseAttributeEntry(element.Name, grouped); entry != nil {
		element.Attributes = append(element.Attributes, entry)
		element.E = entry.E
	}
}

// parseAttributeEntry parses one attribute, or, inside a continuation group,
// a conditional attribute group. It returns nil after recovering from an
// error.
func (p *Parser) parseAttributeEntry(owner string, grouped bool) (entry *ast.AttributeNode) {
	start := p.pos
	conditional := grouped && p.atAttributeCondition()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); ok {
				if conditional {
					p.skipAttributeCondition()
					entry = nil
					return
				}
				if p.pos == start {
					p.next()
				}
				p.syncTo(token.IDENT, token.HASH, token.RPAREN, token.LBRACE, token.RBRACE, token.STRING, token.NUMBER, token.EOF)
				if !grouped && (p.tok == token.STRING || p.tok == token.NUMBER) {
					p.next()
				}
				entry = nil
			} else {
				panic(r)
			}
		}
	}()
	attrPos := p.pos
	if p.tok == token.HASH {
		p.next()
		idName := p.lit
		p.expect(token.IDENT)
		return &ast.AttributeNode{P: attrPos, E: p.prevEnd, Name: "id", Value: idName}
	}
	if grouped && p.atAttributeCondition() {
		return p.parseAttributeCondition(owner)
	}
	if p.atSpread() {
		return p.parseSpread()
	}
	if p.tok != token.IDENT && p.tok != token.COLON && p.tok != token.AT {
		p.errorf("expected attribute or ) in the attribute list of %s, got %s", owner, p.describe())
	}
	attribute := &ast.AttributeNode{P: attrPos, Name: p.attributeName()}
	switch {
	case p.tok == token.ASSIGN:
		p.next()
		if p.tok == token.LPAREN {
			attribute.Bag, attribute.BagRange = p.parseAttributeBag(owner)
			attribute.E = p.prevEnd
			return attribute
		}
		if p.tok == token.STRING {
			attribute.Quoted = p.quotedValue(p.pos, p.lit)
		}
		if p.tok != token.STRING && p.tok != token.IDENT && p.tok != token.NUMBER {
			p.errorf("expected attribute value, got %s", p.tok)
		}
		attribute.Value = p.lit
		p.next()
	case (p.tok == token.STRING || p.tok == token.NUMBER) && (grouped || !p.newlineBefore()):
		if p.tok == token.STRING {
			attribute.Quoted = p.quotedValue(p.pos, p.lit)
		}
		attribute.Value = p.lit
		p.next()
	}
	attribute.E = p.prevEnd
	return attribute
}

// atAttributeCondition reports whether an if inside a continuation group
// starts a conditional attribute group. As in a body, an attribute may still
// be named if when = follows it, or when its name continues with : or .
func (p *Parser) atAttributeCondition() bool {
	if p.tok != token.IDENT || p.lit != "if" || p.nameContinuesAt(p.pos+len(p.lit)) {
		return false
	}
	next := p.peek(1)
	return len(next) == 0 || next[0] != token.ASSIGN
}

// parseAttributeCondition parses if condition { attributes } [else { … } |
// else if …] inside a continuation group.
func (p *Parser) parseAttributeCondition(owner string) *ast.AttributeNode {
	entry := &ast.AttributeNode{P: p.pos}
	condition := &ast.AttributeCondition{KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	entry.Condition = condition
	p.next()
	if p.tok == token.LBRACE {
		p.codeErrorf("condition-expected", "if requires a condition")
	}
	condition.Condition = p.parseOrCondition()
	if p.tok != token.LBRACE {
		p.rejectConditionToken()
		p.codeErrorf("condition-unexpected", "unexpected %s in condition; expected { to start the conditional attributes", p.describe())
	}
	condition.Then, entry.E = p.parseAttributeBlock(owner)
	if p.tok == token.IDENT && p.lit == "else" {
		elseRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
		condition.ElseKeywordRange = &elseRange
		p.next()
		if p.tok == token.IDENT && p.lit == "if" {
			nested := p.parseAttributeCondition(owner)
			condition.ElseIf, condition.Else, entry.E = true, []*ast.AttributeNode{nested}, nested.E
			return entry
		}
		if p.tok != token.LBRACE {
			p.codeErrorf("syntax", "expected { or if after else, got %s", p.describe())
		}
		condition.Else, entry.E = p.parseAttributeBlock(owner)
	}
	return entry
}

// atSpread reports whether the current token starts an attribute spread,
// ...path, with no space inside it.
func (p *Parser) atSpread() bool {
	return p.tok == token.PERIOD && strings.HasPrefix(string(p.src[p.pos:min(len(p.src), p.pos+3)]), "...")
}

// parseSpread parses ...path.
func (p *Parser) parseSpread() *ast.AttributeNode {
	start := p.pos
	p.s.Seek(start + 3)
	p.next()
	if p.tok != token.IDENT || p.pos != start+3 {
		p.record("invalid-spread", start, start+3, "... must be directly followed by the path of an Attributes value")
		panic(bailout{})
	}
	path := p.parseDottedPath("invalid-spread", "spread")
	return &ast.AttributeNode{P: start, E: path.E, Spread: &ast.AttributeSpread{OperatorRange: ast.SourceRange{Start: start, End: start + 3}, Path: path}}
}

// parseAttributeBag parses the entries of an attribute bag literal,
// ( entries ), in which newlines are whitespace.
func (p *Parser) parseAttributeBag(owner string) ([]*ast.AttributeNode, *ast.SourceRange) {
	bagRange := &ast.SourceRange{Start: p.pos}
	p.next()
	entries := []*ast.AttributeNode{}
	for p.tok != token.RPAREN && p.tok != token.LBRACE && p.tok != token.RBRACE && p.tok != token.EOF {
		before := p.pos
		if entry := p.parseAttributeEntry(owner, true); entry != nil {
			entries = append(entries, entry)
		}
		if p.pos == before {
			break
		}
	}
	if p.tok != token.RPAREN {
		p.record("syntax", bagRange.Start, bagRange.Start+1, "unclosed attribute bag; expected )")
		panic(bailout{})
	}
	bagRange.End = p.pos + 1
	p.next()
	return entries, bagRange
}

// skipAttributeCondition recovers from an error in a conditional attribute
// group by skipping the rest of the group: its braced branches and else
// keywords, never past the end of the attribute list.
func (p *Parser) skipAttributeCondition() {
	for {
		for p.tok != token.LBRACE && p.tok != token.RPAREN && p.tok != token.RBRACE && p.tok != token.EOF {
			p.next()
		}
		if p.tok != token.LBRACE {
			return
		}
		for depth := 0; p.tok != token.EOF; {
			if p.tok == token.LBRACE {
				depth++
			} else if p.tok == token.RBRACE {
				depth--
			}
			p.next()
			if depth == 0 {
				break
			}
		}
		if p.tok != token.IDENT || p.lit != "else" {
			return
		}
		p.next()
	}
}

// parseAttributeBlock parses the braced attributes of one conditional branch
// and returns the end of its closing brace.
func (p *Parser) parseAttributeBlock(owner string) ([]*ast.AttributeNode, int) {
	p.expect(token.LBRACE)
	var entries []*ast.AttributeNode
	for p.tok != token.RBRACE && p.tok != token.RPAREN && p.tok != token.EOF {
		before := p.pos
		if entry := p.parseAttributeEntry(owner, true); entry != nil {
			entries = append(entries, entry)
		}
		if p.pos == before {
			break
		}
	}
	end := p.pos + 1
	p.expect(token.RBRACE)
	return entries, end
}

// atInlineCondition reports whether an if in attribute position starts a
// sibling condition. An attribute may still be named if when = follows it.
func (p *Parser) atInlineCondition() bool {
	if p.tok != token.IDENT || p.lit != "if" {
		return false
	}
	next := p.peek(1)
	return len(next) == 0 || next[0] != token.ASSIGN
}

func (p *Parser) Errors() []ParseError {
	return p.errors
}

func (p *Parser) Err() error {
	if len(p.errors) == 0 {
		return nil
	}
	if len(p.errors) == 1 {
		return p.errors[0]
	}
	return fmt.Errorf("%s (and %d more errors)", p.errors[0], len(p.errors)-1)
}

func (p *Parser) Pos() int {
	return p.pos
}
