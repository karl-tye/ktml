package parser

import (
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/token"
)

// atStoryDeclaration reports whether the current top-level token starts a
// story or fixture declaration: the keyword followed by a name. Document-kind
// placement is a semantic rule, so both parse in every Document.
func (p *Parser) atStoryDeclaration() bool {
	if p.tok != token.IDENT || (p.lit != "story" && p.lit != "fixture") {
		return false
	}
	next := p.peek(1)
	return len(next) == 1 && next[0] == token.IDENT
}

// parseFixtureDecl parses fixture name: Type = scalar-literal.
func (p *Parser) parseFixtureDecl() *ast.FixtureDecl {
	fixture := &ast.FixtureDecl{P: p.pos, KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	p.next()
	fixture.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	fixture.Name = p.expect(token.IDENT)
	p.expect(token.COLON)
	fixture.TypeExpr = p.parseType()
	fixture.TypeDecl = fixture.TypeExpr.String()
	fixture.TypeRange = ast.SourceRange{Start: fixture.TypeExpr.P, End: fixture.TypeExpr.E}
	p.expect(token.ASSIGN)
	fixture.Value = p.parseScalarLiteral("fixture")
	fixture.E = fixture.Value.E
	return fixture
}

// parseStoryDecl parses story Component.Name [input = value, …] { content }.
func (p *Parser) parseStoryDecl() *ast.StoryDecl {
	story := &ast.StoryDecl{P: p.pos, KeywordRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	p.next()
	story.ComponentRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	story.Component = p.expect(token.IDENT)
	p.expect(token.PERIOD)
	story.NameRange = ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
	story.Name = p.expect(token.IDENT)
	story.E = story.NameRange.End
	if p.tok == token.LBRACK {
		start := p.pos
		p.next()
		story.Arguments = p.parseValueArguments("story input")
		story.E = p.pos + 1
		p.expect(token.RBRACK)
		story.ArgumentsRange = &ast.SourceRange{Start: start, End: story.E}
	}
	if p.tok == token.LBRACE {
		start := p.pos
		story.Body, story.E = p.parseBody()
		story.BodyRange = &ast.SourceRange{Start: start, End: story.E}
	}
	return story
}

// parseValueArguments parses name = value entries up to, but not including,
// the closing ]. Recovery skips one entry at a time.
func (p *Parser) parseValueArguments(context string) []*ast.ValueArgument {
	var arguments []*ast.ValueArgument
	for p.tok != token.RBRACK && p.tok != token.RBRACE && p.tok != token.EOF && !p.atTopLevelDeclaration() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); ok {
						p.skipValueEntry()
					} else {
						panic(r)
					}
				}
			}()
			arguments = append(arguments, p.parseValueArgument(context))
			if p.tok == token.COMMA {
				p.next()
			} else if p.tok != token.RBRACK {
				p.errorf("expected , or ] after %s, got %s", context, p.tok)
			}
		}()
	}
	return arguments
}

func (p *Parser) parseValueArgument(context string) *ast.ValueArgument {
	argument := &ast.ValueArgument{P: p.pos, NameRange: ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}}
	argument.Name = p.expect(token.IDENT)
	if p.tok != token.ASSIGN {
		p.errorf("expected = after %s name, got %s", context, p.tok)
	}
	p.next()
	argument.Value = p.parseValue()
	argument.E = argument.Value.E
	return argument
}

// skipValueEntry skips to the next entry of a bracketed value list, over
// nested brackets.
func (p *Parser) skipValueEntry() {
	depth := 0
	for p.tok != token.EOF && !p.atTopLevelDeclaration() {
		switch p.tok {
		case token.LBRACK:
			depth++
		case token.RBRACK, token.RBRACE:
			if depth == 0 {
				return
			}
			depth--
		case token.COMMA:
			if depth == 0 {
				p.next()
				return
			}
		}
		p.next()
	}
}

// parseValue parses a story value literal. A bracket is a record literal when
// its first entry is name =, a collection otherwise, and [] is either.
func (p *Parser) parseValue() *ast.ValueExpr {
	switch p.tok {
	case token.STRING, token.NUMBER:
		literal := p.parseScalarLiteral("story value")
		return &ast.ValueExpr{P: literal.P, E: literal.E, Kind: ast.ScalarValue, Scalar: literal}
	case token.IDENT:
		if p.lit == "true" || p.lit == "false" {
			literal := p.parseScalarLiteral("story value")
			return &ast.ValueExpr{P: literal.P, E: literal.E, Kind: ast.ScalarValue, Scalar: literal}
		}
		nameRange := ast.SourceRange{Start: p.pos, End: p.pos + len(p.lit)}
		value := &ast.ValueExpr{P: p.pos, E: nameRange.End, Kind: ast.NameValue, Name: p.lit, NameRange: &nameRange}
		p.next()
		if p.tok == token.LBRACK && !p.newlineBefore() {
			start := p.pos
			p.next()
			value.Fields = p.parseValueArguments("payload field")
			value.E = p.pos + 1
			p.expect(token.RBRACK)
			value.PayloadRange = &ast.SourceRange{Start: start, End: value.E}
		}
		return value
	case token.LBRACK:
		value := &ast.ValueExpr{P: p.pos}
		p.next()
		if next := p.peek(1); p.tok == token.IDENT && len(next) == 1 && next[0] == token.ASSIGN {
			value.Kind = ast.FieldsValue
			value.Fields = p.parseValueArguments("record field")
		} else if p.tok == token.RBRACK {
			value.Kind = ast.EmptyValue
		} else {
			value.Kind = ast.ListValue
			value.Items = p.parseValueItems()
		}
		value.E = p.pos + 1
		p.expect(token.RBRACK)
		return value
	}
	p.errorf("expected a story value, got %s", p.describe())
	return nil
}

// parseValueItems parses collection items up to, but not including, the
// closing ].
func (p *Parser) parseValueItems() []*ast.ValueExpr {
	var items []*ast.ValueExpr
	for p.tok != token.RBRACK && p.tok != token.RBRACE && p.tok != token.EOF && !p.atTopLevelDeclaration() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); ok {
						p.skipValueEntry()
					} else {
						panic(r)
					}
				}
			}()
			items = append(items, p.parseValue())
			if p.tok == token.COMMA {
				p.next()
			} else if p.tok != token.RBRACK {
				p.errorf("expected , or ] after collection item, got %s", p.tok)
			}
		}()
	}
	return items
}
