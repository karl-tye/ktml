// Package tailwind extracts Tailwind CSS class tokens from KTML sources. The
// one extractor serves the language server, `ktml classes` for production
// content scanning, and the workspace symbol index.
package tailwind

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

// Token is one class name and its source byte range.
type Token struct {
	Class      string
	Start, End int
	// Conditional reports a token inside a conditional attribute group, Bag
	// one inside an attribute bag literal, and Script one inside a string
	// literal of an Alpine class binding (:class, x-bind:class).
	Conditional bool
	Bag         bool
	Script      bool
}

// Dynamic is class construction that cannot be discovered statically: an
// interpolation in a class list. Partial reports an interpolation joined to
// other characters of the same class, such as text-{color}-500, which no
// scanner can complete; a whole-token interpolation can still be found where
// the presenter writes the class as a literal.
type Dynamic struct {
	Start, End int
	Path       string
	Partial    bool
}

// List is one class attribute: Start is its name, End the end of its
// closing quote.
type List struct {
	Start, End int
	Script     bool
}

// Extraction is every class token, dynamic construction, and class list of
// a source, in source order.
type Extraction struct {
	Tokens  []Token
	Dynamic []Dynamic
	Lists   []List
	// Holes are the ranges of every interpolation in a class list.
	Holes []ast.SourceRange
}

// Classes returns the distinct class names, sorted.
func (e Extraction) Classes() []string {
	var classes []string
	for _, token := range e.Tokens {
		classes = append(classes, token.Class)
	}
	slices.Sort(classes)
	return slices.Compact(classes)
}

// ListAt returns the class list whose quoted value holds offset.
func (e Extraction) ListAt(offset int) (List, bool) {
	for _, list := range e.Lists {
		if list.Start < offset && offset < list.End {
			return list, true
		}
	}
	return List{}, false
}

// TokenAt returns the token that holds or ends at offset.
func (e Extraction) TokenAt(offset int) (Token, bool) {
	for _, token := range e.Tokens {
		if token.Start <= offset && offset <= token.End {
			return token, true
		}
	}
	return Token{}, false
}

// Extract parses source and returns its class tokens. A source that does
// not parse yields what the parser recovers.
func Extract(source []byte) Extraction {
	document := parser.New(source).ParseDocument()
	extractor := &extractor{source: source}
	if document != nil {
		for _, node := range document.Nodes {
			switch value := node.(type) {
			case *ast.ElementDecl:
				extractor.nodes(value.Body)
			case *ast.RenderClause:
				extractor.nodes(value.Body)
			case *ast.StoryDecl:
				extractor.nodes(value.Body)
			}
		}
	}
	return extractor.result
}

type extractor struct {
	source []byte
	result Extraction
}

type context struct {
	conditional, bag bool
}

func (e *extractor) nodes(nodes []ast.Node) {
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.ElementNode:
			e.attributes(value.Attributes, context{})
			e.nodes(value.Children)
		case *ast.ConditionNode:
			e.nodes(value.Then)
			e.nodes(value.Else)
		case *ast.RepetitionNode:
			e.nodes(value.Body)
		case *ast.SlotNode:
			e.nodes(value.Body)
		}
	}
}

func (e *extractor) attributes(entries []*ast.AttributeNode, ctx context) {
	for _, entry := range entries {
		switch {
		case entry.Condition != nil:
			inner := ctx
			inner.conditional = true
			e.attributes(entry.Condition.Then, inner)
			e.attributes(entry.Condition.Else, inner)
		case entry.Bag != nil || entry.BagRange != nil:
			inner := ctx
			inner.bag = true
			e.attributes(entry.Bag, inner)
		case entry.Spread != nil || entry.Quoted == nil:
		default:
			switch strings.ToLower(entry.Name) {
			case "class":
				e.list(entry, ctx, false)
			case ":class", "x-bind:class":
				e.list(entry, ctx, true)
			}
		}
	}
}

// hole marks the bytes of an interpolation in a masked value.
const hole = 0

func (e *extractor) list(attribute *ast.AttributeNode, ctx context, script bool) {
	quoted := attribute.Quoted
	if quoted.E > len(e.source) || quoted.P >= quoted.E {
		return
	}
	e.result.Lists = append(e.result.Lists, List{Start: attribute.P, End: quoted.E, Script: script})
	// The masked value is the source between the quotes with every
	// interpolation replaced by hole bytes; paths records them by offset.
	start, end := quoted.P+1, quoted.E-1
	if end < start {
		return
	}
	masked := []byte(string(e.source[start:end]))
	var paths []*ast.ValueSegment
	for index := range quoted.Segments {
		segment := &quoted.Segments[index]
		if segment.Kind != ast.PathSegment {
			continue
		}
		for offset := segment.P; offset < segment.E && offset < end; offset++ {
			masked[offset-start] = hole
		}
		paths = append(paths, segment)
		e.result.Holes = append(e.result.Holes, ast.SourceRange{Start: segment.P, End: segment.E})
	}
	if script {
		// JavaScript strings use the quotes the attribute value does not.
		quotes := "'`"
		if e.source[quoted.P] == '`' {
			quotes = "'\""
		}
		e.script(masked, start, paths, ctx, quotes)
		return
	}
	e.words(masked, start, start, end, paths, ctx, false)
}

// words takes every whitespace-separated word of masked[from-base:to-base]
// as a class, or as dynamic construction when it holds an interpolation.
func (e *extractor) words(masked []byte, base, from, to int, paths []*ast.ValueSegment, ctx context, script bool) {
	for offset := from; offset < to; {
		if isSpace(masked[offset-base]) {
			offset++
			continue
		}
		wordStart := offset
		for offset < to && !isSpace(masked[offset-base]) {
			offset++
		}
		word := masked[wordStart-base : offset-base]
		if index := slices.Index(word, hole); index >= 0 {
			path := pathAt(paths, wordStart+index)
			partial := slices.ContainsFunc(word, func(b byte) bool { return b != hole })
			e.result.Dynamic = append(e.result.Dynamic, Dynamic{Start: wordStart, End: offset, Path: path, Partial: partial})
			continue
		}
		e.result.Tokens = append(e.result.Tokens, Token{
			Class:       unescape(string(word)),
			Start:       wordStart,
			End:         offset,
			Conditional: ctx.conditional,
			Bag:         ctx.bag,
			Script:      script,
		})
	}
}

// script takes the classes of the string literals in an Alpine class
// binding. Interpolations outside string literals are JavaScript operands,
// except when one is the whole binding: then the class expression itself is
// built by the presenter.
func (e *extractor) script(masked []byte, base int, paths []*ast.ValueSegment, ctx context, quotes string) {
	trimmed := strings.TrimSpace(string(masked))
	if trimmed != "" && strings.Trim(trimmed, string(rune(hole))) == "" && len(paths) == 1 {
		e.result.Dynamic = append(e.result.Dynamic, Dynamic{Start: paths[0].P, End: paths[0].E, Path: paths[0].Path.String()})
		return
	}
	for offset := 0; offset < len(masked); offset++ {
		quote := masked[offset]
		if !strings.ContainsRune(quotes, rune(quote)) {
			continue
		}
		literalStart := offset + 1
		offset = literalStart
		for offset < len(masked) && masked[offset] != quote {
			if masked[offset] == '\\' {
				offset++
			}
			offset++
		}
		literalEnd := min(offset, len(masked))
		e.words(masked, base, base+literalStart, base+literalEnd, paths, ctx, true)
	}
}

func pathAt(paths []*ast.ValueSegment, offset int) string {
	for _, segment := range paths {
		if segment.P <= offset && offset < segment.E {
			return segment.Path.String()
		}
	}
	return ""
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

// unescape resolves KTML string escapes in a class word.
func unescape(word string) string {
	if !strings.Contains(word, `\`) {
		return word
	}
	if unquoted, err := strconv.Unquote(`"` + word + `"`); err == nil {
		return unquoted
	}
	return word
}

// VirtualDocument returns the document the Tailwind language server reads
// for a KTML source: its class lists, as HTML-like attributes, with every
// other character blanked. Interpolations are blanked too. Each blanked
// character becomes as many spaces as its UTF-16 length and line breaks are
// kept, so every LSP position is the same in both documents and no mapping
// is needed in either direction.
func VirtualDocument(source []byte, extraction Extraction) string {
	keep := make([]bool, len(source))
	for _, list := range extraction.Lists {
		for offset := list.Start; offset < list.End && offset < len(source); offset++ {
			keep[offset] = true
		}
	}
	// Interpolations and the words they join are blanked: no part of a
	// dynamic class is a class the server should check.
	for _, dynamic := range extraction.Dynamic {
		for offset := dynamic.Start; offset < dynamic.End && offset < len(source); offset++ {
			keep[offset] = false
		}
	}
	for _, hole := range extraction.Holes {
		for offset := hole.Start; offset < hole.End && offset < len(source); offset++ {
			keep[offset] = false
		}
	}
	var virtual strings.Builder
	virtual.Grow(len(source))
	for offset := 0; offset < len(source); {
		r, size := utf8.DecodeRune(source[offset:])
		switch {
		case r == '\n' || r == '\r':
			virtual.WriteRune(r)
		case keep[offset]:
			virtual.Write(source[offset : offset+size])
		default:
			virtual.WriteString(strings.Repeat(" ", utf16.RuneLen(r)))
		}
		offset += size
	}
	return virtual.String()
}
