package compiler

import (
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
	"github.com/karl-tye/ktml/internal/scanner"
	"github.com/karl-tye/ktml/internal/token"
)

const (
	// FixtureCompletion is a story fixture offered as a trusted value.
	FixtureCompletion CompletionKind = "fixture"
	// ValueCompletion is a literal value: None, true, or false.
	ValueCompletion CompletionKind = "value"
)

// storyFrame is one open bracket of a story's value list: the inputs of the
// story, a record's or payload's fields, or a collection's items.
type storyFrame struct {
	list    bool
	fields  []namedType
	element Type
	given   map[string]bool
	// name is the field the next value is for; value reports a value
	// position.
	name  string
	value bool
	// payload holds the fields of a Variant just named, which a following
	// [ opens.
	payload []namedType
}

type scannedToken struct {
	pos int
	tok token.Token
	lit string
}

type namedType struct {
	name     string
	typ      Type
	optional bool
	detail   string
}

// StoryCompletions returns the completions at offset of a story Document's
// source: component names after story, input and field names inside a value
// list, and Variants, fixtures, None, and booleans at a value. It reports
// false when offset is not in a story header or value list, so the caller
// offers its other completions (for example in a story's content body).
func (s *Snapshot) StoryCompletions(file string, source []byte, offset int) (CompletionList, bool) {
	items, ok := s.storyCompletions(file, source, offset)
	start := offset
	for start > 0 && isWordByte(source[start-1]) {
		start--
	}
	return CompletionList{Start: start, Items: items}, ok
}

func (s *Snapshot) storyCompletions(file string, source []byte, offset int) ([]Completion, bool) {
	if s == nil || !IsStoryPath(file) || offset > len(source) {
		return nil, false
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil, false
	}
	var sc scanner.Scanner
	sc.Init(source[:offset])
	var tokens []scannedToken
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			continue
		}
		tokens = append(tokens, scannedToken{pos, tok, lit})
	}
	// Find the last story keyword at the start of a declaration.
	start := -1
	for index := len(tokens) - 1; index >= 0; index-- {
		if tokens[index].tok == token.IDENT && tokens[index].lit == "story" && (index == 0 || atDeclarationStart(source, tokens[index].pos)) {
			start = index
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	rest := tokens[start+1:]
	// story | story Name | story Name. | story Name.Story
	if len(rest) == 0 || len(rest) == 1 && rest[0].tok == token.IDENT && rest[0].pos+len(rest[0].lit) == offset {
		scope, _ := s.storyScope(path, source)
		return componentCompletions(scope), true
	}
	if rest[0].tok != token.IDENT {
		return nil, false
	}
	scope, fixtures := s.storyScope(path, source)
	component, ok := scope[rest[0].lit]
	if !ok {
		return nil, false
	}
	index := 1
	for index < len(rest) && (rest[index].tok == token.PERIOD || rest[index].tok == token.IDENT && index == 2) {
		index++
	}
	if index >= len(rest) || rest[index].tok != token.LBRACK {
		return nil, false
	}
	var inputs []namedType
	for _, input := range component.Inputs {
		if input.IsContent() {
			continue
		}
		detail := input.Type.String()
		switch input.Mode {
		case OptionalInput:
			detail += "?"
		case DefaultedInput:
			if input.Default != nil {
				detail += " = " + input.Default.Raw
			}
		}
		inputs = append(inputs, namedType{name: input.Name, typ: input.Type, optional: input.Mode != RequiredInput, detail: detail})
	}
	stack := []*storyFrame{{fields: inputs, given: make(map[string]bool)}}
	for _, current := range rest[index+1:] {
		if len(stack) == 0 {
			return nil, false
		}
		frame := stack[len(stack)-1]
		switch current.tok {
		case token.IDENT:
			switch {
			case !frame.list && !frame.value:
				frame.name = current.lit
				frame.given[current.lit] = true
			default:
				// A Variant name: a following [ opens its payload.
				frame.payload = nil
				if target := frame.valueType(); target != nil && target.typ.Kind == StateTypeKind {
					variant, _ := s.states[target.typ.State].Variant(current.lit)
					frame.payload = []namedType{}
					for _, field := range variant.Fields {
						frame.payload = append(frame.payload, fieldEntry(field))
					}
				}
			}
		case token.ASSIGN:
			frame.value = true
		case token.COMMA:
			if !frame.list {
				frame.value, frame.name = false, ""
			}
			frame.payload = nil
		case token.LBRACK:
			if frame.payload != nil {
				stack = append(stack, &storyFrame{fields: frame.payload, given: make(map[string]bool)})
				frame.payload = nil
				continue
			}
			target := frame.valueType()
			if target == nil {
				stack = append(stack, &storyFrame{given: make(map[string]bool)})
				continue
			}
			switch target.typ.Kind {
			case CollectionTypeKind:
				stack = append(stack, &storyFrame{list: true, element: *target.typ.Element, value: true, given: make(map[string]bool)})
			case RecordTypeKind:
				record := s.records[target.typ.Record]
				fields := make([]namedType, 0, len(record.Fields))
				for _, field := range record.Fields {
					fields = append(fields, fieldEntry(field))
				}
				stack = append(stack, &storyFrame{fields: fields, given: make(map[string]bool)})
			default:
				stack = append(stack, &storyFrame{given: make(map[string]bool)})
			}
		case token.RBRACK:
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) == 0 {
		return nil, false
	}
	frame := stack[len(stack)-1]
	partial := len(rest) > 0 && rest[len(rest)-1].tok == token.IDENT && rest[len(rest)-1].pos+len(rest[len(rest)-1].lit) == offset
	if !frame.list && !frame.value || !frame.list && frame.value && frame.name != "" && partial && !afterAssign(rest) {
		var items []Completion
		for _, entry := range frame.fields {
			if frame.given[entry.name] && !(partial && entry.name == frame.name) {
				continue
			}
			kind := FieldCompletion
			if len(stack) == 1 {
				kind = InputCompletion
			}
			items = append(items, Completion{Label: entry.name, Kind: kind, Detail: entry.detail})
		}
		return items, true
	}
	target := frame.valueType()
	if target == nil {
		return []Completion{}, true
	}
	return s.valueCompletions(fixtures, *target), true
}

// payload is the Variant fields a [ after a Variant name opens.
func (frame *storyFrame) valueType() *namedType {
	if frame.list {
		return &namedType{typ: frame.element}
	}
	for index := range frame.fields {
		if frame.fields[index].name == frame.name {
			return &frame.fields[index]
		}
	}
	return nil
}

func fieldEntry(field Field) namedType {
	detail := field.Type.String()
	if field.Optional {
		detail += "?"
	}
	return namedType{name: field.Name, typ: field.Type, optional: field.Optional, detail: detail}
}

// afterAssign reports whether the last token before a partial word is =.
func afterAssign(tokens []scannedToken) bool {
	return len(tokens) >= 2 && tokens[len(tokens)-2].tok == token.ASSIGN
}

func atDeclarationStart(source []byte, pos int) bool {
	line := strings.LastIndexByte(string(source[:pos]), '\n') + 1
	return strings.TrimSpace(string(source[line:pos])) == ""
}

// storyScope is the components a story Document imports. A Document being
// edited is often unparseable, so its imports are read from the recovered
// parse rather than from the snapshot's validated scope.
func (s *Snapshot) storyScope(path string, source []byte) (map[string]Component, []*ast.FixtureDecl) {
	scope := make(map[string]Component)
	var fixtures []*ast.FixtureDecl
	var document *ast.Document
	func() {
		defer func() { recover() }()
		document = parser.New(source).ParseDocument()
	}()
	if document == nil {
		return scope, nil
	}
	for _, node := range document.Nodes {
		if fixture, ok := node.(*ast.FixtureDecl); ok {
			fixtures = append(fixtures, fixture)
		}
		imp, ok := node.(*ast.ImportStmt)
		if !ok {
			continue
		}
		dependency, err := resolveImportPath(path, imp.Path)
		if err != nil {
			continue
		}
		for _, name := range imp.Items {
			if component, ok := s.components[componentID(dependency, name)]; ok {
				scope[name] = component
			}
		}
	}
	return scope, fixtures
}

func componentCompletions(scope map[string]Component) []Completion {
	var items []Completion
	for name, component := range scope {
		items = append(items, Completion{Label: name, Kind: ComponentCompletion, Detail: component.File})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}

// valueCompletions offers the values a story may write for a type.
func (s *Snapshot) valueCompletions(fixtures []*ast.FixtureDecl, target namedType) []Completion {
	var items []Completion
	if target.optional {
		items = append(items, Completion{Label: "None", Kind: ValueCompletion, Detail: "absent " + target.typ.String()})
	}
	switch target.typ.Kind {
	case ScalarTypeKind:
		if target.typ.Scalar == BooleanType {
			items = append(items, Completion{Label: "true", Kind: ValueCompletion, Detail: "boolean"}, Completion{Label: "false", Kind: ValueCompletion, Detail: "boolean"})
		}
	case StateTypeKind:
		state := s.states[target.typ.State]
		for _, variant := range state.Variants {
			items = append(items, Completion{Label: variant.Name, Kind: VariantCompletion, Detail: VariantSignature(state, variant)})
		}
	case ContextualTypeKind:
		if target.typ.Name == AttributesType {
			items = append(items, Completion{Label: "[]", Kind: ValueCompletion, Detail: "empty attribute bag"})
		}
		for _, fixture := range fixtures {
			if fixture.TypeDecl == target.typ.Name {
				items = append(items, Completion{Label: fixture.Name, Kind: FixtureCompletion, Detail: "fixture " + fixture.TypeDecl})
			}
		}
	}
	return items
}
