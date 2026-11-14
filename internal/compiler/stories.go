package compiler

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// IsStoryPath reports whether path names a story Document (*.story.ktml).
// Story Documents are component Documents for indexing and tooling, but they
// declare only fixtures and stories and are never generated.
func IsStoryPath(path string) bool {
	return ast.IsStoryPath(path)
}

// StoryReference is a story header's reference to its component.
const StoryReference ReferenceKind = "story"

// Fixture is a named story value of a contextual type, the only way a story
// supplies a trusted value.
type Fixture struct {
	ID        SymbolID
	Name      string
	File      string
	Type      Type
	Value     string
	Range     SourceRange
	NameRange SourceRange
}

// Story is one named, type-checked set of inputs for a component.
type Story struct {
	ID            SymbolID
	Name          string
	Component     SymbolID
	ComponentName string
	File          string
	// Range is the whole declaration; NameRange and ComponentRange are the
	// two halves of its Component.Name header.
	Range          SourceRange
	NameRange      SourceRange
	ComponentRange SourceRange
	// Values holds the supplied value inputs, in the component's input order.
	Values []StoryValue
	// Body is the content the story passes as children and slot fills.
	Body    []ast.Node
	HasBody bool
	// Default marks the first story of its component in the workspace.
	Default bool
}

// StoryValue is the value one story gives one input.
type StoryValue struct {
	Input string
	Value Value
	Range SourceRange
}

// Value returns the value a story gives input, if any.
func (story Story) Value(input string) (Value, bool) {
	for _, value := range story.Values {
		if value.Input == input {
			return value.Value, true
		}
	}
	return Value{}, false
}

// ValueKind classifies a typed story value.
type ValueKind string

const (
	ScalarValueKind     ValueKind = "scalar"
	RecordValueKind     ValueKind = "record"
	CollectionValueKind ValueKind = "collection"
	VariantValueKind    ValueKind = "variant"
	// NoneValueKind is an absent optional value.
	NoneValueKind ValueKind = "none"
	// FixtureValueKind is a fixture reference; Fixture names it and Text
	// holds its literal.
	FixtureValueKind ValueKind = "fixture"
	// AttributesValueKind is the empty attribute bag, [], the only
	// attribute bag a story can give.
	AttributesValueKind ValueKind = "attributes"
)

// Value is a typed story value. Scalars use String, Boolean, Number, or
// Integer by Type; records and payloads list their supplied Fields; URL
// values use String.
type Value struct {
	Kind    ValueKind
	Type    Type
	String  string
	Boolean bool
	Number  float64
	Integer int64
	Fields  []FieldValue
	Variant string
	Items   []Value
	Fixture string
	Range   SourceRange
}

// FieldValue is the value of one record or payload field.
type FieldValue struct {
	Name  string
	Value Value
}

// Field returns the value of a named field.
func (value Value) Field(name string) (Value, bool) {
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value, true
		}
	}
	return Value{}, false
}

func storyID(path, component, name string) SymbolID {
	return SymbolID(path + "::story::" + component + "." + name)
}

func fixtureID(path, name string) SymbolID {
	return SymbolID(path + "::fixture::" + name)
}

// FixtureType reports whether a type may be given by a fixture: every
// lowered contextual type except attribute bags.
func FixtureType(typ Type) bool {
	return typ.Kind == ContextualTypeKind && typ.Name != AttributesType
}

// TrustedType reports whether values of a type come only from fixtures. URL
// is policy-checked when it renders, so stories may write it as a string.
func TrustedType(typ Type) bool {
	return FixtureType(typ) && typ.Name != URLType
}

// checkDocumentKinds reports story declarations outside story Documents and
// component declarations inside them.
func (s *Snapshot) checkDocumentKinds() {
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil {
			continue
		}
		story := IsStoryPath(path)
		for _, node := range file.doc.Nodes {
			switch declaration := node.(type) {
			case *ast.StoryDecl:
				if !story {
					s.addDiagnostic(path, "story-outside-story-document", fmt.Sprintf("story %s.%s must be declared in a .story.ktml document", declaration.Component, declaration.Name), declaration.KeywordRange.Start, declaration.NameRange.End)
				}
			case *ast.FixtureDecl:
				if !story {
					s.addDiagnostic(path, "fixture-outside-story-document", fmt.Sprintf("fixture %s must be declared in a .story.ktml document", declaration.Name), declaration.KeywordRange.Start, declaration.NameRange.End)
				}
			case *ast.ElementDecl:
				if story {
					s.addDiagnostic(path, "component-in-story", fmt.Sprintf("component %s must be declared in a .ktml document; a story document declares only fixtures and stories", declaration.Name), declaration.Pos(), declaration.Pos()+len(declaration.Name))
				}
			case *ast.RenderClause:
				if story {
					s.addDiagnostic(path, "component-in-story", fmt.Sprintf("render clause %s must be declared in a .ktml document", declaration.Name), declaration.NameRange.Start, declaration.NameRange.End)
				}
			}
		}
	}
}

// resolveStories types every fixture and story of every story Document.
func (s *Snapshot) resolveStories() {
	defaults := make(map[SymbolID]bool)
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil || !IsStoryPath(path) {
			continue
		}
		fixtures := make(map[string]Fixture)
		for _, node := range file.doc.Nodes {
			declaration, ok := node.(*ast.FixtureDecl)
			if !ok {
				continue
			}
			if fixture, ok := s.resolveFixture(path, declaration, fixtures); ok {
				fixtures[fixture.Name] = fixture
				s.fixtures[fixture.ID] = fixture
			}
		}
		seen := make(map[string]bool)
		sources := new(sourceCounters)
		for _, node := range file.doc.Nodes {
			declaration, ok := node.(*ast.StoryDecl)
			if !ok {
				continue
			}
			key := declaration.Component + "." + declaration.Name
			if seen[key] {
				s.addDiagnostic(path, "duplicate-story", fmt.Sprintf("story %s is declared more than once", key), declaration.NameRange.Start, declaration.NameRange.End)
				continue
			}
			seen[key] = true
			story, ok := s.resolveStory(path, file.source, declaration, fixtures, sources)
			if !ok {
				continue
			}
			if !defaults[story.Component] {
				defaults[story.Component] = true
				story.Default = true
			}
			s.stories[story.ID] = story
		}
	}
}

func (s *Snapshot) resolveFixture(path string, declaration *ast.FixtureDecl, fixtures map[string]Fixture) (Fixture, bool) {
	nameRange := declaration.NameRange
	if !validFieldName(declaration.Name) {
		s.addDiagnostic(path, "invalid-fixture-name", fmt.Sprintf("fixture name %s must be an ASCII lowerCamel identifier", declaration.Name), nameRange.Start, nameRange.End)
		return Fixture{}, false
	}
	if _, duplicate := fixtures[declaration.Name]; duplicate {
		s.addDiagnostic(path, "duplicate-fixture", fmt.Sprintf("fixture %s is declared more than once", declaration.Name), nameRange.Start, nameRange.End)
		return Fixture{}, false
	}
	typ, ok := s.resolveType(path, typeExpression(declaration.TypeExpr, declaration.TypeDecl, declaration.TypeRange))
	if !ok {
		return Fixture{}, false
	}
	if !FixtureType(typ) {
		s.addDiagnostic(path, "fixture-type", fmt.Sprintf("fixture %s has type %s; fixtures give contextual values (URL, TrustedURL, TrustedScript, JSONValue, CSSValue), and other values are written in the story", declaration.Name, typ), declaration.TypeRange.Start, declaration.TypeRange.End)
		return Fixture{}, false
	}
	literal := declaration.Value
	if literal == nil || literal.Kind != ast.StringLiteral {
		start, end := declaration.TypeRange.End, declaration.E
		if literal != nil {
			start, end = literal.P, literal.E
		}
		s.addDiagnostic(path, "fixture-value", fmt.Sprintf("fixture %s must be a string literal", declaration.Name), start, end)
		return Fixture{}, false
	}
	if typ.Name == JSONValueType && !json.Valid([]byte(literal.String)) {
		s.addDiagnostic(path, "invalid-json-fixture", fmt.Sprintf("fixture %s is not valid JSON", declaration.Name), literal.P, literal.E)
		return Fixture{}, false
	}
	return Fixture{
		ID: fixtureID(path, declaration.Name), Name: declaration.Name, File: path, Type: typ, Value: literal.String,
		Range:     SourceRange{File: path, Start: declaration.Pos(), End: declaration.End()},
		NameRange: SourceRange{File: path, Start: nameRange.Start, End: nameRange.End},
	}, true
}

func (s *Snapshot) resolveStory(path string, source []byte, declaration *ast.StoryDecl, fixtures map[string]Fixture, sources *sourceCounters) (Story, bool) {
	componentRange := declaration.ComponentRange
	id, visible := s.visible[path][declaration.Component]
	if !visible {
		s.addDiagnostic(path, "unknown-component", fmt.Sprintf("component %s is not in scope; import it from its .ktml document", declaration.Component), componentRange.Start, componentRange.End)
		return Story{}, false
	}
	target := s.components[id]
	s.references[id] = append(s.references[id], Reference{Symbol: id, Kind: StoryReference, Range: SourceRange{File: path, Start: componentRange.Start, End: componentRange.End}})
	if !validTypeName(declaration.Name) {
		s.addDiagnostic(path, "invalid-story-name", fmt.Sprintf("story name %s must be an ASCII UpperCamel identifier", declaration.Name), declaration.NameRange.Start, declaration.NameRange.End)
		return Story{}, false
	}
	story := Story{
		ID: storyID(path, target.Name, declaration.Name), Name: declaration.Name, Component: id, ComponentName: target.Name, File: path,
		Range:          SourceRange{File: path, Start: declaration.Pos(), End: declaration.End()},
		NameRange:      SourceRange{File: path, Start: declaration.NameRange.Start, End: declaration.NameRange.End},
		ComponentRange: SourceRange{File: path, Start: componentRange.Start, End: componentRange.End},
		Body:           declaration.Body,
		HasBody:        declaration.BodyRange != nil,
	}
	checker := valueChecker{snapshot: s, path: path, fixtures: fixtures}
	supplied := make(map[string]Value)
	for _, argument := range declaration.Arguments {
		input, ok := inputNamed(target, argument.Name)
		nameRange := argument.NameRange
		switch {
		case !ok:
			s.addDiagnostic(path, "unknown-input", fmt.Sprintf("component %s has no input %s", target.Name, argument.Name), nameRange.Start, nameRange.End)
			continue
		case input.IsContent():
			s.addDiagnostic(path, "content-as-story-input", fmt.Sprintf("content input %s is filled in the story body, not given a value", argument.Name), nameRange.Start, nameRange.End)
			continue
		}
		if _, duplicate := supplied[argument.Name]; duplicate {
			s.addDiagnostic(path, "duplicate-input", fmt.Sprintf("input %s is supplied more than once", argument.Name), nameRange.Start, argument.E)
			continue
		}
		value, ok := checker.check(argument.Value, input.Type, input.Mode == OptionalInput)
		if !ok {
			supplied[argument.Name] = Value{}
			continue
		}
		supplied[argument.Name] = value
	}
	failed := false
	for _, input := range target.Inputs {
		value, ok := supplied[input.Name]
		if !ok {
			if input.Mode == RequiredInput && !input.IsContent() {
				s.addDiagnostic(path, "missing-input", fmt.Sprintf("story %s.%s must give required input %s", target.Name, declaration.Name, input.Name), declaration.NameRange.Start, declaration.NameRange.End)
				failed = true
			}
			continue
		}
		if value.Kind == "" {
			failed = true
			continue
		}
		story.Values = append(story.Values, StoryValue{Input: input.Name, Value: value, Range: value.Range})
	}
	call := &ast.ElementNode{P: componentRange.Start, E: declaration.End(), Name: target.Name, Children: declaration.Body}
	caller := pathScope{sources: sources}
	s.validateMarkup(path, declaration.Body)
	s.validateCallContent(path, target, call)
	for _, child := range declaration.Body {
		if slot, fill := child.(*ast.SlotNode); fill {
			s.resolveSlotFill(path, caller, target, slot)
			for _, nested := range slot.Body {
				s.resolveNode(path, source, caller, nested)
			}
			continue
		}
		s.resolveNode(path, source, caller, child)
	}
	return story, !failed
}

// valueChecker types story value literals in one story Document.
type valueChecker struct {
	snapshot *Snapshot
	path     string
	fixtures map[string]Fixture
}

func (c valueChecker) fail(code string, expression *ast.ValueExpr, format string, args ...any) (Value, bool) {
	c.snapshot.addDiagnostic(c.path, code, fmt.Sprintf(format, args...), expression.P, expression.E)
	return Value{}, false
}

// check types expression as typ. optional allows None.
func (c valueChecker) check(expression *ast.ValueExpr, typ Type, optional bool) (Value, bool) {
	if expression == nil {
		return Value{}, false
	}
	valueRange := SourceRange{File: c.path, Start: expression.P, End: expression.E}
	if expression.Kind == ast.NameValue && expression.Name == "None" && expression.PayloadRange == nil {
		if !optional {
			return c.fail("story-type-mismatch", expression, "None is only a value of an optional input or field; this one requires %s", typ)
		}
		return Value{Kind: NoneValueKind, Type: typ, Range: valueRange}, true
	}
	if typ.Kind == ContextualTypeKind && typ.Name == AttributesType {
		if expression.Kind != ast.EmptyValue {
			return c.fail("unsupported-story-input", expression, "a story gives an attribute bag only as the empty bag []")
		}
		return Value{Kind: AttributesValueKind, Type: typ, Range: valueRange}, true
	}
	if expression.Kind == ast.NameValue && startsLower(expression.Name) {
		fixture, ok := c.fixtures[expression.Name]
		if !ok {
			return c.fail("unknown-fixture", expression, "fixture %s is not declared in this story document", expression.Name)
		}
		if !fixture.Type.Equal(typ) {
			return c.fail("story-type-mismatch", expression, "fixture %s is %s; this value requires %s", fixture.Name, fixture.Type, typ)
		}
		c.snapshot.references[fixture.ID] = append(c.snapshot.references[fixture.ID], Reference{Symbol: fixture.ID, Kind: StoryReference, Range: SourceRange{File: c.path, Start: expression.NameRange.Start, End: expression.NameRange.End}})
		return Value{Kind: FixtureValueKind, Type: typ, Fixture: fixture.Name, String: fixture.Value, Range: valueRange}, true
	}
	switch typ.Kind {
	case ScalarTypeKind:
		return c.scalar(expression, typ, valueRange)
	case ContextualTypeKind:
		if typ.Name == URLType && expression.Kind == ast.ScalarValue && expression.Scalar.Kind == ast.StringLiteral {
			return Value{Kind: ScalarValueKind, Type: typ, String: expression.Scalar.String, Range: valueRange}, true
		}
		return c.fail("trusted-value-literal", expression, "%s values come only from fixtures; declare fixture name: %s = \"…\" and give its name", typ, typ)
	case RecordTypeKind:
		record, ok := c.snapshot.records[typ.Record]
		if !ok {
			return Value{}, false
		}
		if expression.Kind != ast.FieldsValue && expression.Kind != ast.EmptyValue {
			return c.fail("story-type-mismatch", expression, "record %s is written [field = value, …]", record.Name)
		}
		fields, ok := c.fields(expression, expression.Fields, record.Fields, "record "+record.Name)
		return Value{Kind: RecordValueKind, Type: typ, Fields: fields, Range: valueRange}, ok
	case CollectionTypeKind:
		if expression.Kind != ast.ListValue && expression.Kind != ast.EmptyValue {
			return c.fail("story-type-mismatch", expression, "%s is written [item, …]", typ)
		}
		value := Value{Kind: CollectionValueKind, Type: typ, Range: valueRange}
		valid := true
		for _, item := range expression.Items {
			checked, ok := c.check(item, *typ.Element, false)
			valid = valid && ok
			value.Items = append(value.Items, checked)
		}
		return value, valid
	case StateTypeKind:
		state, ok := c.snapshot.states[typ.State]
		if !ok {
			return Value{}, false
		}
		if expression.Kind != ast.NameValue {
			return c.fail("story-type-mismatch", expression, "state %s is written as one of its Variants: %s", state.Name, variantNames(state))
		}
		variant, ok := state.Variant(expression.Name)
		if !ok {
			c.snapshot.addDiagnostic(c.path, "unknown-variant", fmt.Sprintf("state %s has no Variant %s; it has %s", state.Name, expression.Name, variantNames(state)), expression.NameRange.Start, expression.NameRange.End)
			return Value{}, false
		}
		value := Value{Kind: VariantValueKind, Type: typ, Variant: variant.Name, Range: valueRange}
		if !variant.Payload {
			if expression.PayloadRange != nil {
				return c.fail("unexpected-payload", expression, "Variant %s.%s has no payload", state.Name, variant.Name)
			}
			return value, true
		}
		fields, ok := c.fields(expression, expression.Fields, variant.Fields, "Variant "+state.Name+"."+variant.Name)
		value.Fields = fields
		return value, ok
	}
	return c.fail("unsupported-story-input", expression, "a story cannot give a %s value", typ)
}

func (c valueChecker) scalar(expression *ast.ValueExpr, typ Type, valueRange SourceRange) (Value, bool) {
	if expression.Kind != ast.ScalarValue {
		return c.fail("story-type-mismatch", expression, "this value requires %s", typ)
	}
	literal := expression.Scalar
	value := Value{Kind: ScalarValueKind, Type: typ, Range: valueRange}
	switch {
	case typ.Scalar == StringType && literal.Kind == ast.StringLiteral:
		value.String = literal.String
	case typ.Scalar == BooleanType && literal.Kind == ast.BooleanLiteral:
		value.Boolean = literal.Boolean
	case typ.Scalar == NumberType && literal.Kind == ast.NumberLiteral:
		value.Number = literal.Number
	case typ.Scalar == IntegerType && literal.Kind == ast.NumberLiteral && integerLiteral(literal.Raw):
		value.Integer, _ = strconv.ParseInt(literal.Raw, 10, 64)
	default:
		return c.fail("story-type-mismatch", expression, "this value requires %s", typ)
	}
	return value, true
}

// fields types a record literal or Variant payload. Every required field must
// be given; omitted optional fields are None.
func (c valueChecker) fields(expression *ast.ValueExpr, arguments []*ast.ValueArgument, declared []Field, owner string) ([]FieldValue, bool) {
	valid := true
	given := make(map[string]bool)
	var values []FieldValue
	for _, argument := range arguments {
		field, ok := fieldNamed(declared, argument.Name)
		if !ok {
			c.snapshot.addDiagnostic(c.path, "unknown-field", fmt.Sprintf("%s has no field %s", owner, argument.Name), argument.NameRange.Start, argument.NameRange.End)
			valid = false
			continue
		}
		if given[argument.Name] {
			c.snapshot.addDiagnostic(c.path, "duplicate-field", fmt.Sprintf("field %s is given more than once", argument.Name), argument.NameRange.Start, argument.NameRange.End)
			valid = false
			continue
		}
		given[argument.Name] = true
		value, ok := c.check(argument.Value, field.Type, field.Optional)
		if !ok {
			valid = false
			continue
		}
		values = append(values, FieldValue{Name: field.Name, Value: value})
	}
	var missing []string
	for _, field := range declared {
		if !field.Optional && !given[field.Name] {
			missing = append(missing, field.Name)
		}
	}
	if len(missing) > 0 {
		c.snapshot.addDiagnostic(c.path, "missing-field", fmt.Sprintf("%s requires %s", owner, strings.Join(missing, ", ")), expression.P, expression.E)
		valid = false
	}
	sort.SliceStable(values, func(i, j int) bool {
		return fieldIndex(declared, values[i].Name) < fieldIndex(declared, values[j].Name)
	})
	return values, valid
}

func fieldNamed(fields []Field, name string) (Field, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

func fieldIndex(fields []Field, name string) int {
	for index, field := range fields {
		if field.Name == name {
			return index
		}
	}
	return math.MaxInt
}

func variantNames(state State) string {
	names := make([]string, len(state.Variants))
	for index, variant := range state.Variants {
		names[index] = variant.Name
	}
	return strings.Join(names, ", ")
}

func startsLower(name string) bool {
	return name != "" && name[0] >= 'a' && name[0] <= 'z'
}

// Stories returns every valid story of the workspace, ordered by file and
// then by declaration.
func (s *Snapshot) Stories() []Story {
	if s == nil {
		return nil
	}
	stories := make([]Story, 0, len(s.stories))
	for _, story := range s.stories {
		stories = append(stories, story)
	}
	sort.Slice(stories, func(i, j int) bool {
		if stories[i].File != stories[j].File {
			return stories[i].File < stories[j].File
		}
		return stories[i].Range.Start < stories[j].Range.Start
	})
	return stories
}

// StoriesIn returns the valid stories a story Document declares.
func (s *Snapshot) StoriesIn(file string) []Story {
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	var stories []Story
	for _, story := range s.Stories() {
		if story.File == path {
			stories = append(stories, story)
		}
	}
	return stories
}

// Story returns a story by ID.
func (s *Snapshot) Story(id SymbolID) (Story, bool) {
	if s == nil {
		return Story{}, false
	}
	story, ok := s.stories[id]
	return story, ok
}

// Fixtures returns the valid fixtures a story Document declares, in source
// order.
func (s *Snapshot) Fixtures(file string) []Fixture {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	var fixtures []Fixture
	for _, fixture := range s.fixtures {
		if fixture.File == path {
			fixtures = append(fixtures, fixture)
		}
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Range.Start < fixtures[j].Range.Start })
	return fixtures
}

// AllDiagnostics returns every diagnostic of the snapshot, ordered by file
// and position.
func (s *Snapshot) AllDiagnostics() []Diagnostic {
	if s == nil {
		return nil
	}
	paths := make([]string, 0, len(s.diagnostics))
	for path := range s.diagnostics {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var all []Diagnostic
	for _, path := range paths {
		all = append(all, s.diagnostics[path]...)
	}
	return all
}
