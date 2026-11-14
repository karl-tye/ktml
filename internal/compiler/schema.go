package compiler

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

const IntegerType ValueType = "integer"

const IntegerLiteral ScalarLiteralKind = "integer"

type TypeKind string

const (
	ScalarTypeKind     TypeKind = "scalar"
	RecordTypeKind     TypeKind = "record"
	CollectionTypeKind TypeKind = "collection"
	StateTypeKind      TypeKind = "state"
	ContextualTypeKind TypeKind = "contextual"
	// ContentTypeKind is the type of children and named slots. It is not a
	// value type: content renders only through outlets.
	ContentTypeKind TypeKind = "content"
)

// ContentTypeName is the spelling of the content type in input lists.
const ContentTypeName = "content"

// Contextual types lowered so far: the minimum URL boundary, the JSON and
// trusted-script values that script contexts accept, the CSS of the style
// attribute, and forwarded attribute bags. TrustedHTML stays reserved.
const (
	URLType           = "URL"
	TrustedURLType    = "TrustedURL"
	JSONValueType     = "JSONValue"
	TrustedScriptType = "TrustedScript"
	// CSSValueType is serialized CSS, rendered only in the style attribute.
	CSSValueType = "CSSValue"
	// AttributesType is a forwarded attribute bag, rendered only by a spread.
	AttributesType = "Attributes"
)

// loweredContextual reports whether name is a contextual type the compiler
// lowers.
func loweredContextual(name string) bool {
	return name == URLType || name == TrustedURLType || name == JSONValueType || name == TrustedScriptType || name == AttributesType || name == CSSValueType
}

// Type is a resolved input or field type. Records and states are nominal;
// collections are structural over their element type. A contextual type is
// identified by Name.
type Type struct {
	Kind    TypeKind
	Scalar  ValueType
	Record  SymbolID
	State   SymbolID
	Name    string
	Element *Type
}

func ScalarTypeOf(value ValueType) Type {
	return Type{Kind: ScalarTypeKind, Scalar: value}
}

func (t Type) String() string {
	switch t.Kind {
	case ScalarTypeKind:
		return string(t.Scalar)
	case RecordTypeKind, StateTypeKind, ContextualTypeKind:
		return t.Name
	case ContentTypeKind:
		return ContentTypeName
	case CollectionTypeKind:
		if t.Element == nil {
			return "[]"
		}
		return "[" + t.Element.String() + "]"
	default:
		return ""
	}
}

func (t Type) Equal(other Type) bool {
	if t.Kind != other.Kind {
		return false
	}
	switch t.Kind {
	case ScalarTypeKind:
		return t.Scalar == other.Scalar
	case RecordTypeKind:
		return t.Record == other.Record
	case StateTypeKind:
		return t.State == other.State
	case ContextualTypeKind:
		return t.Name == other.Name
	case CollectionTypeKind:
		if t.Element == nil || other.Element == nil {
			return t.Element == other.Element
		}
		return t.Element.Equal(*other.Element)
	default:
		return false
	}
}

func (t Type) IsScalar(value ValueType) bool {
	return t.Kind == ScalarTypeKind && t.Scalar == value
}

// Record is a nominal Host Schema record. Range is the name range.
type Record struct {
	ID               SymbolID
	Name             string
	File             string
	Range            SourceRange
	DeclarationRange SourceRange
	Fields           []Field
}

type Field struct {
	ID                  SymbolID
	Record              SymbolID
	Name                string
	GoName              string
	Type                Type
	Optional            bool
	Range               SourceRange
	NameRange           SourceRange
	TypeRange           SourceRange
	OptionalMarkerRange *SourceRange

	typeExpr *ast.TypeExpr
}

// Field returns the named field of a record.
func (r Record) Field(name string) (Field, bool) {
	for _, field := range r.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// ResolvedPart is one typed step of a dotted path. The root names an input or
// a Render Clause binding; the remaining parts name record fields. A binding
// root records the binding's declaration range, and Field is the payload field
// an unaliased binding introduces.
type ResolvedPart struct {
	Name         string
	Range        SourceRange
	Type         Type
	Optional     bool
	Input        string
	Binding      string
	BindingRange SourceRange
	Field        SymbolID
	// Narrowed reports an optional value read as its value type because a
	// presence condition holds where the path appears.
	Narrowed bool
}

func (part ResolvedPart) kind() string {
	switch {
	case part.Input != "":
		return "input"
	case part.Binding != "":
		return "binding"
	default:
		return "field"
	}
}

// ResolvedPath is a fully typed quoted-interpolation path.
type ResolvedPath struct {
	Range     SourceRange
	Component SymbolID
	Parts     []ResolvedPart
	Type      Type
	Optional  bool
}

type SymbolKind string

const (
	RecordSymbol SymbolKind = "record"
	FieldSymbol  SymbolKind = "field"
)

// Symbol is a Host Schema declaration addressable by tooling.
type Symbol struct {
	ID     SymbolID
	Kind   SymbolKind
	Name   string
	Range  SourceRange
	Record SymbolID
	Detail string
}

const (
	TypeReference  ReferenceKind = "type"
	FieldReference ReferenceKind = "field"
)

var contextualTypes = map[string]bool{
	"URL": true, "TrustedURL": true, "CSSValue": true, "JSONValue": true, "TrustedHTML": true, "TrustedScript": true, "Attributes": true,
}

// IsSchemaPath reports whether path names a Host Schema Document.
func IsSchemaPath(path string) bool {
	return strings.HasSuffix(path, ".ktmlschema")
}

// GoFieldName uppercases only the first ASCII letter of a schema field name.
func GoFieldName(name string) string {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return name
	}
	return string(name[0]-'a'+'A') + name[1:]
}

func recordID(path, name string) SymbolID {
	return SymbolID(path + "::record::" + name)
}

func fieldID(record SymbolID, name string) SymbolID {
	return SymbolID(string(record) + "::field::" + name)
}

func validTypeName(name string) bool {
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	return asciiAlphanumeric(name[1:])
}

func validFieldName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	return asciiAlphanumeric(name[1:])
}

func asciiAlphanumeric(value string) bool {
	for index := 0; index < len(value); index++ {
		current := value[index]
		if (current < 'a' || current > 'z') && (current < 'A' || current > 'Z') && (current < '0' || current > '9') {
			return false
		}
	}
	return true
}

func (s *Snapshot) indexRecords() {
	for _, path := range sortedFilePaths(s.files) {
		file := s.files[path]
		if file.doc == nil {
			continue
		}
		if s.types[path] == nil {
			s.types[path] = make(map[string]SymbolID)
		}
		for _, node := range file.doc.Nodes {
			declaration, ok := node.(*ast.RecordDecl)
			if !ok {
				continue
			}
			if !IsSchemaPath(path) {
				s.addDiagnostic(path, "record-outside-schema", fmt.Sprintf("record %s must be declared in a .ktmlschema document", declaration.Name), declaration.KeywordRange.Start, declaration.NameRange.End)
				continue
			}
			nameRange := declaration.NameRange
			switch {
			case contextualTypes[declaration.Name]:
				s.addDiagnostic(path, "reserved-type-name", fmt.Sprintf("%s is a reserved contextual type name", declaration.Name), nameRange.Start, nameRange.End)
				continue
			case !validTypeName(declaration.Name):
				s.addDiagnostic(path, "invalid-type-name", fmt.Sprintf("record name %s must be an ASCII UpperCamel identifier", declaration.Name), nameRange.Start, nameRange.End)
				continue
			}
			if _, duplicate := s.types[path][declaration.Name]; duplicate {
				s.addDiagnostic(path, "duplicate-record", fmt.Sprintf("record %s is declared more than once", declaration.Name), nameRange.Start, nameRange.End)
				continue
			}
			id := recordID(path, declaration.Name)
			record := Record{
				ID: id, Name: declaration.Name, File: path,
				Range:            SourceRange{File: path, Start: nameRange.Start, End: nameRange.End},
				DeclarationRange: SourceRange{File: path, Start: declaration.Pos(), End: declaration.End()},
			}
			if len(declaration.Fields) > 0 {
				record.Fields = make([]Field, 0, len(declaration.Fields))
			}
			names := make(map[string]bool)
			goNames := make(map[string]string)
			for _, fieldDeclaration := range declaration.Fields {
				fieldNameRange := fieldDeclaration.NameRange
				if !validFieldName(fieldDeclaration.Name) {
					s.addDiagnostic(path, "invalid-field-name", fmt.Sprintf("field name %s must be an ASCII lowerCamel identifier", fieldDeclaration.Name), fieldNameRange.Start, fieldNameRange.End)
					continue
				}
				if names[fieldDeclaration.Name] {
					s.addDiagnostic(path, "duplicate-field", fmt.Sprintf("record %s declares field %s more than once", declaration.Name, fieldDeclaration.Name), fieldNameRange.Start, fieldNameRange.End)
					continue
				}
				names[fieldDeclaration.Name] = true
				goName := GoFieldName(fieldDeclaration.Name)
				if first, collision := goNames[goName]; collision {
					s.addDiagnostic(path, "generated-field-collision", fmt.Sprintf("fields %s and %s both generate Go field %s", first, fieldDeclaration.Name, goName), fieldNameRange.Start, fieldNameRange.End)
					continue
				}
				goNames[goName] = fieldDeclaration.Name
				field := Field{
					ID: fieldID(id, fieldDeclaration.Name), Record: id, Name: fieldDeclaration.Name, GoName: goName,
					Optional:  fieldDeclaration.Optional,
					Range:     SourceRange{File: path, Start: fieldDeclaration.Pos(), End: fieldDeclaration.End()},
					NameRange: SourceRange{File: path, Start: fieldNameRange.Start, End: fieldNameRange.End},
					TypeRange: SourceRange{File: path, Start: fieldDeclaration.TypeRange.Start, End: fieldDeclaration.TypeRange.End},
					typeExpr:  typeExpression(fieldDeclaration.TypeExpr, fieldDeclaration.TypeDecl, fieldDeclaration.TypeRange),
				}
				if fieldDeclaration.OptionalMarker != nil {
					field.OptionalMarkerRange = &SourceRange{File: path, Start: fieldDeclaration.OptionalMarker.Start, End: fieldDeclaration.OptionalMarker.End}
				}
				record.Fields = append(record.Fields, field)
			}
			s.records[id] = record
			s.types[path][declaration.Name] = id
		}
	}
}

// typeExpression returns the structured type, synthesizing a named type for
// hand-built syntax trees that only carry the spelling.
func typeExpression(expression *ast.TypeExpr, spelling string, spellingRange ast.SourceRange) *ast.TypeExpr {
	if expression != nil {
		return expression
	}
	nameRange := ast.SourceRange{Start: spellingRange.Start, End: spellingRange.Start + len(spelling)}
	return &ast.TypeExpr{P: nameRange.Start, E: nameRange.End, Kind: ast.NamedTypeExpr, Name: spelling, NameRange: &nameRange}
}

// resolveType resolves a type expression in a document's type namespace and
// records type references for tooling.
func (s *Snapshot) resolveType(path string, expression *ast.TypeExpr) (Type, bool) {
	if expression == nil {
		return Type{}, false
	}
	if expression.Kind == ast.CollectionTypeExpr {
		element, ok := s.resolveType(path, expression.Element)
		if !ok {
			return Type{}, false
		}
		return Type{Kind: CollectionTypeKind, Element: &element}, true
	}
	nameRange := ast.SourceRange{Start: expression.P, End: expression.E}
	if expression.NameRange != nil {
		nameRange = *expression.NameRange
	}
	switch expression.Name {
	case string(StringType), string(BooleanType), string(NumberType), string(IntegerType):
		return ScalarTypeOf(ValueType(expression.Name)), true
	}
	if loweredContextual(expression.Name) {
		return Type{Kind: ContextualTypeKind, Name: expression.Name}, true
	}
	if expression.Name == ContentTypeName {
		s.addDiagnostic(path, "content-field", "content is not a value type; declare children and slots as content inputs of a Component Signature", nameRange.Start, nameRange.End)
		return Type{}, false
	}
	if contextualTypes[expression.Name] {
		s.addDiagnostic(path, "unsupported-contextual-type", fmt.Sprintf("contextual type %s is reserved but not supported yet", expression.Name), nameRange.Start, nameRange.End)
		return Type{}, false
	}
	id, ok := s.types[path][expression.Name]
	if !ok {
		s.addDiagnostic(path, "unknown-type", fmt.Sprintf("unknown type %s", expression.Name), nameRange.Start, nameRange.End)
		return Type{}, false
	}
	reference := SourceRange{File: path, Start: nameRange.Start, End: nameRange.End}
	if state, isState := s.states[id]; isState {
		s.references[id] = append(s.references[id], Reference{Symbol: id, Kind: TypeReference, Range: reference, Alias: expression.Name != state.Name})
		return Type{Kind: StateTypeKind, State: id, Name: state.Name}, true
	}
	record := s.records[id]
	s.references[id] = append(s.references[id], Reference{Symbol: id, Kind: TypeReference, Range: reference, Alias: expression.Name != record.Name})
	return Type{Kind: RecordTypeKind, Record: id, Name: record.Name}, true
}

func sortedRecordIDs(records map[SymbolID]Record) []SymbolID {
	ids := make([]SymbolID, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *Snapshot) resolveRecordFields() {
	for _, id := range sortedRecordIDs(s.records) {
		record := s.records[id]
		fields := record.Fields[:0]
		for _, field := range record.Fields {
			resolved, ok := s.resolveType(record.File, field.typeExpr)
			if !ok {
				continue
			}
			field.Type = resolved
			fields = append(fields, field)
		}
		record.Fields = fields
		s.records[id] = record
	}
}

// checkRecordCycles reports every field edge that closes a cycle through
// records and states. A state contains the payload fields of its Variants.
func (s *Snapshot) checkRecordCycles() {
	const (
		unvisited = iota
		visiting
		done
	)
	type edge struct {
		owner, field, file string
		typ                Type
		typeRange          SourceRange
	}
	edges := func(id SymbolID) []edge {
		var result []edge
		if record, ok := s.records[id]; ok {
			for _, field := range record.Fields {
				result = append(result, edge{owner: record.Name, field: field.Name, file: record.File, typ: field.Type, typeRange: field.TypeRange})
			}
			return result
		}
		state := s.states[id]
		for _, variant := range state.Variants {
			for _, field := range variant.Fields {
				result = append(result, edge{owner: state.Name + "." + variant.Name, field: field.Name, file: state.File, typ: field.Type, typeRange: field.TypeRange})
			}
		}
		return result
	}
	progress := make(map[SymbolID]int)
	var visit func(SymbolID)
	visit = func(id SymbolID) {
		progress[id] = visiting
		for _, current := range edges(id) {
			target, ok := nominalOf(current.typ)
			if !ok {
				continue
			}
			_, isRecord := s.records[target]
			targetState, isState := s.states[target]
			if !isRecord && !isState {
				continue
			}
			switch progress[target] {
			case visiting:
				if isState {
					s.addDiagnostic(current.file, "recursive-state", fmt.Sprintf("field %s.%s makes state %s contain itself", current.owner, current.field, targetState.Name), current.typeRange.Start, current.typeRange.End)
				} else {
					s.addDiagnostic(current.file, "recursive-record", fmt.Sprintf("field %s.%s makes record %s contain itself", current.owner, current.field, s.records[target].Name), current.typeRange.Start, current.typeRange.End)
				}
			case unvisited:
				visit(target)
			}
		}
		progress[id] = done
	}
	ids := sortedRecordIDs(s.records)
	for id := range s.states {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if progress[id] == unvisited {
			visit(id)
		}
	}
}

// nominalOf unwraps collections to the record or state a type ultimately
// contains.
func nominalOf(value Type) (SymbolID, bool) {
	for value.Kind == CollectionTypeKind && value.Element != nil {
		value = *value.Element
	}
	switch value.Kind {
	case RecordTypeKind:
		return value.Record, true
	case StateTypeKind:
		return value.State, true
	default:
		return "", false
	}
}

// resolveInputTypes types component inputs after imports have populated each
// document's type namespace. Inputs whose types do not resolve are dropped.
func (s *Snapshot) resolveInputTypes() {
	ids := make([]SymbolID, 0, len(s.components))
	for id := range s.components {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		component := s.components[id]
		inputs := component.Inputs[:0]
		for _, input := range component.Inputs {
			resolved, ok := s.resolveInputType(component.File, input.typeExpr)
			if !ok {
				continue
			}
			input.Type = resolved
			if resolved.Kind == ContentTypeKind && input.Default != nil {
				s.addDiagnostic(component.File, "content-default", fmt.Sprintf("content input %s cannot have a default", input.Name), input.Default.Range.Start, input.Default.Range.End)
				continue
			}
			if input.Default != nil {
				if resolved.Kind != ScalarTypeKind {
					s.addDiagnostic(component.File, "default-type-mismatch", fmt.Sprintf("input %s of type %s cannot have a default; defaults are scalar literals", input.Name, resolved), input.Default.Range.Start, input.Default.Range.End)
				} else if !scalarValueMatches(input.Default, resolved.Scalar) {
					s.addDiagnostic(component.File, "default-type-mismatch", fmt.Sprintf("default for input %s requires %s", input.Name, resolved.Scalar), input.Default.Range.Start, input.Default.Range.End)
				} else if resolved.Scalar == IntegerType {
					input.Default.Kind = IntegerLiteral
					input.Default.Integer, _ = strconv.ParseInt(input.Default.Raw, 10, 64)
				}
			}
			if input.Name == ChildrenInput && resolved.Kind != ContentTypeKind {
				s.addDiagnostic(component.File, "children-not-content", "children is the unlabelled content input; declare it as children: content or children?: content", input.TypeRange.Start, input.TypeRange.End)
				continue
			}
			if input.Name == ChildrenInput {
				component.AcceptsChildren = true
			}
			inputs = append(inputs, input)
		}
		component.Inputs = inputs
		s.components[id] = component
	}
}

// resolveInputType resolves an input type, which may also be content.
func (s *Snapshot) resolveInputType(path string, expression *ast.TypeExpr) (Type, bool) {
	if expression != nil && expression.Kind == ast.NamedTypeExpr && expression.Name == ContentTypeName {
		return Type{Kind: ContentTypeKind}, true
	}
	for element := expression; element != nil && element.Kind == ast.CollectionTypeExpr; element = element.Element {
		if inner := element.Element; inner != nil && inner.Kind == ast.NamedTypeExpr && inner.Name == ContentTypeName {
			nameRange := ast.SourceRange{Start: inner.P, End: inner.E}
			s.addDiagnostic(path, "content-collection", "content cannot be a collection element; use one content input and compose in Go with ktml.Join", nameRange.Start, nameRange.End)
			return Type{}, false
		}
	}
	return s.resolveType(path, expression)
}

func scalarValueMatches(value *ScalarValue, scalar ValueType) bool {
	switch scalar {
	case StringType:
		return value.Kind == StringLiteral
	case BooleanType:
		return value.Kind == BooleanLiteral
	case NumberType:
		return value.Kind == NumberLiteral
	case IntegerType:
		return value.Kind == NumberLiteral && integerLiteral(value.Raw)
	default:
		return false
	}
}

// integerLiteral accepts a whole base-10 numeral that fits a signed 64-bit
// integer. The shared numeric token never carries a sign or exponent.
func integerLiteral(raw string) bool {
	if strings.ContainsAny(raw, ".eE") {
		return false
	}
	_, err := strconv.ParseInt(raw, 10, 64)
	return err == nil
}

// checkGeneratedNames rejects declarations whose generated Go names collide
// in one directory (Go package): components (Name, NameProps, RenderName),
// records, and states with their Variants. Every colliding declaration is
// reported at its name, naming the declarations it collides with.
func (s *Snapshot) checkGeneratedNames() {
	type owner struct {
		description string
		file        string
		start, end  int
	}
	type claim struct {
		name   string
		owners []int
	}
	var owners []owner
	claims := make(map[string]map[string]*claim)
	var order []*claim
	reserve := func(dir string, names []string, declared owner) {
		if claims[dir] == nil {
			claims[dir] = make(map[string]*claim)
		}
		index := len(owners)
		owners = append(owners, declared)
		for _, name := range names {
			current := claims[dir][name]
			if current == nil {
				current = &claim{name: name}
				claims[dir][name] = current
				order = append(order, current)
			}
			if len(current.owners) == 0 || current.owners[len(current.owners)-1] != index {
				current.owners = append(current.owners, index)
			}
		}
	}
	for _, component := range s.Components() {
		name := GeneratedGoField(component.Name)
		reserve(filepath.Dir(component.File), []string{name, name + "Props", "Render" + name}, owner{
			description: "component " + component.Name + " in " + filepath.Base(component.File),
			file:        component.File, start: component.Range.Start, end: component.Range.End,
		})
	}
	for _, id := range sortedRecordIDs(s.records) {
		record := s.records[id]
		reserve(filepath.Dir(record.File), []string{record.Name}, owner{
			description: "record " + record.Name + " in " + filepath.Base(record.File),
			file:        record.File, start: record.Range.Start, end: record.Range.End,
		})
	}
	for _, id := range sortedStateIDs(s.states) {
		state := s.states[id]
		reserve(filepath.Dir(state.File), StateGoNames(state), owner{
			description: "state " + state.Name + " in " + filepath.Base(state.File),
			file:        state.File, start: state.Range.Start, end: state.Range.End,
		})
	}
	reported := make(map[int]bool)
	for _, current := range order {
		if len(current.owners) < 2 {
			continue
		}
		for _, index := range current.owners {
			if reported[index] {
				continue
			}
			reported[index] = true
			var others []string
			for _, other := range current.owners {
				if other != index {
					others = append(others, owners[other].description)
				}
			}
			declared := owners[index]
			s.addDiagnostic(declared.file, "generated-name-collision", fmt.Sprintf("%s generates Go name %s, which %s in the same package also generates; rename one of them", declared.description, current.name, strings.Join(others, " and ")), declared.start, declared.end)
		}
	}
}

// resolvePath types a dotted interpolation path from its input root through
// record fields, reporting the first invalid step.
func (s *Snapshot) resolvePath(path string, caller pathScope, expression *ast.PathExpression) (ResolvedPath, bool) {
	root := expression.Parts[0]
	rootRange := SourceRange{File: path, Start: root.P, End: root.E}
	resolved := ResolvedPath{Range: SourceRange{File: path, Start: expression.P, End: expression.E}, Component: caller.component.ID}
	if binding, ok := caller.binding(root.Name); ok {
		if binding.Type.Kind == "" {
			// An item whose collection did not type is already diagnosed.
			return ResolvedPath{}, false
		}
		part := ResolvedPart{Name: root.Name, Range: rootRange, Type: binding.Type, Optional: binding.Optional, Binding: binding.Name, BindingRange: binding.Range}
		if binding.Alias == "" && binding.FieldID != "" {
			part.Field = binding.FieldID
			s.references[binding.FieldID] = append(s.references[binding.FieldID], Reference{Symbol: binding.FieldID, Kind: FieldReference, Range: rootRange})
		}
		resolved.Parts = append(resolved.Parts, caller.narrow(part, root.Name))
	} else {
		var input Input
		found := false
		for _, candidate := range caller.component.Inputs {
			if candidate.Name == root.Name {
				input, found = candidate, true
				break
			}
		}
		if !found {
			s.addDiagnostic(path, "unknown-path-root", fmt.Sprintf("path root %s is not declared by component %s", root.Name, caller.component.Name), root.P, root.E)
			return ResolvedPath{}, false
		}
		resolved.Parts = append(resolved.Parts, caller.narrow(ResolvedPart{
			Name: root.Name, Range: rootRange,
			Type: input.Type, Optional: input.Mode == OptionalInput, Input: input.Name,
		}, root.Name))
	}
	for index, part := range expression.Parts[1:] {
		previous := resolved.Parts[len(resolved.Parts)-1]
		if previous.Optional {
			s.addDiagnostic(path, "optional-value-access", fmt.Sprintf("optional %s %s must be narrowed before value access; test it with if %s? { … }", previous.kind(), previous.Name, pathSpelling(expression, index+1)), previous.Range.Start, previous.Range.End)
			return ResolvedPath{}, false
		}
		switch previous.Type.Kind {
		case ContentTypeKind:
			s.addDiagnostic(path, "content-value-access", fmt.Sprintf("content input %s has no field %s; content renders only as an outlet", previous.Name, part.Name), part.P, part.E)
			return ResolvedPath{}, false
		case CollectionTypeKind:
			s.addDiagnostic(path, "collection-field-access", fmt.Sprintf("%s is a collection %s and has no field %s", previous.Name, previous.Type, part.Name), part.P, part.E)
			return ResolvedPath{}, false
		case ScalarTypeKind, ContextualTypeKind:
			s.addDiagnostic(path, "scalar-field-access", fmt.Sprintf("%s is %s and has no field %s", previous.Name, previous.Type, part.Name), part.P, part.E)
			return ResolvedPath{}, false
		case StateTypeKind:
			s.addDiagnostic(path, "state-field-access", fmt.Sprintf("%s is state %s and has no field %s; select a Variant with a Render Clause", previous.Name, previous.Type, part.Name), part.P, part.E)
			return ResolvedPath{}, false
		}
		record := s.records[previous.Type.Record]
		field, ok := record.Field(part.Name)
		if !ok {
			s.addDiagnostic(path, "unknown-field", fmt.Sprintf("record %s has no field %s", record.Name, part.Name), part.P, part.E)
			return ResolvedPath{}, false
		}
		partRange := SourceRange{File: path, Start: part.P, End: part.E}
		s.references[field.ID] = append(s.references[field.ID], Reference{Symbol: field.ID, Kind: FieldReference, Range: partRange})
		resolved.Parts = append(resolved.Parts, caller.narrow(ResolvedPart{Name: part.Name, Range: partRange, Type: field.Type, Optional: field.Optional, Field: field.ID}, pathSpelling(expression, index+2)))
	}
	last := resolved.Parts[len(resolved.Parts)-1]
	resolved.Type, resolved.Optional = last.Type, last.Optional
	s.paths[path] = append(s.paths[path], resolved)
	return resolved, true
}

// pathSpelling returns the dotted spelling of the first count parts.
func pathSpelling(expression *ast.PathExpression, count int) string {
	names := make([]string, count)
	for i := range names {
		names[i] = expression.Parts[i].Name
	}
	return strings.Join(names, ".")
}

// resolveRenderedPath resolves a path whose value is written as text or an
// attribute value. URL, TrustedURL, and JSONValue render as an exact element
// attribute value. Script attributes accept only TrustedScript, JSONValue,
// and boolean values, exact or among literal text.
func (s *Snapshot) resolveRenderedPath(path string, caller pathScope, expression *ast.PathExpression, context renderContext, exact bool, attribute string) {
	resolved, ok := s.resolvePath(path, caller, expression)
	if !ok {
		return
	}
	last := resolved.Parts[len(resolved.Parts)-1]
	if resolved.Type.Kind == ContentTypeKind {
		s.addDiagnostic(path, "content-value-access", fmt.Sprintf("content input %s cannot be interpolated; render it with a bare %s outlet", last.Name, last.Name), expression.P, expression.E)
		return
	}
	if resolved.Optional {
		s.addDiagnostic(path, "optional-value-access", fmt.Sprintf("optional %s %s must be narrowed before value access; test it with if %s? { … }", last.kind(), last.Name, expression.String()), last.Range.Start, last.Range.End)
		return
	}
	contextual := resolved.Type.Kind == ContextualTypeKind
	if contextual && resolved.Type.Name == AttributesType {
		s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("Attributes value %s renders only through a spread, ...%s, in an element's attribute list", expression.String(), expression.String()), expression.P, expression.E)
		return
	}
	if context == styleAttributeContext {
		if contextual && resolved.Type.Name == CSSValueType {
			return
		}
		s.addDiagnostic(path, "style-attribute-value", fmt.Sprintf("%s is CSS, so %s (%s) cannot be interpolated; build the declarations in Go as ktml.CSSValue (ktml.CSS)", attribute, expression.String(), resolved.Type), expression.P, expression.E)
		return
	}
	if context == scriptBodyContext {
		if resolved.Type.IsScalar(BooleanType) || contextual && (resolved.Type.Name == TrustedScriptType || resolved.Type.Name == JSONValueType) {
			if contextual && resolved.Type.Name == TrustedScriptType && s.trustedScripts != nil {
				s.trustedScripts[expression] = true
			}
			s.reportScriptPosition(path, expression, "the script")
			return
		}
		s.addDiagnostic(path, "script-body-value", fmt.Sprintf("script content is JavaScript, so %s (%s) cannot be interpolated; build the value in Go as ktml.JSONValue (ktml.JSON) or ktml.TrustedScript (ktml.TrustScript), or pass a boolean", expression.String(), resolved.Type), expression.P, expression.E)
		return
	}
	if context == scriptAttributeContext {
		if resolved.Type.IsScalar(BooleanType) || contextual && (resolved.Type.Name == TrustedScriptType || resolved.Type.Name == JSONValueType) {
			s.reportScriptPosition(path, expression, attribute)
			return
		}
		s.addDiagnostic(path, "script-attribute-value", fmt.Sprintf("%s is evaluated as JavaScript, so %s (%s) cannot be interpolated; build the value in Go as ktml.JSONValue (ktml.JSON) or ktml.TrustedScript (ktml.TrustScript), or pass a boolean", attribute, expression.String(), resolved.Type), expression.P, expression.E)
		return
	}
	if context == urlTailContext {
		if resolved.Type.Kind == ScalarTypeKind && !resolved.Type.IsScalar(BooleanType) {
			return
		}
		if contextual && (resolved.Type.Name == URLType || resolved.Type.Name == TrustedURLType) {
			s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("%s value %s renders only as a whole element attribute value", resolved.Type, expression.String()), expression.P, expression.E)
			return
		}
		s.addDiagnostic(path, "url-attribute-value", fmt.Sprintf("%s is a URL, so only string, integer, or number paths can follow its literal text, not %s (%s)", attribute, expression.String(), resolved.Type), expression.P, expression.E)
		return
	}
	if context == urlAttributeContext {
		urlValue := contextual && (resolved.Type.Name == URLType || resolved.Type.Name == TrustedURLType)
		if urlValue && exact {
			return
		}
		if urlValue {
			s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("%s value %s renders only as a whole element attribute value", resolved.Type, expression.String()), expression.P, expression.E)
			return
		}
		s.addDiagnostic(path, "url-attribute-value", urlAttributeMessage(attribute, expression, resolved.Type, exact), expression.P, expression.E)
		return
	}
	if contextual && resolved.Type.Name == CSSValueType {
		s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("CSSValue value %s renders only in the style attribute", expression.String()), expression.P, expression.E)
		return
	}
	if contextual {
		switch {
		case resolved.Type.Name == AttributesType:
			s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("Attributes value %s renders only through a spread, ...%s, in an element's attribute list", expression.String(), expression.String()), expression.P, expression.E)
		case resolved.Type.Name == TrustedScriptType:
			s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("TrustedScript value %s renders only in a script attribute such as onclick, @click, or x-on:click", expression.String()), expression.P, expression.E)
		case context != attributeContext || !exact:
			s.addDiagnostic(path, "contextual-type-mismatch", fmt.Sprintf("%s value %s renders only as a whole element attribute value", resolved.Type, expression.String()), expression.P, expression.E)
		}
		return
	}
	if resolved.Type.Kind != ScalarTypeKind {
		s.addDiagnostic(path, "path-type-mismatch", fmt.Sprintf("cannot render %s value %s; only scalar values render", resolved.Type, expression.String()), expression.P, expression.E)
		return
	}
	if resolved.Type.IsScalar(BooleanType) && (context == textOrInputContext || !exact) {
		// Only an exact attribute value gives a boolean meaning (presence);
		// KTML never stringifies one.
		s.addDiagnostic(path, "boolean-text", fmt.Sprintf("boolean %s cannot be written as text; choose the text with if %s { … } else { … }, or build the string in the presenter", expression.String(), expression.String()), expression.P, expression.E)
	}
}

// urlAttributeMessage explains a url-attribute-value diagnostic: a URL
// attribute takes one whole URL or TrustedURL value, so the URL policy sees
// the whole URL, scheme included.
func urlAttributeMessage(attribute string, expression *ast.PathExpression, t Type, exact bool) string {
	if !exact {
		return fmt.Sprintf("%s is a URL, so %s (%s) can follow literal text only when that text fixes the scheme and host, as in \"/jobs/{id}\", \"?q={q}\", or \"https://example.com/{path}\"; otherwise build the whole URL in Go as ktml.URL (ktml.NewURL)", attribute, expression.String(), t)
	}
	return fmt.Sprintf("%s is a URL, so %s (%s) cannot be interpolated; build the value in Go as ktml.URL (ktml.NewURL), which the URL policy checks, or ktml.TrustedURL (ktml.TrustURL) for a URL Go has validated", attribute, expression.String(), t)
}

// Record returns a Host Schema record by symbol.
func (s *Snapshot) Record(id SymbolID) (Record, bool) {
	if s == nil {
		return Record{}, false
	}
	record, ok := s.records[id]
	record.Fields = append([]Field(nil), record.Fields...)
	return record, ok
}

// Records returns every record in deterministic symbol order.
func (s *Snapshot) Records() []Record {
	if s == nil {
		return nil
	}
	records := make([]Record, 0, len(s.records))
	for _, id := range sortedRecordIDs(s.records) {
		record, _ := s.Record(id)
		records = append(records, record)
	}
	return records
}

// RecordsIn returns the records declared by one schema document in source order.
func (s *Snapshot) RecordsIn(file string) []Record {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	var records []Record
	for _, record := range s.Records() {
		if record.File == path {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Range.Start < records[j].Range.Start })
	return records
}

// VisibleTypes returns records in scope in a document, keyed by local name.
func (s *Snapshot) VisibleTypes(file string) map[string]Record {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	visible := make(map[string]Record, len(s.types[path]))
	for name, id := range s.types[path] {
		if record, ok := s.Record(id); ok {
			visible[name] = record
		}
	}
	return visible
}

// Paths returns the typed interpolation paths of a document in source order.
func (s *Snapshot) Paths(file string) []ResolvedPath {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	return append([]ResolvedPath(nil), s.paths[path]...)
}

// Symbol returns a record or field symbol.
func (s *Snapshot) Symbol(id SymbolID) (Symbol, bool) {
	if s == nil {
		return Symbol{}, false
	}
	if record, ok := s.records[id]; ok {
		return Symbol{ID: id, Kind: RecordSymbol, Name: record.Name, Range: record.Range, Detail: RecordSignature(record)}, true
	}
	for _, record := range s.records {
		for _, field := range record.Fields {
			if field.ID == id {
				return Symbol{ID: id, Kind: FieldSymbol, Name: field.Name, Range: field.NameRange, Record: record.ID, Detail: FieldSignature(record, field)}, true
			}
		}
	}
	return s.stateSymbol(id)
}

// SchemaSymbolAt resolves a record or field declaration or reference at a byte
// offset, returning the range of the occurrence under the cursor.
func (s *Snapshot) SchemaSymbolAt(file string, offset int) (Symbol, SourceRange, bool) {
	if s == nil {
		return Symbol{}, SourceRange{}, false
	}
	path, err := canonicalPath(file)
	if err != nil {
		return Symbol{}, SourceRange{}, false
	}
	within := func(r SourceRange) bool { return r.File == path && offset >= r.Start && offset <= r.End }
	for _, id := range sortedRecordIDs(s.records) {
		record := s.records[id]
		if within(record.Range) {
			symbol, _ := s.Symbol(id)
			return symbol, record.Range, true
		}
		for _, field := range record.Fields {
			if within(field.NameRange) {
				symbol, _ := s.Symbol(field.ID)
				return symbol, field.NameRange, true
			}
		}
	}
	if id, declaration, ok := s.stateDeclarationAt(within); ok {
		symbol, _ := s.Symbol(id)
		return symbol, declaration, true
	}
	for id, references := range s.references {
		if _, component := s.components[id]; component {
			continue
		}
		for _, reference := range references {
			if within(reference.Range) {
				if symbol, ok := s.Symbol(id); ok {
					return symbol, reference.Range, true
				}
			}
		}
	}
	return Symbol{}, SourceRange{}, false
}

// RecordSignature renders a record declaration for hover text.
func RecordSignature(record Record) string {
	var builder strings.Builder
	builder.WriteString("record " + record.Name + " [")
	for index, field := range record.Fields {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(fieldSpelling(field))
	}
	builder.WriteString("]")
	return builder.String()
}

// FieldSignature renders a field with its owning record for hover text.
func FieldSignature(record Record, field Field) string {
	return record.Name + "." + fieldSpelling(field)
}

func fieldSpelling(field Field) string {
	optional := ""
	if field.Optional {
		optional = "?"
	}
	return field.Name + optional + ": " + field.Type.String()
}

// Document returns the validated syntax document of one loaded file.
func (s *Snapshot) Document(file string) *ast.Document {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	return s.files[path].doc
}
